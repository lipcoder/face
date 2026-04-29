package tools

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

type Hik struct {
	logger *Logger
	client *http.Client
}

var (
	ErrImage   = errors.New("image")
	ErrRequest = errors.New("request")
)

func NewHik(client *http.Client, Logger *slog.Logger) *Hik {
	if client == nil {
		client = http.DefaultClient
	}

	return &Hik{
		logger: NewLogger(Logger),
		client: client,
	}
}

func (hik *Hik) GetWebImage(URL string) ([]byte, error) {
	imageBytes, err := hik.getWebImage(URL)
	if err != nil {
		hik.handleGetWebImageError(err)
	}
	return imageBytes, err
}

func (hik *Hik) getWebImage(URL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, URL, nil)
	// *url.Error，URL格式错误
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.SetBasicAuth("admin", "Long-Live-NBALab")
	// *url.Error，如果同时为请求超时则有两条日志
	resp, err := hik.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request failed: %w", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	

	// ErrRequest
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return nil, fmt.Errorf(
			"%w: status=%d, contentType=%s, body=%s",
			ErrRequest,
			resp.StatusCode,
			contentType,
			string(body),
		)
	}

	// ErrImage
	imageBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read body failed: %w", ErrImage, err)
	}

	// ErrImage
	if len(imageBytes) == 0 {
		return nil, fmt.Errorf("%w: image is empty", ErrImage)
	}

	return imageBytes, nil
}

func (hik *Hik) handleGetWebImageError(err error) {
	if err != nil {
		var urlErr *url.Error

		switch {
		case errors.As(err, &urlErr):
			hik.logger.Error(
				"http request failed",
				"op", urlErr.Op,
				"url", urlErr.URL,
				"err", urlErr.Err,
			)

			if urlErr.Timeout() {
				hik.logger.Error("request timeout", "err", err)
			}

		case errors.Is(err, ErrRequest):
			hik.logger.Error("device rejected request", "err", err)

		case errors.Is(err, ErrImage):
			hik.logger.Error("image read failed", "err", err)

		default:
			hik.logger.Error("get web image failed", "err", err)
		}
	}
}
