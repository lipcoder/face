package tools

import (
	"fmt"
	"math"
)

func CosineSimilarity(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vector length not equal: %d != %d", len(a), len(b))
	}

	if len(a) == 0 {
		return 0, fmt.Errorf("vector is empty")
	}

	var dot float64
	var normA float64
	var normB float64

	for i := 0; i < len(a); i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0, fmt.Errorf("zero vector")
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}