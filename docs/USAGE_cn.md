# Gemino 使用文档

可跑的端到端代码。项目介绍和主卖点请看 [README](../README_cn.md)。完整 API 参考见 [pkg.go.dev](https://pkg.go.dev/github.com/singchia/gemino)。

## 目录

- [核心接口](#核心接口)
- [带 ack 的消息收发](#带-ack-的消息收发)
- [RPC](#rpc)
- [双向 RPC](#双向-rpc)
- [多路复用 + `net.Conn` 互通](#多路复用--netconn-互通)
- [Options](#options)

---

## 核心接口

库对外暴露的所有抽象都在 [`gemino.go`](../gemino.go)。看懂 `End`，你就看懂了这个库：

```go
// End 表示一个逻辑上的对端：
//   - 它本身是一条默认 Stream（streamID = 1）：RPC + 消息 + 原生 net.Conn
//   - 它是 Multiplexer：可以 OpenStream / AcceptStream 开出更多 Stream
//   - 它是 net.Listener：Accept 代理到 AcceptStream
type End interface {
    Stream
    Multiplexer
    net.Listener
    Close() error
}

type Stream interface {
    RawRPCMessager  // net.Conn + RPCer + Messager
    StreamID() uint64
    ClientID() uint64
    Meta() []byte
    Side() Side
    Peer() string
}

type RPCer interface {
    NewRequest(data []byte, opts ...*options.NewRequestOptions) Request
    Call(ctx context.Context, method string, req Request, opts ...*options.CallOptions) (Response, error)
    CallAsync(ctx context.Context, method string, req Request, ch chan *Call, opts ...*options.CallOptions) (*Call, error)
    Register(ctx context.Context, method string, rpc RPC) error
    Hijack(rpc HijackRPC, opts ...*options.HijackOptions) error
}

type Messager interface {
    NewMessage(data []byte, opts ...*options.NewMessageOptions) Message
    Publish(ctx context.Context, msg Message, opts ...*options.PublishOptions) error
    PublishAsync(ctx context.Context, msg Message, ch chan *Publish, opts ...*options.PublishOptions) (*Publish, error)
    Receive(ctx context.Context) (Message, error)
}
```

## 带 ack 的消息收发

**服务端**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/gemino/server"
)

func main() {
    ln, err := server.Listen("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Fatal(err)
    }
    for {
        end, err := ln.AcceptEnd()
        if err != nil {
            log.Fatal(err)
        }
        go func() {
            msg, err := end.Receive(context.TODO())
            if err != nil {
                return
            }
            log.Printf("收到: %s", msg.Data())
            msg.Done() // 向发送方 ack
        }()
    }
}
```

**客户端**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/gemino/client"
)

func main() {
    end, err := client.NewEnd("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Fatal(err)
    }
    defer end.Close()

    msg := end.NewMessage([]byte("hello"))
    if err := end.Publish(context.TODO(), msg); err != nil {
        log.Fatal(err)
    }
}
```

## RPC

**服务端**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/gemino"
    "github.com/singchia/gemino/server"
)

func main() {
    ln, err := server.Listen("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Fatal(err)
    }
    for {
        end, err := ln.AcceptEnd()
        if err != nil {
            log.Fatal(err)
        }
        go func() {
            if err := end.Register(context.TODO(), "echo", echo); err != nil {
                log.Fatal(err)
            }
        }()
    }
}

func echo(_ context.Context, req gemino.Request, rsp gemino.Response) {
    rsp.SetData(req.Data())
    log.Printf("echo: %s", req.Data())
}
```

**客户端**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/gemino/client"
)

func main() {
    opt := client.NewEndOptions()
    opt.SetWaitRemoteRPCs("echo")

    end, err := client.NewEnd("tcp", "127.0.0.1:8080", opt)
    if err != nil {
        log.Fatal(err)
    }
    defer end.Close()

    rsp, err := end.Call(context.TODO(), "echo", end.NewRequest([]byte("hello")))
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("响应: %s", rsp.Data())
}
```

## 双向 RPC

两端都注册方法，并等对端方法可用。

**服务端**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/gemino"
    "github.com/singchia/gemino/server"
)

func main() {
    opt := server.NewEndOptions()
    opt.SetWaitRemoteRPCs("client-echo")
    opt.SetRegisterLocalRPCs(&gemino.MethodRPC{Method: "server-echo", RPC: echo})

    ln, err := server.Listen("tcp", "127.0.0.1:8080", opt)
    if err != nil {
        log.Fatal(err)
    }
    for {
        end, err := ln.AcceptEnd()
        if err != nil {
            log.Fatal(err)
        }
        go func() {
            rsp, err := end.Call(context.TODO(), "client-echo", end.NewRequest([]byte("foo")))
            if err != nil {
                log.Fatal(err)
            }
            log.Printf("来自客户端: %s", rsp.Data())
        }()
    }
}

func echo(_ context.Context, req gemino.Request, rsp gemino.Response) {
    rsp.SetData(req.Data())
    log.Printf("服务端 echo: %s", req.Data())
}
```

**客户端**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/gemino"
    "github.com/singchia/gemino/client"
)

func main() {
    opt := client.NewEndOptions()
    opt.SetWaitRemoteRPCs("server-echo")
    opt.SetRegisterLocalRPCs(&gemino.MethodRPC{Method: "client-echo", RPC: echo})

    end, err := client.NewEnd("tcp", "127.0.0.1:8080", opt)
    if err != nil {
        log.Fatal(err)
    }
    defer end.Close()

    rsp, err := end.Call(context.TODO(), "server-echo", end.NewRequest([]byte("bar")))
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("来自服务端: %s", rsp.Data())
}

func echo(_ context.Context, req gemino.Request, rsp gemino.Response) {
    rsp.SetData(req.Data())
    log.Printf("客户端 echo: %s", req.Data())
}
```

## 多路复用 + `net.Conn` 互通

**服务端**——一条连接开出多条逻辑流：

```go
package main

import (
    "log"

    "github.com/singchia/gemino/server"
)

func main() {
    ln, err := server.Listen("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Fatal(err)
    }
    for {
        end, err := ln.AcceptEnd()
        if err != nil {
            log.Fatal(err)
        }

        sm1, err := end.OpenStream()
        if err != nil {
            log.Fatal(err)
        }
        sm1.Write([]byte("hello#1"))
        sm1.Close()

        sm2, err := end.OpenStream()
        if err != nil {
            log.Fatal(err)
        }
        sm2.Write([]byte("hello#2"))
        sm2.Close()
    }
}
```

**客户端**——把 `End` 直接当作 `net.Listener` 用：

```go
package main

import (
    "log"
    "net"

    "github.com/singchia/gemino/client"
)

func main() {
    end, err := client.NewEnd("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Fatal(err)
    }
    defer end.Close()

    var ln net.Listener = end
    for {
        conn, err := ln.Accept()
        if err != nil {
            log.Fatal(err)
        }
        go func(c net.Conn) {
            buf := make([]byte, 128)
            n, err := c.Read(buf)
            if err != nil {
                return
            }
            log.Printf("读到: %s", buf[:n])
        }(conn)
    }
}
```

## Options

`client.NewEndOptions()` 和 `server.NewEndOptions()` 返回可修改的 option 构造器，常用项：

- `SetWaitRemoteRPCs(methods ...string)` —— 在 `NewEnd` / `AcceptEnd` 时阻塞，直到对端注册了列出的方法。
- `SetRegisterLocalRPCs(rpcs ...*gemino.MethodRPC)` —— 构造时声明式注册 RPC。
- `SetTimer(...)`、`SetBufferSize(...)`、`SetMeta(...)` —— 生产环境下的调参。

调用级 options（`CallOptions`、`PublishOptions`、`OpenStreamOptions` 等）在 `github.com/singchia/gemino/options`，完整列表见 [pkg.go.dev](https://pkg.go.dev/github.com/singchia/gemino/options)。
