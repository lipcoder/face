## 人脸识别系统

项目使用了cgo，暂未测试Windows直接运行二进制的效果，前端设计将来会迁移

当前完美支持macOS和arch liunx

### Docker 启动

使用 Docker Compose 同时启动数据库和服务：

```bash
docker compose up -d
```

服务地址默认是 `http://127.0.0.1:5090`。

Dockerfile 会根据构建目标架构自动下载 InspireFace SDK：

- `linux/amd64` 使用 `inspireface-linux-x86-manylinux2014`
- `linux/arm64` 使用 `inspireface-linux-aarch64`

执行 `VERSION=local docker compose -f docker-compose.build.yml build` 会构建镜像

GitHub Actions 已发布 `linux/amd64` 和 `linux/arm64` 的多架构镜像，拉取时 Docker 会按运行机器自动选择架构

## License

This project is licensed under the PolyForm Noncommercial License 1.0.0.

You may use this project for learning, research, experimentation, and other
noncommercial purposes. Commercial use is not allowed without permission.

Copyright (c) 2026 lipcoder.
