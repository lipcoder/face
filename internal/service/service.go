package service

import (
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/recognition"
)

type AdminRequest struct {
	Name   string
	Action string
	Cam    camera.Camera
	Rec    recognition.Recognition
	Reply  chan AdminResult
}

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
	NewAddFaceRequest(name string, cam camera.Camera, rec recognition.Recognition) AdminRequest
	NewDeleteFaceRequest(name string) AdminRequest
	NewSearchFaceRequest(name string) AdminRequest
}
