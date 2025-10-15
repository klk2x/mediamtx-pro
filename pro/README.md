# MediaMTX Pro

这是 MediaMTX 的 Pro 版本，完全独立于原仓库，不干扰原始代码。

## 项目结构

```
mediamtx-pro/
├── main.go              # 原始 MediaMTX 入口（保持不变）
├── main_pro.go          # Pro 版本入口
├── internal/            # 原始代码（不修改）
│   ├── api/
│   ├── core/
│   └── ...
├── pro/                 # Pro 版本代码（独立）
│   ├── api/
│   │   └── api_v2.go   # Pro API 实现
│   ├── core/
│   │   ├── core.go     # Pro Core 实现
│   │   └── pathmanager.go
│   └── README.md
└── go.mod
```

## 特性

### 只包含必要模块
- ✅ API
- ✅ WebRTC
- ✅ RTMP
- ✅ RTSP
- ❌ HLS (不需要)
- ❌ SRT (不需要)
- ❌ Playback (不需要)
- ❌ Recording (可选)

### API V2 端点

所有 API 都在 `/v2` 路径下：

#### 基础信息
- `GET /v2/info` - 获取服务信息
- `GET /v2/health` - 健康检查
- `GET /v2/stats` - 统计信息

#### 配置管理
- `GET /v2/config/global/get` - 获取全局配置
- `PATCH /v2/config/global/patch` - 更新全局配置

#### 路径管理
- `GET /v2/paths/list` - 路径列表
- `GET /v2/paths/get/:name` - 获取单个路径

#### RTSP
- `GET /v2/rtspconns/list` - RTSP 连接列表
- `GET /v2/rtspsessions/list` - RTSP 会话列表

#### RTMP
- `GET /v2/rtmpconns/list` - RTMP 连接列表

#### WebRTC
- `GET /v2/webrtcsessions/list` - WebRTC 会话列表

## 编译和运行

### 编译原版 MediaMTX
```bash
go build -o mediamtx main.go
./mediamtx
```

### 编译 Pro 版本
```bash
go build -o mediamtx-pro main_pro.go
./mediamtx-pro
```

### 指定配置文件
```bash
./mediamtx-pro mediamtx.yml
```

## 配置

使用相同的 `mediamtx.yml` 配置文件，但只启用需要的服务：

```yaml
# API
api: yes
apiAddress: :9997

# RTSP
rtsp: yes
rtspAddress: :8554

# RTMP
rtmp: yes
rtmpAddress: :1935

# WebRTC
webrtc: yes
webrtcAddress: :8889

# 不需要的服务设置为 no
hls: no
srt: no
playback: no
```

## 开发指南

### 添加自定义 API 端点

在 `pro/api/api_v2.go` 中：

```go
// 在 Initialize() 中添加路由
func (a *APIV2) Initialize() error {
    // ...
    group := router.Group("/v2")

    // 添加你的端点
    group.POST("/custom/action", a.onCustomAction)
    group.GET("/custom/data", a.onCustomData)
    // ...
}

// 实现处理函数
func (a *APIV2) onCustomAction(ctx *gin.Context) {
    a.Log(logger.Info, "Custom action called")

    // 访问配置
    a.mutex.RLock()
    conf := a.Conf
    a.mutex.RUnlock()

    // 访问 PathManager
    paths, _ := a.PathManager.APIPathsList()

    // 返回响应
    ctx.JSON(http.StatusOK, gin.H{
        "success": true,
        "data": "your data",
    })
}
```

### 扩展 PathManager

在 `pro/core/pathmanager.go` 中实现你自己的逻辑：

```go
func (pm *PathManager) APIPathsList() (*defs.APIPathList, error) {
    // 你的实现
    return &defs.APIPathList{
        Items: yourPaths,
    }, nil
}
```

### 添加新的模块

在 `pro/core/core.go` 中添加：

```go
type Core struct {
    // 现有字段...

    // 添加你的模块
    customModule *YourModule
}

func New(args []string) (*Core, bool) {
    // ...

    // 初始化你的模块
    p.customModule = NewYourModule(p)

    // ...
}
```

## 复用 internal 包

Pro 版本完全复用 internal 包的功能：

### 直接使用的包
- `internal/conf` - 配置管理
- `internal/logger` - 日志
- `internal/auth` - 认证
- `internal/defs` - 接口定义
- `internal/protocols/httpp` - HTTP 协议
- `internal/servers/rtsp` - RTSP 服务器
- `internal/servers/rtmp` - RTMP 服务器
- `internal/servers/webrtc` - WebRTC 服务器

### 通过接口使用
- `defs.APIPathManager` - 路径管理接口
- `defs.APIRTSPServer` - RTSP 服务器接口
- `defs.APIRTMPServer` - RTMP 服务器接口
- `defs.APIWebRTCServer` - WebRTC 服务器接口

## 与原仓库同步

Pro 代码完全独立于 `internal/` 目录，可以安全地与上游仓库同步：

```bash
# 拉取上游更新
git pull upstream main

# 你的 pro/ 目录不会受到影响
# 继续开发你的 Pro 功能
```

## 测试

```bash
# 启动 Pro 版本
./mediamtx-pro

# 测试 API
curl http://localhost:9997/v2/health
curl http://localhost:9997/v2/info
curl http://localhost:9997/v2/stats
curl http://localhost:9997/v2/paths/list
```

## 优势

1. **完全独立** - 不修改 internal/ 代码
2. **易于同步** - 可以随时合并上游更新
3. **复用基础设施** - 使用 internal 包的所有功能
4. **精简模块** - 只包含需要的服务
5. **独立入口** - main_pro.go 独立运行
6. **自定义 API** - V2 API 完全可控
7. **灵活扩展** - 可以添加任何自定义功能
