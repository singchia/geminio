<div align="center">

<img src="./docs/geminio.png" width="180">

**Eine Verbindung. Bidirektionales RPC, quittiertes Messaging und Stream-Multiplexing — hinter einem einzigen `net.Conn`.**

[![Go Reference](https://pkg.go.dev/badge/github.com/singchia/geminio.svg)](https://pkg.go.dev/github.com/singchia/geminio)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/geminio)](https://goreportcard.com/report/github.com/singchia/geminio)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-brightgreen.svg)](https://github.com/singchia/geminio)

[English](./README.md) | [简体中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Español](./README_es.md) | [Français](./README_fr.md) | [Deutsch](./README_de.md)

</div>

---

## Warum Geminio?

Sie bauen einen IM-Server, eine Message Queue, ein API-Gateway, einen Reverse-Tunnel für NAT-Durchbruch oder einen Service-Mesh-Sidecar. Damit das sauber wird, brauchen Sie **bidirektionales RPC**, **zuverlässiges Messaging mit Acks**, **mehrere logische Streams über eine einzige TCP-Verbindung**, **automatisches Reconnect**, und das Ganze muss sich zwanglos in Gos `net.Conn` / `net.Listener` einfügen.

Die übliche Antwort: gRPC fürs RPC, yamux/smux fürs Multiplexing, NATS oder ein eigenes Protokoll fürs Messaging, dazu ein Berg Kleberkode, um die Lebenszyklen in Gleichschritt zu halten. **Geminio liefert das ganze Paket hinter einer einzigen Schnittstelle.**

<p align="center"><img src="./docs/overview.png" width="85%"></p>

## Geminio im Vergleich

|                                       | gRPC              | yamux / smux | NATS | **Geminio** |
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
- ⚡ **~1,3 GB/s** Stream-Durchsatz auf einer 2016er Laptop-CPU (siehe [Benchmarks](#benchmarks)).
- 🧪 **Abgehärtet** — Unit-, Integration-, E2E-, Stress-, Chaos- und Regression-Tests.

## 60-Sekunden-Demo: Datei vom Server zum Client schicken

```bash
go get github.com/singchia/geminio
```

Jeder Geminio-Stream ist ein `net.Conn`, und jedes `End` ist ein `net.Listener`. Also ist eine serverinitiierte Dateiübertragung einfach `io.Copy` — ohne Framing, ohne Codec, ohne Broker.

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

| Szenario | Warum Geminio passt | Beispiel |
| --- | --- | --- |
| **NAT-Durchbruch / Reverse-Tunnel** | eine ausgehende Verbindung trägt bidirektionale Steuerung und viele Datenstreams | [`examples/traversal`](./examples/traversal) |
| **Chat / IM**                      | Messaging mit Ack, Client-IDs, automatisches Reconnect | [`examples/chatroom`](./examples/chatroom) |
| **Message Queue**                  | Topics, Ack, asynchrones Publish                      | [`examples/mq`](./examples/mq) |
| **TCP-Relay / Proxy**              | `net.Conn`-kompatible Streams über eine Steuerebene   | [`examples/relay`](./examples/relay) |
| **API-Gateway / Sidecar**          | bidirektionales RPC + Multiplexing + Client-Identität | direkt auf `End` aufbauen |

## Architektur

<p align="center"><img src="./docs/design.png" width="65%"></p>

Drei Schichten — **Connection** (physisches TCP, Heartbeat, FSM), **Multiplexer / Dialogue** (logische Streams, Routing, Write-Scheduling) und **Application** (RPC- und Messaging-Semantik) — lassen Geminio ein einheitliches `End` liefern und halten jede Zuständigkeit isoliert und testbar. Tiefer ins Detail geht es in [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md).

## Benchmarks

Apple M4 (Laptop-CPU von 2024):

```
BenchmarkMessage-10    253470    14770 ns/op   8874 MB/s
BenchmarkEnd-10        138441    25493 ns/op   5141 MB/s
BenchmarkStream-10     137670    26334 ns/op   4977 MB/s
BenchmarkRPC-10         83877    42875 ns/op   3057 MB/s
```

~5 GB/s auf Streams und End, ~3 GB/s auf Ende-zu-Ende-RPC-Roundtrips, ~8,9 GB/s auf kurzen Messages. Dieselbe Suite läuft auf einem Intel Core i5-6267U (Dual-Core-Laptop von 2016) bei ~1,3 GB/s auf Streams und ~790 MB/s auf RPC — die Bibliothek skaliert sauber mit der Hardware. Auf Ihrer eigenen Maschine: `make bench`.

## Dokumentation

- **Usage guide** — [`docs/USAGE.md`](./docs/USAGE.md)
- **API reference** — [pkg.go.dev/github.com/singchia/geminio](https://pkg.go.dev/github.com/singchia/geminio)
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
    <img alt="Activity Trends of singchia/geminio - Last 28 days" src="https://next.ossinsight.io/widgets/official/compose-activity-trends/thumbnail.png?repo_id=412119706&image_size=auto&color_scheme=light" width="815" height="auto">
  </picture>
</a>

Made with [OSS Insight](https://ossinsight.io/)

</div>
