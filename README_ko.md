<div align="center">

<img src="./docs/geminio.png" width="180">

**연결 하나. 양방향 RPC, ACK 메시징, 스트림 다중화 — 단일 `net.Conn` 뒤에서 모두 해결.**

[![Go Reference](https://pkg.go.dev/badge/github.com/singchia/geminio.svg)](https://pkg.go.dev/github.com/singchia/geminio)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/geminio)](https://goreportcard.com/report/github.com/singchia/geminio)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-brightgreen.svg)](https://github.com/singchia/geminio)

[English](./README.md) | [简体中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Español](./README_es.md) | [Français](./README_fr.md) | [Deutsch](./README_de.md)

</div>

---

## 왜 Geminio 인가?

당신이 만드는 것이 IM 서버, 메시지 큐, API 게이트웨이, NAT 우회용 리버스 터널, 혹은 서비스 메쉬 사이드카라면 — 제대로 만들기 위해 필요한 것은 **양방향 RPC**, **ACK 기반 신뢰성 메시징**, **하나의 TCP 연결 위 여러 논리 스트림**, **자동 재연결**, 그리고 이 모든 것이 Go 의 `net.Conn` / `net.Listener` 와 자연스럽게 맞물리는 것입니다.

일반적인 답은: RPC 는 gRPC, 다중화는 yamux/smux, 메시징은 NATS 나 커스텀 프로토콜, 그리고 이들의 생명주기를 맞추는 연결 코드 한 뭉치. **Geminio 는 이 모두를 하나의 인터페이스 뒤에 담아 줍니다.**

<p align="center"><img src="./docs/overview.png" width="85%"></p>

## Geminio 와 기존 선택지 비교

|                                       | gRPC              | yamux / smux | NATS | **Geminio** |
| ------------------------------------- |:-----------------:|:------------:|:----:|:-----------:|
| 요청 / 응답 RPC                       | ✅                | —            | —    | ✅          |
| **서버→클라이언트 RPC 호출**          | ⚠️ streaming 만    | —            | —    | ✅          |
| publish + ack 메시징                  | —                 | —            | ✅   | ✅          |
| 스트림 다중화                         | ✅ (HTTP/2)       | ✅           | —    | ✅          |
| `net.Conn` / `net.Listener` 호환       | —                 | ✅           | —    | ✅          |
| 클라이언트 자동 재연결                | —                 | —            | ✅   | ✅          |
| 단일 바이너리, broker 불필요           | ✅                | ✅           | —    | ✅          |

> "서버→클라이언트 RPC" 란 서버가 `Call("method", ...)` 로 클라이언트에 등록된 핸들러를 실제로 호출할 수 있다는 뜻입니다. 열린 스트림에 메시지를 흘리는 것이 아닙니다. 대부분의 "RPC 라이브러리" 에는 없는 기능입니다.

## 기능

- 🔄 **양방향 RPC** — 어느 쪽이든 메서드를 등록하고 상대를 호출할 수 있습니다.
- 📨 **ACK 메시징** — `Publish` / `Receive` 에 전송 확인이 포함됩니다. 동기·비동기 모두 지원.
- 🔀 **스트림 다중화** — 하나의 연결 위에서 원하는 만큼 논리 스트림을 열 수 있습니다.
- 🔌 **`net.Conn` / `net.Listener` 호환** — Go 의 net 인터페이스를 쓰는 기존 코드에 스트림을 그대로 꽂아 넣습니다.
- 🆔 **안정적인 peer / stream ID** — `ClientID` 와 `StreamID` 로 라우팅, 권한, 트레이싱이 간결해집니다.
- 🔁 **자동 재연결** — 네트워크 끊김 이후 클라이언트가 투명하게 복귀합니다.
- ⚡ **약 1.3 GB/s** 스트림 처리량(2016 년 노트북 CPU 기준, [벤치마크](#벤치마크) 참고).
- 🧪 **검증됨** — 단위·통합·E2E·스트레스·카오스·회귀 테스트 구성.

## 60 초 데모: 서버가 클라이언트로 파일 전송

```bash
go get github.com/singchia/geminio
```

Geminio 의 모든 스트림은 `net.Conn` 이고, 모든 `End` 는 `net.Listener` 입니다. 그래서 서버가 시작하는 파일 전송은 그냥 `io.Copy` 입니다 — 프레이밍도, 코덱도, broker 도 없습니다.

**서버** — 클라이언트를 받으면, 거꾸로 스트림을 열어 파일을 흘려보냅니다.

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

**클라이언트** — `End` 를 `net.Listener` 로 쓰고, 들어오는 스트림을 그대로 디스크에 씁니다.

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

서버가 주도하고, 클라이언트는 자기가 연 연결 위에서 수신합니다. 나머지는 `io.Copy` 가 처리합니다 — 스트림이 `net.Conn` 을 말하기 때문입니다. RPC, 양방향 RPC, ACK 메시징, 더 많은 다중화 패턴 — 실제로 실행 가능한 전체 예제는 [`docs/USAGE.md`](./docs/USAGE.md) 에 있습니다.

## 무엇을 만들 수 있나요

| 시나리오 | Geminio 가 잘 맞는 이유 | 예제 |
| --- | --- | --- |
| **NAT 우회 / 리버스 터널**     | 발신 연결 하나가 양방향 제어와 다수의 데이터 스트림을 운반 | [`examples/traversal`](./examples/traversal) |
| **채팅 / IM**                | ACK 메시징, 클라이언트 ID, 자동 재연결                  | [`examples/chatroom`](./examples/chatroom) |
| **메시지 큐**                 | 토픽, ack, 비동기 publish                             | [`examples/mq`](./examples/mq) |
| **TCP 릴레이 / 프록시**        | 제어 플레인 위의 `net.Conn` 호환 스트림                | [`examples/relay`](./examples/relay) |
| **API 게이트웨이 / 사이드카**   | 양방향 RPC + 다중화 + 클라이언트 ID                    | `End` 위에 바로 구현 |

## 아키텍처

<p align="center"><img src="./docs/design.png" width="65%"></p>

세 계층 — **Connection**(물리 TCP, 하트비트, FSM), **Multiplexer / Dialogue**(논리 스트림, 라우팅, 쓰기 스케줄링), **Application**(RPC·메시징 의미) — 덕분에 Geminio 는 통합된 `End` 를 외부로 내놓으면서도 각 관심사를 독립적이고 테스트 가능하게 유지합니다. 자세한 설명은 [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md) 에서.

## 벤치마크

Intel Core i5-6267U @ 2.90 GHz(2016 년 듀얼 코어 노트북):

```
BenchmarkMessage-4     10117   112584 ns/op   1164 MB/s
BenchmarkEnd-4         11644    98586 ns/op   1329 MB/s
BenchmarkStream-4      12301    96955 ns/op   1351 MB/s
BenchmarkRPC-4          6960   165384 ns/op    792 MB/s
```

스트림 약 1.3 GB/s, 엔드투엔드 RPC 왕복 약 790 MB/s — 10 년 된 노트북 CPU 에서의 값입니다. 자기 머신에서 `make bench` 를 돌려보세요.

## 문서

- **Usage guide** — [`docs/USAGE.md`](./docs/USAGE.md)
- **API reference** — [pkg.go.dev/github.com/singchia/geminio](https://pkg.go.dev/github.com/singchia/geminio)
- **Runnable examples** — [`examples/`](./examples)
- **Design deep dive** — [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md)
- **Roadmap** — [`ROADMAP.md`](./ROADMAP.md)

## 기여

PR 과 이슈를 환영합니다. 자세한 내용은 [CONTRIBUTING.md](./CONTRIBUTING.md) 에. 요점: PR 하나에 기능 하나, 코드와 함께 테스트, 제출 전 `make test` 실행.

## 라이선스

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
