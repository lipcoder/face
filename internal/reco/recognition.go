package reco

import (
	"context"
	"errors"

	"github.com/lipcoder/face/internal/media"
)

// Rect 表示一个矩形区域，表示人脸在图像中的位置和大小
type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Angles 表示人脸的旋转角度，包括翻滚角、偏航角和俯仰角
type Angles struct {
	Roll  float32 // 翻滚角：头向左/右歪
	Yaw   float32 // 偏航角：头向左/右转
	Pitch float32 // 俯仰角：抬头/低头
}

// FaceInfo 表示检测到的人脸信息
type FaceInfo struct {
	TrackID             int64
	TrackCount          int64
	Box                 Rect
	Angles              Angles
	DetectionConfidence float32
	Quality             float32
	Feature             []float32
}

// MultiFrameFeatureConfig 表示多帧特征聚合的配置参数
type MultiFrameFeatureConfig struct {
	SampleCount int     // 需要聚合的合格人脸帧数
	MaxFrames   int     // 最多检查的总帧数
	MinQuality  float32 // 最低人脸质量分
}

type CyclicFeatureConfig struct {
	MinReadyFrames       int     // 连续满足条件的最少帧数
	MinSearchFaceQuality float32 // 人脸质量的最小阈值，低于此值的人脸不参与特征提取
	FaceEdgeMargin       float64 // 人脸框四周至少留出自身边长的画面空间比例
}

// Session 表示人脸识别会话接口，提供人脸检测、特征提取和多帧特征聚合等功能
type Session interface {
	// GetFaceMultiFeature 从帧流中聚合一张人脸的特征
	GetFaceMultiFeature(ctx context.Context, frames <-chan *media.Frame, status chan<- bool, config MultiFrameFeatureConfig) ([]float32, error)
	// GetFacePlace 获取人脸在图像中的位置和大小
	GetFacePlace(ctx context.Context, frame *media.Frame) ([]FaceInfo, error)
	// GetFaceFeature 获取人脸特征向量
	GetFaceFeature(ctx context.Context, frame *media.Frame) ([]FaceInfo, error)
	// GetCyclicFaceFeature 获取循环人脸特征，retryTracks 返回本帧未匹配、需要重新提取特征的轨迹。
	GetCyclicFaceFeature(ctx context.Context, frames <-chan *media.Frame, results chan<- []FaceInfo, retryTracks <-chan []int64, config CyclicFeatureConfig) error
	// Close 关闭会话，释放资源
	Close() error
}

var (
	ErrTooManyFaces = errors.New("检测到的人脸数量过多")
	ErrNoOneFace    = errors.New("检测到的人脸不止一张")
)
