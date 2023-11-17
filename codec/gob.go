// Package codec 序列化和反序列化RPC调用的网络数据
package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

type GobCodec struct {
	// conn 包含读取、写入和关闭功能的接口
	//
	// 用于进行网络等输入输出操作。
	conn io.ReadWriteCloser
	// buf 带缓冲的Writer
	//
	// 缓冲区满了再写到网卡，减少系统调用次数，提高写效率
	buf *bufio.Writer
	// dec 解码器
	//
	// 用于从网络连接中读取并解码二进制数据
	dec *gob.Decoder
	// enc 编码器
	//
	// 用于将数据编码为二进制格式并写入网络连接
	enc *gob.Encoder
}

func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		dec:  gob.NewDecoder(conn),
		enc:  gob.NewEncoder(buf),
	}
}

// Close 关闭 GobCodec 的连接
func (g *GobCodec) Close() error {
	return g.conn.Close()
}

func (g *GobCodec) ReadHeader(h *Header) error {
	return g.dec.Decode(h)
}

func (g *GobCodec) ReadBody(body interface{}) error {
	return g.dec.Decode(body)
}

func (g *GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		_ = g.buf.Flush() // 刷新输入缓冲区
		if err != nil {
			_ = g.Close()
		}
	}()
	if err := g.enc.Encode(h); err != nil {
		log.Println("rpc codec: gob error encoding header:", err)
		return err
	}
	if err := g.enc.Encode(body); err != nil {
		log.Println("rpc codec: gob error encoding body:", err)
		return err
	}
	return nil
}
