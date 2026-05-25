package local

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gocv.io/x/gocv"
)

type Local int

var (
	ErrLocal     = errors.New("local image")
	ErrNilCamera = errors.New("camera failed")
	ErrNilImages = errors.New("images failed")
)

// 只能靠这个来创建local
func NewLocalCamera(deviceID int) (*Local, error) {
	cam := Local(deviceID)
	return &cam, nil
}

func (a Local) Capture(ctx context.Context) ([]byte, error) {
	imageBytes, err := a.getLocalImage(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLocal, err)
	}

	return imageBytes, nil
}

// 获取本地摄像头的照片
func (a Local) getLocalImage(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	webcam, err := gocv.OpenVideoCaptureWithAPI(int(a), gocv.VideoCaptureV4L2)
	if err != nil {
		return nil, fmt.Errorf("%w: open local camera failed: %w", ErrNilCamera, err)
	}
	defer webcam.Close()

	if !webcam.IsOpened() {
		return nil, ErrNilCamera
	}

	img := gocv.NewMat()
	defer img.Close()

	if err := sleepContext(ctx, 500*time.Millisecond); err != nil {
		return nil, err
	}

	var ok bool

	for i := 0; i < 20; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if webcam.Read(&img) && !img.Empty() {
			ok = true
			break
		}

		if err := sleepContext(ctx, 100*time.Millisecond); err != nil {
			return nil, err
		}
	}

	if !ok {
		return nil, ErrNilCamera
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	buf, err := gocv.IMEncode(".jpg", img)
	if err != nil {
		return nil, fmt.Errorf("%w: encoding jpg failed: %w", ErrNilImages, err)
	}
	defer buf.Close()

	imageBytes := make([]byte, len(buf.GetBytes()))
	copy(imageBytes, buf.GetBytes())

	if len(imageBytes) == 0 {
		return nil, ErrNilImages
	}

	return imageBytes, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
