## 人脸识别系统
项目内当前使用https://github.com/lipcoder/InspireFace
对原项目进行了删减和改动，构建由docker换为podman，计划更新为pod启动

当前项目正在开发中，如有需要，请先使用版本v1.0.0

### 项目需要的依赖
opencv hdf5 vtk 

### Docker 启动

只启动 pgvector 数据库，给本地进程使用：

```bash
docker run -d \
  --name face \
  -e POSTGRES_USER=face \
  -e POSTGRES_PASSWORD=face \
  -e POSTGRES_DB=face-data \
  -p 5432:5432 \
  -v face-data:/var/lib/postgresql/data \
  pgvector/pgvector:pg16

export DATABASE_URL=postgres://face:face@127.0.0.1:5432/face-data?sslmode=disable
```

使用 Docker Compose 同时启动数据库和服务：

```bash
docker compose up -d --build
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
