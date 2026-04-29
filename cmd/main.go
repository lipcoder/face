package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"lipcoder/face/tools"
)

const (
	ImageUrl   = "http://192.168.1.111/ISAPI/Streaming/channels/101/picture"
	extractURL = "http://127.0.0.1:18082/extract-best"
)

type application struct {
	logger *slog.Logger
}

type images struct {
	application
	Client *http.Client
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// 初始化
	tools.NewLogger(logger)
	Hik := tools.NewHik(&http.Client{Timeout: 10 * time.Second}, logger)
	Face := tools.NewFace(&http.Client{Timeout: 10 * time.Second}, logger)
	// Json := tools.NewJson(&http.Client{Timeout: 10 * time.Second}, logger)

	image, _ := Hik.GetWebImage(ImageUrl)
	imgBytes, _ := Hik.GetWebImage(ImageUrl)

	os.WriteFile("test.jpg", imgBytes, 0644)

	face := Face.GetFace(extractURL, image)

	// feature ,_:= Json.BytesToResponse(face)

	fmt.Println(string(face))

}

// 还没想好怎么处理报错
