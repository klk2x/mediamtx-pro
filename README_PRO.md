# MediaMTX Pro

MediaMTX Pro 是 MediaMTX 的增强版本，提供额外的专业功能。

## Pro 版本特性

### 1. **智能录制系统**
- 自动录制管理
- 按日期组织的文件结构
- 网络采集设备智能检测（基于彩色内容）
- 录制任务超时控制
- 文件管理 API（重命名、删除、收藏）

### 2. **健康检查系统**
- 网络采集设备健康监控
- 自动故障检测和恢复
- 设备状态实时监控

### 3. **WebSocket 实时通信**
- WebSocket Hub 管理多客户端连接
- 实时消息广播
- 连接限制和缓冲管理
- 心跳机制保持连接

### 4. **Token 认证系统**
- 基于 AppID/AppSecret 的 JWT 认证
- 无需外部认证服务
- 向后兼容原有认证系统

### 5. **文件导出功能**
- FFmpeg 集成实现视频格式转换
- 支持 MP4 格式导出

## 项目结构

```
mediamtx-pro/
├── internal/               # MediaMTX 核心代码
│   ├── api/               # 基础 API
│   ├── auth/              # 认证管理
│   ├── conf/              # 配置管理
│   ├── logger/            # 日志系统
│   ├── protocols/         # 协议实现 (RTSP, RTMP, WebRTC, etc.)
│   └── servers/           # 服务器实现
│
├── pro/                   # Pro 版本专属功能
│   ├── api/              # Pro API 服务
│   │   ├── api_v2.go                # API v2 主文件
│   │   ├── api_v2_extended.go       # 扩展端点（文件管理、Dashboard等）
│   │   ├── api_v2_snapshot.go       # 快照相关
│   │   ├── api_v2_ffmpeg.go         # FFmpeg 导出
│   │   ├── api_v2_static.go         # 静态资源服务
│   │   ├── api_image.go             # 图像分析（彩色检测）
│   │   └── auth_api.go              # Token 认证中间件
│   │
│   ├── core/             # Pro Core 启动和管理
│   │   ├── core.go                  # 核心启动逻辑
│   │   └── encrypt.go               # 加密工具
│   │
│   ├── recorder/         # 录制管理系统
│   │   ├── manager.go               # 录制管理器
│   │   └── task.go                  # 录制任务
│   │
│   ├── healthcheck/      # 健康检查系统
│   │   └── checker.go               # 健康检查器
│   │
│   ├── websocketapi/     # WebSocket 实时通信
│   │   └── websocket.go             # WebSocket Hub 实现
│   │
│   ├── recordcleaner/    # 录制文件清理
│   │   └── cleaner.go               # 定时清理任务
│   │
│   ├── deviceutil/       # 设备工具
│   │   └── device.go                # 设备状态检查
│   │
│   └── rvideo/           # R-Video 编解码服务集成
│       └── rvideo.go                # R-Video 客户端
│
├── apidocs/              # API 文档
├── mediamtx.yml          # 配置文件示例
├── config.yml            # pro 启动配置文件
└── main.go               # 程序入口
```

## API 端点

### 基础端点

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/api/v2/info` | 服务器信息 |
| GET | `/api/v2/health` | 健康状态 |
| GET | `/api/v2/stats` | 统计信息 |

### 路径管理

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/api/v2/paths/list` | 路径列表 |
| GET | `/api/v2/paths/get/:name` | 获取路径详情 |
| GET | `/api/v2/paths/query` | 查询路径（带过滤） |
| POST | `/api/v2/paths/message` | 发送 WebSocket 广播消息 |

### 录制管理

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | `/api/v2/record/start` | 开始录制 |
| POST | `/api/v2/record/stop` | 停止录制 |
| GET | `/api/v2/record/task/:name` | 获取录制任务状态 |
| GET | `/api/v2/record/tasks` | 获取所有录制任务 |

### 文件管理

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/api/v2/dashboard` | Dashboard 数据 |
| GET | `/api/v2/record/date/files` | 按日期获取文件列表 |
| GET | `/api/v2/record/favorite/files` | 获取收藏文件列表 |
| POST | `/api/v2/file/rename` | 重命名文件 |
| POST | `/api/v2/file/del` | 删除文件 |
| POST | `/api/v2/file/favorite` | 移动文件到收藏 |
| POST | `/api/v2/file/export/mp4` | 导出为 MP4 格式 |

### 快照功能

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/api/v2/snapshot` | 获取快照 |
| GET | `/api/v2/publish/snapshot` | 获取发布流快照 |
| GET | `/api/v2/snapshot/config/:name` | 获取快照配置 |
| POST | `/api/v2/snapshot/config/:name` | 保存快照配置 |

### 设备代理

| 方法 | 路径 | 描述 |
|------|------|------|
| ANY | `/api/v2/proxy/device/*path` | 代理到设备 |

### WebSocket

| 方法 | 路径 | 描述 |
|------|------|------|
| WS | `/ws` | WebSocket 实时通信连接 |

### 静态资源

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/res/*filepath` | 访问录制文件 |

## Token 认证

### 配置

在 `mediamtx.yml` 中启用 Token 认证：

```yaml
# 启用 API Token 认证
apiAuth: true

# 应用 ID（用于生成和验证 token）
appid: "your-app-id"

# 应用密钥（用于签名 token）
appsecret: "your-app-secret"
```

### 生成 Token

使用 LiveKit Protocol 库生成 JWT token：

```go
package main

import (
    "fmt"
    "github.com/livekit/protocol/auth"
)

func main() {
    appID := "your-app-id"
    appSecret := "your-app-secret"

    // 创建 Access Token
    at := auth.NewAccessToken(appID, appSecret)

    // 生成 JWT
    token, err := at.ToJWT()
    if err != nil {
        panic(err)
    }

    fmt.Println("Token:", token)
}
```

### 使用 Token

**方式 1: Authorization Header**
```bash
curl -H "Authorization: Bearer YOUR_TOKEN" \
  http://localhost:9997/api/v2/paths/list
```

**方式 2: Query Parameter**
```bash
curl http://localhost:9997/api/v2/paths/list?access_token=YOUR_TOKEN
```

## WebSocket 使用

### 连接

```javascript
// 创建 WebSocket 连接
const ws = new WebSocket('ws://localhost:9997/ws');

ws.onopen = () => {
    console.log('WebSocket connected');
};

ws.onmessage = (event) => {
    const message = JSON.parse(event.data);
    console.log('Received:', message);
};

ws.onerror = (error) => {
    console.error('WebSocket error:', error);
};
```

### 广播消息

通过 API 向所有连接的 WebSocket 客户端广播消息：

```bash
curl -X POST http://localhost:9997/api/v2/paths/message \
  -H "Content-Type: application/json" \
  -d '{
    "type": "notification",
    "data": "Recording started"
  }'
```

## 录制管理

### 开始录制

```bash
curl -X POST http://localhost:9997/api/v2/record/start \
  -H "Content-Type: application/json" \
  -d '{
    "name": "mystream",
    "videoFormat": "mp4",
    "taskOutMinutes": 30,
    "fileName": "custom-name.mp4"
  }'
```

### 停止录制

```bash
curl -X POST http://localhost:9997/api/v2/record/stop \
  -H "Content-Type: application/json" \
  -d '{
    "name": "mystream"
  }'
```

## 配置示例

```yaml
# API 服务
api: yes
apiAddress: :9997

# Token 认证
apiAuth: true
appid: "mediamtx-app"
appsecret: "your-secret-key-here"

# 录制路径
paths:
  mystream:
    source: rtsp://camera-ip/stream

    # 启用自动录制
    record: yes
    recordPath: ./recordings

    # 录制任务超时（秒）
    autoRecordTaskOutDuration: 1800

    # 文件清理（保留天数）
    recordClearDaysAgo: 30

    # 设备类型（用于智能录制）
    deviceType: network_capture

    # 彩色内容阈值
    recordMinThreshold: 60

    # 显示名称
    sourceName: "前门摄像头"
    groupName: "一楼"
    order: 1
    showList: true
```

## 智能录制

Pro 版本支持网络采集设备的智能录制，基于视频内容的彩色检测：

1. **设备健康检查**: 自动检测设备是否可用
2. **彩色内容检测**: 分析视频帧的彩色占比
3. **阈值判断**: 连续 3 次检查，累计彩色值超过阈值时开始录制
4. **自动停止**: 流断开时自动停止录制

配置参数：
- `deviceType: network_capture` - 启用智能录制
- `recordMinThreshold: 60` - 彩色阈值（默认 60）

## 文件结构

录制文件按日期组织：

```
recordings/
├── 20250116/
│   ├── 20250116-1430-abc12345.mp4
│   └── 20250116-1530-def67890.mp4
├── 20250117/
│   └── 20250117-0900-ghi11223.mp4
└── favorite/
    └── important-recording.mp4
```

## 健康检查

健康检查系统监控网络采集设备状态：

- **检查间隔**: 每 30 秒
- **失败阈值**: 连续 3 次失败触发 `pathNotReady`
- **恢复检测**: 设备恢复后自动触发 `pathReady`
- **自动录制**: 与智能录制系统集成

## 构建

```bash
# 构建 Pro 版本
go build -o mediamtx-pro .

# 运行
./mediamtx-pro mediamtx.yml
```

## 依赖项

Pro 版本额外依赖：

- `github.com/livekit/protocol` - Token 认证
- `github.com/gorilla/websocket` - WebSocket 支持
- `github.com/disintegration/imaging` - 图像处理
- FFmpeg - 视频格式转换（需要系统安装）

## 许可证

Pro 版本需要有效的许可证密钥。在配置文件中设置：

```yaml
coreServerKey: "YOUR_LICENSE_KEY"
```

许可证密钥格式：
- MAC 地址绑定
- 过期日期控制
- 域名限制（可选）

---

更多信息请访问 [MediaMTX 官方文档](https://mediamtx.org)
