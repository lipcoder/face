package local

import (
	"context"
	"fmt"
	"lipcoder/face/internal/camera"
	"time"

	"gocv.io/x/gocv"
)

type Local struct {
	ctx      context.Context
	deviceID int
	localcam *gocv.VideoCapture
}

func NewLocal(ctx context.Context, deviceID int) (*Local, error) {
	if ctx == nil || deviceID < 0 {
		return nil, camera.ErrInvalidConfig
	}
	// 打开本地摄像头
	localcam, err := gocv.OpenVideoCaptureWithAPI(deviceID, gocv.VideoCaptureV4L2)
	if err != nil {
		return nil, fmt.Errorf("%w: open local camera failed: %w", camera.ErrUnavailable, err)
	}
	if !localcam.IsOpened() {
		localcam.Close()
		return nil, camera.ErrInvalidConfig
	}
	time.Sleep(500 * time.Millisecond) // 预热500ms

	return &Local{
		ctx:      ctx,
		deviceID: deviceID,
		localcam: localcam,
	}, nil
}

func (l *Local) Close() {
	if l == nil || l.localcam == nil {
		return
	}

	l.localcam.Close()
	l.localcam = nil
}

func (local *Local) Capture() ([]byte, error) {
	imageBytes, err := local.capture()
	if err != nil {
		return nil, fmt.Errorf("local: %w", err)
	}
	return imageBytes, nil
}

func (l *Local) capture() ([]byte, error) {
	// 检查构建是否正确
	if l == nil || l.ctx == nil || l.deviceID < 0 || l.localcam == nil || !l.localcam.IsOpened() {
		return nil, camera.ErrInvalidState
	}

	// 创建一个空的 OpenCV Mat 容器
	imgcv := gocv.NewMat()
	defer imgcv.Close()

	ok := l.localcam.Read(&imgcv)
	if !ok || imgcv.Empty() {
		return nil, camera.ErrInvalidImage
	}

	select {
	case <-l.ctx.Done():
		return nil, l.ctx.Err()
	default:
	}

	// 将 OpenCV Mat 图像矩阵编码成一张 JPG 图片的二进制数据
	image, err := gocv.IMEncode(".jpg", imgcv)
	if err != nil {
		return nil, fmt.Errorf("%w: encoding jpg failed: %w", camera.ErrInvalidImage, err)
	}
	defer image.Close()

	// 关闭之后，它内部的内存可能不能再安全使用，所以复制一份出来
	imageBytes := make([]byte, len(image.GetBytes()))
	copy(imageBytes, image.GetBytes())

	if len(imageBytes) == 0 {
		return nil, camera.ErrInvalidImage
	}

	return imageBytes, nil
}
