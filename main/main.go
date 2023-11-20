package main

import (
	"GeeRPC"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

func startServer(addr chan string) {
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
			args := fmt.Sprintf("geerpc req %d", i)
			var reply string
			if err := client.Call("Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Println("reply:", reply)
		}(i)
	}
	wg.Wait() // 阻塞，直到计数器变为0
}
