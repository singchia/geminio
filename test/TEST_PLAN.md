# Gemino 测试计划与问题追踪文档

**文档版本:** 1.0
**创建日期:** 2026-03-26
**测试执行:** Claude Code

---

## 目录

1. [测试分类与编号](#一测试分类与编号)
2. [发现问题汇总](#二发现问题汇总)
3. [详细问题清单](#三详细问题清单)
4. [修复计划](#四修复计划)
5. [测试覆盖矩阵](#五测试覆盖矩阵)

---

## 一、测试分类与编号

### 1.1 测试类别编码

| 类别编码 | 类别名称 | 说明 |
|---------|---------|------|
| BENCH | 基准测试 | 性能、吞吐量、内存测试 |
| E2E | 端到端测试 | 集成场景、功能验证 |
| SEC | 安全测试 | 边界、攻击防护、模糊测试 |
| CHAOS | 混沌测试 | 网络故障模拟 |
| REG | 回归测试 | 基础功能回归 |

### 1.2 测试用例编号规则

格式: `[类别]-[模块]-[序号]`

示例:
- `E2E-CONN-001`: E2E测试-连接模块-第1个用例
- `BENCH-RPC-003`: 基准测试-RPC模块-第3个用例
- `SEC-DOS-002`: 安全测试-DoS防护-第2个用例

---

## 二、发现问题汇总

### 2.1 问题统计

| 严重程度 | 数量 | 状态 |
|---------|------|------|
| 🔴 P0 - 致命 | 2 | 待修复 |
| 🟠 P1 - 严重 | 2 | 待修复 |
| 🟡 P2 - 一般 | 3 | 待修复 |
| 🟢 P3 - 轻微 | 2 | 待修复 |

### 2.2 问题分布

```
问题分布图:

multiplexer/     ████████ 3个 (死锁、panic、超时)
conn/            ████ 2个 (heartbeat、资源泄漏)
test/            ███ 2个 (编译错误)
application/     ██ 1个 (RPC错误处理)
examples/        █ 1个 (格式错误)
```

---

## 三、详细问题清单

### 🔴 P0 - 致命级别

#### BUG-P0-001: Panic - 向已关闭 channel 发送数据

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P0-001 |
| **测试编号** | E2E-CONN-004 |
| **测试名称** | TestMultipleClients |
| **问题类型** | 并发竞态条件 |
| **严重级别** | 🔴 P0 - 致命 |
| **发现日期** | 2026-03-26 |
| **所属模块** | multiplexer/dialogue.go |
| **代码位置** | dialogue.go:702 |

**错误堆栈:**
```
panic: send on closed channel
goroutine 3425 [running]:
github.com/singchia/gemino/multiplexer.(*dialogue).Close.func1.1()
    /Users/zhaizenghui/austinzhai/gemino/multiplexer/dialogue.go:702 +0x38
```

**问题描述:**
在高并发连接场景下（100个客户端同时连接/断开），`dialogue.Close()` 方法尝试向 `writeInCh` 发送数据时，该 channel 可能已被关闭，导致 panic。

**复现步骤:**
1. 启动服务器监听
2. 并发创建100个客户端连接
3. 并发关闭所有连接
4. 触发 panic

**根因分析:**
- `Close()` 方法使用了 `sync.Once`，但 channel 的关闭可能在另一个 goroutine 中进行
- 缺少对 channel 状态的检查

**建议修复:**
```go
// 方案1: 使用 select + default
select {
case d.writeInCh <- pkt:
    // 发送成功
default:
    // channel 已关闭或满了，记录日志
    d.log.Debugf("writeInCh closed or full, skip sending")
}

// 方案2: 添加关闭标志
d.mtx.RLock()
closed := d.dialogueOK
d.mtx.RUnlock()
if !closed {
    return
}
```

**相关测试:**
- E2E-CONN-004: TestMultipleClients
- E2E-CONN-005: TestConnectionReconnect

---

#### BUG-P0-002: 死锁 - dialogueMgr 锁竞争导致系统卡死

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P0-002 |
| **测试编号** | E2E-STRESS-001 |
| **测试名称** | TestStressMixedOperations |
| **问题类型** | 死锁/锁竞争 |
| **严重级别** | 🔴 P0 - 致命 |
| **发现日期** | 2026-03-26 |
| **所属模块** | multiplexer/dialogue_mgr.go |
| **代码位置** | dialogue_mgr.go:388, dialogue.go:354 |

**错误堆栈:**
```
goroutine 36 [sync.Mutex.Lock, 9 minutes]:
sync.(*RWMutex).Lock(...)
github.com/singchia/gemino/multiplexer.(*dialogueMgr).handlePkt
    dialogue_mgr.go:388

goroutine 41 [select, 9 minutes]:
github.com/singchia/gemino/application.(*stream).Call
    rpc.go:128
```

**问题描述:**
在高并发混合操作场景（10个 worker，每个执行100次操作：RPC调用、消息发送、Stream创建）下，系统出现死锁，所有操作卡在 `dialogueMgr` 的 mutex 上。

**复现步骤:**
1. 建立连接
2. 启动10个 goroutine 并发执行混合操作
3. 每个 worker 执行100次随机操作（RPC/消息/Stream）
4. 系统死锁，10分钟后测试超时

**根因分析:**
- `dialogueMgr` 使用单个 `sync.RWMutex` 保护所有 dialogue
- `handlePkt` 需要获取写锁，但此时可能已有其他 goroutine 持有读锁等待写锁
- Stream 创建 (`OpenDialogue`) 和消息处理 (`handlePkt`) 形成循环依赖

**建议修复:**
```go
// 方案1: 分段锁 (Shard Lock)
type dialogueMgr struct {
    shardCount int
    shards     []dialogueShard
}

type dialogueShard struct {
    mtx       sync.RWMutex
    dialogues map[uint64]*dialogue
}

func (dm *dialogueMgr) getShard(id uint64) *dialogueShard {
    return &dm.shards[id%uint64(dm.shardCount)]
}

// 方案2: 减少锁粒度
func (dm *dialogueMgr) handlePkt(pkt packet.Packet) {
    // 先读取 dialogue ID，不需要锁
    dialogueID := pkt.DialogueID()

    // 只锁定特定 dialogue
    dg := dm.getDialogue(dialogueID)
    if dg != nil {
        dg.handlePkt(pkt)  // dialogue 内部有自己的锁
    }
}

// 方案3: 使用 channel 替代锁
type dialogueMgr struct {
    pktCh chan packet.Packet
    // 使用单个 goroutine 处理所有 packet
}
```

**相关测试:**
- E2E-STRESS-001: TestStressMixedOperations
- E2E-STREAM-002: TestStreamMultiple

---

### 🟠 P1 - 严重级别

#### BUG-P1-001: Goroutine 泄漏 - 连接关闭后残留60+ goroutine

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P1-001 |
| **测试编号** | E2E-RES-001 |
| **测试名称** | TestResourceCleanup |
| **问题类型** | 资源泄漏 |
| **严重级别** | 🟠 P1 - 严重 |
| **发现日期** | 2026-03-26 |
| **所属模块** | conn/, multiplexer/, application/ |
| **代码位置** | channel_monitor.go, dialogue.go, stream.go |

**错误信息:**
```
possible goroutine leak: started with 2, ended with 62
```

**问题描述:**
创建10个连接并关闭后，系统中残留60个 goroutine 未被清理。长时间运行会导致内存耗尽和 goroutine 数量达到系统限制。

**泄漏来源分析:**

| 来源 | 数量 | 位置 |
|-----|------|------|
| channel_monitor | ~20 | conn/channel_monitor.go:82 |
| dialogue.handlePkt | ~10 | multiplexer/dialogue.go:390 |
| dialogue.writePkt | ~10 | multiplexer/dialogue.go:354 |
| stream.handlePkt | ~10 | application/stream.go:174 |
| stream.readPkt | ~10 | application/stream.go:200 |

**根因分析:**
- `channel_monitor` 的 goroutine 没有正确停止
- `fini()` 方法关闭 channel 但没有通知 monitor goroutine 退出
- Stream 关闭时没有等待所有内部 goroutine 完成

**建议修复:**
```go
// 1. 添加 context 控制 goroutine 生命周期
type dialogue struct {
    ctx    context.Context
    cancel context.CancelFunc
}

func newDialogue(...) *dialogue {
    ctx, cancel := context.WithCancel(parentCtx)
    dg := &dialogue{
        ctx:    ctx,
        cancel: cancel,
    }
    return dg
}

func (dg *dialogue) fini() {
    dg.cancel()  // 通知所有 goroutine 退出

    // 等待 goroutine 完成
    dg.wg.Wait()

    close(dg.writeInCh)
    // ... 其他清理
}

// 2. monitor goroutine 检查 context
func (dg *dialogue) startChannelMonitor() {
    go func() {
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                dg.logChannelStats()
            case <-dg.ctx.Done():
                return  // 正确退出
            }
        }
    }()
}
```

**相关测试:**
- E2E-RES-001: TestResourceCleanup
- SEC-RES-001: TestResourceExhaustionGoroutines

---

#### BUG-P1-002: 高并发 Stream 创建超时

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P1-002 |
| **测试编号** | E2E-STRESS-001 |
| **测试名称** | TestStressMixedOperations |
| **问题类型** | 性能/并发 |
| **严重级别** | 🟠 P1 - 严重 |
| **发现日期** | 2026-03-26 |
| **所属模块** | multiplexer/dialogue_mgr.go |
| **代码位置** | dialogue_mgr.go:284 |

**错误信息:**
```
dialogue open err: timeout, clientID: 7621461310441740184
worker 8 open stream failed: timeout
```

**问题描述:**
在并发创建 Stream 时，`OpenDialogue` 调用频繁超时，导致高并发场景下 Stream 创建失败率高达 80% 以上。

**根因分析:**
- `OpenDialogue` 是同步阻塞操作
- 需要等待对端响应（同步 RPC）
- 高并发时形成请求队列，等待时间超过超时阈值

**建议修复:**
```go
// 方案1: 异步创建 + 回调
func (dm *dialogueMgr) OpenDialogueAsync(opts ...DialogueOption) (*dialogue, error) {
    // 快速创建本地 dialogue，异步完成对端协商
    dg := dm.createDialogue(opts...)

    go func() {
        // 异步完成对端协商
        err := dm.negotiateDialogue(dg)
        if err != nil {
            dg.setError(err)
        }
    }()

    return dg, nil
}

// 方案2: 连接池预创建
type dialoguePool struct {
    available chan *dialogue
    minSize   int
    maxSize   int
}

// 预先创建 dialogue，减少实时创建开销
```

**相关测试:**
- E2E-STRESS-001: TestStressMixedOperations
- SEC-BOUND-002: TestBoundaryStreamCount

---

### 🟡 P2 - 一般级别

#### BUG-P2-001: Heartbeat 定时器未启动错误

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P2-001 |
| **测试编号** | E2E-STREAM-003 |
| **测试名称** | TestStreamBidirectional |
| **问题类型** | 逻辑错误 |
| **严重级别** | 🟡 P2 - 一般 |
| **发现日期** | 2026-03-26 |
| **所属模块** | conn/conn_server.go |
| **代码位置** | conn_server.go:306 |

**错误信息:**
```
ERROR ... wait HEARTBEAT err: timer not started, clientID: 7621461295338916263
```

**问题描述:**
在连接快速关闭的场景下，`waitHBTimeout` 函数报告 timer 未启动。这是一个竞态条件：连接在 heartbeat timer 启动前就被关闭了。

**建议修复:**
```go
func (sc *ServerConn) waitHBTimeout() {
    sc.mtx.RLock()
    if sc.hbTick == nil {
        sc.mtx.RUnlock()
        // Timer 未启动，直接返回
        sc.log.Debugf("heartbeat timer not started yet, skip waiting")
        return
    }
    // ... 原有逻辑
}

// 或者在 Close() 中标记状态
func (sc *ServerConn) Close() {
    sc.closeOnce.Do(func() {
        sc.closed.Store(true)
        // ...
    })
}
```

**相关测试:**
- E2E-STREAM-003: TestStreamBidirectional
- E2E-CONN-002: TestConnectionGracefulClose

---

#### BUG-P2-002: 消息 Timeout 逻辑不符合预期

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P2-002 |
| **测试编号** | E2E-MSG-004 |
| **测试名称** | TestMessageWithTimeout |
| **问题类型** | 功能缺陷 |
| **严重级别** | 🟡 P2 - 一般 |
| **发现日期** | 2026-03-26 |
| **所属模块** | application/message.go |

**错误信息:**
```
publish failed: timeout
```

**问题描述:**
消息设置了 `SetTimeout(50 * time.Millisecond)`，但 `Publish` 是异步操作，timeout 不应该在此阶段触发。

**建议修复:**
- 明确 `Message.SetTimeout()` 的语义
- 如果是消息有效期，应该在服务端检查
- 如果是发送超时，应该使用 `context.WithTimeout`

**相关测试:**
- E2E-MSG-004: TestMessageWithTimeout

---

#### BUG-P2-003: RPC 错误处理测试不符合预期

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P2-003 |
| **测试编号** | E2E-RPC-002 |
| **测试名称** | TestRPCWithError |
| **问题类型** | 测试设计/功能疑问 |
| **严重级别** | 🟡 P2 - 一般 |
| **发现日期** | 2026-03-26 |
| **所属模块** | application/rpc.go |

**错误信息:**
```
call failed: intentional error
```

**问题描述:**
测试设置 `resp.SetError(expectedErr)`，但测试断言 `err == nil`。需要确认 RPC 错误是应该通过返回值还是响应对象传递。

**待确认:**
- 当前实现: 错误通过 `resp.Error()` 返回
- 测试期望: 错误通过 `err` 返回
- 需要统一错误处理语义

**相关测试:**
- E2E-RPC-002: TestRPCWithError

---

### 🟢 P3 - 轻微级别

#### BUG-P3-001: 编译错误 - 未使用的 import

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P3-001 |
| **测试编号** | BENCH-ALL |
| **测试名称** | All Benchmark Tests |
| **问题类型** | 编译错误 |
| **严重级别** | 🟢 P3 - 轻微 |
| **发现日期** | 2026-03-26 |
| **所属模块** | test/bench/benchmark_test.go |
| **代码位置** | line 6 |

**错误信息:**
```
test/bench/benchmark_test.go:6:2: "io" imported and not used
```

**修复方案:**
```go
// 删除或注释掉第6行
import (
    // "io"  // 删除这行
    "sync"
    "testing"
    // ...
)
```

**状态:** ✅ 已修复 (2026-03-26)

---

#### BUG-P3-002: 日志格式字符串错误

| 字段 | 内容 |
|-----|------|
| **问题编号** | BUG-P3-002 |
| **测试编号** | N/A |
| **测试名称** | N/A |
| **问题类型** | 格式错误 |
| **严重级别** | 🟢 P3 - 轻微 |
| **发现日期** | 2026-03-26 |
| **所属模块** | examples/mq/broker/broker.go |
| **代码位置** | line 137 |

**错误信息:**
```
examples/mq/broker/broker.go:137:6: log.Errorf format %s reads arg #1, but call has 0 args
```

**建议修复:**
```go
// 原代码
log.Errorf("... %s ...")  // 缺少参数

// 修复
log.Errorf("... %s ...", arg)
// 或
log.Error("...")
```

---

## 四、修复计划

### 4.1 修复优先级

```
第1周 (P0修复)
├── BUG-P0-001: Panic 修复 [2天]
├── BUG-P0-002: 死锁修复 [3天]
└── 回归测试 [1天]

第2周 (P1修复)
├── BUG-P1-001: Goroutine泄漏 [2天]
├── BUG-P1-002: 并发性能 [2天]
└── 性能测试 [1天]

第3周 (P2修复)
├── BUG-P2-001: Heartbeat错误 [1天]
├── BUG-P2-002: 消息Timeout [1天]
├── BUG-P2-003: RPC错误处理 [1天]
└── 文档更新 [2天]
```

### 4.2 修复状态跟踪

| 问题编号 | 负责人 | 计划开始 | 计划完成 | 状态 | 实际完成 |
|---------|--------|---------|---------|------|---------|
| BUG-P0-001 | TBD | - | - | 🔴 待修复 | - |
| BUG-P0-002 | TBD | - | - | 🔴 待修复 | - |
| BUG-P1-001 | TBD | - | - | 🔴 待修复 | - |
| BUG-P1-002 | TBD | - | - | 🔴 待修复 | - |
| BUG-P2-001 | TBD | - | - | 🟡 待修复 | - |
| BUG-P2-002 | TBD | - | - | 🟡 待修复 | - |
| BUG-P2-003 | TBD | - | - | 🟡 待修复 | - |
| BUG-P3-001 | TBD | - | - | ✅ 已修复 | 2026-03-26 |
| BUG-P3-002 | TBD | - | - | 🟡 待修复 | - |

---

## 五、测试覆盖矩阵

### 5.1 功能覆盖

| 功能模块 | 单元测试 | E2E测试 | 安全测试 | 基准测试 | 混沌测试 |
|---------|---------|---------|---------|---------|---------|
| Connection | ⬜ | ✅ | ✅ | ✅ | ✅ |
| Message | ⬜ | ✅ | ✅ | ✅ | ⬜ |
| RPC | ⬜ | ✅ | ✅ | ✅ | ⬜ |
| Stream | ✅ | ✅ | ✅ | ✅ | ⬜ |
| Multiplexer | ⬜ | ✅ | ✅ | ⬜ | ⬜ |
| Packet | ✅ | ⬜ | ⬜ | ⬜ | ⬜ |

### 5.2 测试用例清单

#### BENCH - 基准测试

| 编号 | 测试名称 | 目的 | 状态 |
|-----|---------|------|------|
| BENCH-MSG-001 | BenchmarkMessageThroughput | 消息吞吐量 | 🔴 编译错误 |
| BENCH-MSG-002 | BenchmarkMessageLatency | 消息延迟 | 🔴 编译错误 |
| BENCH-MSG-003 | BenchmarkMessageConcurrent | 并发消息性能 | 🔴 编译错误 |
| BENCH-RPC-001 | BenchmarkRPCLatency | RPC延迟 | 🔴 编译错误 |
| BENCH-RPC-002 | BenchmarkRPCThroughput | RPC吞吐量 | 🔴 编译错误 |
| BENCH-RPC-003 | BenchmarkRPCConcurrent | 并发RPC性能 | 🔴 编译错误 |
| BENCH-RPC-004 | BenchmarkRPCDifferentSizes | 不同数据大小RPC | 🔴 编译错误 |
| BENCH-STRM-001 | BenchmarkStreamThroughput | 流吞吐量 | 🔴 编译错误 |
| BENCH-STRM-002 | BenchmarkStreamConcurrent | 并发流性能 | 🔴 编译错误 |
| BENCH-END-001 | BenchmarkEndRawThroughput | End原始吞吐 | 🔴 编译错误 |
| BENCH-CONN-001 | BenchmarkConnectionCreation | 连接创建性能 | 🔴 编译错误 |
| BENCH-CONN-002 | BenchmarkStreamCreation | 流创建性能 | 🔴 编译错误 |
| BENCH-MEM-001 | BenchmarkMemoryAllocation | 内存分配 | 🔴 编译错误 |
| BENCH-MEM-002 | BenchmarkMemoryPressure | 内存压力 | 🔴 编译错误 |

#### E2E - 端到端测试

| 编号 | 测试名称 | 目的 | 状态 |
|-----|---------|------|------|
| E2E-CONN-001 | TestConnectionEstablishment | 连接建立 | ✅ 通过 |
| E2E-CONN-002 | TestConnectionGracefulClose | 优雅关闭 | ✅ 通过 |
| E2E-CONN-003 | TestConnectionWithMetadata | 元数据传输 | ✅ 通过 |
| E2E-CONN-004 | TestMultipleClients | 多客户端并发 | 🔴 失败(P0-001) |
| E2E-CONN-005 | TestConnectionReconnect | 重连功能 | ✅ 通过 |
| E2E-MSG-001 | TestMessageBasic | 基本消息 | ✅ 通过 |
| E2E-MSG-002 | TestMessageMultiple | 多消息 | ✅ 通过 |
| E2E-MSG-003 | TestMessageWithTopic | 主题消息 | ✅ 通过 |
| E2E-MSG-004 | TestMessageWithTimeout | 消息超时 | 🟡 失败(P2-002) |
| E2E-RPC-001 | TestRPCBasic | 基本RPC | ✅ 通过 |
| E2E-RPC-002 | TestRPCWithError | RPC错误 | 🟡 失败(P2-003) |
| E2E-RPC-003 | TestRPCMultipleMethods | 多方法 | ✅ 通过 |
| E2E-RPC-004 | TestRPCTimeout | RPC超时 | ✅ 通过 |
| E2E-RPC-005 | TestRPCConcurrent | 并发RPC | ✅ 通过 |
| E2E-STREAM-001 | TestStreamBasic | 基本流 | ✅ 通过 |
| E2E-STREAM-002 | TestStreamMultiple | 多流 | ✅ 通过 |
| E2E-STREAM-003 | TestStreamBidirectional | 双向流 | ✅ 通过(有警告) |
| E2E-STREAM-004 | TestStreamAfterEndClose | End关闭后流 | ✅ 通过 |
| E2E-RES-001 | TestResourceCleanup | 资源清理 | 🔴 失败(P1-001) |
| E2E-STRESS-001 | TestStressMixedOperations | 压力测试 | 🔴 失败(P0-002) |

#### SEC - 安全测试

| 编号 | 测试名称 | 目的 | 状态 |
|-----|---------|------|------|
| SEC-INPUT-001 | TestLargePayload | 大负载处理 | ⏸️ 待运行 |
| SEC-INPUT-002 | TestEmptyPayload | 空负载处理 | ⏸️ 待运行 |
| SEC-INPUT-003 | TestNilData | nil数据处理 | ⏸️ 待运行 |
| SEC-INPUT-004 | TestSpecialCharacters | 特殊字符 | ⏸️ 待运行 |
| SEC-BOUND-001 | TestBoundaryMessageID | 消息ID边界 | ⏸️ 待运行 |
| SEC-BOUND-002 | TestBoundaryStreamCount | 流数量边界 | ⏸️ 待运行 |
| SEC-BOUND-003 | TestBoundaryConnectionCount | 连接数边界 | ⏸️ 待运行 |
| SEC-DOS-001 | TestDoSRapidConnections | 快速连接攻击 | ⏸️ 待运行 |
| SEC-DOS-002 | TestDoSRapidMessages | 消息洪泛 | ⏸️ 待运行 |
| SEC-DOS-003 | TestDoSMemoryExhaustion | 内存耗尽 | ⏸️ 待运行 |
| SEC-FUZZ-001 | FuzzRPCData | RPC数据模糊 | ⏸️ 待运行 |
| SEC-FUZZ-002 | FuzzMessageData | 消息数据模糊 | ⏸️ 待运行 |
| SEC-FUZZ-003 | FuzzStreamData | 流数据模糊 | ⏸️ 待运行 |
| SEC-INJ-001 | TestSQLInjection | SQL注入 | ⏸️ 待运行 |
| SEC-INJ-002 | TestCommandInjection | 命令注入 | ⏸️ 待运行 |
| SEC-INJ-003 | TestPathTraversal | 路径遍历 | ⏸️ 待运行 |
| SEC-RACE-001 | TestRaceCloseAndSend | 关闭发送竞态 | ⏸️ 待运行 |
| SEC-RACE-002 | TestRaceMultipleCloses | 多关闭竞态 | ⏸️ 待运行 |
| SEC-RES-001 | TestResourceExhaustionStreams | 流资源耗尽 | ⏸️ 待运行 |
| SEC-RES-002 | TestResourceExhaustionGoroutines | goroutine泄漏 | ⏸️ 待运行 |
| SEC-TIME-001 | TestTimingSideChannel | 时序攻击 | ⏸️ 待运行 |
| SEC-AUTH-001 | TestUnauthorizedRPC | 未授权RPC | ⏸️ 待运行 |

---

## 附录

### A. 测试执行命令

```bash
# 运行所有测试
./test/run_tests.sh

# 运行特定类别测试
go test -v ./test/bench/        # 基准测试
go test -v ./test/e2e/          # E2E测试
go test -v ./test/security/     # 安全测试

# 运行特定问题相关测试
go test -v -run TestMultipleClients ./test/e2e/        # P0-001
go test -v -run TestStressMixedOperations ./test/e2e/  # P0-002
go test -v -run TestResourceCleanup ./test/e2e/        # P1-001
```

### B. 文档更新记录

| 日期 | 版本 | 修改内容 | 作者 |
|-----|------|---------|------|
| 2026-03-26 | 1.0 | 初始版本，记录9个问题 | Claude Code |

---

**文档结束**
