# MediaMTX Pro API v2 文档

完整的 RESTful API 参考文档

## 认证说明

所有 API 接口都需要 HTTP Basic Auth 认证。

```bash
curl -u username:password http://localhost:9997/v2/...
```

---

## 系统管理

### GET /v2/info
获取服务器版本和启动时间信息

### GET /v2/health
健康检查接口，返回服务状态

### GET /v2/stats
获取系统统计信息（路径数、服务器状态等）

### GET /v2/dashboard
获取设备状态信息（文件统计、磁盘使用、路径数量）

---

## 配置管理

### GET /v2/config/global/get
获取全局配置

### PATCH /v2/config/global/patch
修改全局配置（部分更新）

---

## 路径管理

### GET /v2/paths/list
获取所有路径列表

### GET /v2/paths/get/:name
获取指定路径的详细信息

### GET /v2/paths/query
查询路径信息（包含录制状态和配置）

---

## 录制管理

### POST /v2/record/start
开始录制任务

**请求示例:**
```json
{
  "pathName": "cam1",
  "videoFormat": "mp4",
  "timeout": 3600000
}
```

### POST /v2/record/stop
停止录制任务

**请求示例:**
```json
{
  "name": "cam1"
}
```

### GET /v2/record/task/:name
查询单个录制任务状态

### GET /v2/record/tasks
查询所有录制任务

---

## 文件管理

### GET /v2/record/date/files
按日期查询录制文件列表

**查询参数:**
```
?date=2025-10-15&fileType=video
```

### GET /v2/record/favorite/files
查询收藏文件列表

### POST /v2/file/rename
重命名文件

**请求示例:**
```json
{
  "fullPath": "/20251015/video.mp4",
  "name": "new_name.mp4"
}
```

### POST /v2/file/del
删除文件

### POST /v2/file/favorite
移动文件到收藏夹

---

## 截图功能

### GET /v2/snapshot
从网络设备 API 获取截图

**查询参数:**
```
?name=cam1&fileType=url&brightness=10
```

### GET /v2/publish/snapshot
使用 FFmpeg 从流中截图

**查询参数:**
```
?name=cam1&imageCopy={"x":0,"y":0,"w":1920,"h":1080}
```

### GET /v2/snapshot/config/:name
获取路径的截图配置

### POST /v2/snapshot/config/:name
保存路径的截图配置

---

## 视频处理

### POST /v2/file/export/mp4
使用 FFmpeg 导出视频（裁剪、合并、字幕、水印）

**请求示例:**
```json
{
  "exportConfig": [
    {
      "id": "1",
      "inputStart": 10,
      "inputEnd": 60,
      "resPath": "/20251015/video.mp4"
    }
  ]
}
```

---

## 服务器信息

### GET /v2/rtspconns/list
获取 RTSP 连接列表

### GET /v2/rtspsessions/list
获取 RTSP 会话列表

### GET /v2/rtmpconns/list
获取 RTMP 连接列表

### GET /v2/webrtcsessions/list
获取 WebRTC 会话列表

---

## 其他功能

### POST /v2/paths/message
WebSocket 消息广播

### GET /v2/proxy/device/*path
设备代理（转发到设备 HTTP API）

**查询参数:**
```
?deviceAddr=192.168.1.100
```

---

## 快速开始

1. 确保 MediaMTX Pro 服务正在运行
2. 配置认证信息（用户名和密码）
3. 使用任何 HTTP 客户端调用 API

### 示例：获取系统信息

```bash
curl -u admin:password http://localhost:9997/v2/info
```

### 示例：开始录制

```bash
curl -u admin:password -X POST http://localhost:9997/v2/record/start \
  -H "Content-Type: application/json" \
  -d '{"pathName":"cam1","videoFormat":"mp4","timeout":3600000}'
```

### 示例：获取截图

```bash
curl -u admin:password -X GET "http://localhost:9997/v2/snapshot?name=cam1" \
  -o snapshot.jpg
```

### 示例：查询路径列表

```bash
curl -u admin:password http://localhost:9997/v2/paths/query
```

---

## API 端点总览

共 **32 个端点**，分为以下类别：

- **系统管理**: 4 个端点
- **配置管理**: 2 个端点
- **路径管理**: 3 个端点
- **录制管理**: 4 个端点
- **文件管理**: 5 个端点
- **截图功能**: 4 个端点
- **视频处理**: 1 个端点
- **服务器信息**: 4 个端点
- **其他功能**: 2 个端点
- **静态资源**: 3 个端点（/admin/, /docs, /res/*）
