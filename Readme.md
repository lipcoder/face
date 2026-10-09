# 人脸识别

当前项目内的添加人脸只支持使用接入浏览器的视频流，暂未考虑升级

日常的识别支持使用指定摄像头，包括rtsp和local

## macOS ARM64 本机测试

```sh
go build -trimpath -ldflags='-s -w' -o build/face-darwin-arm64 ./cmd/run
./build/face-darwin-arm64
```

模型和前端已内嵌，运行时需要 `.env` 和系统安装的 FFmpeg。InspireFace、MNN 使用静态库，但程序仍依赖 macOS 系统动态库；当前 macOS 工具链不支持此项目完全静态链接。

## Linux x86-64 编译

在 Linux x86-64 机器上安装 Go（版本满足 `go.mod`）、GCC 和 G++，在项目根目录执行：

```sh
CGO_ENABLED=1 go build -trimpath -ldflags='-s -w -linkmode external -extldflags "-static"' -o face-linux-amd64 ./cmd/run
```

编译时需要项目内的 `.sdk/models` 和 `.sdk/inspireface-static-linux-amd64` 资源。

产物为 `face-linux-amd64`。模型和前端嵌入程序，InspireFace 与 C/C++ 运行库静态链接。摄像头拉流需要在部署机器上安装 FFmpeg。

将程序和识别配置 `.env` 放到部署目录后运行：
