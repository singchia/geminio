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
