<p align=center>
<img src="./docs/geminio.png" width="60%" height="60%">
</p>

<div align="center">

[![Go Reference](https://pkg.go.dev/badge/badge/github.com/singchia/geminio.svg)](https://pkg.go.dev/github.com/singchia/geminio)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/geminio)](https://goreportcard.com/report/github.com/singchia/geminio)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
![Platform](https://img.shields.io/badge/platform-linux-brightgreen.svg)
![Platform](https://img.shields.io/badge/platform-mac-brightgreen.svg)
![Platform](https://img.shields.io/badge/platform-windows-brightgreen.svg)

[English](./README.md) | 简体中文

</div>

## 介绍

Geminio是一个提供**应用层**网络编程的库，命名取自[Geminio](https://harrypotter.fandom.com/wiki/Doubling_Charm)，寓意有二，一是客户端和服务端连接的对等性，二是体现多路复用下会话的轻量性，如同复制魔法一样非常容易从一个连接上获取另一个抽象连接；集成这个库能让你的网络应用程序的事半功倍。

这个库的诞生是因为市面上缺少如双向RPC、消息收发确认、裸连接管理、多会话和多路复用等多综合能力的库，而常常我们在开发例如消息队列、即时通讯、接入层网关、内网穿透、代理等应用软件或中间件时都严重依赖这些抽象，故此我开发了这个网络程序库，以能够让上层软件开发十分轻松。

## 架构

<img src="./docs/biz-arch.png" width="100%" height="100%">

### 接口

本库的所有抽象基本都在首页```geminio.go```里，从End开始结合上面架构图即可理解本库的设计，当然你也可以跳到下面的使用章节直接看示例。

```golang
type RawRPCMessager interface {
	// raw
	Raw
	// rpc
	RPCer
	// message
	Messager
}

type Stream interface {
	// a stream is a geminio
	RawRPCMessager
	// meta info for a stream
	StreamID() uint64
	ClientID() uint64
	Meta() []byte
}
	
// Stream multiplexer
type Multiplexer interface {
	OpenStream(opts ...*options.OpenStreamOptions) (Stream, error)
	AcceptStream() (Stream, error)
	ListStreams() []Stream
}
	
type End interface {
	// End is a default stream with streamID 1
	// Close on default stream will close all from the End
	Stream
	// End is a stream multiplexer
	Multiplexer
}

```

### 特性

* 基本RPC（注册和调用）
* 双向RPC（双方注册和调用）
* 消息收发确认（消息一致性保障）
* 同步/异步消息（等待返回、异步等待）
* 连接多路复用（单tcp/udp连接上抽象无数tcp/udp连接）
* 连接标识（唯一ClientID和唯一StreamID）
* 支持net.Conn和net.Listener抽象
* 高可用（RetryEnd的持续重连机制）
* 测试充分（压力测试、Chaos测试、运行时PProf分析等）
* ...

## 使用

### 消息

**服务端：**

```golang
package main

import (
    "context"

    "github.com/jumboframes/armorigo/log"
    "github.com/singchia/geminio/server"
)

func main() {
    ln, err := server.Listen("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Errorf("server listen err: %s", err)
        return
    }

    for {
        end, err := ln.AcceptEnd()
        if err != nil {
            log.Errorf("accept err: %s", err)
            break
        }
        go func() {
            msg, err := end.Receive(context.TODO())
            if err != nil {
                return
            }
            log.Infof("end receive: %s", string(msg.Data()))
            msg.Done()
        }()
    }

}
```

**客户端：**

```golang
package main

import (
    "context"

    "github.com/jumboframes/armorigo/log"
    "github.com/singchia/geminio/client"
)

func main() {
    end, err := client.NewEnd("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Errorf("client dial err: %s", err)
        return
    }
    msg := end.NewMessage([]byte("hello"))
    err = end.Publish(context.TODO(), msg)
    if err != nil {
        log.Errorf("end publish err: %s", err)
        return
    }
    end.Close()
}
```

### RPC

**服务端：**

```golang
package main

import (
    "context"

    "github.com/jumboframes/armorigo/log"
    "github.com/singchia/geminio"
    "github.com/singchia/geminio/server"
)

func main() {
    ln, err := server.Listen("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Errorf("server listen err: %s", err)
        return
    }

    for {
        end, err := ln.AcceptEnd()
        if err != nil {
            log.Errorf("accept err: %s", err)
            break
        }
        go func() {
            err := end.Register(context.TODO(), "echo", echo)
            if err != nil {
                return
            }
        }()
    }
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Info("echo:", string(req.Data()))
}
```

**客户端：**

```golang
package main

import (
    "context"

    "github.com/jumboframes/armorigo/log"
    "github.com/singchia/geminio/client"
)

func main() {
    opt := client.NewEndOptions()
    opt.SetWaitRemoteRPCs("echo")
    end, err := client.NewEnd("tcp", "127.0.0.1:8080", opt)
    if err != nil {
        log.Errorf("client dial err: %s", err)
        return
    }
    rsp, err := end.Call(context.TODO(), "echo", end.NewRequest([]byte("hello")))
    if err != nil {
        log.Errorf("end call err: %s", err)
        return
    }
    if string(rsp.Data()) != "hello" {
        log.Fatal("wrong echo", string(rsp.Data()))
    }
    log.Info("echo:", string(rsp.Data()))
    end.Close()
}
```

### 双向RPC

**服务端：**

```golang
package main

import (
    "context"

    "github.com/jumboframes/armorigo/log"
    "github.com/singchia/geminio"
    "github.com/singchia/geminio/server"
)

func main() {
    opt := server.NewEndOptions()
    // the option means all End from server will wait for the rpc registration
    opt.SetWaitRemoteRPCs("client-echo")
    // pre-register server side method
    opt.SetRegisterLocalRPCs(&geminio.MethodRPC{"server-echo", echo})

    ln, err := server.Listen("tcp", "127.0.0.1:8080", opt)
    if err != nil {
        log.Errorf("server listen err: %s", err)
        return
    }

    for {
        end, err := ln.AcceptEnd()
        if err != nil {
            log.Errorf("accept err: %s", err)
            break
        }
        go func() {
            // call client side method
            rsp, err := end.Call(context.TODO(), "client-echo", end.NewRequest([]byte("foo")))
            if err != nil {
                log.Errorf("end call err: %s", err)
                return
            }
            if string(rsp.Data()) != "foo" {
                log.Fatal("wrong echo", string(rsp.Data()))
            }
            log.Info("client echo:", string(rsp.Data()))
        }()
    }
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Info("server echo:", string(req.Data()))
}
```

**客户端：**

```golang
package main

import (
    "context"

    "github.com/jumboframes/armorigo/log"
    "github.com/singchia/geminio"
    "github.com/singchia/geminio/client"
)

func main() {
    opt := client.NewEndOptions()
    // the option means all End from server will wait for the rpc registration
    opt.SetWaitRemoteRPCs("server-echo")
    // pre-register client side method
    opt.SetRegisterLocalRPCs(&geminio.MethodRPC{"client-echo", echo})

    end, err := client.NewEnd("tcp", "127.0.0.1:8080", opt)
    if err != nil {
        log.Errorf("client dial err: %s", err)
        return
    }
    // call server side method
    rsp, err := end.Call(context.TODO(), "server-echo", end.NewRequest([]byte("bar")))
    if err != nil {
        log.Errorf("end call err: %s", err)
        return
    }
    if string(rsp.Data()) != "bar" {
        log.Fatal("wrong echo", string(rsp.Data()))
    }
    log.Info("server echo:", string(rsp.Data()))
    end.Close()
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Info("client echo:", string(req.Data()))
}
```

### 多路复用

**服务端：**

```golang
package main

import (
    "github.com/jumboframes/armorigo/log"
    "github.com/singchia/geminio/server"
)

func main() {
    ln, err := server.Listen("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Errorf("server listen err: %s", err)
        return
    }

    for {
        end, err := ln.AcceptEnd()
        if err != nil {
            log.Errorf("accept err: %s", err)
            break
        }
        // stream #1, and it's also a net.Conn
        sm1, err := end.OpenStream()
        if err != nil {
            log.Errorf("end open stream err: %s", err)
            break
        }
        sm1.Write([]byte("hello#1"))
        sm1.Close()

        // stream #2 and it's also a net.Conn
        sm2, err := end.OpenStream()
        if err != nil {
            log.Errorf("end open stream err: %s", err)
            break
        }
        sm2.Write([]byte("hello#2"))
        sm2.Close()
    }
}
```

**客户端：**

```golang
package main

import (
    "net"

    "github.com/jumboframes/armorigo/log"
    "github.com/singchia/geminio/client"
)

func main() {
    end, err := client.NewEnd("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Errorf("client dial err: %s", err)
        return
    }
    // the end is also a net.Listener
    ln := net.Listener(end)
    for {
        conn, err := ln.Accept()
        if err != nil {
            log.Errorf("end accept err: %s", err)
            break
        }
        go func(conn net.Conn) {
            buf := make([]byte, 128)
            _, err := conn.Read(buf)
            if err != nil {
                return
            }
            log.Info("read:", string(buf))
        }(conn)
    }
    end.Close()
}
```

## 示例

* 消息和确认 [messager](./examples/messager)
* 简单消息队列  [mq](./examples/mq)
* 聊天室  [chatroom](./examples/chatroom)
* 中继器  [relay](./examples/relay)
* 内网穿透 [traversal](./examples/traversal)

## 测试

### Benchmarks

```
goos: darwin
goarch: amd64
pkg: github.com/singchia/geminio/test/bench
cpu: Intel(R) Core(TM) i5-6267U CPU @ 2.90GHz
BenchmarkMessage-4   	   10117	    112584 ns/op	1164.21 MB/s	    5764 B/op	     181 allocs/op
BenchmarkEnd-4       	   11644	     98586 ns/op	1329.52 MB/s	  550534 B/op	      73 allocs/op
BenchmarkStream-4    	   12301	     96955 ns/op	1351.88 MB/s	  550605 B/op	      82 allocs/op
BenchmarkRPC-4       	    6960	    165384 ns/op	 792.53 MB/s	   38381 B/op	     187 allocs/op
PASS
```

## 设计

本库按照以下架构实现

<p align=center>
<img src="./docs/design.png" width="80%" height="80%">
</p>

## 参与开发

如果你发现任何Bug，请提出Issue，项目Maintainers会及时响应相关问题。
 
 如果你希望能够提交Feature，更快速解决项目问题，满足以下简单条件下欢迎提交PR：
 
 * 代码风格保持一致
 * 每次提交一个Feature
 * 提交的代码都携带单元测试

## 许可证

© Austin Zhai, 2023-2030

Released under the [Apache License 2.0](https://github.com/singchia/geminio/blob/main/LICENSE)
