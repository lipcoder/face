package inspireface

import (
	"encoding/json"
	"fmt"
)

type Response struct {
	Decoder                    string       `json:"decoder"`                      // 图片解码器，例如 opencv
	FaceCount                  int          `json:"face_count"`                   // 检测到的人脸数量
	Faces                      []FaceResult `json:"faces"`                        // 检测到的人脸结果列表，每个人脸对应一个 FaceResult
	Image                      ImageInfo    `json:"image"`                        // 原始图片信息，包括宽、高、通道数
	OK                         bool         `json:"ok"`                           // 本次人脸解析是否成功
	RecommendedCosineThreshold float64      `json:"recommended_cosine_threshold"` // 推荐的余弦相似度阈值
}

type FaceResult struct {
	Box          []int     `json:"box"`           // 人脸框位置，通常表示人脸在图片中的区域；你这里更像是 [x, y, width, height]
	DetScore     float64   `json:"det_score"`     // 人脸检测置信度，越接近 1 表示模型越确信这里是人脸
	Embedding    []float64 `json:"embedding"`     // 人脸特征向量，用于后续人脸比对、识别、去重
	EmbeddingDim int       `json:"embedding_dim"` // 人脸特征向量维度，例如 512，表示 embedding 里有 512 个浮点数
	Index        int       `json:"index"`         // 当前人脸在人脸列表中的索引，从 0 开始
	Pose         PoseInfo  `json:"pose"`          // 人脸姿态信息，包括抬头低头、左右转头、歪头角度
	Quality      float64   `json:"quality"`       // 人脸质量分，越高表示越适合做人脸识别或比对
	TrackID      int       `json:"track_id"`      // 人脸跟踪 ID，视频流中用于追踪同一个人；-1 通常表示没有启用或没有有效跟踪
}

type PoseInfo struct {
	Pitch float64 `json:"pitch"` // 俯仰角，表示抬头或低头的角度
	Roll  float64 `json:"roll"`  // 翻滚角，表示头部左右歪斜的角度
	Yaw   float64 `json:"yaw"`   // 偏航角，表示头部左右转动的角度
}

type ImageInfo struct {
	Channels int `json:"channels"` // 图片通道数，3 通常表示彩色图片
	Height   int `json:"height"`   // 图片高度，单位是像素
	Width    int `json:"width"`    // 图片宽度，单位是像素
}

func BytesFromResponse(respBody []byte) (*Response, error) {
	var result Response
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse extract-best response failed %w", err)
	}
	return &result, nil
}
