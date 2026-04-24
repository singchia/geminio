# Gemino 测试套件

本目录包含 Gemino 项目的全面测试套件，包括基准测试、端到端(E2E)测试和安全测试。

## 目录结构

```
test/
├── bench/           # 基准性能测试
├── e2e/             # 端到端集成测试
├── security/        # 安全测试
├── chaos/           # 混沌测试
├── regression/      # 回归测试
├── run_tests.sh     # 测试运行脚本
└── README.md        # 本文档
```

## 测试类型

### 1. Benchmark 测试 (`test/bench/`)

性能基准测试，用于评估各种操作的性能指标。

#### 测试内容：
- **Message Benchmarks**: 消息发布/接收性能
  - `BenchmarkMessageThroughput`: 消息吞吐量测试
  - `BenchmarkMessageLatency`: 消息延迟测试
  - `BenchmarkMessageConcurrent`: 并发消息测试

- **RPC Benchmarks**: RPC 调用性能
  - `BenchmarkRPCLatency`: RPC 延迟测试
  - `BenchmarkRPCThroughput`: RPC 吞吐量测试
  - `BenchmarkRPCConcurrent`: 并发 RPC 测试
  - `BenchmarkRPCDifferentSizes`: 不同数据大小 RPC 测试

- **Stream Benchmarks**: 流操作性能
  - `BenchmarkStreamThroughput`: 流吞吐量测试
  - `BenchmarkStreamConcurrent`: 多流并发测试

- **End Benchmarks**: End 对象性能
  - `BenchmarkEndRawThroughput`: 原始数据吞吐测试
  - `BenchmarkConnectionCreation`: 连接创建性能
  - `BenchmarkStreamCreation`: 流创建性能

- **Memory Benchmarks**: 内存分配测试
  - `BenchmarkMemoryAllocation`: 内存分配基准
  - `BenchmarkMemoryPressure`: 内存压力测试

#### 运行方式：
```bash
# 运行所有基准测试
go test -bench=. -benchmem ./test/bench/

# 运行特定测试
go test -bench=BenchmarkMessage -benchmem ./test/bench/

# 长时间运行以获得更准确结果
go test -bench=. -benchmem -benchtime=30s ./test/bench/
```

### 2. E2E 集成测试 (`test/e2e/`)

端到端场景测试，验证真实使用场景。

#### 测试内容：

- **Connection Lifecycle Tests**: 连接生命周期
  - `TestConnectionEstablishment`: 连接建立
  - `TestConnectionGracefulClose`: 优雅关闭
  - `TestConnectionWithMetadata`: 带元数据的连接

- **Multiple Connections Tests**: 多连接测试
  - `TestMultipleClients`: 多客户端并发连接

- **Message Tests**: 消息传递测试
  - `TestMessageBasic`: 基本消息传递
  - `TestMessageMultiple`: 多条消息测试
  - `TestMessageWithTopic`: 带主题的消息
  - `TestMessageWithTimeout`: 消息超时测试

- **RPC Tests**: RPC 调用测试
  - `TestRPCBasic`: 基本 RPC 调用
  - `TestRPCWithError`: 错误响应测试
  - `TestRPCMultipleMethods`: 多方法注册
  - `TestRPCTimeout`: RPC 超时测试
  - `TestRPCConcurrent`: 并发 RPC 测试

- **Stream Tests**: 流操作测试
  - `TestStreamBasic`: 基本流操作
  - `TestStreamMultiple`: 多流管理
  - `TestStreamBidirectional`: 双向数据传输

- **Error Recovery Tests**: 错误恢复测试
  - `TestConnectionReconnect`: 连接重连
  - `TestStreamAfterEndClose`: End 关闭后的流行为

- **Resource Cleanup Tests**: 资源清理测试
  - `TestResourceCleanup`: 资源泄漏检测

- **Stress Tests**: 压力测试
  - `TestStressMixedOperations`: 混合操作压力测试

#### 运行方式：
```bash
# 运行所有 E2E 测试
go test -v ./test/e2e/

# 运行特定测试
go test -v -run TestRPC ./test/e2e/

# 包含压力测试
go test -v ./test/e2e/ -timeout=10m
```

### 3. Security 测试 (`test/security/`)

安全测试，验证系统对各种攻击和异常输入的防御能力。

#### 测试内容：

- **Input Validation Tests**: 输入验证测试
  - `TestLargePayload`: 大负载处理
  - `TestEmptyPayload`: 空负载处理
  - `TestNilData`: nil 数据处理
  - `TestSpecialCharacters`: 特殊字符处理

- **Boundary Tests**: 边界测试
  - `TestBoundaryMessageID`: 消息 ID 边界
  - `TestBoundaryStreamCount`: 流数量边界
  - `TestBoundaryConnectionCount`: 连接数量边界

- **DoS Protection Tests**: DoS 防护测试
  - `TestDoSRapidConnections`: 快速连接攻击
  - `TestDoSRapidMessages`: 消息洪泛攻击
  - `TestDoSMemoryExhaustion`: 内存耗尽攻击

- **Fuzzing Tests**: 模糊测试
  - `TestFuzzMessageContent`: 消息内容模糊测试
  - `TestFuzzRPCMethod`: RPC 方法模糊测试
  - `FuzzRPCData`: RPC 数据原生模糊测试
  - `FuzzMessageData`: 消息数据原生模糊测试
  - `FuzzStreamData`: 流数据原生模糊测试

- **Injection Tests**: 注入攻击测试
  - `TestSQLInjection`: SQL 注入测试
  - `TestCommandInjection`: 命令注入测试
  - `TestPathTraversal`: 路径遍历测试

- **Race Condition Tests**: 竞态条件测试
  - `TestRaceCloseAndSend`: 关闭与发送竞态
  - `TestRaceMultipleCloses`: 多次关闭竞态

- **Resource Exhaustion Tests**: 资源耗尽测试
  - `TestResourceExhaustionStreams`: 流资源耗尽
  - `TestResourceExhaustionGoroutines`: goroutine 泄漏测试

- **Timing Attack Tests**: 时序攻击测试
  - `TestTimingSideChannel`: 时序侧信道检测

- **Permission Tests**: 权限测试
  - `TestUnauthorizedRPC`: 未授权 RPC 调用

#### 运行方式：
```bash
# 运行所有安全测试
go test -v ./test/security/

# 运行带 race detector
go test -race -v ./test/security/

# 运行 fuzzing 测试 (Go 1.18+)
go test -fuzz=FuzzRPCData -fuzztime=30s ./test/security/
go test -fuzz=FuzzMessageData -fuzztime=30s ./test/security/
go test -fuzz=FuzzStreamData -fuzztime=30s ./test/security/
```

### 4. 混沌测试 (`test/chaos/`)

网络故障模拟测试。

- **Linux Only**: 使用 iptables 模拟网络丢包
- `TestPacketDrop`: 测试自动重连功能

### 5. 回归测试 (`test/regression/`)

基础功能回归测试。

- `TestCall`: RPC 调用基础测试
- `TestMessage`: 消息传递基础测试
- `TestServer`: 服务器基础测试

## 快速运行所有测试

使用提供的脚本一键运行所有测试：

```bash
# 运行完整测试套件
cd test
./run_tests.sh

# 或者从项目根目录
./test/run_tests.sh
```

## 手动运行测试

### 运行全部测试
```bash
go test ./test/...
```

### 带覆盖率报告
```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

### 带 Race Detector
```bash
go test -race ./test/...
```

### 性能分析
```bash
# CPU 分析
go test -bench=. -cpuprofile=cpu.prof ./test/bench/
go tool pprof cpu.prof

# 内存分析
go test -bench=. -memprofile=mem.prof ./test/bench/
go tool pprof mem.prof
```

## 测试环境要求

- Go 1.18+ (支持 fuzzing)
- Linux 系统 (混沌测试需要)
- iptables (混沌测试需要)

## 持续集成建议

在 CI/CD 中建议按以下顺序运行测试：

1. **快速检查**: `go test -short ./...`
2. **单元测试**: `go test ./...`
3. **Race 检测**: `go test -race ./...`
4. **基准测试**: `go test -bench=. -benchtime=10s ./test/bench/`
5. **E2E 测试**: `go test -v ./test/e2e/`
6. **安全测试**: `go test -v ./test/security/`
7. **覆盖率检查**: `go test -coverprofile=coverage.out ./...`

## 注意事项

1. 部分测试需要较长时间，请根据 CI 环境调整超时设置
2. 混沌测试仅在 Linux 系统上运行
3. Fuzzing 测试需要 Go 1.18+
4. 压力测试可能占用大量系统资源
5. Race detector 会显著增加测试时间
