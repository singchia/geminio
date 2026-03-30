# 心跳包优化方案

## 问题描述

当应用层或session层消息太多时，心跳包可能被挤压，导致：
- 心跳包发送延迟
- 心跳包接收延迟
- 连接可能因为心跳超时而被关闭

## 优化方案

### 1. **接收端优先处理** ✅ **已实现**

**位置**: `conn/conn_base.go`, `conn/conn_server.go`, `conn/conn_client.go`

**实现**:
- 在 `readPkt()` 中，心跳包被路由到 `heartbeatCh` 优先channel
- 在 `handlePkt()` 中，`heartbeatCh` 的 case 放在最前面，优先处理心跳包

**代码示例**:
```go
// 在 readPkt() 中
if pkt.Type() == packet.TypeHeartbeatPacket || pkt.Type() == packet.TypeHeartbeatAckPacket {
    select {
    case bc.heartbeatCh <- pkt:
    default:
        readInCh <- pkt  // fallback
    }
}

// 在 handlePkt() 中
select {
case pkt := <-heartbeatCh:
    // 优先处理心跳包
case pkt, ok := <-readInCh:
    // 处理其他数据包
}
```

**效果**:
- 心跳包不会被其他数据包阻塞
- 即使应用层消息很多，心跳包也能及时处理

---

### 2. **发送端优先处理** ✅ **已实现**

**位置**: `conn/conn_base.go`, `conn/conn_server.go`, `conn/conn_client.go`

**实现**:
- 添加 `heartbeatWriteCh` 优先channel用于心跳包发送
- 在 `writePkt()` 中，`heartbeatWriteCh` 的 case 放在最前面，优先发送心跳包
- 在 `handleOutHeartbeatPacket()` 和 `handleInHeartbeatPacket()` 中，心跳包优先发送到 `heartbeatWriteCh`

**代码示例**:
```go
// 在 writePkt() 中
select {
case pkt := <-heartbeatWriteCh:
    // 优先发送心跳包
case pkt, ok := <-writeOutCh:
    // 发送其他数据包
}

// 在 handleOutHeartbeatPacket() 中
select {
case cc.heartbeatWriteCh <- pkt:
    // 成功发送到优先channel
default:
    cc.writeOutCh <- pkt  // fallback
}
```

**效果**:
- 心跳包不会被应用层消息阻塞
- 即使 `writeOutCh` 被填满，心跳包也能及时发送

---

## 优化效果

### 接收端优化
- ✅ 心跳包通过 `heartbeatCh` 优先处理
- ✅ 不会被应用层消息阻塞
- ✅ 即使有大量应用层消息，心跳包也能及时处理

### 发送端优化
- ✅ 心跳包通过 `heartbeatWriteCh` 优先发送
- ✅ 不会被应用层消息阻塞
- ✅ 即使 `writeOutCh` 被填满，心跳包也能及时发送

### 整体效果
- ✅ 心跳包在接收和发送两端都有优先处理
- ✅ 确保心跳包及时处理，避免连接超时
- ✅ 提高系统稳定性和可靠性

---

## 其他优化建议

### 1. **增加缓冲区大小**

如果心跳包仍然被阻塞，可以考虑增加 `heartbeatCh` 和 `heartbeatWriteCh` 的缓冲区大小：

```go
sc.heartbeatCh = make(chan packet.Packet, 20) // 从 10 增加到 20
sc.heartbeatWriteCh = make(chan packet.Packet, 20)
```

### 2. **监控心跳包延迟**

可以添加监控指标，跟踪心跳包的发送和接收延迟：

```go
// 在 sendHeartbeat 中记录发送时间
sendTime := time.Now()
// 在 handleInHeartbeatAckPacket 中计算延迟
latency := time.Since(sendTime)
```

### 3. **动态调整心跳间隔**

根据网络状况和应用负载，动态调整心跳间隔：

```go
// 如果检测到心跳延迟，可以缩短心跳间隔
if latency > threshold {
    // 缩短心跳间隔
}
```

---

## 相关文件

- `conn/conn_base.go`: 基础连接实现，包含 `writePkt()` 和 `readPkt()`
- `conn/conn_client.go`: 客户端连接实现，包含心跳包发送逻辑
- `conn/conn_server.go`: 服务端连接实现，包含心跳包接收和响应逻辑
