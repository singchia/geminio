<div align="center">

<img src="./docs/gemino.png" width="180">

**Eine Verbindung. Bidirektionales RPC, quittiertes Messaging und Stream-Multiplexing — hinter einem einzigen `net.Conn`.**

[![Go Reference](https://pkg.go.dev/badge/github.com/singchia/gemino.svg)](https://pkg.go.dev/github.com/singchia/gemino)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/gemino)](https://goreportcard.com/report/github.com/singchia/gemino)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-brightgreen.svg)](https://github.com/singchia/gemino)

[English](./README.md) | [简体中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Español](./README_es.md) | [Français](./README_fr.md) | [Deutsch](./README_de.md)

</div>

---

## Warum Gemino?

Sie bauen einen IM-Server, eine Message Queue, ein API-Gateway, einen Reverse-Tunnel für NAT-Durchbruch oder einen Service-Mesh-Sidecar. Damit das sauber wird, brauchen Sie **bidirektionales RPC**, **zuverlässiges Messaging mit Acks**, **mehrere logische Streams über eine einzige TCP-Verbindung**, **automatisches Reconnect**, und das Ganze muss sich zwanglos in Gos `net.Conn` / `net.Listener` einfügen.

Die übliche Antwort: gRPC fürs RPC, yamux/smux fürs Multiplexing, NATS oder ein eigenes Protokoll fürs Messaging, dazu ein Berg Kleberkode, um die Lebenszyklen in Gleichschritt zu halten. **Gemino liefert das ganze Paket hinter einer einzigen Schnittstelle.**

<p align="center"><img src="./docs/overview.png" width="85%"></p>

## Gemino im Vergleich

|                                       | gRPC              | yamux / smux | NATS | **Gemino** |
| ------------------------------------- |:-----------------:|:------------:|:----:|:-----------:|
| Request / Response RPC                | ✅                | —            | —    | ✅          |
| **Serverinitiierter RPC-Aufruf zum Client** | ⚠️ nur streaming | —        | —    | ✅          |
| Messaging mit publish/ack             | —                 | —            | ✅   | ✅          |
| Stream-Multiplexing                   | ✅ (HTTP/2)       | ✅           | —    | ✅          |
| `net.Conn` / `net.Listener`-kompatibel | —                | ✅           | —    | ✅          |
| Automatisches Reconnect (Client)      | —                 | —            | ✅   | ✅          |
| Einzelbinary, kein Broker             | ✅                | ✅           | —    | ✅          |

> "Serverinitiierter RPC-Aufruf" heißt, der Server kann mit `Call("method", ...)` einen Handler aufrufen, den der Client registriert hat — nicht bloß Nachrichten in einen offenen Stream schieben. Genau dieses Stück fehlt den meisten "RPC-Bibliotheken".

## Features

- 🔄 **Bidirektionales RPC** — beide Seiten können Methoden registrieren und die des Gegenübers aufrufen.
- 📨 **Quittiertes Messaging** — `Publish` / `Receive` mit Zustellbestätigung, synchron wie asynchron.
- 🔀 **Stream-Multiplexing** — beliebig viele logische Streams über eine Verbindung.
- 🔌 **`net.Conn` / `net.Listener`-kompatibel** — Streams passen in jeden Code, der die net-Schnittstellen von Go spricht.
- 🆔 **Stabile Peer- und Stream-IDs** — `ClientID` und `StreamID` machen Routing, Autorisierung und Tracing leicht.
- 🔁 **Automatisches Reconnect** — der Client kommt nach Netzaussetzern transparent zurück.
- ⚡ **~5 GB/s** Stream-Durchsatz und **~23K RPC-Roundtrips/s** auf einer Laptop-CPU (siehe [Benchmarks](#benchmarks)).
- 🧪 **Abgehärtet** — Unit-, Integration-, E2E-, Stress-, Chaos- und Regression-Tests.

## 60-Sekunden-Demo: Datei vom Server zum Client schicken

```bash
go get github.com/singchia/gemino
```

Jeder Gemino-Stream ist ein `net.Conn`, und jedes `End` ist ein `net.Listener`. Also ist eine serverinitiierte Dateiübertragung einfach `io.Copy` — ohne Framing, ohne Codec, ohne Broker.

**Server** — Clients annehmen, einen Stream in Gegenrichtung öffnen und die Datei hineinkopieren.

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

**Client** — das `End` als `net.Listener` verwenden und jeden eingehenden Stream auf Platte schreiben.

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

Der Server sendet aktiv, der Client lauscht auf seiner eigenen ausgehenden Verbindung. Den Rest erledigt `io.Copy`, weil der Stream `net.Conn` spricht. Vollständig lauffähige Beispiele — RPC, bidirektionales RPC, quittiertes Messaging, weiteres Multiplexing — in [`docs/USAGE.md`](./docs/USAGE.md).

## Wofür es sich eignet

| Szenario | Warum Gemino passt | Beispiel |
| --- | --- | --- |
| **NAT-Durchbruch / Reverse-Tunnel** | eine ausgehende Verbindung trägt bidirektionale Steuerung und viele Datenstreams | [`examples/traversal`](./examples/traversal) |
| **Chat / IM**                      | Messaging mit Ack, Client-IDs, automatisches Reconnect | [`examples/chatroom`](./examples/chatroom) |
| **Message Queue**                  | Topics, Ack, asynchrones Publish                      | [`examples/mq`](./examples/mq) |
| **TCP-Relay / Proxy**              | `net.Conn`-kompatible Streams über eine Steuerebene   | [`examples/relay`](./examples/relay) |
| **API-Gateway / Sidecar**          | bidirektionales RPC + Multiplexing + Client-Identität | direkt auf `End` aufbauen |

## Architektur

<p align="center"><img src="./docs/design.png" width="65%"></p>

Drei Schichten — **Connection** (physisches TCP, Heartbeat, FSM), **Multiplexer / Dialogue** (logische Streams, Routing, Write-Scheduling) und **Application** (RPC- und Messaging-Semantik) — lassen Gemino ein einheitliches `End` liefern und halten jede Zuständigkeit isoliert und testbar. Tiefer ins Detail geht es in [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md).

## Benchmarks

Apple M4 (Laptop-CPU von 2024):

```
BenchmarkMessage-10    235592    14600 ns/op   8977 MB/s   68495 ops/sec
BenchmarkEnd-10        137131    25537 ns/op   5132 MB/s   39159 ops/sec
BenchmarkStream-10     137937    25853 ns/op   5069 MB/s   38680 ops/sec
BenchmarkRPC-10         84450    42527 ns/op   3082 MB/s   23515 ops/sec
```

~39K Streams/s bei 5 GB/s, ~23K Ende-zu-Ende-RPC-Roundtrips/s bei 3 GB/s, ~68K kurze Messages/s bei 8,9 GB/s. Auf Ihrer eigenen Maschine: `make bench`.

## Dokumentation

- **Usage guide** — [`docs/USAGE.md`](./docs/USAGE.md)
- **API reference** — [pkg.go.dev/github.com/singchia/gemino](https://pkg.go.dev/github.com/singchia/gemino)
- **Runnable examples** — [`examples/`](./examples)
- **Design deep dive** — [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md)
- **Roadmap** — [`ROADMAP.md`](./ROADMAP.md)

## Mitmachen

PRs und Issues sind willkommen. Siehe [CONTRIBUTING.md](./CONTRIBUTING.md). Kurz: ein Feature pro PR, Tests neben dem Code, vor dem Absenden `make test` laufen lassen.

## Lizenz

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
