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
	Rec    recognition.Analyzer
	Reply  chan AdminResult
}

// 返回的内容
type AdminResult struct {
	Action string
	Name   string
	Names  []string
	Exists bool
	Err    error
}

type Service interface {
	StartAdminLoop() error
	StartSignIn() error
}

type Action interface {
	AddFace(name string, cam camera.Camera, rec recognition.Analyzer) AdminRequest
	DeleteFace(name string) AdminRequest
	SearchFace(name string) AdminRequest
	ListFaceNames() AdminRequest
}

type Box struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type FaceDetection struct {
	FaceID     int64               `json:"face_id,omitempty"`
	Name       string              `json:"name,omitempty"`
	Similarity float64             `json:"similarity,omitempty"`
	Box        Box                 `json:"box"`
	Quality    float64             `json:"quality,omitempty"`
	DetScore   float64             `json:"det_score,omitempty"`
	Pose       recognition.Pose    `json:"pose,omitempty"`
	Emotion    recognition.Emotion `json:"emotion,omitempty"`
}

type RecognitionResult struct {
	ImageWidth  int             `json:"image_width,omitempty"`
	ImageHeight int             `json:"image_height,omitempty"`
	Faces       []FaceDetection `json:"faces"`
}
