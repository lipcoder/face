package example

import (
	"context"
	"errors"
	"fmt"
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/data"
	"lipcoder/face/internal/recognition"
	"time"
)

type SignInLoop struct {
	ctx        context.Context
	cam        camera.Camera
	rec        recognition.Recognition
	facedb     data.Facedb
	interval   time.Duration
	similarity float64
}

// 每隔interval获取一次图像
func NewSignInLoop(
	ctx context.Context,
	cam camera.Camera,
	rec recognition.Recognition,
	facedb data.Facedb,
	interval time.Duration,
	similarity float64,
) *SignInLoop {
	return &SignInLoop{
		ctx:        ctx,
		cam:        cam,
		rec:        rec,
		facedb:     facedb,
		interval:   interval,
		similarity: similarity,
	}
}

func (l *SignInLoop) StartSignIn() error {
	if l == nil {
		return fmt.Errorf("sign in loop cannot be nil")
	}
	if l.ctx == nil {
		return fmt.Errorf("context cannot be nil")
	}
	if l.cam == nil {
		return fmt.Errorf("camera cannot be nil")
	}
	if l.rec == nil {
		return fmt.Errorf("recognition cannot be nil")
	}
	if l.facedb == nil {
		return fmt.Errorf("facedb cannot be nil")
	}
	if l.interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	if l.similarity <= 0 {
		return fmt.Errorf("similarity must be positive")
	}

	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return l.ctx.Err()

		case <-ticker.C:
			bestembedding, err := extractBestEmbeddingFromCamera(l.ctx, l.cam, l.rec)
			if errors.Is(err, recognition.ErrNoFace) {
				continue
			}
			if errors.Is(err, recognition.ErrNoFaceEmbedding) {
				continue
			}
			if err != nil {
				return err
			}

			name, facesimilarity, err := l.facedb.SearchFaceByEmbedding(l.ctx, bestembedding, l.similarity)
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

	embedding, err := rec.GetFaceEmbedding(ctx, imageBytes, 1)
	if err != nil {
		return nil, fmt.Errorf("get embedding from recognition response: %w", err)
	}
	if len(embedding) == 0 {
		return nil, recognition.ErrNoFaceEmbedding
	}

	return embedding[0], nil
}
