package recognition

import (
	"context"
	"errors"
)

var (
	ErrNoFace          = errors.New("face is empty")
	ErrNotOneFace      = errors.New("not only one face")
	ErrNoFaceEmbedding = errors.New("face embedding is empty")
)

type Recognition interface {
	GetFaceEmbedding(ctx context.Context, imgBytes []byte, rank int) ([][]float64, error)
}
