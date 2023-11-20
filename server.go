package GeeRPC

import (
	"GeeRPC/codec"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"
)

const MagicNumber = 0x3bef5c

// Option 首部选项
type Option struct {
	// MagicNumber 用于标记请求
	MagicNumber int
	// CodecType 编解码器类型
	CodecType codec.Type
	// ConnectTimeout 连接超时时间
	ConnectTimeout time.Duration // 0 means no limit
	// HandleTimeout 处理超时时间
	HandleTimeout time.Duration // 0 means no limit
}

var DefaultOption = &Option{
	MagicNumber:    MagicNumber,
	CodecType:      codec.GobType,    // 默认编解码器为gob
	ConnectTimeout: time.Second * 10, // 默认连接超时时间为10s
}

// Server represents an RPC Server
type Server struct {
	// serviceMap is the registry of service
	serviceMap sync.Map
}

func NewServer() *Server {
	return &Server{}
}

var DefaultServer = NewServer()

func Register(rcvr interface{}) error {
	return DefaultServer.Register(rcvr)
}

// Register publishes in the server the set of methods of the receiver value
func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return fmt.Errorf("rpc: service already defined: %s", s.name)
	}
	return nil
}

// findService 查找服务，通过服务名找到服务
func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".") // 找到最后一个点的位置
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	// 通过最后一个点将服务名和方法名分开
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	// 通过服务名找到服务
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service " + serviceName)
		return
	}
	// 通过反射找到方法
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}
	return
}

func (server *Server) Accept(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println("RPC Server Accept Error:", err)
			return
		}
		log.Printf("接收到客户端连接 {Local Addr: %s; Remote Addr: %s\n ", conn.LocalAddr().String(), conn.RemoteAddr().String())
		go server.ServeConn(conn)
	}
}

func Accept(lis net.Listener) {
	DefaultServer.Accept(lis)
}

func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	defer func() { conn.Close() }()
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("RPC Server option error: ", err)
		return
	}
	if opt.MagicNumber != MagicNumber {
		log.Printf("RPC Server invaild magic number: %x\n", opt.MagicNumber)
		return
	}
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("RPC Server invaild codec type %s\n", opt.CodecType)
	}
	server.ServerCodec(f(conn), &opt)
}

var invalidRequest = struct{}{}

func (server *Server) ServerCodec(cc codec.Codec, opt *Option) {
	sending := new(sync.Mutex) // make sure to send a complete response
	wg := new(sync.WaitGroup)  // wait until all request are handled
	for {
		req, err := server.readRequest(cc)
		if err != nil {
			if req == nil {
				break // it's not possible to recover, so close the connection
			}
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		go server.handleRequest(cc, req, sending, wg, opt.HandleTimeout)
	}
	wg.Wait()
	_ = cc.Close()
}

// request stores all information of a call
type request struct {
	h            *codec.Header // header of request
	argv, replyv reflect.Value // argv and replyv of request
	mtype        *methodType   // type of request
	svc          *service      // service of request
}

func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()

	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface() // 如果不是指针类型，那么取地址
	}
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read argv err:", err)
		return req, err
	}
	return req, nil
}

func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error:", err)
		}
		return nil, err
	}
	return &h, nil
}

func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error:", err)
	}
}

func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	defer wg.Done()
	called := make(chan struct{}) // call 方法是否被调用
	sent := make(chan struct{})   // call 方法是否被发送
	go func() {
		err := req.svc.call(req.mtype, req.argv, req.replyv)
		called <- struct{}{}
		if err != nil {
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			sent <- struct{}{}
			return
		}
		server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
		sent <- struct{}{}
	}()
	if timeout == 0 {
		<-called
		<-sent
		return
	}
	select {
	case <-time.After(timeout):
		req.h.Error = fmt.Sprintf("rpc server: request handle timeout: expect within %s", timeout)
		server.sendResponse(cc, req.h, invalidRequest, sending)
	case <-called:
		<-sent
	}
}
