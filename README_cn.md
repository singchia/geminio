<div align="center">

<img src="./docs/geminio.png" width="180">

**一个连接，搞定双向 RPC、带确认的消息、流多路复用——全部藏在一个 `net.Conn` 后面。**

[![Go Reference](https://pkg.go.dev/badge/github.com/singchia/geminio.svg)](https://pkg.go.dev/github.com/singchia/geminio)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/geminio)](https://goreportcard.com/report/github.com/singchia/geminio)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-brightgreen.svg)](https://github.com/singchia/geminio)

[English](./README.md) | [简体中文](./README_cn.md)

</div>

---

## 为什么是 Geminio?

你要写的可能是一个 IM 服务、消息队列、API 网关、内网穿透隧道，或者 service mesh 的 sidecar。要把它做对，你会需要：**双向 RPC**、**带 ack 的可靠消息**、**一条 TCP 连接上多路逻辑流**、**客户端自动重连**，而且整套东西必须能无缝融入 Go 的 `net.Conn` / `net.Listener` 生态。

今天的主流做法是：RPC 用 gRPC，多路复用用 yamux/smux，消息用 NATS 或自研协议，再写一堆胶水代码把它们的生命周期捏到一起。**Geminio 把这些能力统一在一个接口下面。**

<p align="center"><img src="./docs/overview.png" width="85%"></p>

## Geminio 与常见替代品

|                                       | gRPC              | yamux / smux | NATS | **Geminio** |
| ------------------------------------- |:-----------------:|:------------:|:----:|:-----------:|
| 请求/响应 RPC                         | ✅                | —            | —    | ✅          |
| **服务端反向调用客户端方法**           | ⚠️ 仅 streaming    | —            | —    | ✅          |
| 消息 publish + ack                    | —                 | —            | ✅   | ✅          |
| 流多路复用                            | ✅（HTTP/2）      | ✅           | —    | ✅          |
| 原生 `net.Conn` / `net.Listener` 兼容 | —                 | ✅           | —    | ✅          |
| 客户端自动重连                        | —                 | —            | ✅   | ✅          |
| 单二进制、无 broker                   | ✅                | ✅           | —    | ✅          |

> 这里"服务端反向调用"指的是服务端可以 `Call("method", ...)` 去触发客户端注册的处理器——不是"在已打开的流里推消息"。这是多数"RPC 库"不提供的能力。

## 特性

- 🔄 **双向 RPC**——任一端都可以注册方法并调用对端方法。
- 📨 **带 ack 的消息**——`Publish` / `Receive` 可确认收发；支持同步和异步。
- 🔀 **流多路复用**——一条连接上开任意多条逻辑流。
- 🔌 **`net.Conn` / `net.Listener` 兼容**——流可以直接塞进任何走 Go net 接口的代码。
- 🆔 **稳定的对端和流标识**——`ClientID`、`StreamID` 让路由、鉴权、追踪简单自然。
- 🔁 **自动重连**——客户端在网络抖动后自行恢复。
- ⚡ **~1.3 GB/s** 的流吞吐（2016 年双核笔记本 CPU 实测，见 [基准测试](#基准测试)）。
- 🧪 **稳定可靠**——覆盖单测、集成、端到端、压力、混沌和回归测试。

## 60 秒看个 demo

```bash
go get github.com/singchia/geminio
```

**服务端**

```go
ln, _ := server.Listen("tcp", "127.0.0.1:8080")
for {
    end, _ := ln.AcceptEnd()
    end.Register(context.TODO(), "echo", func(_ context.Context, req geminio.Request, rsp geminio.Response) {
        rsp.SetData(req.Data())
    })
}
```

**客户端**

```go
opt := client.NewEndOptions()
opt.SetWaitRemoteRPCs("echo")

end, _ := client.NewEnd("tcp", "127.0.0.1:8080", opt)
defer end.Close()

rsp, _ := end.Call(context.TODO(), "echo", end.NewRequest([]byte("hello")))
fmt.Println(string(rsp.Data())) // => hello
```

没有 proto 文件，没有代码生成，没有 broker。完整用法见 [`docs/USAGE_cn.md`](./docs/USAGE_cn.md)。

## 可以用它搭什么

| 场景 | Geminio 带来的 | 示例 |
| --- | --- | --- |
| **内网穿透 / 反向隧道** | 一条向外连接承载双向控制 + 多条数据流 | [`examples/traversal`](./examples/traversal) |
| **聊天室 / IM** | 带 ack 的消息、客户端标识、自动重连 | [`examples/chatroom`](./examples/chatroom) |
| **消息队列** | 主题、ack、异步发布 | [`examples/mq`](./examples/mq) |
| **TCP 中继 / 代理** | 控制面上跑 `net.Conn` 兼容的数据流 | [`examples/relay`](./examples/relay) |
| **API 网关 / sidecar** | 双向 RPC + 多路复用 + 客户端身份 | 直接基于 `End` 构建 |

## 架构

<p align="center"><img src="./docs/design.png" width="80%"></p>

三层——**连接层**（物理 TCP、心跳、FSM）、**多路复用 / Dialogue 层**（逻辑流、路由、写调度）、**应用层**（RPC 和消息语义）——让 Geminio 对外只暴露一个统一的 `End`，而每层关注点各自隔离、各自可测。完整细节见 [`docs/MULTIPLEXING_cn.md`](./docs/MULTIPLEXING_cn.md)。

## 基准测试

Intel Core i5-6267U @ 2.90 GHz（2016 年双核笔记本 CPU）：

```
BenchmarkMessage-4     10117   112584 ns/op   1164 MB/s
BenchmarkEnd-4         11644    98586 ns/op   1329 MB/s
BenchmarkStream-4      12301    96955 ns/op   1351 MB/s
BenchmarkRPC-4          6960   165384 ns/op    792 MB/s
```

流吞吐约 1.3 GB/s，RPC 端到端往返约 790 MB/s——这还是十年前的笔记本 CPU。在你自己机器上跑 `make bench` 看看现代硬件的表现。

## 文档

- **使用手册** —— [`docs/USAGE_cn.md`](./docs/USAGE_cn.md)（[English](./docs/USAGE.md)）
- **API 参考** —— [pkg.go.dev/github.com/singchia/geminio](https://pkg.go.dev/github.com/singchia/geminio)
- **可跑示例** —— [`examples/`](./examples)
- **设计原理** —— [`docs/MULTIPLEXING_cn.md`](./docs/MULTIPLEXING_cn.md)（[English](./docs/MULTIPLEXING.md)）
- **Roadmap** —— [`ROADMAP_cn.md`](./ROADMAP_cn.md)（[English](./ROADMAP.md)）

## 参与开发

欢迎 PR 和 Issue，请看 [CONTRIBUTING.md](./CONTRIBUTING.md)。简单来说：一次 PR 只做一件事，代码带测试，提交前请跑 `make test`。

## 许可证

Apache 2.0 —— © Austin Zhai, 2023–2030。

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
