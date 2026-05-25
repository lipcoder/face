package inspireface

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"lipcoder/face/internal/recognition"
	"mime/multipart"
	"net/http"
	"time"
)

type Inspire struct {
	client *http.Client
	Config
}

type Config struct {
	Host string
}

var (
	ErrBuildImageRequest = errors.New("build image request failed")
	ErrPostImageRequest  = errors.New("post image request failed")
	ErrPostImageResponse = errors.New("post image response failed")
)

func NewInspire(cfg Config, client *http.Client) (*Inspire, error) {
	if cfg.Host == "" {
		return nil, errors.New("inspireface host cannot be empty")
	}

	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
		}
	} else if client.Timeout == 0 {
		copied := *client
		copied.Timeout = 5 * time.Second
		client = &copied
	}

	return &Inspire{
		client: client,
		Config: cfg,
	}, nil
}

func (a Inspire) PostImage(ctx context.Context, imgBytes []byte) ([]byte, error) {
	var body bytes.Buffer

	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("image", "image.jpg")
	if err != nil {
		return nil, fmt.Errorf("%w: create form file failed: %w", ErrBuildImageRequest, err)
	}

	if _, err := part.Write(imgBytes); err != nil {
		return nil, fmt.Errorf("%w: write image bytes failed: %w", ErrBuildImageRequest, err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("%w: close multipart writer failed: %w", ErrBuildImageRequest, err)
	}

	req, err := http.NewRequest(http.MethodPost, a.Config.Host, &body)
	if err != nil {
		return nil, fmt.Errorf("%w: create request failed: %w", ErrBuildImageRequest, err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: do request failed: %w", ErrPostImageRequest, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read response failed: %w", ErrPostImageResponse, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"%w: status=%d, body=%s",
			ErrPostImageResponse,
			resp.StatusCode,
			string(respBody),
		)
	}

	return respBody, nil
}

func (a Inspire) GetFaceEmbedding(ctx context.Context, imageBytes []byte, rank int) ([][]float64, error) {
	respBody, err := a.PostImage(ctx, imageBytes)
	if err != nil {
		return nil, fmt.Errorf("post image failed: %w", err)
	}
	response, err := BytesFromResponse(respBody)
	if err != nil {
		return nil, err
	}
	if !response.OK {
		return nil, errors.New("inspireface response ok is false")
	}
	if response.FaceCount == 0 || len(response.Faces) == 0 {
		return nil, recognition.ErrNoFace
	}
	switch rank {
	case -1:
		// 返回所有人脸 embedding
		embeddings := make([][]float64, 0, len(response.Faces))

		for _, face := range response.Faces {
			if len(face.Embedding) == 0 {
				return nil, recognition.ErrNoFaceEmbedding
			}

			embeddings = append(embeddings, face.Embedding)
		}

		return embeddings, nil
	case 0:
		// 只允许图片里有一张脸
		if len(response.Faces) != 1 {
			return nil, recognition.ErrNotOneFace
		}

		embedding := response.Faces[0].Embedding
		if len(embedding) == 0 {
			return nil, recognition.ErrNoFaceEmbedding
		}

		return [][]float64{embedding}, nil
	case 1:
		// 返回质量最高的一张脸
		bestFace := response.Faces[0]

		for _, face := range response.Faces[1:] {
			if face.Quality > bestFace.Quality {
				bestFace = face
			}
		}

		if len(bestFace.Embedding) == 0 {
			return nil, recognition.ErrNoFaceEmbedding
		}

		return [][]float64{bestFace.Embedding}, nil
	default:
		return nil, fmt.Errorf("unsupported rank: %d", rank)
	}
}
