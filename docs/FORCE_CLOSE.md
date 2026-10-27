# 本地强制关闭

`Stream.ForceClose() error` 发起单流本地强制终止，不等待数据排空、关闭握手或资源清理完成。可重复调用，也可在 `Close()` 已发起后调用。已有应用接收缓冲仍可能被 `Read` 读到，之后读到 EOF；它不是远端数据交付确认。

`End.ForceClose()` 则终止整个底层连接及其流；RetryEnd 同时禁用重连。单流取消使用 dialogue 原有的 context，不增加常驻 goroutine 或平行的 aborted channel。

正常 `Close()` 保留 Dismiss 握手。正常结束时 writer 先排空 writeOutCh，包括最终 ACK，再取消 dialogue context；ForceClose 直接取消，底层 WriteContext 立即停止等待共享发送队列。发送线程不直接关闭 application 接收 channel，接收线程统一收尾。

本操作不发送单流 reset。对端可能继续发送并保留资源；本地 manager 丢弃已移除流的数据，当前实现可能记录 unknown dialogue 日志。应用若要求两端及时回收，仍需要对端通知或有界的空闲超时策略。不要用关闭整个连接代替单流通知，否则会影响其他流。

此变更给 Stream、Dialogue 和 Conn 接口增加方法。自定义实现和 mock 需要适配；仓库内 mock 已用 mockgen 重新生成。

验证包括：

- 旧流停止读取，ForceClose 后同一连接的新流能建立并传输。
- 多个 ForceClose 与正常 Close 并发，无数据 channel 关闭竞态。
- 单流写队列等待取消后，底层连接仍可用。
- 原有正常关闭、半关闭及双向背压回归。

测试命令：

```sh
go test -race ./conn ./test/integration ./test/unit ./test/regression -timeout=120s
go test -short ./... -timeout=90s
go vet ./...
```

## 有界心跳队列与可取消建流

心跳请求/响应使用独立的 10 包发送队列；收到心跳 ACK 不再等待数据输出排空。现有 socket writer 在两次写入之间优先发送心跳，不增加每条流的常驻 goroutine，也不改动数据和关闭 ACK 的顺序。控制队列满时关闭当前底层连接，避免无界积压；它不能抢占已经阻塞的 socket 写入，也不能解决共享 TCP 的队头阻塞。

使用可取消建流接口：

```go
ctx, cancel := context.WithTimeout(parent, 5*time.Second)
defer cancel()
stream, err := geminio.OpenStreamContext(ctx, end, options.OpenStream())
```

`ContextMultiplexer` 是可选扩展接口，原 `End` / `Multiplexer` 接口不增加必选方法。内置 client、server、RetryEnd 均支持；自定义 End 不支持时返回 `ErrOpenStreamContextUnsupported`，不会偷偷忽略取消。旧 `OpenStream` 仍使用原有默认协商超时。

取消覆盖协商包入队和 Session ACK 等待；失败后强制结束本地 dialogue 并移除 negotiating 登记。晚到 ACK 不会重新发布流。成功返回后，建流 ctx 的取消不关闭 stream；其生命周期仍由调用方管理。

RetryEnd 的重连等待可取消；同一端点的建流请求共享最多一个重连 worker，调用方取消不会中断其他调用方共用的重连。传统 Dialer 本身没有 context；若自定义 Dialer 永久不返回，共享重连 worker 仍可能等待，但已取消的 OpenStreamContext 会返回。

此处没有新增远端 reset 协议。若对端已创建流，本地取消不保证远端立即回收，也不把请求 context 自动传入远端应用。manager 的逐流队列隔离属于后续工作。

定向回归：

```sh
go test -race ./conn ./multiplexer ./test/integration -run 'TestHeartbeat|TestOpenDialogueContext|TestOpenStreamContext|TestForceCloseAbandonedStream|TestFullDuplexBackpressure' -count=20 -timeout=180s
```
