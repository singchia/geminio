# Geminio Multiplexing Design

[English](./MULTIPLEXING.md) | [简体中文](../多路复用原理.md)

## Overview

Geminio is a multiplexing network framework: many logical conversations (**Dialogues**) share one physical connection. This reduces connection count and raises resource utilization.

## Core concepts

### 1. Layered architecture

Three layers:

```
┌─────────────────────────────────────┐
│           Application layer         │
│   exchanges data via Dialogue       │
└─────────────────────────────────────┘
              | |
              | |
              v ^
┌─────────────────────────────────────┐
│         Multiplexer layer           │
│   DialogueMgr manages Dialogues     │
└─────────────────────────────────────┘
              | |
              | |
              v ^
┌─────────────────────────────────────┐
│         Connection layer            │
│   owns the physical TCP connection  │
└─────────────────────────────────────┘
```

### 2. Key components

#### 2.1 Connection (connection layer)

- **Responsibility**: manages the underlying TCP connection.
- **Functions**:
  - connection setup and teardown (`ConnPacket` / `ConnAckPacket`)
  - heartbeat keepalive (`HeartbeatPacket`)
  - packet encoding / decoding
  - connection state via FSM

#### 2.2 DialogueMgr (dialogue manager)

- **Responsibility**: owns all dialogues, routes packets, and schedules writes.
- **Core data structure**:

  ```go
  type dialogueMgr struct {
      cn conn.Conn                               // underlying connection
      dialogues map[uint64]*dialogue             // established dialogues
      negotiatingDialogues map[uint64]*dialogue  // dialogues being negotiated
      schedulerDialogueChs []*dialogueWriteCh    // scheduler dialogue list
  }
  ```

- **Key features**:
  1. **Packet routing** (`handlePkt`):
     - read packets from the connection layer
     - route by `dialogueID` to the corresponding Dialogue
     - handle new-dialogue requests (`SessionPacket`)
  2. **Write scheduler** (`writeScheduler`):
     - round-robin scheduling across all Dialogues' write channels
     - fairness: at most one packet per Dialogue per round
     - uses `reflect.Select` for dynamic channel selection

#### 2.3 Dialogue (a logical conversation)

- **Responsibility**: a single logical conversation with its own read/write interface.
- **Core data structure**:

  ```go
  type dialogue struct {
      dialogueID uint64               // unique dialogue identifier
      cn conn.Conn                    // underlying connection
      readInCh  chan packet.Packet    // inbound packets
      writeOutCh chan packet.Packet   // outbound packets
      readOutCh chan packet.Packet    // output to application
      fsm *yafsm.FSM                  // state machine
      rateLimiter *RateLimiter        // per-dialogue rate limiter
  }
  ```

- **State machine**:
  - `INIT → SESSION_SENT / SESSION_RECV → SESSIONED → DISMISS_SENT / DISMISS_RECV → DISMISSED → FINI`
  - The FSM drives the lifecycle of the dialogue.

## How multiplexing works

### 1. Dialogue identifier

Every packet carries a `dialogueID` (called `SessionID` on the wire) so routing knows which dialogue it belongs to:

```go
type SessionAbove interface {
    SessionID() uint64      // get dialogue ID
    SetSessionID(uint64)    // set dialogue ID
}
```

### 2. Packet routing

#### 2.1 Inbound (downstream)

```
Network Packet
    |
    v
Connection.readPkt()         [decode packet]
    |
    v
Connection.readOutCh         [connection-layer output channel]
    |
    v
DialogueMgr.readPkt()        [read connection-layer packet]
    |
    v
DialogueMgr.handlePkt()      [route by dialogueID]
    |
    +-- SessionPacket     --> create new Dialogue
    +-- SessionAckPacket  --> negotiating Dialogue
    `-- DataPacket        --> established Dialogue
    |
    v
Dialogue.readInCh            [dialogue receive channel]
    |
    v
Dialogue.handlePkt()         [dialogue processes packet]
    |
    v
Dialogue.readOutCh           [dialogue output channel]
    |
    v
Application Read
```

#### 2.2 Outbound (upstream)

```
Application Write
    |
    v
Dialogue.Write()             [set dialogueID]
    |
    v
Dialogue.writeOutCh          [dialogue send channel]
    |
    v
DialogueMgr.writeScheduler() [round-robin scheduler]
    |
    +-- poll every Dialogue.writeOutCh
    +-- at most one packet per Dialogue per round
    `-- reflect.Select for dynamic selection
    |
    v
Dialogue.handleOut()         [process outbound packet]
    |
    v
Connection.Write()           [into the connection layer]
    |
    v
Connection.writeOutCh        [connection-layer send channel]
    |
    v
Connection.writePkt()        [encode and send on the wire]
    |
    v
Network Send
```

### 3. Write scheduler

The write scheduler is the key to multiplexing fairness.

#### 3.1 Polling mechanism

```go
// pseudocode
func (dm *dialogueMgr) writeScheduler() {
    for {
        // 1. non-blocking poll: try to read one packet from each Dialogue
        for i := 0; i < len(dialogueChs); i++ {
            idx := (roundRobinIndex + i) % len(dialogueChs)
            select {
            case pkt := <-dialogueChs[idx].writeOutCh:
                processPacket(pkt)
                roundRobinIndex = (idx + 1) % len(dialogueChs)
                break
            default:
                // no data on this Dialogue, try the next
                continue
            }
        }

        // 2. if nothing was available, block-wait on all channels
        if noDataAvailable {
            reflect.Select(cases) // dynamically built select cases
        }
    }
}
```

#### 3.2 Fairness guarantees

- **Round-robin index**: maintained with atomic operations so every Dialogue gets a turn.
- **Per-round cap**: each Dialogue processes at most one packet per round, preventing monopolization.
- **Dynamic list**: when Dialogues are added or removed, the scheduler's channel list updates automatically.

### 4. Dialogue lifecycle

#### 4.1 Opening a dialogue

```
Initiator                        Recipient
   |                               |
   |-- SessionPacket ------------->|
   |   (negotiatingID)             |  create new Dialogue
   |                               |  add to negotiatingDialogues
   |                               |
   |<-- SessionAckPacket ----------|
   |   (dialogueID)                |  assign dialogueID
   |                               |  move to dialogues
   |                               |
   |-- DataPacket(dialogueID) ---> |
   |                               |  routed to the corresponding Dialogue
```

#### 4.2 Closing a dialogue (four-way handshake)

```
Initiator                        Recipient
   |                               |
   |  state: SESSIONED             |  state: SESSIONED
   |                               |
   |-- DismissPacket ------------->|
   |  state: DISMISS_SENT          |  state: DISMISS_RECV
   |                               |  send DismissAckPacket
   |                               |  Close() -> send DismissPacket
   |<-- DismissAckPacket ----------|
   |  state: DISMISS_HALF          |  state: DISMISS_SENT
   |                               |
   |<-- DismissPacket -------------|
   |  state: DISMISS_HALF          |  (awaiting DismissAckPacket)
   |  send DismissAckPacket        |
   |-- DismissAckPacket ---------->|
   |  state: DISMISSED             |  state: DISMISS_HALF
   |                               |
   |  close complete               |  state: DISMISSED
   |                               |  close complete
```

### 5. Ordering and delivery semantics

End-to-end messaging systems have to answer two questions.

#### 5.1 Ordering consistency

**Question**: are sent and received messages in the same order?

**Mechanisms**:

1. **Channel FIFO guarantee**:
   - all packets travel through Go channels
   - channels are FIFO, so order is preserved naturally
   - packets queue in send order
2. **Sequential state machine**:

   ```go
   // Dialogue FSM uses the WithInSeq() option
   dg.fsm = yafsm.NewFSM(yafsm.WithInSeq())
   ```

   - the FSM processes events in order, keeping state transitions consistent
   - prevents out-of-order events from causing state divergence
3. **Blocking send preserves order**:
   - packets are sent through blocking channels, so they hit the wire in order
   - the sender must finish one before starting the next
4. **Single-threaded processing**:
   - every Dialogue has its own `handlePkt()` goroutine
   - inbound packets are processed on one thread, preserving order

**Example**:

```
Send order:    Packet1 → Packet2 → Packet3
               ↓         ↓         ↓
Channel:       [P1][P2][P3]       (FIFO queue)
               ↓         ↓         ↓
Recv order:    Packet1 → Packet2 → Packet3  (same order)
```

#### 5.2 Delivery semantics

**Question**: at-most-once, at-least-once, or exactly-once processing?

The packet header carries a `Cnss` (Consistency) field supporting three semantics:

```go
const (
    CnssAtMostOnce      Cnss = 0x01  // at-most-once
    CnssAtLeastOnce     Cnss = 0x02  // at-least-once
    CnssAtEffectiveOnce Cnss = 0x03  // exactly-once (at-effective-once)
)
```

##### 5.2.1 At-most-once

**Characteristics**:

- messages may be lost, but never duplicated
- no ack, returns immediately after sending
- fits scenarios with low reliability requirements

**Implementation**:

```go
if msg.Cnss() == options.CnssAtMostOnce {
    // no ack; just send
    sm.writeInCh <- pkt
    return nil
}
```

**Use cases**: log shipping, metrics — losses are tolerable.

##### 5.2.2 At-least-once

**Characteristics**:

- messages are never lost, but may duplicate
- uses an ack mechanism; retransmits on missing ack
- receivers must handle duplicates

**How it works**:

1. **Sender**:

   ```go
   // create a sync wait
   sync = sm.shub.New(msg.ID(), synchub.WithTimeout(msg.Timeout()))
   sm.writeInCh <- pkt

   // wait for ack
   event := <-sync.C()
   if event.Error != nil {
       // timeout or error; may retransmit
       return event.Error
   }
   ```

2. **Receiver**:

   ```go
   // send ack after receiving
   retPkt := sm.pf.NewMessageAckPacket(pkt.ID(), ...)
   sm.dg.WriteWait(retPkt)
   ```

3. **Ack handling**:

   ```go
   // notify the sender on ack
   sm.shub.Ack(pkt.ID(), nil)
   ```

**Retransmission**:

- `synchub` manages sync waits
- on timeout, retransmit
- `PacketID` identifies each message — enables dedup

**Use cases**: reliable messaging that can tolerate duplicates.

##### 5.2.3 Exactly-once (at-effective-once)

**Characteristics**:

- messages are neither lost nor processed twice
- the receiver must be idempotent
- uses `PacketID` for deduplication

**How it works**:

1. **Sender**: same ack mechanism as at-least-once.
2. **Receiver dedup**:
   - identify messages by `PacketID`
   - maintain a set of processed IDs (application layer)
   - duplicates are acked without reprocessing

**Dedup strategy**:

```go
// the application keeps track of processed PacketIDs
processedPackets := make(map[uint64]bool)

func handleMessage(pkt *packet.MessagePacket) {
    if processedPackets[pkt.ID()] {
        // already processed — just ack
        sendAck(pkt.ID())
        return
    }

    processMessage(pkt.Data)

    processedPackets[pkt.ID()] = true
    sendAck(pkt.ID())
}
```

**Use cases**: financial transactions, order processing — anything needing true exactly-once.

#### 5.3 Acknowledgment

**Role of PacketID**:

- every packet has a unique `PacketID`
- used for message identification, acking, and dedup
- an ack packet carries the same `PacketID` to correlate with the original

**Sync-wait mechanism**:

```go
// sender creates a sync wait
sync := shub.New(packetID,
    synchub.WithTimeout(timeout),
    synchub.WithContext(ctx))

// send the packet
sendPacket(pkt)

// wait for the ack
event := <-sync.C()
if event.Error != nil {
    // timeout or error
    return event.Error
}
// ack received, send succeeded
```

**Ack flow**:

```
Sender                            Receiver
   |                                 |
   |-- MessagePacket(PacketID) ----->|
   |                                 |  process message
   |                                 |  send ack
   |<-- MessageAckPacket(PacketID) --|
   |  notify synchub of ack          |
   |  return success                 |
```

#### 5.4 Summary

| Semantic | Can lose? | Can duplicate? | Ack | Dedup | Use cases |
| --- | :---: | :---: | :---: | :---: | --- |
| At-most-once   | yes | no  | —   | —   | logs, metrics |
| At-least-once  | no  | yes | yes | —   | general messaging |
| Exactly-once   | no  | no  | yes | yes | finance, orders |

**Design essentials**:

1. **Ordering**: channel FIFO + sequential state machine.
2. **Reliability**: ack mechanism + timeout retransmission.
3. **Idempotency**: PacketID dedup + application-layer idempotent handling.
4. **Flexibility**: three semantics, choose per scenario.

### 6. Concurrency safety

#### 6.1 Locks

- **`DialogueMgr.mtx`** protects `dialogues` and `negotiatingDialogues`.
- **`Dialogue.mtx`** protects dialogue state and channel references.
- **Lock order**: `DialogueMgr.mtx → Dialogue.mtx` to avoid deadlock.

#### 6.2 Channel safety

- `recover` handles panics on closed-channel sends.
- channel state is checked before sending.
- `sync.Once` ensures resources are closed exactly once.

## Advantages

1. **Resource efficient**: one connection carries many logical dialogues.
2. **Fair scheduling**: round-robin scheduler gives every dialogue its share of bandwidth.
3. **Independent state**: each dialogue has its own state machine and lifecycle.
4. **Rate control**: each dialogue can have its own rate limiter.
5. **Graceful close**: four-way handshake teardown.
6. **Ordering**: channel FIFO + sequential state machine preserve message order.
7. **Flexible semantics**: at-most-once, at-least-once, and exactly-once supported.

## Performance characteristics

1. **Non-blocking polling**: prefer non-blocking reads to improve responsiveness.
2. **Batching**: the scheduler can batch packets across Dialogues.
3. **Dynamic updates**: the scheduler list adjusts automatically as Dialogues come and go.
4. **Buffering**: every channel has a buffer to absorb traffic spikes.

## Example

```go
// 1. create a connection
conn, _ := NewClientConn(netconn)

// 2. create a dialogue manager
mgr, _ := NewDialogueMgr(conn)

// 3. open dialogues
dialogue1, _ := mgr.OpenDialogue(meta1, "peer1")
dialogue2, _ := mgr.OpenDialogue(meta2, "peer2")

// 4. use them concurrently
go func() {
    for {
        pkt, _ := dialogue1.Read()
        // handle dialogue1
    }
}()

go func() {
    for {
        pkt, _ := dialogue2.Read()
        // handle dialogue2
    }
}()

// 5. write
dialogue1.Write(dataPkt1)
dialogue2.Write(dataPkt2)
```

## Summary

Geminio's multiplexing works by:

1. **Identifier separation**: `dialogueID` distinguishes packets across dialogues.
2. **Central routing**: `DialogueMgr` owns routing for every dialogue.
3. **Fair scheduling**: the write scheduler gives every dialogue equal access to the connection.
4. **Independent management**: each dialogue has its own state, channels, and lifecycle.
5. **Ordering**: channel FIFO plus sequential state-machine processing keeps messages in order.
6. **Reliable transport**: three delivery semantics cover different reliability needs.

The result: a simple design that delivers efficient multiplexing and reliable messaging.
