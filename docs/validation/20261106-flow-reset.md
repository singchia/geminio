# 数据面修复发布记录

## 范围

此分支基于 main `56600f5`，包含此前本地验证的完整数据面修复：

- `bed932e`：有界双向待处理队列，避免背压阻塞另一方向。
- `562f51d`：本地 ForceClose 与阻塞写取消。
- `da5f776`：心跳与数据背压解耦。
- `2b276bc`：传播开流取消。
- 后续逐流字节/包窗口、WindowUpdate 协商、慢流隔离。
- StreamReset 协商、AbortStream、关闭总时限、窗口耗尽与开流期间终止。
- 注册通知与等待竞争、RPC hijack 发布、服务端回调初始化、快速 reset 清理的修复。

兼容说明见 `MULTIPLEXING.md` / `MULTIPLEXING_cn.md` 和 `FORCE_CLOSE.md`。只有协商成功的连接使用新控制包；旧端回退不提供相同的慢流隔离与远端异常终止保证。ForceClose 仍为本地关闭，AbortStream 是协商后的远端通知。

## 源码恢复

历史临时工作树文件已被清理，因此从保留的修改记录恢复未提交的后续代码。在 `2b276bc` 上恢复后，历史 stream-reset 验证清单中的 128 个 Go 文件逐一 SHA-256 匹配，无差异。清单保存在 `20261029-stream-reset.sha256`。

原开发工作区的其他修改未纳入本分支。此次新增的 Frontlas 注册恢复和跨连接 ClientID 问题仍属于待修复事项，不能视为已由本分支解决。

## 本轮验证

- 全包 short race 回归：通过。
- `go vet ./...`：通过。
- Frontier SDK 与 exchange 使用本分支依赖进行 race 回归：通过。
- `make test-race`：全包完整 race 测试通过，包含 chaos（229.707 秒）、integration、e2e、security、regression；无 race 报告。
- 本轮在 macOS 上运行；Linux 专属故障注入测试不在本轮覆盖范围内。

根 Makefile 新增 `test-short-race`、`test-race`、`vet`，便于复验。默认完整测试超时为每包 600 秒，可用 `TEST_TIMEOUT` 覆盖。
