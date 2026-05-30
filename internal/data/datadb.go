package data

import (
	"context"
	"errors"
)

var (
	ErrNotFound      = errors.New("face not found")
	ErrAlreadyExists = errors.New("face already exists")
)

type Facedb interface {
	AddFace(ctx context.Context, name string, embedding []float64) (int64, error)
	DeleteFaceByName(ctx context.Context, name string) error
	FaceExistsByName(ctx context.Context, name string) (bool, error)
	ListFaceNames(ctx context.Context) ([]string, error)
	SearchFaceByEmbedding(
		ctx context.Context,
		embedding []float64,
		threshold float64,
	) (string, float64, error)
}
