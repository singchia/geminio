# Geminio Usage

Runnable, end-to-end patterns. For the pitch and the big picture, see [README](../README.md). For API reference, see [pkg.go.dev](https://pkg.go.dev/github.com/singchia/geminio).

## Table of contents

- [Core interface](#core-interface)
- [Messaging with ack](#messaging-with-ack)
- [RPC](#rpc)
- [Bidirectional RPC](#bidirectional-rpc)
- [Stream multiplexing + `net.Conn` interop](#stream-multiplexing--netconn-interop)
- [Options](#options)

---

## Core interface

Everything the library exposes lives in [`geminio.go`](../geminio.go). If you understand `End`, you understand the library:

```go
// An End is a single logical peer:
//   - a default Stream (streamID = 1): RPC + Messaging + raw net.Conn
//   - a Multiplexer: OpenStream / AcceptStream to get more Streams
//   - a net.Listener: Accept delegates to AcceptStream
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

## Messaging with ack

**Server**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/geminio/server"
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
            log.Printf("received: %s", msg.Data())
            msg.Done() // acks the sender
        }()
    }
}
```

**Client**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/geminio/client"
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

**Server**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/geminio"
    "github.com/singchia/geminio/server"
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

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Printf("echo: %s", req.Data())
}
```

**Client**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/geminio/client"
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
    log.Printf("response: %s", rsp.Data())
}
```

## Bidirectional RPC

Both sides register handlers and wait for the other's method to be available.

**Server**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/geminio"
    "github.com/singchia/geminio/server"
)

func main() {
    opt := server.NewEndOptions()
    opt.SetWaitRemoteRPCs("client-echo")
    opt.SetRegisterLocalRPCs(&geminio.MethodRPC{Method: "server-echo", RPC: echo})

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
            log.Printf("from client: %s", rsp.Data())
        }()
    }
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Printf("server echo: %s", req.Data())
}
```

**Client**

```go
package main

import (
    "context"
    "log"

    "github.com/singchia/geminio"
    "github.com/singchia/geminio/client"
)

func main() {
    opt := client.NewEndOptions()
    opt.SetWaitRemoteRPCs("server-echo")
    opt.SetRegisterLocalRPCs(&geminio.MethodRPC{Method: "client-echo", RPC: echo})

    end, err := client.NewEnd("tcp", "127.0.0.1:8080", opt)
    if err != nil {
        log.Fatal(err)
    }
    defer end.Close()

    rsp, err := end.Call(context.TODO(), "server-echo", end.NewRequest([]byte("bar")))
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("from server: %s", rsp.Data())
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Printf("client echo: %s", req.Data())
}
```

## Stream multiplexing + `net.Conn` interop

**Server** — open multiple logical streams over one connection:

```go
package main

import (
    "log"

    "github.com/singchia/geminio/server"
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

**Client** — use `End` directly as a `net.Listener`:

```go
package main

import (
    "log"
    "net"

    "github.com/singchia/geminio/client"
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
            log.Printf("read: %s", buf[:n])
        }(conn)
    }
}
```

## Options

Both `client.NewEndOptions()` and `server.NewEndOptions()` return mutable option builders. Common knobs:

- `SetWaitRemoteRPCs(methods ...string)` — block `NewEnd` / `AcceptEnd` until the peer has registered the listed methods.
- `SetRegisterLocalRPCs(rpcs ...*geminio.MethodRPC)` — register RPCs declaratively at construction time.
- `SetTimer(...)`, `SetBufferSize(...)`, `SetMeta(...)` — tuning for production deployments.

Call-site options like `CallOptions`, `PublishOptions`, `OpenStreamOptions` live in `github.com/singchia/geminio/options`. See [pkg.go.dev](https://pkg.go.dev/github.com/singchia/geminio/options) for the full list.
