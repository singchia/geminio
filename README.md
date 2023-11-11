# Geminio

[![Go Reference](https://pkg.go.dev/badge/badge/github.com/singchia/geminio.svg)](https://pkg.go.dev/badge/github.com/singchia/geminio)
[![Go Report Card](https://goreportcard.com/badge/github.com/singchia/geminio)](https://goreportcard.com/report/github.com/singchia/geminio)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
![Platform](https://img.shields.io/badge/platform-linux|windows|mac-brightgreen.svg)

## 介绍

Geminio是一个提供**应用层**网络编程的库，命名取自[Geminio](https://harrypotter.fandom.com/wiki/Doubling_Charm)，寓意有三，一是客户端和服务端连接的对等性，二是体现多路复用下会话的轻量性，如同复制咒语一样非常容易从一个连接上获取另一个抽象连接，三是这个库有着魔法一样的能力，集成这个库能让你的网络应用程序的事半功倍。

这个库的诞生是因为市面上缺少如双向RPC、消息收发确认、裸连接管理、多会话和多路复用等多综合能力的库，而常常我们在开发例如消息队列、即时通讯、接入层网关、内网穿透、代理等应用软件或中间件时都严重依赖这些抽象，故此我开发了这个网络程序库，以能够让上层软件开发十分轻松。

## 架构

<img src="./docs/biz-arch.png" width="100%" height="100%">

### 接口

本库的所有抽象基本都在首页```geminio.go```里，从End开始结合上面架构图即可理解本库的设计，当然你也可以跳到下面的使用章节直接看示例。

```
type RawRPCMessager interface {
	// raw
	Raw
	// rpc
	RPCer
	// message
	Messager
}

type Stream interface {
	// a stream is a geminio
	RawRPCMessager
	// meta info for a stream
	StreamID() uint64
	ClientID() uint64
	Meta() []byte
}
	
// Stream multiplexer
type Multiplexer interface {
	OpenStream(opts ...*options.OpenStreamOptions) (Stream, error)
	AcceptStream() (Stream, error)
	ListStreams() []Stream
}
	
type End interface {
	// End is a default stream with streamID 1
	// Close on default stream will close all from the End
	Stream
	// End is a stream multiplexer
	Multiplexer
}

```

### 特性

* 基本RPC（注册和调用）
* 双向RPC（双方注册和调用）
* 消息收发确认（消息一致性保障）
* 同步/异步消息（等待返回、异步等待）
* 连接多路复用（单tcp/udp连接上抽象无数tcp/udp连接）
* 连接标识（唯一ClientID和唯一StreamID）
* 支持net.Conn和net.Listener抽象
* 高可用（RetryEnd的持续重连机制）
* 测试充分（压力测试、Chaos测试、运行时PProf分析等）
* 还有非常多特性等待你的发掘

## 使用

### 获得End

服务端End：

```

ln, err := server.Listen("tcp", "127.0.0.1:8080", opt)
if err != nil {
	log.Errorf("server listen err: %s", err)
	return
}

for {
	end, err := ln.AcceptEnd()
	if err != nil {
		log.Errorf("accept err: %s", err)
		break
	}
	// TODO 处理客户端End
}

```

客户端End：

```
end, err := client.NewEnd("tcp", "127.0.0.1:8080")
if err != nil {
	log.Errorf("server listen err: %s", err)
	return
}
```

以上双方获取的End，逻辑上代表了双方，持有这个连接即刻体验。

### 示例

* 消息和确认 [messager](./examples/messager)
* 简单消息队列  [mq](./examples/mq)
* 聊天室  [chatroom](./examples/chatroom)
* 中继器  [relay](./examples/relay)
* 内网穿透 [traversal](./examples/traversal)

## 实现

本库按照以下架构实现

<img src="./docs/implementation.png" width="60%" height="60%">

## 参与开发

如果你发现任何Bug，请随意提出Issue，项目Maintainers会及时响应相关问题。
 
 如果你希望能够提交Feature，更快速解决项目问题，满足以下简单条件下欢迎提交PR：
 
 * 代码风格保持一致
 * 每次提交一个Feature
 * 提交的代码都携带单元测试

## 许可证

© Austin Zhai, 2023-2030

Released under the [Apache License 2.0](https://github.com/singchia/geminio/blob/main/LICENSE)

