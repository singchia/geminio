# Gemino Bug 追踪表

**快速参考版本** | [详细测试计划](./TEST_PLAN.md)

---

## 🔴 P0 - 致命 (立即修复)

### BUG-P0-001: Panic - 向已关闭 channel 发送数据
- **位置:** `multiplexer/dialogue.go:702`
- **触发:** `TestMultipleClients` (E2E-CONN-004)
- **现象:** `panic: send on closed channel`
- **修复:** 添加 select + default 保护
```go
select {
case d.writeInCh <- pkt:
default:
    d.log.Debugf("channel closed, skip")
}
```

### BUG-P0-002: 死锁 - dialogueMgr 锁竞争
- **位置:** `multiplexer/dialogue_mgr.go:388`
- **触发:** `TestStressMixedOperations` (E2E-STRESS-001)
- **现象:** 系统卡死，10分钟超时
- **修复:** 使用分段锁或 channel 替代
```go
// 方案: 分段锁
type dialogueMgr struct {
    shards [16]dialogueShard
}
```

---

## 🟠 P1 - 严重 (本周修复)

### BUG-P1-001: Goroutine 泄漏 (60+残留)
- **位置:** `conn/`, `multiplexer/`, `application/`
- **触发:** `TestResourceCleanup` (E2E-RES-001)
- **现象:** 10个连接关闭后残留60个goroutine
- **修复:** 使用 context 控制生命周期
```go
type dialogue struct {
    ctx    context.Context
    cancel context.CancelFunc
}
```

### BUG-P1-002: 高并发 Stream 创建超时
- **位置:** `multiplexer/dialogue_mgr.go:284`
- **触发:** `TestStressMixedOperations`
- **现象:** `dialogue open err: timeout`
- **修复:** 异步创建 + 预创建池

### BUG-P1-003: Benchmark 死锁 — drain goroutine 与 defer Close 互等
- **发现日期:** 2026-03-27
- **位置:** `test/bench/benchmark_test.go`（`BenchmarkMessageThroughput` 等所有带 drain goroutine 的 benchmark）
- **触发:** `go test -bench=BenchmarkEnd -benchtime=5s ./test/bench/...` 挂起超过 10 分钟
- **现象:** goroutine dump 显示 `BenchmarkMessageThroughput.func1` 在 `sEnd.Receive(context.TODO())` 处 `[select, 10 minutes]` 阻塞
- **根因:** 经典循环死锁
  1. `<-done` 等 drain goroutine 退出
  2. drain goroutine 等 `sEnd.Receive()` 返回 error
  3. `sEnd.Receive()` 等 `sEnd.Close()` 被调用
  4. `sEnd.Close()` 是 `defer`，等函数返回
  5. 函数因 `<-done` 无法返回 → 死锁
- **修复:** 将 `defer sEnd.Close()` / `defer cEnd.Close()` 改为在 `<-done` 前显式调用：
```go
b.StopTimer()
cEnd.Close()   // 触发服务端 EOF，解除 drain goroutine 阻塞
<-done
sEnd.Close()
```

---

## 🟡 P2 - 一般 (下周修复)

### BUG-P2-001: Heartbeat 定时器未启动错误
- **发现日期:** 2026-03-27
- **位置:** `conn/conn_server.go:522-531`（`waitHBTimeout` 回调）
- **触发:** benchmark 高频创建/销毁连接（`BenchmarkStreamConcurrent`、`BenchmarkStreamCreation` 等）
- **现象:** `ERROR conn.(*ServerConn).waitHBTimeout wait HEARTBEAT err: timer not started`
- **根因:** 连接关闭速度快于 heartbeat timer 启动，timer 回调收到 `timer not started` 错误后走入 `Errorf` 分支打印误报 ERROR，随后调用 `sc.Close()`（功能不受影响）
- **修复:** 在 `waitHBTimeout` 中对 `timer not started` 降级为 Debug：
```go
if event.Error == timer.ErrTimerForceClosed || strings.Contains(event.Error.Error(), "timer not started") {
    sc.log.Debugf(...)
} else {
    sc.log.Errorf(...)
}
```
或确认 go-timer 是否暴露 `ErrTimerNotStarted` 常量直接比较

### BUG-P2-002: 消息 Timeout 逻辑不符合预期
- **位置:** `application/message.go`
- **触发:** `TestMessageWithTimeout`
- **现象:** 异步 Publish 返回 timeout
- **修复:** 明确 timeout 语义

### BUG-P2-003: RPC 错误处理测试疑问
- **位置:** `application/rpc.go`
- **触发:** `TestRPCWithError`
- **现象:** 错误通过 resp 还是 err 返回？
- **修复:** 统一错误处理语义

---

## 🟢 P3 - 轻微 (有空修复)

### BUG-P3-001: 编译错误 - 未使用的 import ✅ 已修复
- **位置:** `test/bench/benchmark_test.go:6`
- **修复:** 删除 `"io"` import

### BUG-P3-002: 日志格式字符串错误
- **位置:** `examples/mq/broker/broker.go:137`
- **现象:** `log.Errorf format %s reads arg #1, but call has 0 args`
- **修复:** 添加缺失参数或改用 `log.Error()`

### BUG-P3-003: 测试脚本误报 — `signal: killed` on `conn` 包
- **发现日期:** 2026-03-27
- **位置:** `test/run_tests.sh:82`
- **现象:** `FAIL github.com/singchia/gemino/conn — signal: killed`，实为伪失败
- **根因:** `go test -v ./... -short -count=1 2>&1 | head -100` 中 `head -100` 读够 100 行后退出，向 `go test` 发送 SIGPIPE，`conn` 包进程被杀死。`conn` 包仅含 `BenchmarkSelectClosed`，无 `Test*` 函数，测试结果无意义但被误报为 FAIL
- **修复:** 脚本改为只跑 Test*，或去掉 `| head -100`：
```bash
go test -v ./... -short -count=1 -run Test 2>&1 || true
```

---

## 修复优先级

```
第1周: P0 修复 (致命问题)
├── BUG-P0-001 [2天]
├── BUG-P0-002 [3天]
└── 回归测试 [1天]

第2周: P1 修复 (严重问题)
├── BUG-P1-001 [2天]
├── BUG-P1-002 [2天]
└── 性能测试 [1天]

第3周: P2 + P3 修复
├── BUG-P2-001~003 [3天]
├── BUG-P3-002 [0.5天]
└── 文档更新 [1.5天]
```

---

## 快速修复检查清单

### 修复前
- [ ] 复现问题
- [ ] 添加调试日志
- [ ] 确认根因

### 修复后
- [ ] 单元测试通过
- [ ] 原失败测试通过
- [ ] Race detector 通过
- [ ] 性能无明显下降
- [ ] 代码审查

---

## 状态图例

| 图标 | 含义 |
|-----|------|
| 🔴 | 致命 - 生产不可用 |
| 🟠 | 严重 - 影响性能/稳定性 |
| 🟡 | 一般 - 功能缺陷 |
| 🟢 | 轻微 - 编译/格式问题 |
| ✅ | 已修复 |
| ⏸️ | 待修复 |

---

**最后更新:** 2026-03-27
