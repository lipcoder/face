## 人脸识别系统

项目完全脱离python设计，推荐直接使用项目pack内的镜像

### Docker 启动

使用 Docker Compose 同时启动数据库和服务：

```bash
docker compose up -d
```

服务地址默认是 `http://127.0.0.1:5090`。

Dockerfile 会根据构建目标架构自动下载 InspireFace SDK：

- `linux/amd64` 使用 `inspireface-linux-x86-manylinux2014`
- `linux/arm64` 使用 `inspireface-linux-aarch64`

在 Apple Silicon Mac 上直接执行 `docker compose up -d --build` 会构建 arm64 镜像。GitHub Actions 会发布 `linux/amd64` 和 `linux/arm64` 的多架构镜像，拉取时 Docker 会按运行机器自动选择架构。

## License

This project is licensed under the PolyForm Noncommercial License 1.0.0.

You may use this project for learning, research, experimentation, and other
noncommercial purposes. Commercial use is not allowed without permission.

Copyright (c) 2026 lipcoder.
