package ins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"lipcoder/face/internal/recognition"
	"mime/multipart"
	"net/http"
)

type Inspire struct {
	client *http.Client
	ctx    context.Context
	url    string
}

func NewInspire(url string, client *http.Client) (*Inspire, error) {
	if url == "" || client == nil {
		return nil, recognition.ErrInvalidConfig
	}
	return &Inspire{
		client: client,
		url:    url,
	}, nil
}

// func (a *Inspire) GetFaceData(imageBytes []byte, rank int) ([][]float64, error) {
// 	if a.url == "" || a.client == nil {
// 		return nil, recognition.ErrInvalidState
// 	}
// 	respBody, err := a.PostImage(imageBytes)

// }

func (a *Inspire) PostImage(imgBytes []byte) ([]byte, error) {
	// 构建 multipart form 数据
	var postBody bytes.Buffer
	
	// multipart.NewWriter 会创建一个新的 multipart writer，并将 multipart 数据写入 postBody
	writer := multipart.NewWriter(&postBody)	

	// CreateFormFile 会创建一个新的 multipart section，并返回一个 io.Writer 用于写入文件内容
	part, err := writer.CreateFormFile("image", "image.jpg")
	if err != nil {
		return nil, recognition.ErrBuildImageRequest
	}

	// 将图片 bytes 写入 multipart section
	_, err = part.Write(imgBytes)
	if err != nil {
		return nil, recognition.ErrBuildImageRequest
	}
	// 关闭 multipart writer，写入结束标志
	err = writer.Close()
	if err != nil {
		return nil, recognition.ErrBuildImageRequest
	}

	// 创建 HTTP POST 请求，Content-Type 需要设置为 multipart writer 的 FormDataContentType
	req, err := http.NewRequest("POST", a.url, &postBody)
	if err != nil {
		return nil, recognition.ErrBuildImageRequest
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// 发送 HTTP 请求
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, recognition.ErrRequestFailed
	}
	defer resp.Body.Close()

	// 读取响应 body，限制最大读取 10MB，防止恶意响应导致内存耗尽
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, recognition.ErrRequestFailed
	}

	// 如果 HTTP 状态码不是 200，返回错误，包含状态码和响应 body 以便调试
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"%w: status=%d, body=%s",
			recognition.ErrInvalidResponse,
			resp.StatusCode,
			string(respBody),
		)
	}

	return respBody, nil
}
