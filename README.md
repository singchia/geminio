<div align="center">

<img src="./docs/gemino.png" width="180">

**One connection. Bidirectional RPC, acked messaging, and stream multiplexing — behind a single `net.Conn`.**

[![Go Reference](https://pkg.go.dev/badge/github.com/singchia/gemino.svg)](https://pkg.go.dev/github.com/singchia/gemino)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/gemino)](https://goreportcard.com/report/github.com/singchia/gemino)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-brightgreen.svg)](https://github.com/singchia/gemino)

[English](./README.md) | [简体中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Español](./README_es.md) | [Français](./README_fr.md) | [Deutsch](./README_de.md)

</div>

---

## Why Gemino?

You're building an IM server, a message queue, an API gateway, a reverse tunnel for NAT traversal, or a service-mesh sidecar. To get it right you need **bidirectional RPC**, **reliable messaging with acks**, **many logical streams over one TCP connection**, **automatic reconnect**, and all of it has to play nicely with Go's `net.Conn` / `net.Listener`.

The usual answer is: gRPC for RPC, yamux/smux for multiplexing, NATS or a custom protocol for messaging, and a tangle of glue to keep their lifecycles in sync. **Gemino offers the whole bundle behind one interface.**

<p align="center"><img src="./docs/overview.png" width="85%"></p>

## Gemino vs. the usual suspects

|                                       | gRPC              | yamux / smux | NATS | **Gemino** |
| ------------------------------------- |:-----------------:|:------------:|:----:|:-----------:|
| Request / response RPC                | ✅                | —            | —    | ✅          |
| **Server-initiated RPC to client**    | ⚠️ streaming only | —            | —    | ✅          |
| Messaging with publish/ack            | —                 | —            | ✅   | ✅          |
| Stream multiplexing                   | ✅ (HTTP/2)       | ✅           | —    | ✅          |
| Drop-in `net.Conn` / `net.Listener`   | —                 | ✅           | —    | ✅          |
| Client-side auto-reconnect            | —                 | —            | ✅   | ✅          |
| Single binary, no broker              | ✅                | ✅           | —    | ✅          |

> "Server-initiated RPC" means the server can `Call("method", ...)` a handler the client registered — not just push messages on an open stream. It's the piece most "RPC libraries" don't ship.

## Features

- 🔄 **Bidirectional RPC** — either side can register methods and call the other's.
- 📨 **Acked messaging** — `Publish` / `Receive` with delivery confirmation; sync and async.
- 🔀 **Stream multiplexing** — open any number of logical streams over one connection.
- 🔌 **`net.Conn` / `net.Listener` compatible** — streams drop into any code that speaks Go's net interfaces.
- 🆔 **Stable peer & stream IDs** — `ClientID` and `StreamID` make routing, authz, and tracing straightforward.
- 🔁 **Auto-reconnect** — client resumes transparently after network blips.
- ⚡ **~5 GB/s** per-stream throughput and **~23K RPC round-trips/sec** on a laptop-class CPU (see [Benchmarks](#benchmarks)).
- 🧪 **Hardened** — unit, integration, e2e, stress, chaos, and regression test suites.

## 60-second demo: push a file from server to client

```bash
go get github.com/singchia/gemino
```

Every Gemino stream is a `net.Conn`, and every `End` is a `net.Listener`. So a server-initiated file transfer is just `io.Copy` — no framing, no codec, no broker.

**Server** — accept clients, open a stream back, copy the file in.

```go
ln, _ := server.Listen("tcp", "127.0.0.1:8080")
for {
    end, _ := ln.AcceptEnd()
    go func() {
        stream, _ := end.OpenStream()
        defer stream.Close()
        f, _ := os.Open("payload.bin")
        defer f.Close()
        io.Copy(stream, f)
    }()
}
```

**Client** — treat the `End` as a `net.Listener`, save each incoming stream.

```go
end, _ := client.NewEnd("tcp", "127.0.0.1:8080")
defer end.Close()
for {
    conn, _ := end.Accept()
    f, _ := os.Create("received.bin")
    io.Copy(f, conn)
    f.Close()
    conn.Close()
}
```

The server initiates. The client listens on its own dial-out connection. `io.Copy` does the rest because the stream speaks `net.Conn`. Full runnable examples — RPC, bidirectional RPC, acked messaging, more multiplexing — in [`docs/USAGE.md`](./docs/USAGE.md).

## What you can build

| Scenario | Why Gemino fits | Example |
| --- | --- | --- |
| **NAT traversal / reverse tunnel** | one outbound connection carries bidirectional control + many data streams | [`examples/traversal`](./examples/traversal) |
| **Chatroom / IM** | acked messaging, per-client IDs, auto-reconnect | [`examples/chatroom`](./examples/chatroom) |
| **Message queue** | topics, ack, async publish | [`examples/mq`](./examples/mq) |
| **TCP relay / proxy** | `net.Conn`-compatible streams over a control plane | [`examples/relay`](./examples/relay) |
| **API gateway / sidecar** | bidirectional RPC + multiplexing + client identity | build directly on `End` |

## Architecture

<p align="center"><img src="./docs/design.png" width="65%"></p>

Three layers — **Connection** (physical TCP, heartbeat, FSM), **Multiplexer / Dialogue** (logical streams, routing, write scheduling), and **Application** (RPC and messaging semantics) — let Gemino ship one unified `End` while keeping each concern isolated and testable. Deep dive in [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md).

## Benchmarks

Apple M4 (2024 laptop-class CPU):

```
BenchmarkMessage-10    235592    14600 ns/op   8977 MB/s   68495 ops/sec
BenchmarkEnd-10        137131    25537 ns/op   5132 MB/s   39159 ops/sec
BenchmarkStream-10     137937    25853 ns/op   5069 MB/s   38680 ops/sec
BenchmarkRPC-10         84450    42527 ns/op   3082 MB/s   23515 ops/sec
```

~39K streams/sec at 5 GB/s, ~23K RPC round-trips/sec at 3 GB/s, ~68K short-message ops/sec at 8.9 GB/s. Run `make bench` on your own box.

## Documentation

- **Usage guide** — [`docs/USAGE.md`](./docs/USAGE.md)
- **API reference** — [pkg.go.dev/github.com/singchia/gemino](https://pkg.go.dev/github.com/singchia/gemino)
- **Runnable examples** — [`examples/`](./examples)
- **Design deep dive** — [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md)
- **Roadmap** — [`ROADMAP.md`](./ROADMAP.md)

## Contributing

PRs and issues are welcome. See [CONTRIBUTING.md](./CONTRIBUTING.md). In short: one feature per PR, tests alongside code, run `make test` before submitting.

## License

Apache 2.0 — © Austin Zhai, 2023–2030.

---

<div align="center">

<a href="https://next.ossinsight.io/widgets/official/compose-activity-trends?repo_id=412119706" target="_blank" style="display: block" align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://next.ossinsight.io/widgets/official/compose-activity-trends/thumbnail.png?repo_id=412119706&image_size=auto&color_scheme=dark" width="815" height="auto">
    <img alt="Activity Trends of singchia/gemino - Last 28 days" src="https://next.ossinsight.io/widgets/official/compose-activity-trends/thumbnail.png?repo_id=412119706&image_size=auto&color_scheme=light" width="815" height="auto">
  </picture>
</a>

Made with [OSS Insight](https://ossinsight.io/)

</div>
