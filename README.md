## 人脸识别系统

项目提供配置驱动的多摄像头视频采集、人脸录入、识别签到和 pgvector
存储。

### 项目结构

基础能力统一位于 `internal/app`：

```text
internal/
├── app/
│   ├── camera/       # 本地、RTSP、海康采集，流 Hub 与订阅
│   ├── recognition/  # InspireFace 图像处理与 embedding 聚合
│   └── database/     # 数据库接口和 pgvector 实现
├── config/           # YAML 配置加载、默认值、校验和环境覆盖
├── service/          # 在线录入、签到、管理 worker channel
└── web/              # HTTP/MJPEG 接口和页面
```

视频链路采用有界 channel：

```text
本地设备 / RTSP / 海康
          │
          ▼
    camera.Source
          │ 有界帧队列
          ▼
      camera.Hub
       ├── 在线录入（逐帧提取 embedding，不保存图片集合）
       ├── 签到识别
       └── MJPEG 预览
```

每个订阅者有独立缓冲。消费者落后时只替换其旧帧，不会阻塞摄像头或让
延迟持续累积。管理请求使用有界队列、固定 worker 数和单次缓冲 reply
channel。

### 配置

默认读取根目录的 `config.yaml`，也可使用：

```bash
go run ./cmd/faced -config /path/to/config.yaml
```

`cameras` 和 `processors` 都以 ID 为 key 加载成 map。`cmd/faced` 和
`cmd/facecli` 在 main 中根据 `type` 将 `options` 分发给 `local`、
`rtsp`、`hikvision` 或 `inspireface` 构造器。

本地摄像头通过 FFmpeg 持续读取视频，不再调用单次拍照接口。使用前需安装
FFmpeg，并将 `config.yaml` 中 `cameras.local.enabled` 改为 `true`。
支持的本地参数包括 `device_id`、`device`、`width`、`height`、`fps`、
`queue_size`、`jpeg_quality` 和 `retry_delay`。

以下环境变量可覆盖 YAML，方便容器部署：

- `CONFIG_PATH`
- `HTTP_ADDR`
- `DATABASE_URL`
- `INSPIREFACE_PACK_PATH`

### 录入方式

Web 管理页位于 `http://127.0.0.1:5090/manager`。页面直接预览服务端
MJPEG 视频流，录入请求只提交姓名和摄像头 ID。服务端从对应订阅中逐帧分析，
达到质量阈值后在线聚合少量 embedding；不再上传固定 25 帧，也不会先挑选
和保存图片。

关键参数位于 `app`：

- `enrollment_samples`：需要聚合的有效特征数量。
- `enrollment_min_quality`：录入质量阈值。
- `enrollment_timeout`：单次录入最长时间。
- `similarity_threshold`：数据库匹配阈值。
- `sign_in_interval`：从每路视频流取帧识别的时间间隔。
- `sign_in_cooldown`：同一人重复写入签到日志的最短间隔。

### 运行

本机运行：

```bash
go run ./cmd/faced -config config.yaml
```

终端管理及签到：

```bash
go run ./cmd/facecli -config config.yaml
```

Docker Compose：

```bash
docker compose up -d
```

容器镜像内包含 FFmpeg 和默认配置，数据库地址、监听地址、模型路径由 Compose
环境变量覆盖。容器默认不启用本地摄像头；如需使用设备，应挂载对应设备并提供
一份启用该摄像头的配置。

完整验证：

```bash
go test ./...
```

### License

This project is licensed under the PolyForm Noncommercial License 1.0.0.
Commercial use is not allowed without permission.

Copyright (c) 2026 lipcoder.
