package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"math"
	"sync"

	"github.com/lipcoder/face/internal/media"
)

// VideoInfo 是前端 JS 检测并返回的视频规格。
type VideoInfo struct {
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Framerate float64 `json:"framerate"`
}

type FrameOptions struct {
	VideoInfo
	Frames <-chan []byte
}

type Client struct {
	options FrameOptions
	closed  chan struct{}
	once    sync.Once
}

// Open 用前端返回的视频规格初始化，不读取视频帧。
func Open(options FrameOptions) (*Client, error) {
	if options.Frames == nil || options.Width <= 0 || options.Height <= 0 ||
		options.Width > 16384 || options.Height > 16384 ||
		int64(options.Width)*int64(options.Height)*3 > 256<<20 ||
		options.Framerate <= 0 || options.Framerate > 240 ||
		math.IsNaN(options.Framerate) || math.IsInf(options.Framerate, 0) {
		return nil, errors.New("无效的浏览器视频参数")
	}
	return &Client{options: options, closed: make(chan struct{})}, nil
}

func (c *Client) Read(ctx context.Context) (*media.Frame, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, errors.New("浏览器视频源已关闭")
	case data, ok := <-c.options.Frames:
		if !ok {
			return nil, errors.New("浏览器帧流已关闭")
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("解码浏览器视频帧: %w", err)
		}
		bounds := img.Bounds()
		if bounds.Dx() != c.options.Width || bounds.Dy() != c.options.Height {
			return nil, errors.New("视频帧尺寸与初始化规格不一致")
		}
		pixels := make([]byte, c.options.Width*c.options.Height*3)
		for y := 0; y < c.options.Height; y++ {
			for x := 0; x < c.options.Width; x++ {
				r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
				i := (y*c.options.Width + x) * 3
				pixels[i], pixels[i+1], pixels[i+2] = byte(b>>8), byte(g>>8), byte(r>>8)
			}
		}
		return &media.Frame{Data: pixels, Size: len(pixels), Width: c.options.Width, Height: c.options.Height,
			Framerate: c.options.Framerate, Format: media.PixelBGR, Rotation: media.Rotation0}, nil
	}
}

func (c *Client) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}
