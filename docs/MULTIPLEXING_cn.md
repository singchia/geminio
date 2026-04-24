# Geminio 多路复用设计

[English](./MULTIPLEXING.md) | [简体中文](./MULTIPLEXING_cn.md)

## 概述

Geminio 是一个支持多路复用的网络框架，允许在单个物理连接上同时运行多个逻辑对话（Dialogue）。这种设计可以显著减少连接数，提高资源利用率。

## 核心概念

### 1. 分层架构

框架采用三层架构：

```
┌─────────────────────────────────────┐
│        应用层 (Application)          │
│   使用 Dialogue 进行数据传输         │
└─────────────────────────────────────┘
              | |
              | |
              v ^
┌─────────────────────────────────────┐
│     多路复用层 (Multiplexer)          │
│  DialogueMgr 管理多个 Dialogue       │
└─────────────────────────────────────┘
              | |
              | |
              v ^
┌─────────────────────────────────────┐
│       连接层 (Connection)            │
│   管理物理 TCP 连接                  │
└─────────────────────────────────────┘
```

### 2. 关键组件

#### 2.1 Connection（连接层）

- **职责**：管理底层 TCP 连接
- **功能**：
  - 连接建立与断开（`ConnPacket` / `ConnAckPacket`）
  - 心跳保活（`HeartbeatPacket`）
  - 数据包的编码/解码
  - 连接状态管理（FSM 状态机）

#### 2.2 DialogueMgr（对话管理器）

- **职责**：管理所有对话，负责数据包路由和写调度
- **核心数据结构**：

  ```go
  type dialogueMgr struct {
      cn conn.Conn                               // 底层连接
      dialogues map[uint64]*dialogue             // 已建立的对话
      negotiatingDialogues map[uint64]*dialogue  // 协商中的对话
      schedulerDialogueChs []*dialogueWriteCh    // 调度器对话列表
  }
  ```

- **关键功能**：
  1. **数据包路由**（`handlePkt`）：
     - 从连接层读取数据包
     - 根据 `dialogueID` 路由到对应的 Dialogue
     - 处理新对话的建立请求（`SessionPacket`）
  2. **写调度器**（`writeScheduler`）：
     - 轮询调度所有 Dialogue 的写操作
     - 确保公平性：每个 Dialogue 每轮最多处理一个数据包
     - 使用 `reflect.Select` 实现动态 channel 选择

#### 2.3 Dialogue（对话）

- **职责**：单个逻辑对话，提供独立的读写接口
- **核心数据结构**：

  ```go
  type dialogue struct {
      dialogueID uint64               // 对话唯一标识
      cn conn.Conn                    // 底层连接
      readInCh  chan packet.Packet    // 接收数据包通道
      writeOutCh chan packet.Packet   // 发送数据包通道
      readOutCh chan packet.Packet    // 输出给应用层的通道
      fsm *yafsm.FSM                  // 状态机
      rateLimiter *RateLimiter        // 速率限制器
  }
  ```

- **状态机**：
  - `INIT → SESSION_SENT / SESSION_RECV → SESSIONED → DISMISS_SENT / DISMISS_RECV → DISMISSED → FINI`
  - 使用 FSM 管理对话生命周期

## 多路复用原理

### 1. 对话标识（DialogueID）

每个数据包都携带 `dialogueID`（在数据包中称为 `SessionID`），用于标识该数据包属于哪个对话：

```go
type SessionAbove interface {
    SessionID() uint64      // 获取对话 ID
    SetSessionID(uint64)    // 设置对话 ID
}
```

### 2. 数据包路由机制

#### 2.1 接收路径（下行）

```
Network Packet
    |
    v
Connection.readPkt()         [解码数据包]
    |
    v
Connection.readOutCh         [连接层输出通道]
    |
    v
DialogueMgr.readPkt()        [读取连接层数据包]
    |
    v
DialogueMgr.handlePkt()      [按 dialogueID 路由]
    |
    +-- SessionPacket     --> 创建新 Dialogue
    +-- SessionAckPacket  --> 协商中的 Dialogue
    `-- DataPacket        --> 已建立的 Dialogue
    |
    v
Dialogue.readInCh            [对话接收通道]
    |
    v
Dialogue.handlePkt()         [对话处理数据包]
    |
    v
Dialogue.readOutCh           [对话输出通道]
    |
    v
Application Read
```

#### 2.2 发送路径（上行）

```
Application Write
    |
    v
Dialogue.Write()             [设置 dialogueID]
    |
    v
Dialogue.writeOutCh          [对话发送通道]
    |
    v
DialogueMgr.writeScheduler() [轮询调度器]
    |
    +-- 轮询每个 Dialogue.writeOutCh
    +-- 每 Dialogue 每轮最多 1 个包
    `-- reflect.Select 动态选择
    |
    v
Dialogue.handleOut()         [处理发送数据包]
    |
    v
Connection.Write()           [写入连接层]
    |
    v
Connection.writeOutCh        [连接层发送通道]
    |
    v
Connection.writePkt()        [编码并发送到网络]
    |
    v
Network Send
```

### 3. 写调度器（Write Scheduler）

写调度器是确保多路复用公平性的关键组件。

#### 3.1 轮询机制

```go
// 伪代码示例
func (dm *dialogueMgr) writeScheduler() {
    for {
        // 1. 非阻塞轮询：尝试从每个 Dialogue 读取一个包
        for i := 0; i < len(dialogueChs); i++ {
            idx := (roundRobinIndex + i) % len(dialogueChs)
            select {
            case pkt := <-dialogueChs[idx].writeOutCh:
                processPacket(pkt)
                roundRobinIndex = (idx + 1) % len(dialogueChs)
                break
            default:
                // 该 Dialogue 没有数据，继续下一个
                continue
            }
        }

        // 2. 所有 Dialogue 都没数据时阻塞等待
        if noDataAvailable {
            reflect.Select(cases) // 动态构建 select cases
        }
    }
}
```

#### 3.2 公平性保证

- **轮询索引**：使用原子操作维护轮询索引，确保每个 Dialogue 都有机会。
- **每轮限制**：每个 Dialogue 每轮最多处理一个数据包，防止独占。
- **动态更新**：Dialogue 增删时，调度器自动更新 channel 列表。

### 4. 对话生命周期

#### 4.1 建立对话

```
Initiator                        Recipient
   |                               |
   |-- SessionPacket ------------->|
   |   (negotiatingID)             |  创建新 Dialogue
   |                               |  加入 negotiatingDialogues
   |                               |
   |<-- SessionAckPacket ----------|
   |   (dialogueID)                |  分配 dialogueID
   |                               |  移入 dialogues
   |                               |
   |-- DataPacket(dialogueID) ---> |
   |                               |  路由到对应 Dialogue
```

#### 4.2 关闭对话（4 次挥手）

```
Initiator                        Recipient
   |                               |
   |  状态: SESSIONED              |  状态: SESSIONED
   |                               |
   |-- DismissPacket ------------->|
   |  状态: DISMISS_SENT           |  状态: DISMISS_RECV
   |                               |  发送 DismissAckPacket
   |                               |  Close() -> 发送 DismissPacket
   |<-- DismissAckPacket ----------|
   |  状态: DISMISS_HALF           |  状态: DISMISS_SENT
   |                               |
   |<-- DismissPacket -------------|
   |  状态: DISMISS_HALF           |  (等待 DismissAckPacket)
   |  发送 DismissAckPacket        |
   |-- DismissAckPacket ---------->|
   |  状态: DISMISSED              |  状态: DISMISS_HALF
   |                               |
   |  关闭完成                     |  状态: DISMISSED
   |                               |  关闭完成
```

### 5. 消息顺序与到达语义

端到端通信系统中，有两个关键问题要解决。

#### 5.1 顺序一致性

**问题**：发送和接收的消息顺序是否一致？

**解决方案**：

1. **Channel FIFO 保证**：
   - 所有数据包通过 Go channel 传递
   - channel 是 FIFO，天然保证顺序
   - 数据包按发送顺序排队
2. **状态机顺序处理**：

   ```go
   // Dialogue 的 FSM 使用 WithInSeq() 选项
   dg.fsm = yafsm.NewFSM(yafsm.WithInSeq())
   ```

   - 状态机按顺序处理事件，确保状态转换的一致性
   - 防止乱序事件导致状态不一致
3. **阻塞发送保证顺序**：
   - 数据包通过阻塞 channel 发送，确保按顺序发送到网络
   - 发送方必须等前一个包完成才能发送下一个
4. **单线程处理**：
   - 每个 Dialogue 有独立的 `handlePkt()` goroutine
   - 单线程处理接收的数据包，保证处理顺序

**示例**：

```
发送方顺序: Packet1 → Packet2 → Packet3
           ↓         ↓         ↓
Channel:   [P1][P2][P3]        (FIFO 队列)
           ↓         ↓         ↓
接收方顺序: Packet1 → Packet2 → Packet3  (顺序一致)
```

#### 5.2 消息到达语义

**问题**：最多一次、至少一次、准确处理一次？

框架在数据包头中定义了 `Cnss`（Consistency）字段，支持三种语义：

```go
const (
    CnssAtMostOnce      Cnss = 0x01  // 最多一次
    CnssAtLeastOnce     Cnss = 0x02  // 至少一次
    CnssAtEffectiveOnce Cnss = 0x03  // 准确处理一次
)
```

##### 5.2.1 最多一次（At-Most-Once）

**特点**：

- 消息可能丢失，但不会重复
- 不等待确认，发送即返回
- 适用于对可靠性要求不高的场景

**实现**：

```go
if msg.Cnss() == options.CnssAtMostOnce {
    // 不等待确认，直接发送
    sm.writeInCh <- pkt
    return nil
}
```

**适用场景**：日志上报、指标统计等可容忍丢失的场景。

##### 5.2.2 至少一次（At-Least-Once）

**特点**：

- 消息不会丢失，但可能重复
- 使用确认机制，未收到确认会重传
- 接收方需处理重复消息

**实现机制**：

1. **发送方**：

   ```go
   // 创建同步等待
   sync = sm.shub.New(msg.ID(), synchub.WithTimeout(msg.Timeout()))
   sm.writeInCh <- pkt

   // 等待确认
   event := <-sync.C()
   if event.Error != nil {
       // 超时或错误，可重传
       return event.Error
   }
   ```

2. **接收方**：

   ```go
   // 接收后发送确认
   retPkt := sm.pf.NewMessageAckPacket(pkt.ID(), ...)
   sm.dg.WriteWait(retPkt)
   ```

3. **确认处理**：

   ```go
   // 收到确认后通知发送方
   sm.shub.Ack(pkt.ID(), nil)
   ```

**重传机制**：

- 使用 `synchub` 管理同步等待
- 超时未收到确认可重传
- `PacketID` 标识消息，支持去重

**适用场景**：需要保证消息不丢失，但可以容忍重复的场景。

##### 5.2.3 准确处理一次（At-Effective-Once / Exactly-Once）

**特点**：

- 消息不会丢失，也不会重复处理
- 需要接收方实现幂等处理
- 使用 `PacketID` 进行去重

**实现机制**：

1. **发送方**：与 At-Least-Once 相同，使用确认机制。
2. **接收方去重**：
   - 使用 `PacketID` 标识消息
   - 维护已处理消息的集合（应用层实现）
   - 重复消息直接返回确认，不重复处理

**去重策略**：

```go
// 应用层维护已处理的 PacketID 集合
processedPackets := make(map[uint64]bool)

func handleMessage(pkt *packet.MessagePacket) {
    if processedPackets[pkt.ID()] {
        // 已处理过，直接返回确认
        sendAck(pkt.ID())
        return
    }

    processMessage(pkt.Data)

    processedPackets[pkt.ID()] = true
    sendAck(pkt.ID())
}
```

**适用场景**：金融交易、订单处理等需要精确一次处理的场景。

#### 5.3 确认机制

**PacketID 的作用**：

- 每个数据包都有唯一的 `PacketID`
- 用于消息标识、确认和去重
- 确认包使用相同的 `PacketID` 关联原消息

**同步等待机制**：

```go
// 发送方创建同步等待
sync := shub.New(packetID,
    synchub.WithTimeout(timeout),
    synchub.WithContext(ctx))

// 发送数据包
sendPacket(pkt)

// 等待确认
event := <-sync.C()
if event.Error != nil {
    return event.Error
}
// 收到确认，发送成功
```

**确认流程**：

```
发送方                         接收方
   |                              |
   |-- MessagePacket(PacketID) -->|
   |                              |  处理消息
   |                              |  发送确认
   |<-- MessageAckPacket(PacketID)|
   |  通知 synchub                 |
   |  返回成功                      |
```

#### 5.4 小结

| 语义类型 | 丢失 | 重复 | 确认 | 去重 | 适用场景 |
| --- | :---: | :---: | :---: | :---: | --- |
| At-Most-Once      | 可能 | 不会 | —   | —   | 日志、指标 |
| At-Least-Once     | 不会 | 可能 | 有   | —   | 一般消息 |
| At-Effective-Once | 不会 | 不会 | 有   | 有   | 金融、订单 |

**关键设计**：

1. **顺序保证**：Channel FIFO + 状态机顺序处理。
2. **可靠性**：确认机制 + 超时重传。
3. **幂等性**：PacketID 去重 + 应用层幂等处理。
4. **灵活性**：支持三种语义，按需选择。

### 6. 并发安全

#### 6.1 锁机制

- **`DialogueMgr.mtx`**：保护 `dialogues` 和 `negotiatingDialogues` 映射。
- **`Dialogue.mtx`**：保护对话状态和通道引用。
- **锁顺序**：`DialogueMgr.mtx → Dialogue.mtx`，避免死锁。

#### 6.2 Channel 安全

- 使用 `recover` 处理 channel 关闭的 panic。
- 发送前检查 channel 状态。
- 使用 `sync.Once` 确保资源只关闭一次。

## 优势

1. **资源高效**：单连接承载多个逻辑对话，减少连接数。
2. **公平调度**：轮询调度器确保所有对话公平使用带宽。
3. **独立状态**：每个对话有独立的状态机和生命周期。
4. **速率控制**：每个对话可配置独立的速率限制。
5. **优雅关闭**：支持 4 次挥手的优雅关闭机制。
6. **顺序保证**：Channel FIFO + 状态机顺序处理，保证消息顺序一致性。
7. **灵活语义**：支持最多一次、至少一次和准确处理一次三种消息到达语义。

## 性能特性

1. **非阻塞轮询**：优先使用非阻塞读取，提高响应速度。
2. **批量处理**：调度器可批量处理多个 Dialogue 的数据包。
3. **动态调整**：调度器列表动态更新，适应 Dialogue 的增删。
4. **内存缓冲**：每个通道都有缓冲区，平滑流量波动。

## 使用示例

```go
// 1. 创建连接
conn, _ := NewClientConn(netconn)

// 2. 创建对话管理器
mgr, _ := NewDialogueMgr(conn)

// 3. 打开新对话
dialogue1, _ := mgr.OpenDialogue(meta1, "peer1")
dialogue2, _ := mgr.OpenDialogue(meta2, "peer2")

// 4. 并发使用多个对话
go func() {
    for {
        pkt, _ := dialogue1.Read()
        // 处理 dialogue1 的数据
    }
}()

go func() {
    for {
        pkt, _ := dialogue2.Read()
        // 处理 dialogue2 的数据
    }
}()

// 5. 写入数据
dialogue1.Write(dataPkt1)
dialogue2.Write(dataPkt2)
```

## 总结

Geminio 的多路复用机制通过以下方式实现：

1. **标识分离**：使用 `dialogueID` 区分不同对话的数据包。
2. **集中路由**：`DialogueMgr` 统一管理所有对话的路由。
3. **公平调度**：写调度器确保所有对话公平使用连接。
4. **独立管理**：每个对话有独立的状态、通道和生命周期。
5. **顺序保证**：Channel FIFO 和状态机顺序处理保证消息顺序一致性。
6. **可靠传输**：支持三种消息到达语义，满足不同场景的可靠性需求。

这种设计在保持简单性的同时，提供了高效的多路复用能力和可靠的消息传输保证。
