# MediaMTX Pro 使用说明

## 快速开始

### 1. 编译

```bash
go build -o mediamtx-pro main_pro.go
```

### 2. 运行

```bash
# 使用默认配置文件 config.yml
./mediamtx-pro

# 或指定配置文件
./mediamtx-pro config.yml
```

### 3. 测试 API

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

## 配置说明

Pro 版本使用 `config.yml` 作为默认配置文件，只包含必要的模块：

### 启用的模块

- ✅ **API** - Control API (端口 9997)
- ✅ **RTSP** - RTSP 服务器 (端口 8554)
- ✅ **RTMP** - RTMP 服务器 (端口 1935)
- ✅ **WebRTC** - WebRTC 服务器 (端口 8889)
- ✅ **Path Manager** - 路径管理

### 禁用的模块

- ❌ HLS
- ❌ SRT
- ❌ Playback
- ❌ Metrics (默认关闭，可配置启用)
- ❌ PPROF (默认关闭，可配置启用)

### 关键配置项

#### 日志配置
```yaml
logLevel: info              # 日志级别: error, warn, info, debug
logDestinations: [stdout]   # 输出到标准输出
logFile: mediamtx-pro.log   # 日志文件路径
```

#### 认证配置
```yaml
authMethod: internal        # 认证方式: internal, http, jwt
authInternalUsers:          # 内部用户列表
  - user: any               # 允许匿名访问
    permissions:
      - action: publish
      - action: read
```

#### API 配置
```yaml
api: yes                    # 启用 API
apiAddress: :9997           # API 监听地址
apiAllowOrigin: '*'         # CORS 配置
```

#### RTSP 配置
```yaml
rtsp: yes                   # 启用 RTSP
rtspAddress: :8554          # RTSP 端口
rtspTransports: [udp, multicast, tcp]  # 传输协议
```

#### RTMP 配置
```yaml
rtmp: yes                   # 启用 RTMP
rtmpAddress: :1935          # RTMP 端口
```

#### WebRTC 配置
```yaml
webrtc: yes                 # 启用 WebRTC
webrtcAddress: :8889        # WebRTC HTTP 端口
webrtcLocalUDPAddress: :8189  # WebRTC UDP 端口
```

## API V2 端点

### 基础信息

#### GET /v2/health
健康检查

**响应示例：**
```json
{
  "status": "healthy",
  "version": "v1.0.0-pro",
  "uptime": "1h23m45s"
}
```

#### GET /v2/info
服务信息

**响应示例：**
```json
{
  "version": "v2-pro-v1.0.0-pro",
  "started": "2025-10-14T10:30:00Z"
}
```

#### GET /v2/stats
统计数据

**响应示例：**
```json
{
  "version": "v1.0.0-pro",
  "uptime": "1h23m45s",
  "pathsCount": 5,
  "servers": {
    "rtsp": true,
    "rtmp": true,
    "webrtc": true
  },
  "config": {
    "logLevel": "info",
    "configuredPaths": 3
  }
}
```

### 配置管理

#### GET /v2/config/global/get
获取全局配置

#### PATCH /v2/config/global/patch
更新全局配置（动态重载）

### 路径管理

#### GET /v2/paths/list
获取所有活动路径列表

**查询参数：**
- `itemsPerPage` - 每页数量（默认 100）
- `page` - 页码（从 0 开始）

#### GET /v2/paths/get/:name
获取指定路径详情

### 协议连接管理

#### RTSP
- `GET /v2/rtspconns/list` - RTSP 连接列表
- `GET /v2/rtspsessions/list` - RTSP 会话列表

#### RTMP
- `GET /v2/rtmpconns/list` - RTMP 连接列表

#### WebRTC
- `GET /v2/webrtcsessions/list` - WebRTC 会话列表

## 使用示例

### 1. 推送 RTSP 流

使用 FFmpeg 推流：

```bash
ffmpeg -re -i input.mp4 -c copy -f rtsp rtsp://localhost:8554/mystream
```

查看流状态：

```bash
curl http://localhost:9997/v2/paths/get/mystream
```

### 2. 推送 RTMP 流

使用 OBS 或 FFmpeg：

```bash
ffmpeg -re -i input.mp4 -c copy -f flv rtmp://localhost:1935/mystream
```

### 3. WebRTC 推流/拉流

访问 WebRTC 测试页面（需要配置 HTTPS 或使用 localhost）

### 4. 配置路径

编辑 `config.yml`：

```yaml
paths:
  # 从摄像头拉流
  camera1:
    source: rtsp://192.168.1.100:554/stream
    record: yes

  # 允许推流
  live:
    source: publisher

  # 正则匹配多个路径
  ~^stream\d+$:
    source: publisher
    maxReaders: 10
```

### 5. 启用录制

```yaml
pathDefaults:
  record: yes
  recordPath: ./recordings/%path/%Y-%m-%d_%H-%M-%S-%f
  recordFormat: fmp4
  recordSegmentDuration: 1h
  recordDeleteAfter: 7d  # 保留 7 天
```

## 端口占用

确保以下端口未被占用：

| 服务 | 端口 | 协议 | 说明 |
|------|------|------|------|
| API | 9997 | HTTP | Control API |
| RTSP | 8554 | TCP | RTSP 信令 |
| RTP | 8000 | UDP | RTP 数据 |
| RTCP | 8001 | UDP | RTCP 控制 |
| RTMP | 1935 | TCP | RTMP |
| WebRTC HTTP | 8889 | HTTP | WebRTC 信令 |
| WebRTC UDP | 8189 | UDP | WebRTC 数据 |

## 常见问题

### Q: 如何修改 API 端口？

编辑 `config.yml`：
```yaml
apiAddress: :8080
```

### Q: 如何启用认证？

方法1：使用内部认证
```yaml
authMethod: internal
authInternalUsers:
  - user: admin
    pass: mypassword
    permissions:
      - action: publish
      - action: read
```

方法2：使用 HTTP 认证
```yaml
authMethod: http
authHTTPAddress: http://localhost:8080/auth
```

方法3：使用 JWT 认证
```yaml
authMethod: jwt
authJWTJWKS: https://your-auth-server/.well-known/jwks.json
```

### Q: 如何启用 HTTPS/TLS？

生成证书：
```bash
openssl genrsa -out server.key 2048
openssl req -new -x509 -sha256 -key server.key -out server.crt -days 3650
```

配置：
```yaml
apiEncryption: yes
apiServerKey: server.key
apiServerCert: server.crt

webrtcEncryption: yes
webrtcServerKey: server.key
webrtcServerCert: server.crt
```

### Q: 如何查看日志？

方法1：输出到文件
```yaml
logDestinations: [file]
logFile: mediamtx-pro.log
```

方法2：同时输出到标准输出和文件
```yaml
logDestinations: [stdout, file]
```

方法3：输出到 syslog
```yaml
logDestinations: [syslog]
```

### Q: 如何限制连接数？

```yaml
pathDefaults:
  maxReaders: 100  # 最大读取者数量
```

### Q: 如何配置按需拉流？

```yaml
pathDefaults:
  sourceOnDemand: yes
  sourceOnDemandStartTimeout: 10s
  sourceOnDemandCloseAfter: 10s
```

## 性能调优

### 1. 增加写队列大小（提高吞吐量）

```yaml
writeQueueSize: 1024  # 默认 512
```

### 2. 调整 UDP 缓冲区

```yaml
rtspUDPReadBufferSize: 8192  # 增大可减少丢包
```

### 3. 禁用不需要的传输协议

```yaml
rtspTransports: [tcp]  # 只使用 TCP，减少资源占用
```

### 4. 启用 Metrics 监控

```yaml
metrics: yes
metricsAddress: :9998
```

然后用 Prometheus 采集：
```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'mediamtx-pro'
    static_configs:
      - targets: ['localhost:9998']
```

## 与原版 MediaMTX 的区别

| 特性 | MediaMTX 原版 | MediaMTX Pro |
|------|--------------|--------------|
| 配置文件 | mediamtx.yml | config.yml |
| API 路径 | /v3/* | /v2/* |
| HLS 支持 | ✅ | ❌ |
| SRT 支持 | ✅ | ❌ |
| Playback | ✅ | ❌ |
| RTSP | ✅ | ✅ |
| RTMP | ✅ | ✅ |
| WebRTC | ✅ | ✅ |
| 代码位置 | internal/ | pro/ |
| 可独立开发 | ❌ | ✅ |

## 开发扩展

参考 `pro/README.md` 了解如何添加自定义功能。

## 故障排查

### 1. 启动失败

检查端口是否被占用：
```bash
lsof -i :9997
lsof -i :8554
lsof -i :1935
lsof -i :8889
```

### 2. 连接失败

检查防火墙设置：
```bash
# Linux
sudo ufw allow 8554/tcp
sudo ufw allow 1935/tcp
sudo ufw allow 8889/tcp
```

### 3. 查看详细日志

```yaml
logLevel: debug
```

### 4. 测试 API

```bash
# 测试 API 是否可访问
curl -v http://localhost:9997/v2/health

# 测试认证
curl -u user:pass http://localhost:9997/v2/paths/list
```

## 更多信息

- [PRO_QUICKSTART.md](PRO_QUICKSTART.md) - 快速开始指南
- [pro/README.md](pro/README.md) - Pro 版本详细说明
- [config.yml](config.yml) - 配置文件模板
- [MediaMTX 官方文档](https://mediamtx.org/docs/)
