package inspire

import (
	"context"
	"fmt"
	"math"

	"github.com/lipcoder/face/internal/media"
	"github.com/lipcoder/face/internal/reco"
)

func (s *Session) GetFaceMultiFeature(
	ctx context.Context,
	frames <-chan *media.Frame,
	config reco.MultiFrameFeatureConfig,
) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.closed || !s.enableQuality || !s.enableRecognition {
		return nil, fmt.Errorf("多特征聚合要求的功能未被满足")
	}
	if frames == nil {
		return nil, fmt.Errorf("帧流为空")
	}
	if config.SampleCount <= 0 || config.MaxFrames <= 0 || config.MinQuality < 0 {
		return nil, fmt.Errorf("配置参数无效")
	}

	// var checked int    // 已检查的帧数
	// var samples int    // 已获得的合格样本数
	// var sum []float64  // 特征向量的加权和
	// var weight float64 // 特征向量的总权重

	var (
		sum     []float64 // 特征向量的加权和
		weight  float64   // 特征向量的总权重
		samples int       // 已获得的合格样本数
		checked int       // 已检查的帧数
	)

	for checked < config.MaxFrames {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return nil, fmt.Errorf(
					"帧流已关闭: 已检查 %d 帧，需要 %d 帧",
					samples,
					config.SampleCount,
				)
			}
			if frame == nil {
				return nil, fmt.Errorf("帧数据为空")
			}
			checked++

			faces, err := s.process(ctx, frame, true, true)
			if err != nil {
				return nil, err
			}
			if len(faces) == 0 {
				continue // 无人脸，跳过
			}
			if len(faces) > 1 {
				return nil, fmt.Errorf("%w: 检测到 %d 张人脸", reco.ErrNoOneFace, len(faces))
			}

			face := faces[0]
			if face.Quality < config.MinQuality {
				continue // 质量不合格，跳过
			}

			if sum == nil {
				sum = make([]float64, len(face.Feature))
			} else if len(sum) != len(face.Feature) {
				return nil, fmt.Errorf("特征向量长度不一致: 期望 %d，实际 %d", len(sum), len(face.Feature))
			}

			sampleWeight := float64(face.Quality)
			for i, v := range face.Feature {
				sum[i] += float64(v) * sampleWeight
			}
			weight += sampleWeight
			samples++

			if samples >= config.SampleCount {
				return AggregatedFeature(sum, weight)
			}

		}
	}
	return nil, fmt.Errorf(
		"多帧特征聚合失败: 已检查 %d 帧，获得 %d 个合格样本，需要 %d 个",
		checked,
		samples,
		config.SampleCount,
	)
}

// AggregatedFeature 归一化聚合特征向量
func AggregatedFeature(sum []float64, weight float64) ([]float32, error) {
	var normSquared float64
	for _, value := range sum {
		average := value / weight
		normSquared += average * average
	}
	if normSquared == 0 {
		return nil, fmt.Errorf("无法归一化全零的特征向量")
	}

	norm := math.Sqrt(normSquared)
	feature := make([]float32, len(sum))
	for i, value := range sum {
		feature[i] = float32((value / weight) / norm)
	}
	return feature, nil
}
