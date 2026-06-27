package service

import (
	"context"

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

type RecognitionService struct {
	rec recognition.Analyzer
}

func New(rec recognition.Analyzer) (*RecognitionService, error) {
	if rec == nil {
		return nil, recognition.ErrInvalidConfig
	}
	return &RecognitionService{rec: rec}, nil
}

func (s *RecognitionService) AnalyzePhoto(ctx context.Context, imgBytes []byte) (*recognition.FaceResult, error) {
	if s == nil || s.rec == nil {
		return nil, recognition.ErrInvalidState
	}
	return s.rec.AnalyzePhoto(ctx, imgBytes)
}

func (s *RecognitionService) AnalyzePhotoPose(ctx context.Context, imgBytes []byte) (*recognition.FaceResult, error) {
	if s == nil || s.rec == nil {
		return nil, recognition.ErrInvalidState
	}
	return s.rec.AnalyzePhotoPose(ctx, imgBytes)
}

func (s *RecognitionService) AnalyzePhotoEmotion(ctx context.Context, imgBytes []byte) (*recognition.EmotionResult, error) {
	if s == nil || s.rec == nil {
		return nil, recognition.ErrInvalidState
	}
	return s.rec.AnalyzePhotoEmotion(ctx, imgBytes)
}

func (s *RecognitionService) AnalyzeFrameSet(ctx context.Context, frames [][]byte) (*recognition.FaceResult, error) {
	if s == nil || s.rec == nil {
		return nil, recognition.ErrInvalidState
	}
	return s.rec.AnalyzeFrameSet(ctx, frames)
}
