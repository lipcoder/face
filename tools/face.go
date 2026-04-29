package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
)

type Face struct {
	logger *Logger
	client *http.Client
}

func NewFace(client *http.Client, Logger *slog.Logger) *Face {
	if client == nil {
		client = http.DefaultClient
	}

	return &Face{
		logger: NewLogger(Logger),
		client: client,
	}
}

var (
	ErrBuildImageRequest = errors.New("build image request failed")
	ErrPostImageRequest  = errors.New("post image request failed")
	ErrPostImageResponse = errors.New("post image response failed")
)

func (image *Face) GetFace(posturl string, imgBytes []byte) []byte {
	respBody, err := image.PostImage(posturl, imgBytes)
	if err != nil {
		image.handlePostImageError(err)
		return nil
	}

	return respBody
}
func (face *Face) PostImage(posturl string, imgBytes []byte) ([]byte, error) {
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

	req, err := http.NewRequest(http.MethodPost, posturl, &body)
	if err != nil {
		return nil, fmt.Errorf("%w: create request failed: %w", ErrBuildImageRequest, err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := face.client.Do(req)
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

func (face *Face) handlePostImageError(err error) {
	var urlErr *url.Error

	switch {
	case errors.As(err, &urlErr):
		face.logger.Error(
			"post image http request failed",
			"op", urlErr.Op,
			"url", urlErr.URL,
			"err", urlErr.Err,
		)

		if urlErr.Timeout() {
			face.logger.Error("post image request timeout", "err", err)
		}

	case errors.Is(err, ErrBuildImageRequest):
		face.logger.Error("build image request failed", "err", err)

	case errors.Is(err, ErrPostImageRequest):
		face.logger.Error("post image request failed", "err", err)

	case errors.Is(err, ErrPostImageResponse):
		face.logger.Error("post image response failed", "err", err)

	default:
		face.logger.Error("post image failed", "err", err)
	}
}
