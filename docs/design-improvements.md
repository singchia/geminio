# Gemino 设计改进方案

> 针对死锁、panic、goroutine 泄漏的根本性修复建议

---

## 根本问题总览

| # | 问题 | 现象 | 位置 |
|---|------|------|------|
| 1 | channel 生命周期与发送方不同步 | `panic: send on closed channel` | `dialogue.fini()` |
| 2 | 持锁期间阻塞 channel 发送 | 死锁，系统卡死 | `dialogueMgr.handlePkt()` L388 |
| 3 | accept/closed channel 满后阻塞主路径 | 整个连接停止处理数据包 | `dialogueMgr.DialogueOnline()` L216 |
| 4 | goroutine 缺乏统一 WaitGroup | 60+ goroutine 泄漏 | `dialogue.start()` |

**核心原则：不在持锁期间做 channel 操作；不在 channel 关闭后发送；用 context 而不是 bool flag 控制生命周期。**

---

## 问题 1：channel 生命周期与发送方不同步（→ panic）

**位置：** `multiplexer/dialogue.go` `fini()` vs 所有调用 `writeInCh <- pkt` 的地方

### 竞争窗口

```
fini()                           goroutine A
------                           -----------
mtx.Lock()
dialogueOK = false
close(writeInCh)  <─────────────┐
mtx.Unlock()                    │   mtx.RLock() → dialogueOK=false → RUnlock()
                                └── writeInCh <- pkt  ← PANIC: send on closed channel
```

`dialogueOK` 检查和 channel send 之间存在 TOCTOU 窗口。即使加了读锁，unlock 之后到实际发送之前 channel 可能已被关闭。当前用 `recover()` 打补丁只是掩盖问题。

### 改进方案

用 `context.Context` 替代 `dialogueOK` bool，所有写入统一通过一个入口函数：

```go
type dialogue struct {
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
    // 移除 dialogueOK bool 和 dialogueMtx
}

// 所有发送方统一走这里，不再散落各处直接 writeInCh <- pkt
func (dg *dialogue) sendToWriteIn(pkt packet.Packet) error {
    select {
    case <-dg.ctx.Done():
        return io.EOF
    case dg.writeInCh <- pkt:
        return nil
    }
}
```

`fini()` 改为先 cancel 再等所有 goroutine 退出，最后才 close channel：

```go
func (dg *dialogue) fini() {
    dg.cancel()         // 通知所有发送方停止
    dg.wg.Wait()        // 等所有 goroutine 退出；此后不会有新的发送
    close(dg.writeInCh) // 现在安全关闭
    for pkt := range dg.writeInCh {
        if dg.failedCh != nil && !isSessionLayerPkt(pkt) {
            dg.failedCh <- pkt
        }
    }
    close(dg.readOutCh)
    close(dg.writeOutCh)
    dg.fsm.EmitEvent(ET_FINI)
    dg.fsm.Close()
}
```

---

## 问题 2：持锁期间阻塞 channel 发送（→ 死锁）

**位置：** `multiplexer/dialogue_mgr.go` `handlePkt()` L388

### 死锁的完整闭环

`dialogueMgr` 有两条并发路径在争同一把锁：

```
路径 A（readPkt goroutine）               路径 B（dialogue.handlePkt goroutine）
─────────────────────────────            ──────────────────────────────────────
handlePkt()                              handleOutSessionAckPacket()
  dm.mtx.Lock()                            dlgt.DialogueOnline()
    negotiatingDialogues[id] = dg            dm.mtx.Lock()  ← 等路径 A 释放
    dg.readInCh <- pkt  ← 阻塞
      （消费者是 dialogue.handlePkt，
        但它卡在 DialogueOnline 里）
```

**闭环**：A 持 `dm.mtx` 等 `readInCh` 被消费 → 消费方（`dialogue.handlePkt`）在调 `DialogueOnline` → `DialogueOnline` 等 `dm.mtx` → A 永远不释放锁。

用锁保护握手生命周期的需求是合理的，问题在于**在持锁期间做了阻塞 IO**。

### 改进方案：两阶段注册（Two-Phase Registration）

**原则：锁只保护 map，不参与任何 channel 操作。握手正确性由 dialogue 自身的 ctx 保证。**

| 操作 | 是否持 `dm.mtx` |
|------|----------------|
| map 增删查 | ✅ 是 |
| `readInCh <- pkt` | ❌ 否 |
| `dialogueAcceptCh <- dg` | ❌ 否 |
| `dialogueClosedCh <- dg` | ❌ 否 |

**SessionPacket 路径（新 dialogue 的第一个握手包）：**

```go
// 当前（有死锁）：                    改进后：
// ─────────────────                  ────────────────────────────────
// mtx.Lock()                         mtx.Lock()
//   map[id] = dg                       map[id] = dg
//   dg.readInCh <- pkt  ← 阻塞+持锁  mtx.Unlock()  ← 先释放
// mtx.Unlock()
//                                    // 锁外用 goroutine 异步投递
//                                    // （SessionPacket 极低频，每 dialogue 仅一次）
go func() {
    select {
    case dg.readInCh <- pkt:
        // 握手包送达
    case <-dg.ctx.Done():
        // dialogue 已关闭，清理 map
        dm.mtx.Lock()
        delete(dm.negotiatingDialogues, negotiatingID)
        dm.mtx.Unlock()
        dg.fini()
    case <-dm.closeCh:
        dg.fini()
    }
}()
```

为什么 SessionPacket 用 goroutine：`readPkt` 是整个连接的读路径，不能阻塞；SessionPacket 频率极低（每个 dialogue 仅一次），代价可忽略。

**SessionAckPacket 路径（第二次握手）：**

```go
// 锁只用于 map 查找，查到后立即释放
dm.mtx.RLock()
dg, ok := dm.negotiatingDialogues[realPkt.NegotiateID()]
dm.mtx.RUnlock()  // ← 先释放

if !ok { ... return }

// 锁外发送，ctx 保护
select {
case dg.readInCh <- pkt:
case <-dg.ctx.Done():
    // 握手超时或对端关闭，synchub 30s timeout 已兜底
}
```

**数据包路径：**

```go
dm.mtx.RLock()
dg, ok := dm.dialogues[dialogueID]
dm.mtx.RUnlock()  // ← 先释放

select {
case dg.readInCh <- pkt:
case <-dg.ctx.Done():
    dm.log.Debugf("dialogue closing, drop data pkt")
}
```

**DialogueOnline / DialogueOffline：**

```go
func (dm *dialogueMgr) DialogueOnline(dg delegate.DialogueDescriber) error {
    dm.mtx.Lock()
    if !dm.mgrOK { dm.mtx.Unlock(); return ErrOperationOnClosedMultiplexer }
    delete(dm.negotiatingDialogues, dg.NegotiatingID())
    dm.dialogues[dg.DialogueID()] = dg.(*dialogue)
    acceptFn := dm.dialogueAcceptFn
    acceptCh := dm.dialogueAcceptCh
    dm.mtx.Unlock()  // ← map 操作完即释放

    // 锁外执行 IO
    if acceptFn != nil {
        acceptFn(dg.(Dialogue))
    } else if acceptCh != nil {
        select {
        case acceptCh <- dg.(*dialogue):
        case <-dm.closeCh:
        }
    }
    return nil
}

func (dm *dialogueMgr) DialogueOffline(dg delegate.DialogueDescriber) error {
    dm.mtx.Lock()
    delete(dm.dialogues, dg.DialogueID())
    closedFn := dm.dialogueClosedFn
    closedCh := dm.dialogueClosedCh
    dm.mtx.Unlock()  // ← map 操作完即释放

    // 锁外执行 IO
    if closedFn != nil {
        closedFn(dg.(Dialogue))
    } else if closedCh != nil {
        select {
        case closedCh <- dg.(*dialogue):
        case <-dm.closeCh:
        }
    }
    return nil
}
```

### 握手失败场景的处理

改进后各失败场景行为不变，正确性由 ctx + synchub timeout 双重保证：

| 失败场景 | 处理方式 |
|----------|----------|
| SessionPacket 投递时 dialogue 已 ctx.Done | goroutine 感知，删 map，fini dialogue，握手从未开始 |
| SessionAckPacket 投递时 dialogue 已关闭 | select ctx.Done，忽略，30s synchub timeout 兜底 |
| 握手超时（对端无响应） | synchub timeout → closeIO() → ctx cancel → 清理 map |
| manager Close() 时有 dialogue 正在协商 | dm.closeCh 关闭 → 所有 goroutine 的 select 退出 → fini() |

---

## 问题 3：accept/closed channel 满后阻塞整个系统

**位置：** `multiplexer/dialogue_mgr.go` `DialogueOnline()` L216

### 问题

注释写了 "this must not be blocked"，但实际上 buffer(32) 填满后会阻塞整个 `handlePkt` 路径，导致连接上所有数据包都无法处理：

```go
// 危险：可能阻塞
dm.dialogueAcceptCh <- dg.(*dialogue)
```

### 改进方案

非阻塞发送 + 异步重试，保证主路径不阻塞：

```go
select {
case dm.dialogueAcceptCh <- dg:
default:
    // buffer 满，异步重试，不阻塞主路径
    go func() {
        select {
        case dm.dialogueAcceptCh <- dg:
        case <-dm.closeCh:
            // manager 关闭，dialogue 也一并关闭
            dg.fini()
        }
    }()
}
```

更彻底的方案是优先使用回调（`dialogueAcceptFn`），彻底消除 channel 背压问题，只在没有回调时才用 channel。

---

## 问题 4：goroutine 泄漏 — 缺乏统一 WaitGroup

**位置：** `multiplexer/dialogue.go` `start()` 等启动 goroutine 的地方

### 问题

每个 `dialogue` 启动若干 goroutine（readPkt/writePkt/handlePkt 等），但 `fini()` 没有等它们真正退出就关闭资源，导致：
- 残留 goroutine 访问已关闭的 channel → panic
- 连接关闭后 goroutine 无法退出 → 泄漏（实测 10 个连接关闭后残留 60+ goroutine）

### 改进方案

每个 `dialogue` 持有自己的 `sync.WaitGroup`，所有 goroutine 启动时 Add，退出时 Done：

```go
func (dg *dialogue) start() {
    dg.wg.Add(3)
    go func() { defer dg.wg.Done(); dg.readPkt() }()
    go func() { defer dg.wg.Done(); dg.writePkt() }()
    go func() { defer dg.wg.Done(); dg.handlePkt() }()
}
```

各 goroutine 的退出条件改为监听 `ctx.Done()`：

```go
func (dg *dialogue) writePkt() {
    for {
        select {
        case <-dg.ctx.Done():
            return
        case pkt, ok := <-dg.writeInCh:
            if !ok {
                return
            }
            // ... 写入下游
        }
    }
}

func (dg *dialogue) readPkt() {
    for {
        select {
        case <-dg.ctx.Done():
            return
        case pkt, ok := <-dg.readInCh:
            if !ok {
                return
            }
            // ... 处理
        }
    }
}
```

---

## 问题 5：close channel 被用来承担两种不同语义（→ panic 根源）

### 问题诊断

当前代码里 `close(ch)` 同时承担了两个**完全不同**的职责，这是所有 `recover()` 补丁的根源：

```
用途 1：生产者信号（Producer Done）
  readPkt goroutine 网络断开 → close(readInCh) → handlePkt 知道"没有新包了"
  ✅ Go channel 的正确用法：单一写入方，关闭即完成信号

用途 2：生命周期控制（Stop Signal）
  fini() 调用 close(writeInCh) → 意图是"停止接受新包"
  ❌ writeInCh 有多个写入方（外部 Write/Close、内部协议回复）
     多写方 + close = panic，只能打 recover() 补丁
```

**Go 的铁律：只有唯一的写入方才能 close channel。**

### 四条 channel 的现状分析

```
channel       写入方                         关闭方      问题
──────────    ─────────────────────────────  ──────────  ──────────────────────────
readInCh      readPkt goroutine（唯一）       closeIO()   ✅ 单写方，关闭合理
                                                         ✅ close 既是生产者信号也是退出信号

writeInCh     ① handlePkt 内部协议回复        fini()      ❌ 多写方，任何一方 close 都有 panic
              ② 外部 Write()/Close()                      需要 recover() 补丁
              ③ dialogueMgr 分发

readOutCh     handlePkt goroutine（唯一）      fini()      ✅ 单写方，关闭合理
                                                          上层 Receive() 读到关闭即 EOF

writeOutCh    handlePkt goroutine（唯一）      fini()      ✅ 单写方，关闭合理
                                                          writePkt goroutine 读到关闭即退出
```

**结论：问题只在 `writeInCh`，另外三条 channel 的关闭设计是合理的，不需要改动。**

### 关于退出后残留包的处理

| 包类型 | 能否丢弃 | 理由 |
|--------|----------|------|
| SessionPacket | ✅ 可以 | 对端 30s timeout，正常失败路径 |
| DismissPacket / DismissAckPacket | ✅ 可以 | 4 次挥手已在进行，timeout 兜底 |
| 数据包（Data） | ⚠️ 看需求 | 需要 at-least-once 则通过 `failedCh` 通知；at-most-once 直接丢弃 |

`failedCh` 机制已存在，设计正确。问题只是**如何在不 close 的情况下触发 drain**。

### 改进方案：写入方自己退出，channel 不 close

**`writeInCh` 永远不被 close，用 `ctx.Done()` 作退出信号，由 `handlePkt` goroutine 在退出时��己做 non-blocking drain。**

```go
func (dg *dialogue) handlePkt() {
    defer dg.wg.Done()
    for {
        select {
        case pkt, ok := <-dg.readInCh:
            if !ok { goto DRAIN }   // 网络断开，readInCh 被 readPkt 关闭（单写方，合理）
            // ... 处理入包
        case pkt, ok := <-dg.writeInCh:
            // ... 处理出包
        case <-dg.ctx.Done():       // fini() 触发退出，替代 close(writeInCh)
            goto DRAIN
        }
    }
DRAIN:
    // non-blocking drain：通知上层未发送成功的数据包
    for {
        select {
        case pkt := <-dg.writeInCh:
            if dg.failedCh != nil && !packet.SessionLayer(pkt) {
                dg.failedCh <- pkt
            }
        default:
            return   // drain 完毕，goroutine 正常退出
        }
    }
}
```

所有写入方统一走 `sendToWriteIn`，用 `select + ctx.Done()` 替代裸发送：

```go
func (dg *dialogue) sendToWriteIn(pkt packet.Packet) error {
    select {
    case dg.writeInCh <- pkt:
        return nil
    case <-dg.ctx.Done():
        return io.EOF   // 对方已关闭，正常的 EOF，不是 panic
    }
}
```

`fini()` 不再 close `writeInCh`，只需 cancel + wait：

```go
func (dg *dialogue) fini() {
    dg.cancel()     // 所有 sendToWriteIn 感知到 ctx.Done()，停止发送
    dg.wg.Wait()    // 等 handlePkt goroutine 自己 drain 完退出
                    // 此后 writeInCh 无人持有，GC 回收

    // handlePkt 退出时已关闭 readOutCh 和 writeOutCh（它是单写方）
    dg.shub.Close()
    dg.fsm.EmitEvent(ET_FINI)
    dg.fsm.Close()
}
```

### 改进前后对比

```
              当前设计                          改进后
              ────────────────────────────    ──────────────────────────────
writeInCh     close() 作退出信号              ctx.Done() 作退出信号
              → 多写方必须 recover()           → 永远不 close，零 panic 风险

残留包处理     fini() 在 close 之后 range      handlePkt goroutine 自己 non-blocking drain
              → 需要先 close 再 drain          → 不需要 close，drain 后 goroutine 正常退出

生命周期感知   dialogueOK bool + RWMutex       context.Context
              → TOCTOU 窗口                   → goroutine 安全，无竞争

readInCh      ✅ 不变（单写方，合理 close）     ✅ 不变
readOutCh     ✅ 不变（单写方，合理 close）     ✅ 不变
writeOutCh    ✅ 不变（单写方，合理 close）     ✅ 不变
```

---

## 改动优先级

| 优先级 | 改动 | 解决问题 |
|--------|------|----------|
| **P0** | `dialogue` 加 `ctx+cancel+wg`，`fini()` 改为 cancel+wait，移除 `close(writeInCh)` | panic + goroutine 泄漏 |
| **P0** | `dialogueMgr.handlePkt` 两阶段注册：锁内改 map，锁外异步投递握手包 | 死锁 |
| **P1** | `DialogueOnline/Offline` 锁内只改 map，channel 发送移到锁外 | 潜在死锁 |
| **P1** | 所有 `writeInCh <- pkt` 统一走 `sendToWriteIn()`，移除全部 `recover()` 补丁 | panic 根源 |
| **P2** | `dialogueAcceptCh/dialogueClosedCh` 发送改为非阻塞（select + closeCh） | 系统卡死 |

---

## 改动影响范围

```
multiplexer/
  dialogue.go       — 加 ctx/cancel/wg；新增 sendToWriteIn()；
                      fini() 改为 cancel+wait，不再 close(writeInCh)；
                      handlePkt() 加 DRAIN 逻辑；移除 dialogueOK bool
  dialogue_mgr.go   — handlePkt() 两阶段注册，SessionPacket 异步投递；
                      DialogueOnline/Offline 锁外做 channel 操作
conn/
  conn_base.go      — Write() 的 writeInCh <- pkt 同样需��� ctx 保护（同问题 5）
```
