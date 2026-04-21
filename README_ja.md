<div align="center">

<img src="./docs/geminio.png" width="180">

**1 本の接続。双方向 RPC、ACK 付きメッセージング、ストリーム多重化——すべてを単一の `net.Conn` の背後に。**

[![Go Reference](https://pkg.go.dev/badge/github.com/singchia/geminio.svg)](https://pkg.go.dev/github.com/singchia/geminio)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/geminio)](https://goreportcard.com/report/github.com/singchia/geminio)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-brightgreen.svg)](https://github.com/singchia/geminio)

[English](./README.md) | [简体中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Español](./README_es.md) | [Français](./README_fr.md) | [Deutsch](./README_de.md)

</div>

---

## なぜ Geminio？

あなたが作っているのは IM サーバー、メッセージキュー、API ゲートウェイ、NAT 越えのリバーストンネル、あるいはサービスメッシュのサイドカーかもしれません。どれをまともに作るにも、**双方向 RPC**、**ACK 付きの信頼できるメッセージング**、**1 本の TCP 接続上で複数の論理ストリーム**、**自動再接続**、そしてこれらすべてが Go の `net.Conn` / `net.Listener` と自然に噛み合うことが必要になります。

通常の答えは：RPC に gRPC、多重化に yamux/smux、メッセージングに NATS か独自プロトコル、そしてそれらのライフサイクルを合わせるための大量の糊付けコード。**Geminio はこの一式を単一のインターフェースの背後に提供します。**

<p align="center"><img src="./docs/overview.png" width="85%"></p>

## Geminio と既存の選択肢

|                                       | gRPC              | yamux / smux | NATS | **Geminio** |
| ------------------------------------- |:-----------------:|:------------:|:----:|:-----------:|
| リクエスト / レスポンス RPC            | ✅                | —            | —    | ✅          |
| **サーバーから クライアントへの RPC**  | ⚠️ streaming のみ  | —            | —    | ✅          |
| publish / ack 付きメッセージング       | —                 | —            | ✅   | ✅          |
| ストリーム多重化                      | ✅ (HTTP/2)       | ✅           | —    | ✅          |
| `net.Conn` / `net.Listener` 互換       | —                 | ✅           | —    | ✅          |
| クライアント側の自動再接続             | —                 | —            | ✅   | ✅          |
| 単一バイナリ、broker 不要              | ✅                | ✅           | —    | ✅          |

> "サーバーから クライアントへの RPC" とは、サーバーが `Call("method", ...)` でクライアントが登録したハンドラを呼び出せる、ということです。開いたストリームにメッセージを流すだけではありません。ほとんどの "RPC ライブラリ" が提供しない要素です。

## 特徴

- 🔄 **双方向 RPC** — どちら側もメソッドを登録し、相手を呼び出せます。
- 📨 **ACK 付きメッセージング** — `Publish` / `Receive` に配送確認あり。同期と非同期の両方に対応。
- 🔀 **ストリーム多重化** — 1 本の接続上にいくつでも論理ストリームを開けます。
- 🔌 **`net.Conn` / `net.Listener` 互換** — ストリームは Go の net インターフェースを扱う既存コードにそのまま差し込めます。
- 🆔 **安定した peer / stream ID** — `ClientID` と `StreamID` でルーティング、認可、トレーシングが直感的に書けます。
- 🔁 **自動再接続** — ネットワーク瞬断後、クライアントは透過的に復帰します。
- ⚡ **約 1.3 GB/s** のストリームスループット（2016 年のラップトップ CPU で計測。[ベンチマーク](#ベンチマーク)参照）。
- 🧪 **堅牢** — 単体・統合・E2E・ストレス・カオス・回帰テスト一式を完備。

## 60 秒 デモ：サーバーからクライアントへファイル送信

```bash
go get github.com/singchia/geminio
```

Geminio のストリームはすべて `net.Conn` で、`End` はすべて `net.Listener` です。ですから、サーバー起点のファイル転送はただの `io.Copy` になります——フレーミングもコーデックも broker も不要。

**サーバー** — クライアントを受け入れ、逆方向にストリームを開いてファイルを流し込みます。

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

**クライアント** — `End` を `net.Listener` として扱い、到着したストリームをそのままディスクへ保存します。

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

サーバーが送信を開始し、クライアントは自分で張った接続の上で受けます。あとは `io.Copy` がすべてこなします——ストリームが `net.Conn` を話すからです。RPC、双方向 RPC、ACK 付きメッセージング、その他の多重化パターン——完全な実行可能サンプルは [`docs/USAGE.md`](./docs/USAGE.md) に。

## こんなものが作れます

| シナリオ | Geminio が合う理由 | 例 |
| --- | --- | --- |
| **NAT 越え / リバーストンネル**       | 1 本の発信接続で双方向の制御と多数のデータストリームを運ぶ | [`examples/traversal`](./examples/traversal) |
| **チャット / IM**                   | ACK 付きメッセージ、クライアント単位 ID、自動再接続         | [`examples/chatroom`](./examples/chatroom) |
| **メッセージキュー**                 | トピック、ack、非同期 publish                          | [`examples/mq`](./examples/mq) |
| **TCP リレー / プロキシ**            | 制御プレーン上を走る `net.Conn` 互換ストリーム            | [`examples/relay`](./examples/relay) |
| **API ゲートウェイ / サイドカー**     | 双方向 RPC + 多重化 + クライアント ID                    | `End` の上に直接構築 |

## アーキテクチャ

<p align="center"><img src="./docs/design.png" width="65%"></p>

3 層構成——**Connection**（物理 TCP、ハートビート、FSM）、**Multiplexer / Dialogue**（論理ストリーム、ルーティング、書き込みスケジューリング）、**Application**（RPC とメッセージングのセマンティクス）——により、Geminio は統一された `End` を提供しつつ、各関心事を独立かつテスト可能に保ちます。詳細は [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md) で。

## ベンチマーク

Apple M4（2024 年のラップトップクラス CPU）：

```
BenchmarkMessage-10    235592    14600 ns/op   8977 MB/s   68495 ops/sec
BenchmarkEnd-10        137131    25537 ns/op   5132 MB/s   39159 ops/sec
BenchmarkStream-10     137937    25853 ns/op   5069 MB/s   38680 ops/sec
BenchmarkRPC-10         84450    42527 ns/op   3082 MB/s   23515 ops/sec
```

ストリーム約 3.9 万件/秒・5 GB/s、エンドツーエンドの RPC ラウンドトリップ約 2.3 万件/秒・3 GB/s、短メッセージ約 6.8 万件/秒・8.9 GB/s。同じスイートを 2016 年の Intel Core i5-6267U（2 コアラップトップ）で走らせてもストリーム約 1.3 GB/s、RPC 約 790 MB/s——ライブラリの性能はハードウェアに沿ってスケールします。自分のマシンで `make bench` をどうぞ。

## ドキュメント

- **Usage guide** — [`docs/USAGE.md`](./docs/USAGE.md)
- **API reference** — [pkg.go.dev/github.com/singchia/geminio](https://pkg.go.dev/github.com/singchia/geminio)
- **Runnable examples** — [`examples/`](./examples)
- **Design deep dive** — [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md)
- **Roadmap** — [`ROADMAP.md`](./ROADMAP.md)

## コントリビュート

PR と Issue を歓迎します。詳しくは [CONTRIBUTING.md](./CONTRIBUTING.md) を。要点：1 PR 1 機能、コードにはテストを添える、提出前に `make test`。

## ライセンス

Apache 2.0 — © Austin Zhai, 2023–2030。

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
