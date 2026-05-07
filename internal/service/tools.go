package service

import (
	"context"
	"fmt"
	"lipcoder/face/internal/camera"
	facedb "lipcoder/face/internal/data"
	"lipcoder/face/internal/recognition"
)

func AddFaceFromCamera(
	ctx context.Context,
	name string,
	cam camera.Camera,
	rec recognition.Recognition,
) (int64, error) {
	exists, err := QueryFace(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("add face false %w", err)
	}
	if exists {
		return 0, nil
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	imageBytes, err := cam.Capture()
	if err != nil {
		return 0, fmt.Errorf("mainAction get image failed,%w", err)
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	embedding, err := rec.GetFaceEmbedding(imageBytes, 0)
	if err != nil && embedding != nil {
		return 0, fmt.Errorf("get embedding from inspireface response: %w", err)
	} else if err != nil && embedding == nil {
		return 0, fmt.Errorf("chect face counts %w", err)
	}

	embeddingText := rec.EmbeddingToPGVector(embedding[0])

	id, err := facedb.AddFace(ctx, name, embeddingText)
	if err != nil {
		return 0, fmt.Errorf("add face to database: %w", err)
	}

	return id, nil
}

func DeleteFace(
	ctx context.Context,
	name string,
) (bool, error) {
	exists, err := QueryFace(ctx, name)
	if err != nil {
		return false, fmt.Errorf("delete face false %w", err)
	}
	if !exists {
		return false, nil
	}

	if err := facedb.DeleteFaceByName(ctx, name); err != nil {
		return false, fmt.Errorf("delete face by name: %w", err)
	}
	return true, nil
}

func QueryFace(
	ctx context.Context,
	name string,
) (bool, error) {
	exists, err := facedb.FaceExistsByName(ctx, name)
	if err != nil {
		return false, fmt.Errorf("query face by name: %w", err)
	}
	return exists, nil
}
