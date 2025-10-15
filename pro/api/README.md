# MediaMTX Pro API v2

MediaMTX Pro 专业版 API 服务器实现。

## 文件结构

```
pro/api/
├── api_v2.go           # 主 API 服务器（基础端点、配置、服务器状态）
├── api_v2_extended.go  # 扩展端点（文件管理、录制任务、路径查询）
├── api_snapshot.go     # 截图功能（设备截图、流截图）
├── api_ffmpeg.go       # FFmpeg 视频导出（MP4格式转换）
├── api_static.go       # 静态资源服务（管理面板、API文档）
├── webrtc.go           # WebRTC 端点处理
├── web/                # 嵌入式管理面板静态资源
└── API_DOCS.md         # API 文档（Markdown格式）
```

## API 端点概览

### 系统信息
- `GET /v2/info` - 服务器版本和启动时间
- `GET /v2/health` - 健康检查
- `GET /v2/stats` - 服务器统计信息
- `GET /v2/dashboard` - 系统仪表板（文件统计、磁盘使用）

### 配置管理
- `GET /v2/config/global/get` - 获取全局配置
- `PATCH /v2/config/global/patch` - 修改全局配置

### 路径管理
- `GET /v2/paths/list` - 列出所有路径
- `GET /v2/paths/get/:name` - 获取单个路径详情
- `GET /v2/paths/get2/:name` - 获取路径详情（扩展版本）
- `GET /v2/paths/query` - 查询路径（带分组和排序）
- `POST /v2/paths/message` - 向路径发送消息

### 录制管理
- `POST /v2/record/start` - 启动录制任务
- `POST /v2/record/stop` - 停止录制任务
- `GET /v2/record/task/:name` - 查询单个录制任务
- `GET /v2/record/tasks` - 查询所有录制任务

### 文件管理
- `GET /v2/record/date/files` - 按日期查询录制文件
- `GET /v2/record/favorite/files` - 查询收藏文件
- `POST /v2/file/rename` - 重命名文件
- `POST /v2/file/del` - 删除文件
- `POST /v2/file/favorite` - 移动文件到收藏夹
- `POST /v2/file/export/mp4` - 导出 MP4 视频

### 截图功能
- `GET /v2/snapshot` - 从设备获取截图
- `GET /v2/publish/snapshot` - 从流中截图（FFmpeg）
- `GET /v2/snapshot/config/:name` - 获取截图配置
- `POST /v2/snapshot/config/:name` - 保存截图配置

### 服务器状态
- `GET /v2/rtspconns/list` - RTSP 连接列表
- `GET /v2/rtspsessions/list` - RTSP 会话列表
- `GET /v2/rtmpconns/list` - RTMP 连接列表
- `GET /v2/webrtcsessions/list` - WebRTC 会话列表

### 静态资源
- `GET /admin/` - 管理控制台（需要认证）
- `GET /docs` - API 文档
- `GET /res/*` - 录制文件访问
- `ANY /proxy/device/*` - 设备代理

## 认证

所有 API 端点都使用 HTTP Basic Auth 认证。认证配置通过 MediaMTX 的 `api` 配置项控制。

## 关键实现说明

### Path 访问模式

代码使用 MediaMTX 标准的 `AddReader/RemoveReader` 模式访问路径：

```go
path, stream, err := a.PathManager.AddReader(defs.PathAddReaderReq{
    Author: a,
    AccessRequest: defs.PathAccessRequest{
        Name:     pathName,
        SkipAuth: true,
        Proto:    auth.ProtocolWebRTC,
        IP:       net.IPv4(127, 0, 0, 1),
    },
})
if err != nil {
    return err
}
defer path.RemoveReader(defs.PathRemoveReaderReq{Author: a})

// 使用 path.SafeConf() 获取线程安全的配置
pathConf := path.SafeConf()
```

这种方式确保：
- 正确注册为路径的读取者
- 路径在使用期间不会被删除
- 线程安全的配置访问

### 静态资源嵌入

使用 Go 1.16+ 的 `embed` 包将静态资源编译到二进制文件中：

```go
//go:embed web
var webFS embed.FS

//go:embed apiv2_docs_index.html
var apiV2DocsIndex []byte
```

### FFmpeg 视频导出

`ExportMP4` 端点支持将录制的视频片段导出为单个 MP4 文件：
- 自动合并多个 TS 文件
- 使用 FFmpeg concat 协议
- 支持自定义输出文件名

## 安全注意事项

1. **路径遍历防护**: 所有文件操作都进行路径验证
2. **认证**: 所有端点都需要 HTTP Basic Auth
3. **并发安全**: 使用 `mutex` 保护配置读写
4. **资源清理**: 使用 `defer` 确保资源正确释放

## 使用示例

### 启动录制任务

```bash
curl -X POST http://localhost:9997/v2/record/start \
  -u admin:password \
  -H "Content-Type: application/json" \
  -d '{
    "name": "camera01",
    "videoFormat": "mp4"
  }'
```

### 获取截图

```bash
curl -X GET "http://localhost:9997/v2/snapshot?name=camera01" \
  -u admin:password \
  -o snapshot.jpg
```

### 查询路径列表

```bash
curl -X GET http://localhost:9997/v2/paths/query \
  -u admin:password
```

## 依赖

- `github.com/gin-gonic/gin` - HTTP 路由框架
- `github.com/bluenviron/mediamtx/internal/*` - MediaMTX 核心模块
- FFmpeg - 视频处理和截图功能

## 相关文档

完整的 API 文档可通过 `/docs` 端点访问，或直接查看 `API_DOCS.md`。
