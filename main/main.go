package main

import (
	"GeeRPC"
	"context"
	"log"
	"net"
	"sync"
	"time"
)

type Foo int

type Args struct{ Num1, Num2 int }

// Sum 方法
func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func startServer(addr chan string) {
	var foo Foo
	if err := GeeRPC.Register(&foo); err != nil {
		log.Fatal("register error:", err)
	}
	// pick a free port
	l, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("start rpc server on", l.Addr())
	addr <- l.Addr().String()
	GeeRPC.Accept(l)
}

func main() {
	log.SetFlags(0)
	addr := make(chan string) // 用于存储服务端地址
	go startServer(addr)      // 异步启动服务端

	client, _ := GeeRPC.Dial("tcp", <-addr) // 连接服务端
	defer func() { _ = client.Close() }()   // defer关闭连接

	time.Sleep(time.Second)
	// send request & receive response
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1) // 计数器加1
		go func(i int) {
			defer wg.Done() // 计数器减1
			args := &Args{Num1: i, Num2: i * i}
			var reply int
			ctx, _ := context.WithTimeout(context.Background(), time.Second) // 设置超时时间
			if err := client.Call(ctx, "Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Printf("%d + %d = %d\n", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait() // 阻塞，直到计数器变为0
	runArr := []rune("hello world")
}
