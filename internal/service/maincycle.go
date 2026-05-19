package service

import (
	"context"
	"errors"
	"fmt"
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/data"
	"lipcoder/face/internal/recognition"
	"time"
)

const (
	DefaultFaceInterval   = 500 * time.Millisecond
	DefaultFaceSimilarity = 0.45
	// DefaultFaceQuality    = 0.45	
)

// 每隔interval获取一次图像
func SignIn(
	ctx context.Context,
	cam camera.Camera,
	rec recognition.Recognition,
	facedb data.Facedb,
	interval time.Duration,
	similarity float64,
) error {
	if cam == nil {
		return fmt.Errorf("camera cannot be nil")
	}
	if rec == nil {
		return fmt.Errorf("recognition cannot be nil")
	}
	if facedb == nil {
		return fmt.Errorf("facedb cannot be nil")
	}

	if interval <= 0 {
		interval = DefaultFaceInterval
	}

	if similarity <= 0 {
		similarity = DefaultFaceSimilarity
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			bestembedding, err := extractBestEmbeddingFromCamera(ctx, cam, rec)
			if errors.Is(err, recognition.ErrNoFace) {
				continue
			}
			if errors.Is(err, recognition.ErrNoFaceEmbedding) {
				continue
			}
			if err != nil {
				return err
			}

			name, facesimilarity, err := facedb.SearchFaceByEmbedding(ctx, bestembedding, similarity)
			if err != nil {
				if errors.Is(err, data.ErrNotFound) {
					continue
				}
				return fmt.Errorf("attendance search face failed %w", err)
			}

			err = RecordFaceSimilarity(name, facesimilarity)
			if err != nil {
				return fmt.Errorf("write attendance record file %w", err)
			}
		}
	}
}

func extractBestEmbeddingFromCamera(
	ctx context.Context,
	cam camera.Camera,
	rec recognition.Recognition,
) ([]float64, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	imageBytes, err := cam.Capture(ctx)
	if err != nil {
		return nil, fmt.Errorf("mainCycle get image failed,%w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	embedding, err := rec.GetFaceEmbedding(imageBytes, 1)
	if err != nil {
		return nil, fmt.Errorf("get embedding from recognition response: %w", err)
	}
	if embedding == nil {
		return nil, err
	}
	if len(embedding) == 0 {
		return nil, err
	}

	return embedding[0], nil
}
