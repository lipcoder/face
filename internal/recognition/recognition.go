package recognition

import (
	"context"
	"errors"
)

var (
	// 参数或配置错误
	ErrInvalidConfig = errors.New("recognition invalid config")
	// 构造状态错误
	ErrInvalidState = errors.New("recognition invalid state")

	// 输入图片无效 例如图片 bytes 为空、图片损坏
	ErrInvalidImage = errors.New("recognition invalid image")

	// 构建图片请求失败：例如 multipart form 创建失败、写入图片 bytes 失败、创建 HTTP request 失败
	ErrBuildImageRequest = errors.New("recognition build image request failed")

	// 请求识别服务失败：例如网络错误、连接超时、DNS 失败、服务不可达，client.Do(req)使用
	ErrRequestFailed = errors.New("recognition request failed")

	// 识别服务响应无效 例如 HTTP 非 200、响应 body 为空、JSON 解析失败、响应结构不符合预期
	ErrInvalidResponse = errors.New("recognition invalid response")
)
var (
	// 识别服务处理失败 例如服务正常返回 JSON，但 ok=false，表示服务内部识别失败
	ErrRecognitionFailed = errors.New("recognition failed")

	// 没有检测到人脸
	ErrNoFace = errors.New("recognition no face")

	// 人脸数量不符合要求
	ErrNotOneFace = errors.New("recognition not one face")

	// 没有拿到人脸特征 检测到了人脸，但 embedding 为空
	ErrNoFaceEmbedding = errors.New("recognition no face embedding")

	// 人脸特征无效 例如 embedding 维度为 0，或者 embedding 长度和 embedding_dim 不一致
	ErrInvalidEmbedding = errors.New("recognition invalid embedding")

	// 人脸选择策略无效
	ErrInvalidRank = errors.New("recognition invalid rank")

	// 人脸质量过低
	ErrLowFaceQuality = errors.New("recognition low face quality")
)

type Recognition interface {
	GetOneFaceEmbedding(ctx context.Context, imgBytes []byte) ([]float64, error)
	GetBestFaceEmbedding(ctx context.Context, imgBytes []byte) ([]float64, error)
}

type Face struct {
	Index        int
	Box          []int
	DetScore     float64
	Embedding    []float64
	EmbeddingDim int
	Pose         Pose
	Quality      float64
	RGBLiveness  float64
	Emotion      Emotion
}

type Pose struct {
	Pitch float64
	Roll  float64
	Yaw   float64
}

type Emotion struct {
	ID    int
	Label string
}

type PhotoResult struct {
	Image     Image
	FaceCount int
	Faces     []Face
}

type Image struct {
	Channels int
	Height   int
	Width    int
}

type StateResult struct {
	Image     Image
	FaceCount int
	Faces     []Face
}

type EmotionResult struct {
	Image     Image
	FaceCount int
	Faces     []Face
}

type LivenessResult struct {
	FrameCount int
	Live       bool
	BestFrame  Face
	Embedding  []float64
	Frames     []Face
}

// FaceAnalyzer 做基础照片识别，返回检测到的人脸、box、quality、embedding 等信息。
type FaceAnalyzer interface {
	AnalyzeBasic(ctx context.Context, imgBytes []byte) (*PhotoResult, error)
}

// FaceEnrollment 用于录入人脸，要求照片里只能有一张脸。
type FaceEnrollment interface {
	GetOneFaceEmbedding(ctx context.Context, imgBytes []byte) ([]float64, error)
}

// FaceIdentifier 用于基础人脸识别，照片里有多张脸时返回质量最好的人脸 embedding。
type FaceIdentifier interface {
	GetBestFaceEmbedding(ctx context.Context, imgBytes []byte) ([]float64, error)
}

// EmotionAnalyzer 用于检测人脸情感状态，同时返回 box 和 embedding，名字由上层用 embedding 匹配。
type EmotionAnalyzer interface {
	AnalyzeEmotion(ctx context.Context, imgBytes []byte) (*EmotionResult, error)
}

// StateAnalyzer 用于检测当前人的状态，主要关注头部 pitch、roll、yaw 等姿态信息。
type StateAnalyzer interface {
	AnalyzeState(ctx context.Context, imgBytes []byte) (*StateResult, error)
}

// FineIdentifier 用于连续帧精细识别，服务会聚合多帧并返回活体判断和稳定 embedding。
type FineIdentifier interface {
	AnalyzeLivenessEmbedding(ctx context.Context, frames [][]byte) (*LivenessResult, error)
	GetLivenessEmbedding(ctx context.Context, frames [][]byte) ([]float64, error)
}

// Analyzer 包含当前识别服务的完整业务能力：
// 情感检测、基础识别、录入单脸、最佳脸识别、连续帧精细识别、状态检测。
type Analyzer interface {
	Recognition
	FaceAnalyzer
	EmotionAnalyzer
	StateAnalyzer
	FineIdentifier
}
