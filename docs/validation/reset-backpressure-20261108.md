# reset 积压恢复验证

本次修改针对 Frontier #117 的短流压测触发整连接关闭。

- resetPending 统计待发送与已经取出、尚未写完的通知。
- 256 是新流接入的高水位，不再是关闭连接的阈值。实际积压可以超过 256，已有流必须能够发布终止通知，内存上界与此前接纳的流数量有关；这不是全连接总流数配额。
- 达到高水位时，暂停本地 OpenDialogueContext，以及远端新流的 ACK。等待只在调用方/该流握手 goroutine，不能卡住共享读循环或 Abort。
- 实际完成 reset 写入后才减少计数，降到 128 时唤醒握手等待者。
- 本地等待支持调用方 context；远端等待沿用 30 秒握手时间范围，连接关闭取消等待。
- 已有流保留通路。写失败/持续写超时仍关闭连接，维持不可达对端的资源清理。
- 不新增 packet、不改变线格式；不支持 reset 的旧连接仍使用原有行为。

专项测试刻意暂停 reset 写入，建立 301 条流后 abort 其中 300 条：新建流等待、原有流双向传输、取消可退出、恢复后双向新建流成功、通知全部到达。重复 10 次并启用 race。

这些变更不是无限接纳新流的方案；远端可以在等待 ACK 时继续发起协商，协商中的对象仍受现有协议与调用方并发约束。独立的全连接接入配额不在此次范围内。

## 实测结果

基线 Geminio main `5b8b07f`，本机 Apple M4，GOMAXPROCS=4，Go 1.25.0。通过独立 modfile 将 Frontier benchmark 的 Geminio 替换为本地修复分支，正式部署与依赖文件未改变。路径为 Service SDK → Frontier → Edge SDK echo，loopback TCP，不包含 TUN/MASQUE/Docker。

| 新建流并发 | 实际测量次数 | 成功/秒 | P99 | 失败 |
| --- | ---: | ---: | ---: | ---: |
| 64（60s 目标，实际约 68.5s） | 858096 | 12533 | 9.893 ms | 0 |
| 256（30s 目标，实际约 34.6s） | 373153 | 10795 | 40.50 ms | 0 |

每次包含新建 Geminio 流、256 字节 echo 校验与本地 Abort。两轮合计 1,231,249 次，均使用独立 `-count=1` 进程，退出码成功且逐轮失败数为零。当前结果证明此次实验未复现此前断连，不等同于长期稳定性保证。旧基准使用 Go 1.24.13，因此不主张速度提升或零性能损耗。

- `make test-reset-pressure`：race 下重复 10 次通过。
- `make test-short-race`：通过。
- `make vet`：通过。
- Frontier 使用修复依赖执行 `make test-edge-route-recovery`：race 重复 5 次及 vet 均通过。

日志：`/private/tmp/geminio-reset-pressure-test.log`、`/private/tmp/geminio-reset-short-race.log`、`/private/tmp/frontier-reset-fixed-60s.log`、`/private/tmp/frontier-reset-fixed-c256.log`。
