# WebSocket API 安全建议和改进方案

## 紧急修复清单

### 1. 立即修复（严重）
- [ ] 限制 CORS，不要允许所有来源
- [ ] 添加身份认证机制
- [ ] 限制最大连接数（建议 1000-5000）
- [ ] 添加连接速率限制

### 2. 高优先级修复
- [ ] 添加消息缓冲区大小限制
- [ ] 实现完整的心跳机制（包括读取端）
- [ ] 修复 Goroutine 泄露问题
- [ ] 改进资源清理逻辑

### 3. 代码质量改进
- [ ] 使用结构体封装而不是全局变量
- [ ] 集成 MediaMTX 的 logger
- [ ] 添加配置选项
- [ ] 添加单元测试

## 完整的安全配置示例

```go
type WebSocketAPIConfig struct {
    MaxConnections    int           // 最大连接数
    AllowOrigin       string        // 允许的源
    PingInterval      time.Duration // 心跳间隔
    PongTimeout       time.Duration // Pong 超时
    WriteTimeout      time.Duration // 写超时
    MessageBufferSize int           // 消息缓冲区大小
    MaxMessageSize    int64         // 最大消息大小
}

var DefaultConfig = WebSocketAPIConfig{
    MaxConnections:    1000,
    AllowOrigin:       "*", // 生产环境应改为具体域名
    PingInterval:      10 * time.Second,
    PongTimeout:       60 * time.Second,
    WriteTimeout:      10 * time.Second,
    MessageBufferSize: 256,
    MaxMessageSize:    512,
}
```

## 使用示例

```go
// 在 Pro API 中注册
wsAPI := &websocketapi.WebSocketAPI{}
wsAPI.Initialize(websocketapi.DefaultConfig, a)

router.GET("/ws", wsAPI.HandleConnection)

// 发送消息
wsAPI.Broadcast(myData)
```

## 监控指标建议

- 当前连接数
- 消息发送/接收速率
- 错误率
- 平均延迟
- 断开连接原因统计

## 测试建议

1. 并发连接测试（测试最大连接数限制）
2. 消息广播压力测试
3. 客户端异常断开测试
4. 内存泄漏测试（长时间运行）
5. 恶意连接测试（认证失败、过快连接）
