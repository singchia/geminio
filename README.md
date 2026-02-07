<div align="center">

<img src="./docs/geminio.png" width="200">


> A powerful application-layer network programming library for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/singchia/geminio.svg)](https://pkg.go.dev/github.com/singchia/geminio)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/geminio)](https://goreportcard.com/report/github.com/singchia/geminio)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-brightgreen.svg)](https://github.com/singchia/geminio)

[English](./README.md) | [简体中文](./README_cn.md)

</div>

---

## 📖 Introduction

**Geminio** is a comprehensive application-layer network programming library for Go, named after the [Doubling Charm](https://harrypotter.fandom.com/wiki/Doubling_Charm) from Harry Potter. It provides a unified interface for building network applications with features like RPC, bidirectional RPC, messaging, multi-session management, connection multiplexing, and raw connection handling.

Geminio simplifies network development by abstracting away the complexity of low-level network programming, allowing developers to focus on business logic rather than connection management.

## ✨ Features

- 🔄 **RPC & Bidirectional RPC** - Full support for remote procedure calls with bidirectional capabilities
- 📨 **Messaging** - Reliable message delivery with acknowledgment guarantees
- 🔀 **Connection Multiplexing** - Multiple logical connections over a single physical connection
- 🆔 **Connection Identification** - Unique ClientID and StreamID for connection management
- 🔌 **Native Compatibility** - Seamless integration with Go's `net.Conn` and `net.Listener`
- 🔁 **High Availability** - Built-in automatic reconnection mechanism for clients
- ⚡ **High Performance** - Optimized for low latency and high throughput
- 🛡️ **Production Ready** - Extensive testing including stress tests, chaos tests, and performance profiling
- 📦 **Zero Dependencies** - Lightweight with minimal external dependencies

## 🚀 Quick Start

### Installation

```bash
go get github.com/singchia/geminio
```

### Basic Example

**Server:**

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
            log.Printf("Received: %s", string(msg.Data()))
            msg.Done()
        }()
    }
}
```

**Client:**

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

    msg := end.NewMessage([]byte("Hello, Geminio!"))
    if err := end.Publish(context.TODO(), msg); err != nil {
        log.Fatal(err)
    }
}
```

## 📚 Documentation

### Architecture

Geminio follows a layered architecture design:

<img src="./docs/biz-arch.png" width="100%">

### Core Interfaces

The library's main abstractions are defined in `geminio.go`:

```go
// RPC interface
type RPCer interface {
    NewRequest(data []byte, opts ...*options.NewRequestOptions) Request
    Call(ctx context.Context, method string, req Request, opts ...*options.CallOptions) (Response, error)
    CallAsync(ctx context.Context, method string, req Request, ch chan *Call, opts ...*options.CallOptions) (*Call, error)
    Register(ctx context.Context, method string, rpc RPC) error
}

// Messaging interface
type Messager interface {
    NewMessage(data []byte, opts ...*options.NewMessageOptions) Message
    Publish(ctx context.Context, msg Message, opts ...*options.PublishOptions) error
    PublishAsync(ctx context.Context, msg Message, ch chan *Publish, opts ...*options.PublishOptions) (*Publish, error)
    Receive(ctx context.Context) (Message, error)
}

// Stream interface (combines RPC, Messaging, and Raw connection)
type Stream interface {
    RawRPCMessager  // RPC + Messaging + net.Conn
    StreamID() uint64
    ClientID() uint64
    Meta() []byte
}

// Multiplexer for managing multiple streams
type Multiplexer interface {
    OpenStream(opts ...*options.OpenStreamOptions) (Stream, error)
    AcceptStream() (Stream, error)
    ListStreams() []Stream
}

// End is the main entry point
type End interface {
    Stream      // End is also a default stream (streamID = 1)
    Multiplexer // End can manage multiple streams
    Close()
}
```

## 💡 Usage Examples

### Message Publishing

**Server:**

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
            log.Printf("Received: %s", string(msg.Data()))
            msg.Done()
        }()
    }
}
```

**Client:**

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

### RPC

**Server:**

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
            err := end.Register(context.TODO(), "echo", echo)
            if err != nil {
                log.Fatal(err)
            }
        }()
    }
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Printf("Echo: %s", string(req.Data()))
}
```

**Client:**

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
    
    log.Printf("Response: %s", string(rsp.Data()))
}
```

### Bidirectional RPC

**Server:**

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
    opt.SetRegisterLocalRPCs(&geminio.MethodRPC{"server-echo", echo})

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
            log.Printf("Client echo: %s", string(rsp.Data()))
        }()
    }
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Printf("Server echo: %s", string(req.Data()))
}
```

**Client:**

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
    opt.SetRegisterLocalRPCs(&geminio.MethodRPC{"client-echo", echo})

    end, err := client.NewEnd("tcp", "127.0.0.1:8080", opt)
    if err != nil {
        log.Fatal(err)
    }
    defer end.Close()

    rsp, err := end.Call(context.TODO(), "server-echo", end.NewRequest([]byte("bar")))
    if err != nil {
        log.Fatal(err)
    }
    
    log.Printf("Server echo: %s", string(rsp.Data()))
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
    rsp.SetData(req.Data())
    log.Printf("Client echo: %s", string(req.Data()))
}
```

### Multiplexing

**Server:**

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
        
        // Open stream #1
        sm1, err := end.OpenStream()
        if err != nil {
            log.Fatal(err)
        }
        sm1.Write([]byte("hello#1"))
        sm1.Close()

        // Open stream #2
        sm2, err := end.OpenStream()
        if err != nil {
            log.Fatal(err)
        }
        sm2.Write([]byte("hello#2"))
        sm2.Close()
    }
}
```

**Client:**

```go
package main

import (
    "net"
    "log"

    "github.com/singchia/geminio/client"
)

func main() {
    end, err := client.NewEnd("tcp", "127.0.0.1:8080")
    if err != nil {
        log.Fatal(err)
    }
    defer end.Close()

    // End can be used as net.Listener
    ln := net.Listener(end)
    for {
        conn, err := ln.Accept()
        if err != nil {
            log.Fatal(err)
        }
        
        go func(conn net.Conn) {
            buf := make([]byte, 128)
            n, err := conn.Read(buf)
            if err != nil {
                return
            }
            log.Printf("Read: %s", string(buf[:n]))
        }(conn)
    }
}
```

## 📦 More Examples

Check out the [examples](./examples) directory for more comprehensive examples:

- **[Messager](./examples/messager)** - Message publishing and receiving with acknowledgment
- **[Message Queue](./examples/mq)** - A simple message queue implementation
- **[Chatroom](./examples/chatroom)** - Real-time chatroom example
- **[Relay](./examples/relay)** - Network relay proxy
- **[Intranet Penetration](./examples/traversal)** - NAT traversal example

## ⚡ Performance

Benchmark results (Intel Core i5-6267U @ 2.90GHz):

```
goos: darwin
goarch: amd64
pkg: github.com/singchia/geminio/test/bench
cpu: Intel(R) Core(TM) i5-6267U CPU @ 2.90GHz

BenchmarkMessage-4   	   10117	    112584 ns/op	1164.21 MB/s	    5764 B/op	     181 allocs/op
BenchmarkEnd-4       	   11644	     98586 ns/op	1329.52 MB/s	  550534 B/op	      73 allocs/op
BenchmarkStream-4    	   12301	     96955 ns/op	1351.88 MB/s	  550605 B/op	      82 allocs/op
BenchmarkRPC-4       	    6960	    165384 ns/op	 792.53 MB/s	   38381 B/op	     187 allocs/op
```

## 🏗️ Design

Geminio is built with a layered architecture:

<p align="center">
<img src="./docs/design.png" width="80%">
</p>

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

### Guidelines

- Maintain consistent code style
- Submit one feature at a time
- Include unit tests with your code
- Update documentation as needed

For bug reports or feature requests, please open an issue on GitHub.

## 📄 License

Copyright © Austin Zhai, 2023-2030

Licensed under the [Apache License 2.0](./LICENSE)

---

<div align="center">

<a href="https://next.ossinsight.io/widgets/official/compose-activity-trends?repo_id=412119706" target="_blank" style="display: block" align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://next.ossinsight.io/widgets/official/compose-activity-trends/thumbnail.png?repo_id=412119706&image_size=auto&color_scheme=dark" width="815" height="auto">
    <img alt="Activity Trends of singchia/geminio - Last 28 days" src="https://next.ossinsight.io/widgets/official/compose-activity-trends/thumbnail.png?repo_id=412119706&image_size=auto&color_scheme=light" width="815" height="auto">
  </picture>
</a>

Made with [OSS Insight](https://ossinsight.io/)

</div>
