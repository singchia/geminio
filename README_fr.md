<div align="center">

<img src="./docs/geminio.png" width="180">

**Une seule connexion. RPC bidirectionnel, messagerie avec accusés de réception et multiplexage de streams — derrière un unique `net.Conn`.**

[![Go Reference](https://pkg.go.dev/badge/github.com/singchia/geminio.svg)](https://pkg.go.dev/github.com/singchia/geminio)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/geminio)](https://goreportcard.com/report/github.com/singchia/geminio)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-brightgreen.svg)](https://github.com/singchia/geminio)

[English](./README.md) | [简体中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Español](./README_es.md) | [Français](./README_fr.md) | [Deutsch](./README_de.md)

</div>

---

## Pourquoi Geminio ?

Vous construisez un serveur IM, une file de messages, un API gateway, un tunnel inverse pour traverser le NAT, ou un sidecar de service mesh. Pour que ça tienne la route, il vous faut du **RPC bidirectionnel**, une **messagerie fiable avec ack**, **plusieurs streams logiques sur une seule connexion TCP**, une **reconnexion automatique**, et que tout cela s'intègre proprement avec `net.Conn` / `net.Listener` de Go.

La réponse habituelle : gRPC pour le RPC, yamux/smux pour le multiplexage, NATS ou un protocole maison pour la messagerie, plus une pile de code de colle pour synchroniser leurs cycles de vie. **Geminio livre tout le kit derrière une seule interface.**

<p align="center"><img src="./docs/overview.png" width="85%"></p>

## Geminio face aux alternatives courantes

|                                       | gRPC              | yamux / smux | NATS | **Geminio** |
| ------------------------------------- |:-----------------:|:------------:|:----:|:-----------:|
| RPC requête / réponse                 | ✅                | —            | —    | ✅          |
| **RPC initié par le serveur**         | ⚠️ streaming seul. | —            | —    | ✅          |
| Messagerie publish/ack                | —                 | —            | ✅   | ✅          |
| Multiplexage de streams               | ✅ (HTTP/2)       | ✅           | —    | ✅          |
| Compatible `net.Conn` / `net.Listener` | —                | ✅           | —    | ✅          |
| Reconnexion automatique côté client   | —                 | —            | ✅   | ✅          |
| Binaire unique, sans broker           | ✅                | ✅           | —    | ✅          |

> "RPC initié par le serveur" veut dire que le serveur peut faire `Call("méthode", ...)` sur un handler enregistré côté client — pas juste pousser des messages dans un stream ouvert. C'est la pièce que la plupart des "bibliothèques RPC" ne fournissent pas.

## Fonctionnalités

- 🔄 **RPC bidirectionnel** — chaque côté enregistre des méthodes et appelle celles de l'autre.
- 📨 **Messagerie avec ack** — `Publish` / `Receive` avec confirmation de livraison, synchrone ou asynchrone.
- 🔀 **Multiplexage de streams** — ouvrez autant de streams logiques que vous voulez sur une connexion.
- 🔌 **Compatible `net.Conn` / `net.Listener`** — les streams s'intègrent à tout code qui parle les interfaces net de Go.
- 🆔 **IDs stables de peer et de stream** — `ClientID` et `StreamID` rendent routing, autorisation et tracing naturels.
- 🔁 **Reconnexion automatique** — le client se rétablit de façon transparente après une coupure réseau.
- ⚡ **~1.3 Go/s** de débit par stream sur un CPU de portable de 2016 (voir [Benchmarks](#benchmarks)).
- 🧪 **Éprouvé** — suites de tests unitaires, d'intégration, e2e, stress, chaos et régression.

## Démo en 60 secondes : envoyer un fichier du serveur au client

```bash
go get github.com/singchia/geminio
```

Chaque stream Geminio est un `net.Conn`, et chaque `End` est un `net.Listener`. Du coup, transférer un fichier du serveur vers le client, c'est un simple `io.Copy` — pas de framing, pas de codec, pas de broker.

**Serveur** — accepte les clients, ouvre un stream dans l'autre sens, et y déverse le fichier.

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

**Client** — traite le `End` comme un `net.Listener` et écrit chaque stream entrant sur disque.

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

Le serveur pousse, le client écoute sur sa propre connexion sortante. `io.Copy` s'occupe du reste, parce que le stream parle `net.Conn`. Exemples complets exécutables — RPC, RPC bidirectionnel, messagerie avec ack, plus de multiplexage — dans [`docs/USAGE.md`](./docs/USAGE.md).

## Ce que vous pouvez construire

| Scénario | Pourquoi Geminio est adapté | Exemple |
| --- | --- | --- |
| **Traversée de NAT / tunnel inverse** | une seule connexion sortante porte le contrôle bidirectionnel et plusieurs streams de données | [`examples/traversal`](./examples/traversal) |
| **Chat / IM**                         | messagerie avec ack, identifiants par client, reconnexion automatique | [`examples/chatroom`](./examples/chatroom) |
| **File de messages**                  | topics, ack, publish asynchrone | [`examples/mq`](./examples/mq) |
| **Relais TCP / proxy**                | streams compatibles `net.Conn` sur un plan de contrôle | [`examples/relay`](./examples/relay) |
| **API gateway / sidecar**             | RPC bidirectionnel + multiplexage + identité client | construit directement sur `End` |

## Architecture

<p align="center"><img src="./docs/design.png" width="65%"></p>

Trois couches — **Connection** (TCP physique, heartbeat, FSM), **Multiplexer / Dialogue** (streams logiques, routage, ordonnancement des écritures) et **Application** (sémantique RPC et messagerie) — permettent à Geminio d'exposer un `End` unifié tout en gardant chaque préoccupation isolée et testable. Approfondissement dans [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md).

## Benchmarks

Apple M4 (CPU de portable de 2024) :

```
BenchmarkMessage-10    253470    14770 ns/op   8874 MB/s
BenchmarkEnd-10        138441    25493 ns/op   5141 MB/s
BenchmarkStream-10     137670    26334 ns/op   4977 MB/s
BenchmarkRPC-10         83877    42875 ns/op   3057 MB/s
```

~5 Go/s sur les streams et End, ~3 Go/s sur les aller-retours RPC de bout en bout, ~8.9 Go/s sur les messages courts. La même suite sur un Intel Core i5-6267U (portable dual-core de 2016) tourne autour de 1.3 Go/s sur les streams et 790 Mo/s sur RPC — la bibliothèque passe à l'échelle proprement avec le matériel. Lancez `make bench` sur votre propre machine.

## Documentation

- **Usage guide** — [`docs/USAGE.md`](./docs/USAGE.md)
- **API reference** — [pkg.go.dev/github.com/singchia/geminio](https://pkg.go.dev/github.com/singchia/geminio)
- **Runnable examples** — [`examples/`](./examples)
- **Design deep dive** — [`docs/MULTIPLEXING.md`](./docs/MULTIPLEXING.md)
- **Roadmap** — [`ROADMAP.md`](./ROADMAP.md)

## Contribuer

Les PR et issues sont les bienvenues. Voir [CONTRIBUTING.md](./CONTRIBUTING.md). En résumé : une fonctionnalité par PR, des tests avec le code, exécutez `make test` avant de soumettre.

## Licence

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
