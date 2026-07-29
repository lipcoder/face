package recognition

import (
	"context"
	"errors"
	"math"
)

var (
	// ErrInvalidConfig 表示初始化参数不完整或非法，例如没有传模型包路径。
	ErrInvalidConfig = errors.New("recognition invalid config")
	// ErrInvalidState 表示 SDK 还没初始化、已经关闭，或底层 session 创建失败。
	ErrInvalidState = errors.New("recognition invalid state")
	// ErrInvalidImage 表示图片 bytes 为空，或 Go 标准库无法解码 jpeg/png。
	ErrInvalidImage = errors.New("recognition invalid image")
	// ErrRecognitionFailed 表示 InspireFace C API 返回了非成功状态码。
	ErrRecognitionFailed = errors.New("recognition failed")
	// ErrNoFace 表示图片有效，但没有检测到人脸。
	ErrNoFace = errors.New("recognition no face")
	// ErrNotOneFace 表示录入帧不是刚好一张脸。
	ErrNotOneFace = errors.New("recognition not one face")
	// ErrNoFaceEmbedding 表示检测到脸，但没有拿到人脸特征向量。
	ErrNoFaceEmbedding = errors.New("recognition no face embedding")
	// ErrInvalidEmbedding 表示特征为空、维度不一致或包含非法值。
	ErrInvalidEmbedding = errors.New("recognition invalid embedding")
	// ErrLowFaceQuality 表示视频流中没有足够的高质量人脸。
	ErrLowFaceQuality = errors.New("recognition low face quality")
)

// Analyzer 处理视频流中的单帧。JPEG/PNG 只是摄像头与 SDK 之间的帧编码，
// 上层业务通过 camera.Frame channel 工作，不再负责拍照和图片挑选。
type Analyzer interface {
	AnalyzeFrame(ctx context.Context, frame []byte) (*FaceResult, error)
	AnalyzeFramePose(ctx context.Context, frame []byte) (*FaceResult, error)
}

// FaceResult 是单帧图像处理结果。
type FaceResult struct {
	FaceCount int64 `json:"face_count"`
	// Box 是扁平数组，每 4 个数表示一张脸：[x, y, width, height]。
	// 多张脸时按检测顺序连续追加，例如两张脸就是 8 个数。
	Box []float64 `json:"box"`
	// Quality 是本次结果里的最高人脸质量分。
	Quality float64 `json:"quality"`
	// Embedding 按检测顺序保存每张脸的特征向量。
	Embedding [][]float64 `json:"embedding"`
	// Pose 与检测到的人脸一一对应，顺序和 Box/Embedding 一致。
	Pose []Pose `json:"pose,omitempty"`
}

// Pose 是 InspireFace 返回的人脸欧拉角。
type Pose struct {
	Roll  float64 `json:"roll"`
	Yaw   float64 `json:"yaw"`
	Pitch float64 `json:"pitch"`
}

// AggregateEmbeddings 对视频流中在线采集的特征向量做 L2 归一化、平均，
// 再归一化。函数不接触也不保留原始帧。
func AggregateEmbeddings(embeddings [][]float64) ([]float64, error) {
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return nil, ErrInvalidEmbedding
	}
	dim := len(embeddings[0])
	sum := make([]float64, dim)
	for _, embedding := range embeddings {
		if len(embedding) != dim {
			return nil, ErrInvalidEmbedding
		}
		normalized, ok := l2Normalize(embedding)
		if !ok {
			return nil, ErrInvalidEmbedding
		}
		for index, value := range normalized {
			sum[index] += value
		}
	}
	for index := range sum {
		sum[index] /= float64(len(embeddings))
	}
	aggregated, ok := l2Normalize(sum)
	if !ok {
		return nil, ErrInvalidEmbedding
	}
	return aggregated, nil
}

func l2Normalize(values []float64) ([]float64, bool) {
	output := append([]float64(nil), values...)
	sum := 0.0
	for _, value := range output {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, false
		}
		sum += value * value
	}
	if sum <= 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
		return nil, false
	}
	inverse := 1 / math.Sqrt(sum)
	for index := range output {
		output[index] *= inverse
	}
	return output, true
}
