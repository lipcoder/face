package hikvision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Hik struct {
	client *http.Client
	Config
}

type Config struct {
	Host     string
	Username string
	Password string
}

func NewHik(cfg Config, client *http.Client) (*Hik, error) {
	if cfg.Host == "" {
		return nil, errors.New("hikvision host cannot be empty")
	}
	if cfg.Username == "" {
		return nil, errors.New("hikvision username cannot be empty")
	}
	if cfg.Password == "" {
		return nil, errors.New("hikvision password cannot be empty")
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

	return &Hik{
		client: client,
		Config: cfg,
	}, nil
}

var (
	ErrHik     = errors.New("hik image")
	ErrUrl     = errors.New("url failed")
	ErrRequest = errors.New("request failed")
	ErrImage   = errors.New("image failed")
)

func (a *Hik) Capture(ctx context.Context) ([]byte, error) {
	con := a.Config

	imageBytes, err := a.getWebImage(ctx, con.Host, con.Username, con.Password)
	if err != nil {
		return nil, fmt.Errorf("capture hikvision image: %w", err)
	}

	return imageBytes, nil
}

func (a *Hik) getWebImage(ctx context.Context, URL, username, passwd string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w create request failed: %w", ErrUrl, err)
	}

	req.SetBasicAuth(username, passwd)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w send request failed: %w", ErrRequest, err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

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

	if err = validateImageBytes(imageBytes); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrImage, err)
	}

	return imageBytes, nil
}

func validateImageBytes(body []byte) error {
	if len(body) == 0 {
		return errors.New("empty image body")
	}

	contentType := http.DetectContentType(body)

	switch contentType {
	case "image/jpeg", "image/png":
		return nil
	default:
		return fmt.Errorf("invalid image content type: %s", contentType)
	}
}
