package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/lipcoder/face/internal/source"
	"github.com/lipcoder/face/internal/source/local"
	"github.com/lipcoder/face/internal/source/rtsp"
)

type recognitionConfig struct {
	Source    string
	RTSPURL   string
	Transport string
	Device    string
	Width     int
	Height    int
	FPS       int
}

func loadRecognitionConfig(path string) (recognitionConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return recognitionConfig{}, fmt.Errorf("读取识别配置 %s: %w", path, err)
	}
	defer file.Close()
	return parseRecognitionConfig(file)
}

func parseRecognitionConfig(input io.Reader) (recognitionConfig, error) {
	config := recognitionConfig{Transport: "tcp", Device: "0", Width: 1280, Height: 720, FPS: 30}
	if runtime.GOOS == "linux" {
		config.Device = "/dev/video0"
	}
	scanner := bufio.NewScanner(input)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return recognitionConfig{}, fmt.Errorf("识别配置第 %d 行必须使用 KEY=VALUE 格式", lineNumber)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		switch key {
		case "RECO_SOURCE":
			config.Source = value
		case "RECO_RTSP_URL":
			config.RTSPURL = value
		case "RECO_RTSP_TRANSPORT":
			config.Transport = value
		case "RECO_DEVICE":
			config.Device = value
		case "RECO_WIDTH":
			n, err := strconv.Atoi(value)
			if err != nil {
				return recognitionConfig{}, fmt.Errorf("RECO_WIDTH 不是整数")
			}
			config.Width = n
		case "RECO_HEIGHT":
			n, err := strconv.Atoi(value)
			if err != nil {
				return recognitionConfig{}, fmt.Errorf("RECO_HEIGHT 不是整数")
			}
			config.Height = n
		case "RECO_FPS":
			n, err := strconv.Atoi(value)
			if err != nil {
				return recognitionConfig{}, fmt.Errorf("RECO_FPS 不是整数")
			}
			config.FPS = n
		default:
			return recognitionConfig{}, fmt.Errorf("识别配置第 %d 行包含未知配置项", lineNumber)
		}
	}
	if err := scanner.Err(); err != nil {
		return recognitionConfig{}, fmt.Errorf("读取识别配置: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return recognitionConfig{}, fmt.Errorf("识别画面宽高必须大于 0")
	}
	switch config.Source {
	case "local":
		if config.Device == "" || config.FPS <= 0 {
			return recognitionConfig{}, fmt.Errorf("本地摄像头需要设备编号和大于 0 的帧率")
		}
	case "rtsp":
		if config.RTSPURL == "" {
			return recognitionConfig{}, fmt.Errorf("RTSP 识别源缺少 RECO_RTSP_URL")
		}
		if config.Transport != "tcp" && config.Transport != "udp" {
			return recognitionConfig{}, fmt.Errorf("RECO_RTSP_TRANSPORT 必须为 tcp 或 udp")
		}
	default:
		return recognitionConfig{}, fmt.Errorf("RECO_SOURCE 必须为 local 或 rtsp")
	}
	return config, nil
}

func openRecognitionSource(config recognitionConfig) (source.Source, error) {
	switch config.Source {
	case "rtsp":
		return rtsp.Open(rtsp.FrameConfig{
			URL: config.RTSPURL, Width: config.Width, Height: config.Height, Transport: config.Transport,
		})
	case "local":
		return local.Open(local.FrameOptions{
			Device: config.Device, Width: config.Width, Height: config.Height, Framerate: config.FPS,
		})
	default:
		return nil, fmt.Errorf("RECO_SOURCE 必须为 local 或 rtsp")
	}
}
