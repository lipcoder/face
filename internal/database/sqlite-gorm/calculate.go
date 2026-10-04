package sqlitegorm

import (
	"fmt"
	"math"

	"github.com/lipcoder/face/internal/database"
)

func (d *DataBase) SearchByFeature(feature []float32) (*database.Person, bool, error) {
	if len(feature) != FeatureLength {
		return nil, false, fmt.Errorf("无效的人脸特征")
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	var best *database.Person
	bestSimilarity := float32(-1)

	for i := range d.persons {
		similarity := cosineSimilarity(feature, d.persons[i].Feature)
		if similarity > bestSimilarity {
			bestSimilarity, best = similarity, &d.persons[i]
		}
	}
	if best == nil || bestSimilarity < FaceSimilarityThreshold {
		return nil, false, nil
	}
	
	// 复制特征向量，避免外部修改原始数据
	result := *best
	result.Feature = append([]float32(nil), best.Feature...)

	return &result, true, nil
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	similarity := dot / (math.Sqrt(normA) * math.Sqrt(normB))
	if similarity > 1 {
		similarity = 1
	} else if similarity < -1 {
		similarity = -1
	}
	return float32(similarity)
}
