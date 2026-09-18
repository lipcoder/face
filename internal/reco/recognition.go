package reco

import (
	"context"
	"errors"

	"github.com/lipcoder/face/internal/media"
)

// MultiFrameFeatureConfig 表示多帧特征聚合的配置参数
type MultiFrameFeatureConfig struct {
	SampleCount int     // 需要聚合的合格人脸帧数
	MaxFrames   int     // 最多检查的总帧数
	MinQuality  float32 // 最低人脸质量分
}

// Session 表示人脸识别会话接口，提供人脸检测、特征提取和多帧特征聚合等功能
type Session interface {
	// GetFaceMultiFeature 从帧流中聚合一张人脸的特征
	GetFaceMultiFeature(ctx context.Context, frames <-chan *media.Frame, config MultiFrameFeatureConfig) ([]float32, error)
	// GetFacePlace 获取人脸在图像中的位置和大小
	GetFacePlace(ctx context.Context, frame *media.Frame) ([]media.FaceInfo, error)
	// GetFaceFeature 获取人脸特征向量
	GetFaceFeature(ctx context.Context, frame *media.Frame) ([]media.FaceInfo, error)
	// Close 关闭会话，释放资源
	Close() error
}

var (
	ErrTooManyFaces = errors.New("检测到的人脸数量过多")
	ErrNoOneFace    = errors.New("检测到的人脸不止一张")
)
