# MediaMTX Pro 配置系统开发文档

## 项目概述

MediaMTX Pro 是基于开源项目 mediamtx 的定制版本，在保持与上游同步能力的同时，添加 Pro 版本专属的配置功能。

## 实现方案

采用**直接扩展 internal/conf 结构体**的方案，将 Pro 专属字段添加到现有配置结构中。

### 方案优点
- 代码简洁，易于维护
- 配置解析统一，无需额外处理逻辑
- 与上游代码集成良好，便于同步更新
- 类型安全，编译时检查

## 代码实现

### 1. 全局配置扩展 (internal/conf/conf.go)

在 `Conf` 结构体末尾添加 Pro 专属字段：

```go
// Pro Extension
CoreServerKey string `json:"coreServerKey"` // 许可证密钥
APIAuth       bool   `json:"apiAuth"`       // API 是否需要认证
APIAdminPage  bool   `json:"apiAdminPage"`  // 是否启用管理页面
AppID         string `json:"appid"`         // 应用 ID
AppSecret     string `json:"appsecret"`     // 应用密钥
```

在 `setDefaults()` 方法中设置默认值：

```go
// Pro Extension
conf.APIAuth = true
```

**字段说明**:
- `CoreServerKey`: Pro 版本的许可证密钥（必填）
- `APIAuth`: 控制 API 是否需要认证，局域网环境可关闭，默认 true
- `APIAdminPage`: 是否启用 Web 管理界面
- `AppID`: 应用标识符
- `AppSecret`: 应用密钥，用于认证和加密

### 2. 路径配置扩展 (internal/conf/path.go)

在 `Path` 结构体末尾添加 Pro 专属字段：

```go
// Pro Extension

// pathDefaults 级别的配置（所有路径通用，需要默认值）
RecordClearDaysAgo        int    `json:"recordClearDaysAgo"`        // 自动清理超出天数前的录制文件，0 表示不清理
RecordCreateWebhook       string `json:"recordCreateWebhook"`       // 录制创建时的 webhook 回调 URL
RecordDelWebhook          string `json:"recordDelWebhook"`          // 录制删除时的 webhook 回调 URL
ThumbnailSize             int    `json:"thumbnailSize"`             // 缩略图尺寸（像素）
VideoSnapshotEnable       bool   `json:"videoSnapshotEnable"`       // 自动截图开关
VideoSnapshotModulePath   string `json:"videoSnapshotModulePath"`   // 自动截图模块路径

// 特定路径级别的配置（可选，用于覆盖 pathDefaults）
VideoSnapshotPipelineConf *string  `json:"videoSnapshotPipelineConf,omitempty"` // 图像识别配置文件名
SourceName                *string  `json:"sourceName,omitempty"`                // 流显示名称
GroupName                 *string  `json:"groupName,omitempty"`                 // 流分组名称
Cut                       *[4]int  `json:"cut,omitempty"`                       // 裁剪区域 [x,y,w,h]
Brightness                *int     `json:"brightness,omitempty"`                // 亮度调节 -100 to 100
Contrast                  *int     `json:"contrast,omitempty"`                  // 对比度调节 -100 to 100
Saturation                *int     `json:"saturation,omitempty"`                // 饱和度调节 -100 to 100
```

在 `setDefaults()` 方法中设置默认值：

```go
// Pro Extension
pconf.RecordClearDaysAgo = 10
pconf.ThumbnailSize = 300
pconf.VideoSnapshotEnable = false
```

**类型选择说明**:
- **pathDefaults 级别**：使用非指针类型，需要明确的默认值
- **特定路径级别**：使用指针类型 + `omitempty` 标签，表示可选且可继承 pathDefaults 的值

### 3. 数组类型支持 (internal/conf/env/env.go)

MediaMTX 的配置加载使用环境变量加载器 + YAML 解析器。环境变量加载器需要支持固定大小数组类型。

在 `loadEnvInternal()` 函数的 `switch rt.Kind()` 代码块中，`case reflect.Slice:` 之后添加：

```go
case reflect.Array:
	// Handle fixed-size arrays like [4]int
	if rt.Elem() == reflect.TypeOf(int(0)) {
		// Array of int
		return nil // Arrays are usually handled by YAML unmarshaler, just return nil
	}
```

**原理**: 环境变量加载器遇到数组类型时跳过处理，由 YAML 解析器直接处理数组语法。

### 4. Pro 核心代码 (pro/core/core.go)

使用 `internal/conf` 包，无需额外封装：

```go
import "mediamtx-pro/internal/conf"

type Core struct {
    conf *conf.Conf
    // ... 其他字段
}

// 加载配置
p.conf, p.confPath, err = conf.Load(confPath, confPaths, tempLogger)

// 使用配置
coreServerKey := p.conf.CoreServerKey                        // 全局配置
recordClearDays := p.conf.PathDefaults.RecordClearDaysAgo   // pathDefaults 配置

// 遍历路径配置
for pathName, pathConf := range p.conf.Paths {
    if pathConf.SourceName != nil {
        name := *pathConf.SourceName
        // 使用流名称
    }
}
```

## 配置文件示例

### 全局 Pro 配置

```yaml
###############################################
# Pro 版本专属配置

# 许可证密钥（必填）
coreServerKey: IO3sSdW8SNm8Qkh/GNmc4pwPm4MZ+RzL3MvuHZ/vFYvWSRvQ9qRK2bZg3T0PwJM5Bhgcb4X65A==

# API 是否需要认证（局域网环境可以关闭）
apiAuth: no

# 是否启用 Web 管理页面
apiAdminPage: yes

# 应用 ID
appid: devNm5C3

# 应用密钥
appsecret: PhLCTSEVNm5C3wf3IZKAXDPXK64PGdCnqL6LbnzZnwwW3MyMSCb5W9MT63jGwBAQ
```

### pathDefaults Pro 配置

```yaml
pathDefaults:
  ###############################################
  # Pro 版本扩展配置（适用于所有路径）

  # 自动清理超出天数前的录制文件，单位天数，0 表示不清理
  recordClearDaysAgo: 10

  # 录制创建回调
  recordCreateWebhook: http://127.0.0.1:9000/api/v1/org/p/res_record/insert/1001

  # 录制删除回调
  recordDelWebhook: http://127.0.0.1:9000/api/v1/org/p/res_record/file/del

  # 缩略图尺寸
  thumbnailSize: 300

  # 自动截图开关
  videoSnapshotEnable: no

  # 自动截图模块路径
  videoSnapshotModulePath: /Users/xlt/workspace/Release/Autosnap/snapshot.launcher
```

### 特定路径配置示例

```yaml
paths:
  # 内镜3 直播流配置
  livedemo3:
    # 标准配置
    source: rtsp://192.168.3.33/stream0
    sourceOnDemand: no
    record: yes

    # Pro 扩展配置
    sourceName: 内镜3
    groupName: 内镜
    cut: [0, 0, 1300, 1080]  # 裁剪区域 [x, y, width, height]
    brightness: 20           # 亮度 -100 to 100
    contrast: 30             # 对比度 -100 to 100
    saturation: 30           # 饱和度 -100 to 100
    videoSnapshotEnable: yes
    videoSnapshotPipelineConf: AB-290.json

  # 其他路径使用 pathDefaults 默认配置
  all_others:
```

## 配置字段详解

### 全局配置字段

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| coreServerKey | string | - | Pro 版本许可证密钥（必填） |
| apiAuth | bool | true | API 是否需要认证 |
| apiAdminPage | bool | false | 是否启用 Web 管理界面 |
| appid | string | - | 应用标识符 |
| appsecret | string | - | 应用密钥 |

### pathDefaults 配置字段

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| recordClearDaysAgo | int | 10 | 自动清理 N 天前的录制文件，0 表示不清理 |
| recordCreateWebhook | string | "" | 录制创建时调用的 HTTP webhook URL |
| recordDelWebhook | string | "" | 录制删除时调用的 HTTP webhook URL |
| thumbnailSize | int | 300 | 缩略图尺寸（像素） |
| videoSnapshotEnable | bool | false | 是否启用自动截图 |
| videoSnapshotModulePath | string | "" | 自动截图模块可执行文件路径 |

### 特定路径配置字段（可选）

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| videoSnapshotPipelineConf | *string | nil | 图像识别配置文件名 |
| sourceName | *string | nil | 流显示名称（用于 UI） |
| groupName | *string | nil | 流分组名称 |
| cut | *[4]int | nil | 裁剪区域 [x坐标, y坐标, 宽度, 高度] |
| brightness | *int | nil | 亮度调节 -100 to 100 |
| contrast | *int | nil | 对比度调节 -100 to 100 |
| saturation | *int | nil | 饱和度调节 -100 to 100 |

## 开发最佳实践

### 1. 添加新配置字段

1. 在对应结构体末尾的 `// Pro Extension` 注释块中添加字段
2. 使用清晰的 JSON 标签和中文注释
3. 在 `setDefaults()` 方法中设置合理的默认值（如果需要）
4. 更新 config.yml 中的示例和注释

### 2. 类型选择原则

- **需要默认值**：使用非指针类型（如 `int`, `bool`, `string`）
- **可选字段**：使用指针类型 + `omitempty`（如 `*int`, `*string`, `*[4]int`）
- **固定大小数组**：使用 `[N]T` 而非 `[]T`，确保数据格式正确

### 3. 与上游同步

Pro 扩展字段统一放在结构体末尾，并用 `// Pro Extension` 注释标记，方便：
- 识别哪些是 Pro 专属字段
- 合并上游更新时减少冲突
- 代码审查和维护

### 4. 配置文件组织

```yaml
# 1. 全局 Pro 配置（放在文件开头，Pro 专属配置区域）
coreServerKey: xxx
apiAuth: no
apiAdminPage: yes

# 2. 标准全局配置
logLevel: info
api: yes

# 3. pathDefaults 标准配置
pathDefaults:
  source: publisher
  record: no

  # Pro 扩展配置（放在 pathDefaults 末尾）
  recordClearDaysAgo: 10
  thumbnailSize: 300

# 4. 特定路径配置
paths:
  stream1:
    # 标准配置在前
    source: rtsp://xxx
    record: yes

    # Pro 扩展配置在后
    sourceName: 摄像头1
    cut: [0, 0, 1920, 1080]
```

## 常见场景示例

### 场景 1: 启用录制自动清理

```yaml
pathDefaults:
  record: yes
  recordClearDaysAgo: 7  # 自动删除 7 天前的录制文件
```

### 场景 2: 配置录制 Webhook 回调

```yaml
pathDefaults:
  record: yes
  recordCreateWebhook: http://your-server.com/api/record/created
  recordDelWebhook: http://your-server.com/api/record/deleted
```

### 场景 3: 特定流的图像处理

```yaml
paths:
  medical_cam:
    source: rtsp://192.168.1.100/stream
    sourceName: 医用内窥镜
    groupName: 医疗设备
    cut: [100, 50, 1280, 720]  # 裁剪画面
    brightness: 15              # 提高亮度
    contrast: 20                # 增强对比度
    videoSnapshotEnable: yes    # 启用自动截图
    videoSnapshotPipelineConf: medical-device.json
```

### 场景 4: 分组管理多个流

```yaml
paths:
  cam1:
    source: rtsp://192.168.1.101/stream
    sourceName: 1号机位
    groupName: 手术室A

  cam2:
    source: rtsp://192.168.1.102/stream
    sourceName: 2号机位
    groupName: 手术室A

  cam3:
    source: rtsp://192.168.1.103/stream
    sourceName: 内窥镜
    groupName: 手术室B
```

## 技术要点

### 固定大小数组的使用

**定义**：
```go
Cut *[4]int `json:"cut,omitempty"`
```

**YAML 语法**：
```yaml
cut: [0, 0, 1920, 1080]
```

**Go 代码访问**：
```go
if path.Cut != nil {
    x := (*path.Cut)[0]
    y := (*path.Cut)[1]
    w := (*path.Cut)[2]
    h := (*path.Cut)[3]
}
```

### 可选字段的判断

```go
// 字符串指针
if path.SourceName != nil {
    name := *path.SourceName
    // 使用 name
}

// 整数指针
if path.Brightness != nil {
    brightness := *path.Brightness
    // 使用 brightness
}
```

## 维护指南

### 添加新的 Pro 配置字段

1. 确定字段级别（全局、pathDefaults、特定路径）
2. 在对应的结构体末尾添加字段
3. 添加 JSON 标签和注释
4. 在 `setDefaults()` 中设置默认值（如需要）
5. 更新 config.yml 示例
6. 更新本文档

### 修改现有字段

1. 修改结构体定义
2. 更新默认值设置
3. 检查 Pro 核心代码中的使用
4. 更新配置文件和文档

### 删除废弃字段

1. 在结构体中标记为 deprecated（保留一个版本）
2. 更新文档说明该字段已废弃
3. 下个大版本中移除

## 相关文件

### 配置系统相关
- `internal/conf/conf.go` - 全局配置结构
- `internal/conf/path.go` - 路径配置结构
- `internal/conf/env/env.go` - 环境变量加载器
- `config.yml` - 配置文件示例

### Pro 核心代码
- `pro/core/core.go` - Pro 版本核心实现
- `main_pro.go` - Pro 版本入口

### 文档
- `docs/pro-config-development.md` - 本文档

## 技术栈

- **Go 版本**: 1.25.0
- **配置格式**: YAML
- **反射机制**: Go reflect 包
- **序列化**: encoding/json

## API 录制功能

### 概述

MediaMTX Pro 提供了基于 API 的录制功能，支持通过 HTTP 接口动态开始和停止录制任务，无需修改配置文件。

### 功能特性

- ✅ **标准 MP4 格式**: 使用标准 MP4 容器（非分段 FMP4），兼容性更好，文件体积更小
- ✅ **MPEG-TS 格式**: 支持传统的 TS 格式录制
- ✅ **H264/H265 支持**: H264（必须），H265（尽力而为）
- ✅ **纯视频录制**: 仅录制视频轨道，不录制音频
- ✅ **自动超时**: 可设置录制时长，到期自动停止
- ✅ **自定义文件名**: 支持自定义录制文件名
- ✅ **短文件名**: 自动生成格式：`YYYYMMDD-HHMM-<8位UUID>.ext`
- ✅ **按日期组织**: 文件按日期目录组织：`/recordPath/YYYYMMDD/filename.ext`
- ✅ **HTTP 访问**: 通过 `/res/` 路径直接访问录制文件

### API 端点

#### 1. 开始录制

**请求**:
```http
POST /v2/record/start
Content-Type: application/json

{
  "name": "stream_name",           // 必填：流路径名称
  "videoFormat": "mp4",             // 必填：视频格式 "mp4" 或 "ts"
  "taskOutMinutes": 30,             // 可选：超时时长（分钟），默认 30
  "fileName": "my_recording"        // 可选：自定义文件名（不含扩展名）
}
```

**响应**:
```json
{
  "existed": false,                                    // 是否已存在录制任务
  "success": true,                                     // 是否成功
  "id": "550e8400-e29b-41d4-a716-446655440000",       // 任务 ID
  "name": "stream_name",                               // 流名称
  "fileName": "20251015-1004-abc12345.mp4",           // 文件名
  "filePath": "/20251015/20251015-1004-abc12345.mp4", // 相对路径
  "fullPath": "/tmp/recordings/20251015/20251015-1004-abc12345.mp4", // 绝对路径
  "fileURL": "http://localhost:9997/res/20251015/20251015-1004-abc12345.mp4", // HTTP 访问 URL
  "taskEndTime": "2025-10-15T10:34:00Z"               // 预计结束时间
}
```

#### 2. 停止录制

**请求**:
```http
POST /v2/record/stop
Content-Type: application/json

{
  "name": "stream_name"  // 必填：要停止录制的流名称
}
```

**响应**:
```json
{
  "success": true,
  "name": "stream_name",
  "fileName": "20251015-1004-abc12345.mp4",
  "filePath": "/20251015/20251015-1004-abc12345.mp4",
  "fullPath": "/tmp/recordings/20251015/20251015-1004-abc12345.mp4",
  "fileURL": "http://localhost:9997/res/20251015/20251015-1004-abc12345.mp4"
}
```

#### 3. 访问录制文件

录制的文件可以通过 HTTP 静态文件服务访问：

```
http://localhost:9997/res/{日期目录}/{文件名}
```

例如：
```
http://localhost:9997/res/20251015/20251015-1004-abc12345.mp4
```

### 配置要求

在 `config.yml` 中配置录制路径：

```yaml
pathDefaults:
  # 录制文件保存路径
  recordPath: /tmp/recordings
```

### 使用示例

#### 示例 1: 录制 30 分钟 MP4

```bash
curl -X POST http://localhost:9997/v2/record/start \
  -H "Content-Type: application/json" \
  -d '{
    "name": "camera1",
    "videoFormat": "mp4",
    "taskOutMinutes": 30
  }'
```

#### 示例 2: 自定义文件名录制

```bash
curl -X POST http://localhost:9997/v2/record/start \
  -H "Content-Type: application/json" \
  -d '{
    "name": "camera1",
    "videoFormat": "mp4",
    "taskOutMinutes": 60,
    "fileName": "meeting_room_A_2025"
  }'
```

#### 示例 3: 录制 TS 格式

```bash
curl -X POST http://localhost:9997/v2/record/start \
  -H "Content-Type: application/json" \
  -d '{
    "name": "camera1",
    "videoFormat": "ts",
    "taskOutMinutes": 120
  }'
```

#### 示例 4: 手动停止录制

```bash
curl -X POST http://localhost:9997/v2/record/stop \
  -H "Content-Type: application/json" \
  -d '{
    "name": "camera1"
  }'
```

#### 示例 5: 下载录制文件

```bash
# 使用返回的 fileURL
wget http://localhost:9997/res/20251015/20251015-1004-abc12345.mp4

# 或使用 curl
curl -O http://localhost:9997/res/20251015/20251015-1004-abc12345.mp4
```

### 技术实现

#### 架构设计

```
API 请求
    ↓
RecordManager (pro/recorder/manager.go)
    ↓
Task (pro/recorder/task.go)
    ↓
┌─────────────┬────────────────┐
│ MP4 格式    │ TS 格式        │
│ MP4Recorder │ internal/      │
│ (gomedia)   │ recorder       │
└─────────────┴────────────────┘
```

#### 核心组件

1. **RecordManager** (`pro/recorder/manager.go`)
   - 管理所有录制任务
   - 处理 API 请求
   - 防止重复录制

2. **Task** (`pro/recorder/task.go`)
   - 代表单个录制任务
   - 管理任务生命周期
   - 生成文件名和路径

3. **MP4Recorder** (`pro/recorder/mp4_recorder.go`)
   - 标准 MP4 格式录制
   - 使用 `gomedia` 库
   - 支持 H264/H265

4. **PathManager 扩展**
   - 添加 `GetStreamForRecording()` 方法
   - 允许 Pro 录制器访问 stream 对象

#### 文件组织

```
/tmp/recordings/
├── 20251015/
│   ├── 20251015-1004-abc12345.mp4
│   ├── 20251015-1030-def67890.mp4
│   └── 20251015-1100-ghi11223.ts
├── 20251016/
│   └── 20251016-0900-jkl44556.mp4
```

### 格式对比

| 特性 | MP4 (标准) | TS (MPEG-TS) |
|------|-----------|--------------|
| 容器格式 | MP4 | MPEG-TS |
| 文件体积 | 较小 | 较大 |
| 浏览器兼容性 | 优秀 | 一般 |
| 流媒体支持 | 需要完整文件 | 支持流式播放 |
| 编辑友好性 | 高 | 低 |
| 使用场景 | 存档、分享 | 直播、监控 |

### 注意事项

1. **路径检查**: 开始录制前会检查路径是否存在且有活动流
2. **重复录制**: 同一路径只能有一个录制任务，重复请求返回已存在的任务信息
3. **自动清理**: 录制任务超时或停止后自动清理资源
4. **文件访问**: 需确保 `recordPath` 目录有写权限
5. **格式选择**:
   - MP4: 推荐用于录制后存档、分享
   - TS: 推荐用于需要流式传输的场景

### 错误处理

**常见错误响应**:

```json
// 路径不存在
{
  "error": "path 'stream_name' not found"
}

// 没有推流
{
  "error": "no one is publishing to path 'stream_name'"
}

// 格式错误
{
  "error": "videoFormat must be 'mp4' or 'ts'"
}

// 任务不存在
{
  "error": "task id that does not exist"
}
```

### 依赖库

- **gomedia**: `github.com/yapingcat/gomedia` - 标准 MP4 格式写入
- **mediacommon**: H264/H265 编解码支持
- **gortsplib**: RTSP 流处理

## 版本历史

- **v1.1** (2025-10-15): 添加 API 录制功能，支持标准 MP4 和 TS 格式
- **v1.0** (2025-10-14): 初始版本，实现 Pro 配置系统

---

**备注**: 本文档记录了 MediaMTX Pro 配置系统和 API 录制功能的实现方案和使用方法，供开发和维护参考。
