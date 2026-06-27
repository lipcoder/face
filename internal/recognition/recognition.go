package recognition

import (
	"context"
	"errors"
)

var (
	// ErrInvalidConfig 表示初始化参数不完整或非法，例如没有传模型包路径。
	ErrInvalidConfig = errors.New("recognition invalid config")
	// ErrInvalidState 表示 SDK 还没初始化、已经关闭，或底层 session 创建失败。
	ErrInvalidState = errors.New("recognition invalid state")
	// ErrInvalidImage 表示图片 bytes 为空，或 Go 标准库无法解码 jpeg/png。
	ErrInvalidImage = errors.New("recognition invalid image")
	// ErrInvalidFrameCount 表示连续帧接口没有收到固定的 25 张图片。
	ErrInvalidFrameCount = errors.New("recognition invalid frame count")
	// ErrRecognitionFailed 表示 InspireFace C API 返回了非成功状态码。
	ErrRecognitionFailed = errors.New("recognition failed")
	// ErrNoFace 表示图片有效，但没有检测到人脸。
	ErrNoFace = errors.New("recognition no face")
	// ErrNotOneFace 表示连续帧接口中某一帧不是刚好一张脸。
	ErrNotOneFace = errors.New("recognition not one face")
	// ErrNoFaceEmbedding 表示检测到脸，但没有拿到人脸特征向量。
	ErrNoFaceEmbedding = errors.New("recognition no face embedding")
	// ErrInvalidEmbedding 表示多帧聚合时 embedding 为空或维度不一致。
	ErrInvalidEmbedding = errors.New("recognition invalid embedding")
	// ErrLowFaceQuality 表示 25 帧里没有达到质量阈值的候选帧。
	ErrLowFaceQuality = errors.New("recognition low face quality")
)

// Analyzer 是上层业务真正需要的三个能力
// 调用方只需要把图片文件,读成 []byte
type Analyzer interface {
	// AnalyzePhoto 分析单张照片，返回人脸数量、所有人脸 box、最高质量分和每张脸的 embedding
	AnalyzePhoto(ctx context.Context, imgBytes []byte) (*FaceResult, error)
	// AnalyzePhotoPose 分析单张照片，并额外返回每张脸的 yaw/pitch/roll 头部姿态角。
	AnalyzePhotoPose(ctx context.Context, imgBytes []byte) (*FaceResult, error)
	// AnalyzePhotoEmotion 在 AnalyzePhoto 的基础上额外返回每张脸的 emotion
	AnalyzePhotoEmotion(ctx context.Context, imgBytes []byte) (*EmotionResult, error)
	// AnalyzeFrameSet 分析固定 25 张连续帧，要求每帧刚好一张脸，并聚合出一个稳定 embedding
	AnalyzeFrameSet(ctx context.Context, frames [][]byte) (*FaceResult, error)
}

// FaceResult 是基础识别和 25 帧识别的统一返回
type FaceResult struct {
	// FaceCount 是检测到的人脸数量。25 帧接口成功时固定为 1
	FaceCount int64 `json:"face_count"`
	// Box 是扁平数组，每 4 个数表示一张脸：[x, y, width, height]。
	// 多张脸时按检测顺序连续追加，例如两张脸就是 8 个数。
	Box []float64 `json:"box"`
	// Quality 是本次结果里的最高人脸质量分。
	Quality float64 `json:"quality"`
	// Embedding 按检测顺序保存每张脸的特征向量。25 帧接口会返回一个聚合后的向量。
	Embedding [][]float64 `json:"embedding"`
	// Pose 与检测到的人脸一一对应，顺序和 Box/Embedding 一致。
	Pose []Pose `json:"pose,omitempty"`
}

// EmotionResult 是带情绪识别的单图返回
type EmotionResult struct {
	FaceCount int64       `json:"face_count"`
	Box       []float64   `json:"box"`
	Quality   float64     `json:"quality"`
	Embedding [][]float64 `json:"embedding"`
	// Emotion 与检测到的人脸一一对应，顺序和 Box/Embedding 一致。
	Emotion []Emotion `json:"emotion"`
	// Pose 与检测到的人脸一一对应，顺序和 Box/Embedding 一致。
	Pose []Pose `json:"pose,omitempty"`
}

// Pose 是 InspireFace 返回的人脸欧拉角。
type Pose struct {
	Roll  float64 `json:"roll"`
	Yaw   float64 `json:"yaw"`
	Pitch float64 `json:"pitch"`
}

// Emotion 是 InspireFace 返回的情绪类别。
type Emotion struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}
