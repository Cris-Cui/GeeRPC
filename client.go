// GeeRPC 客户端
//
//
package GeeRPC

import (
	"GeeRPC/codec"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Call represents an active RPC.
type Call struct {
	// Seq 请求的唯一标识编号
	Seq uint64
	// ServiceMethod 服务方法
	ServiceMethod string
	// Args 参数
	Args interface{}
	// Reply 传出参数-返回值
	Reply interface{}
	// Error 错误信息
	Error error
	// Done 完成通知的channel，用于支持异步调用, 当调用结束后通知调用方
	Done chan *Call
}

// done 支持异步调用, 当调用结束后通知调用方
func (call *Call) done() {
	call.Done <- call
}

// Client represents an RPC Client.
type Client struct {
	// cc 消息的编解码器
	cc codec.Codec
	// opt 固定在消息报文Header之前，协商消息的编解码方式
	opt *Option
	// sending 互斥锁，保证请求的有序发送
	sending sync.Mutex
	// header 消息请求头
	header codec.Header
	// mu 互斥锁
	mu sync.Mutex
	// seq 请求的唯一编号
	seq uint64
	// pending 存储未处理完的请求
	pending map[uint64]*Call
	// closing 客户端主动调用关闭连接
	closing bool
	// shutdown 错误发生连接被关闭
	shutdown bool
}

// clientResult 存储client和error
type clientResult struct {
	client *Client
	err    error
}

var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connection is shut down")

// IsAvailable return true if the client does work
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return !client.shutdown && !client.closing
}

// registerCall 注册请求，将请求注册到pending中
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}
	call.Seq = client.seq
	client.pending[call.Seq] = call
	client.seq++
	return call.Seq, nil
}

// removeCall 移除请求，将请求从pending中移除并返回
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

// terminateCalls 错误发生时，将所有未处理完的请求移除
func (client *Client) terminateCalls(err error) {
	client.sending.Lock()
	defer client.sending.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	client.shutdown = true
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
}

// receive 接收响应
func (client *Client) receive() {
	var err error
	for err == nil { // 循环读取响应
		var h codec.Header
		if err = client.cc.ReadHeader(&h); err != nil { // 读取响应头
			break
		}
		call := client.removeCall(h.Seq) // 从pending中移除请求并接收
		switch {
		case call == nil: // call已经被移除
			// 通常意味着Write部分失败并且已经删除了调用
			err = client.cc.ReadBody(nil) // 读取响应体
		case h.Error != "": // 服务端处理请求出错
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done() // 通知调用方
		default:
			err = client.cc.ReadBody(call.Reply) // 读取响应体
			if err != nil {                      // 读取响应体出错
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done() // 通知调用方
		}
	}
	// 错误发生，将所有未处理完的请求移除
	client.terminateCalls(err)
}

// send 发送请求
func (client *Client) send(call *Call) {
	// 确保客户端发送完整的请求
	client.sending.Lock()
	defer client.sending.Unlock()

	// 注册请求，将请求注册到pending中
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	// 准备请求头
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""

	// 编码并发送请求
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		call := client.removeCall(seq)
		// call可能为nil，通常意味着Write部分失败，client收到响应并处理
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

// Go 异步调用
func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 1) // 无缓冲通道
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	client.send(call)
	return call
}

// Call 同步调用
func (client *Client) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done // 从done通道中阻塞读取响应
	select {
	case <-ctx.Done(): // 超时
		client.removeCall(call.Seq)
		return errors.New("rpc client: call failed: " + ctx.Err().Error())
	case call := <-call.Done: // 读取响应
		return call.Error
	}
}

// NewClient 创建Client
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType] // 根据编解码类型获取对应的编解码器
	if f == nil {                             // 如果编解码器不存在，则返回错误
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}
	// send options with server
	if err := json.NewEncoder(conn).Encode(opt); err != nil { // 将编解码器类型发送给服务端
		log.Println("rpc client: options error: ", err)
		_ = conn.Close()
		return nil, err
	}
	// f(conn)是一个编解码器，将conn作为参数传入，返回一个编解码器
	return newClientCodec(f(conn), opt), nil // 创建Client
}

// newClientCodec 创建Client的编解码器
func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1, // 请求编号从1开始，0表示无效的请求
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	go client.receive() // 开启接收响应的goroutine
	return client       // 返回Client
}

// Dial 连接服务端
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout(network, address, opt.ConnectTimeout) // 建立连接
	if err != nil {
		return nil, err
	}
	// close the connection if client is nil
	defer func() {
		if client == nil { // 如果client为nil，则关闭连接
			_ = conn.Close()
		}
	}()
	ch := make(chan clientResult) // 创建通道
	go func() {
		client, err = NewClient(conn, opt) // 创建客户端
		ch <- clientResult{client: client, err: err}
	}()
	if opt.ConnectTimeout == 0 { // 如果没有设置连接超时，则直接返回
		result := <-ch
		return result.client, result.err
	}
	select {
	case <-time.After(opt.ConnectTimeout): // 超时
		return nil, fmt.Errorf("rpc client: connect timeout: expect within %s", opt.ConnectTimeout)
	case result := <-ch: // 从通道中读取结果
		return result.client, result.err
	}
}

// Close 关闭连接
func (client *Client) Close() error {
	client.mu.Lock()         // 加锁
	defer client.mu.Unlock() // defer解锁
	if client.closing {      // 如果已经关闭，则返回错误
		return ErrShutdown
	}
	client.closing = true    // 设置关闭标志
	return client.cc.Close() // 关闭Codec编解码器
}

// parseOptions 解析可选可变长参数
//
// Option {
// 	MagicNumber:    MagicNumber,
// 	CodecType:      codec.GobType,
//  ConnectTimeout time.Duration,
//	HandleTimeout time.Duration
// }
//
// 1. 如果opts为空或者opts[0]为空，则返回默认值
//
// 2. 如果opts长度大于1，则返回错误
//
// 3. 如果opts长度为1，则返回opts[0]
func parseOptions(opts ...*Option) (*Option, error) {
	// if opts is nil or pass nil as parameter
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber // 如果opts[0].MagicNumber使用默认值
	if opt.CodecType == "" {                    // 如果opts[0].CodecType为空，则使用默认值
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}

// NewHTTPClient 创建HTTP客户端
// TODO 存在已经关闭的连接
func NewHTTPClient(conn net.Conn, opt *Option) (*Client, error) {
	_, _ = io.WriteString(conn, fmt.Sprintf("CONNECT %s HTTP/1.0\n\n", defaultRPCPath)) // 发送HTTP请求
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err == nil && resp.Status == connected { // 如果成功连接
		return NewClient(conn, opt)
	}
	if err == nil { // 如果连接失败，则关闭连接
		err = errors.New("unexpected HTTP response: " + resp.Status)
	}
	return nil, err
}

// DialHTTP 连接HTTP服务端
func DialHTTP(network, address string, opts ...*Option) (*Client, error) {
	return dialTimeout(NewHTTPClient, network, address, opts...)
}

type newClientFunc func(conn net.Conn, opt *Option) (client *Client, err error)

// dialTimeout 带有超时的连接
func dialTimeout(f newClientFunc, network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout(network, address, opt.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	// close the connection if client is nil
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()
	ch := make(chan clientResult)

	go func() {
		client, err := f(conn, opt)
		ch <- clientResult{client: client, err: err}
	}()
	if opt.ConnectTimeout == 0 {
		result := <-ch // 如果连接超时设置为0，程序阻塞在这里，等待goroutine执行完毕返回结果
		return result.client, result.err
	}
	select {
	case <-time.After(opt.ConnectTimeout): // 超时
		return nil, fmt.Errorf("rpc client: connect timeout: expect within %s", opt.ConnectTimeout)
	case result := <-ch: // 从通道中读取结果
		return result.client, result.err
	}
}

func XDial(rpcAddr string, opts ...*Option) (*Client, error) {
	// 即可处理TCP又可处理HTTP
	parts := strings.Split(rpcAddr, "@") // 以@分割rpcAddr
	if len(parts) != 2 {
		return nil, fmt.Errorf("rpc client: invalid format %s", rpcAddr)
	}
	protocol, addr := parts[0], parts[1] // 协议和地址
	switch parts[0] {
	case "http":
		return DialHTTP("tcp", addr, opts...)
	default:
		return Dial(protocol, addr, opts...)
	}
}
