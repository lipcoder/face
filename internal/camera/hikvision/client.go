package hikvision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"lipcoder/face/internal/camera"
	"net/http"
)

type Hikvision struct {
	client       *http.Client
	ctx          context.Context
	url          string
	username     string
	passwd       string
	maxImageSize int64
}

func NewHikvision(
	client *http.Client,
	ctx context.Context,
	url string,
	name string,
	passwd string,
	maxImageSize int64,
) (*Hikvision, error) {
	if client == nil || ctx == nil || url == "" || name == "" || passwd == "" || maxImageSize <= 0 || client.Timeout <= 0{
		return nil, camera.ErrInvalidConfig
	}

	return &Hikvision{
		client:       client,
		ctx:          ctx,
		url:          url,
		username:     name,
		passwd:       passwd,
		maxImageSize: maxImageSize,
	}, nil
}

func (hik *Hikvision) Capture() ([]byte, error) {
	imageBytes, err := hik.capture()
	if err != nil {
		return nil, fmt.Errorf("hikvision: %w", err)
	}
	return imageBytes, nil
}

func (hik *Hikvision) capture() ([]byte, error) {
	// 检查构建是否正确
	if hik == nil || hik.client == nil || hik.ctx == nil || hik.url == "" || hik.username == "" || hik.passwd == "" || hik.client.Timeout <= 0{
		return nil, camera.ErrInvalidState
	}

	// 创建请求
	req, err := http.NewRequestWithContext(hik.ctx, http.MethodGet, hik.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create request failed: %w", camera.ErrInvalidConfig, err)
	}
	req.SetBasicAuth(hik.username, hik.passwd) // 请求要带上账号密码

	// 发送请求
	resp, err := hik.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		return nil, fmt.Errorf("%w: send request failed: %w", camera.ErrUnavailable, err)
	}
	defer resp.Body.Close()

	// 判断响应头
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

		return nil, fmt.Errorf(
			"%w: status=%d,contentType=%s,body=%s",
			camera.ErrRequestFailed,
			resp.StatusCode,
			resp.Header.Get("Content-Type"),
			string(body),
		)
	}

	// 判断响应体
	reader := io.LimitReader(resp.Body, hik.maxImageSize+1)
	imageBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("%w: read response body failed: %w", camera.ErrRequestFailed, err) // 可能是网络波动等原因
	}
	if len(imageBytes) == 0 {
		return nil, camera.ErrInvalidImage // 检查是否为空照片空帧
	}
	contentType := http.DetectContentType(imageBytes)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return nil, camera.ErrUnsupportedImageFormat // 检查返回的照片格式是否被支持
	}

	return imageBytes, nil
}
