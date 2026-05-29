package service

import (
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/recognition"
)

// 发送的请求，包含返回的channel
type AdminRequest struct {
	Name   string
	Action string
	Cam    camera.Camera
	Rec    recognition.Recognition
	Reply  chan AdminResult
}

// 返回的内容
type AdminResult struct {
	Action string
	Name   string
	Exists bool
	Err    error
}

type Service interface {
	StartAdminLoop() error
	StartSignIn() error
}

type Action interface {
	AddFace(name string, cam camera.Camera, rec recognition.Recognition) AdminRequest
	DeleteFace(name string) AdminRequest
	SearchFace(name string) AdminRequest
}
