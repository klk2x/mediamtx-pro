# MediaMTX Pro 快速开始

## 项目结构

```
mediamtx-pro/
├── main.go                  # 原始 MediaMTX 入口（不修改）
├── main_pro.go              # Pro 版本入口（新增）
├── internal/                # 原始代码（完全不修改）
├── pro/                     # Pro 独立代码
│   ├── api/
│   │   └── api_v2.go       # Pro API V2 实现
│   ├── core/
│   │   ├── core.go         # Pro Core 实现
│   │   └── pathmanager.go  # 简化的 PathManager
│   └── README.md
├── mediamtx.yml            # 配置文件（共用）
└── go.mod                  # 依赖文件（共用）
```

## 核心特点

### ✅ 完全独立
- Pro 代码在 `pro/` 目录，完全独立
- 不修改 `internal/` 任何代码
- 可以随时同步上游仓库

### ✅ 复用基础库
- 复用 `internal/` 的所有基础功能
- 复用 logger、auth、conf 等模块
- 通过接口调用，不直接依赖实现

### ✅ 精简模块
只包含必要的模块：
- API（必需）
- RTSP（可选）
- RTMP（可选）
- WebRTC（可选）

不包含：
- HLS
- SRT
- Playback
- Recording（可后续添加）

## 快速开始

### 1. 编译

```bash
# 编译 Pro 版本
go build -o mediamtx-pro main_pro.go

# 或者直接运行
go run main_pro.go
```

### 2. 配置

使用相同的 `mediamtx.yml`，确保 API 已启用：

```yaml
# 启用 API
api: yes
apiAddress: :9997

# 认证配置（可选）
authMethod: internal

# 其他协议按需启用
rtsp: yes
rtmp: yes
webrtc: yes

# 不需要的协议
hls: no
srt: no
playback: no
```

### 3. 运行

```bash
#使用默认配置
./mediamtx-pro

# 或指定配置文件
./mediamtx-pro mediamtx.yml
```

### 4. 测试 API

```bash
# 健康检查
curl http://localhost:9997/v2/health

# 服务信息
curl http://localhost:9997/v2/info

# 统计数据
curl http://localhost:9997/v2/stats

# 路径列表
curl http://localhost:9997/v2/paths/list

# 全局配置
curl http://localhost:9997/v2/config/global/get
```

## API V2 端点列表

### 基础端点
- `GET /v2/info` - 服务信息
- `GET /v2/health` - 健康检查
- `GET /v2/stats` - 统计数据

### 配置管理
- `GET /v2/config/global/get` - 获取全局配置
- `PATCH /v2/config/global/patch` - 更新全局配置

### 路径管理
- `GET /v2/paths/list` - 路径列表
- `GET /v2/paths/get/:name` - 获取单个路径信息

### 协议管理（如果启用）
- `GET /v2/rtspconns/list` - RTSP 连接列表
- `GET /v2/rtspsessions/list` - RTSP 会话列表
- `GET /v2/rtmpconns/list` - RTMP 连接列表
- `GET /v2/webrtcsessions/list` - WebRTC 会话列表

## 添加自定义功能

### 1. 添加 API 端点

编辑 `pro/api/api_v2.go`：

```go
// 在 Initialize() 中添加路由
func (a *APIV2) Initialize() error {
    // ...
    group := router.Group("/v2")

    // 添加你的端点
    group.POST("/custom/action", a.onCustomAction)

    // ...
}

// 实现处理函数
func (a *APIV2) onCustomAction(ctx *gin.Context) {
    // 记录日志
    a.Log(logger.Info, "执行自定义操作")

    // 访问配置（线程安全）
    a.mutex.RLock()
    conf := a.Conf
    a.mutex.RUnlock()

    // 返回结果
    ctx.JSON(http.StatusOK, gin.H{
        "success": true,
        "message": "操作成功",
    })
}
```

### 2. 扩展 PathManager

编辑 `pro/core/pathmanager.go`：

```go
func (pm *PathManager) APIPathsList() (*defs.APIPathList, error) {
    // 实现你的路径管理逻辑
    paths := []*defs.APIPath{
        {
            Name: "stream1",
            // ... 其他字段
        },
    }

    return &defs.APIPathList{
        Items: paths,
    }, nil
}
```

### 3. 添加新模块

编辑 `pro/core/core.go`：

```go
type Core struct {
    // 现有字段...

    // 添加你的模块
    customModule *CustomModule
}

func New(args []string) (*Core, bool) {
    // ...

    // 初始化你的模块
    p.customModule = NewCustomModule(p)

    // ...
}
```

## 与上游同步

Pro 版本不修改 internal/ 代码，可以安全同步：

```bash
# 添加上游仓库（首次）
git remote add upstream https://github.com/bluenviron/mediamtx.git

# 拉取上游更新
git fetch upstream
git merge upstream/main

# pro/ 目录不会冲突
```

## 目前状态

### ✅ 已完成
- Pro Core 基础框架
- API V2 框架
- 配置加载和管理
- 认证系统集成
- 日志系统集成
- 简化的 PathManager

### 🚧 待完成（可按需添加）
- RTSP Server 集成
- RTMP Server 集成
- WebRTC Server 集成
- Path Manager 完整实现
- 录制功能（如需要）

## 下一步

1. **测试基础功能**
   ```bash
   go run main_pro.go
   curl http://localhost:9997/v2/health
   ```

2. **添加协议服务器**
   - 参考 `internal/core/core.go` 的服务器初始化
   - 在 `pro/core/core.go` 中添加需要的服务器

3. **实现 PathManager**
   - 根据你的业务需求
   - 实现路径管理逻辑

4. **扩展 API**
   - 添加自定义端点
   - 实现业务逻辑

## 示例响应

### GET /v2/health
```json
{
  "status": "healthy",
  "version": "v1.0.0-pro",
  "uptime": "1h23m45s"
}
```

### GET /v2/info
```json
{
  "version": "v2-pro-v1.0.0-pro",
  "started": "2025-10-14T10:30:00Z"
}
```

### GET /v2/stats
```json
{
  "version": "v1.0.0-pro",
  "uptime": "1h23m45s",
  "pathsCount": 0,
  "servers": {
    "rtsp": false,
    "rtmp": false,
    "webrtc": false
  },
  "config": {
    "logLevel": "info",
    "configuredPaths": 0
  }
}
```

## 常见问题

### Q: 如何添加 RTSP 服务器？
A: 在 `pro/core/core.go` 中参考原始 `internal/core/core.go` 的 RTSP 初始化代码，添加到 Pro Core 中。

### Q: 如何自定义认证？
A: 通过 `mediamtx.yml` 配置 `authMethod: http`，然后实现自己的认证服务。

### Q: 可以同时运行原版和 Pro 版本吗？
A: 可以，只要使用不同的端口配置。

### Q: 如何调试？
A: 设置 `logLevel: debug` 在配置文件中，查看详细日志。

## 技术支持

查看更多信息：
- `pro/README.md` - 详细说明
- `internal/api/api.go` - API 参考实现
- `internal/core/core.go` - Core 参考实现
