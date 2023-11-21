package main

import (
	"GeeRPC"
	"context"
	"log"
	"net"
	"net/http"
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

func startServer(addrCh chan string) {
	var foo Foo
	l, _ := net.Listen("tcp", ":9999")
	_ = GeeRPC.Register(&foo)
	GeeRPC.HandleHTTP()
	addrCh <- l.Addr().String()
	_ = http.Serve(l, nil)
}

func call(addr chan string) {
	address := <-addr // addr是一个无缓冲的channel，所以这里会阻塞，直到服务端启动并将地址发送到chan中
	log.Println("address:", address)
	client, _ := GeeRPC.DialHTTP("tcp", address) // 连接服务端
	defer func() { _ = client.Close() }()        // defer关闭连接

	time.Sleep(time.Second)
	// send request & receive response
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1) // 计数器加1
		go func(i int) {
			defer wg.Done() // 计数器减1
			args := &Args{Num1: i, Num2: i * i}
			var reply int
			//ctx, _ := context.WithTimeout(context.Background(), time.Second) // 设置超时时间
			if err := client.Call(context.Background(), "Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Printf("%d + %d = %d\n", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait() // 阻塞，直到计数器变为0
}

func main() {
	log.SetFlags(log.Lshortfile | log.Ldate | log.Ltime)
	addr := make(chan string) // 用于存储服务端地址
	go call(addr)             // 异步调用call
	startServer(addr)         // 启动服务端
}
