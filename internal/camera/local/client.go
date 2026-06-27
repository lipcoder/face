package local

import (
	"context"
	"fmt"
	"lipcoder/face/internal/camera"
	"runtime"
	"sync"
	"time"

	"gocv.io/x/gocv"
)

type Local struct {
	ctx      context.Context
	deviceID int
	mu       sync.Mutex
	localcam *gocv.VideoCapture //GoCV 的摄像头捕获对象，底层封装 OpenCV 的 VideoCapture
}

func NewLocal(ctx context.Context, deviceID int) (*Local, error) {
	if ctx == nil || deviceID < 0 {
		return nil, camera.ErrInvalidConfig
	}
	// 检查摄像头是否可用
	localcam, err := openLocalCamera(deviceID)
	if err != nil {
		return nil, fmt.Errorf("%w: open local camera failed: %w", camera.ErrUnavailable, err)
	}
	if !localcam.IsOpened() {
		localcam.Close()
		return nil, camera.ErrUnavailable
	}

	select {
	case <-time.After(500 * time.Millisecond): // 等待摄像头稳定
	case <-ctx.Done():
		localcam.Close()
		return nil, ctx.Err()
	}

	return &Local{
		ctx:      ctx,
		deviceID: deviceID,
		localcam: localcam,
	}, nil
}

func (local *Local) Close() {
	if local == nil {
		return
	}

	local.mu.Lock()
	defer local.mu.Unlock()

	if local.localcam == nil {
		return
	}

	local.localcam.Close()
	local.localcam = nil
}

func (local *Local) Capture() ([]byte, error) {
	imageBytes, err := local.capture()
	if err != nil {
		return nil, fmt.Errorf("local: %w", err)
	}
	return imageBytes, nil
}

func (local *Local) capture() ([]byte, error) {
	if local == nil ||
		local.ctx == nil ||
		local.deviceID < 0 {
		return nil, camera.ErrInvalidState
	}

	select {
	case <-local.ctx.Done():
		return nil, local.ctx.Err()
	default:
	}

	// Mat 是一个矩阵类型，表示图像数据
	imgcv := gocv.NewMat()
	defer imgcv.Close()

	// 只锁住对 localcam 的访问。
	// localcam 是共享资源，不能让多个 goroutine 同时 Read，
	// 也不能让 Close 和 Read 同时发生。
	local.mu.Lock()

	if local.localcam == nil || !local.localcam.IsOpened() {
		local.mu.Unlock()
		return nil, camera.ErrInvalidState
	}

	ok := local.localcam.Read(&imgcv)

	local.mu.Unlock()

	if !ok || imgcv.Empty() {
		return nil, camera.ErrInvalidImage
	}

	select {
	case <-local.ctx.Done():
		return nil, local.ctx.Err()
	default:
	}

	image, err := gocv.IMEncode(".jpg", imgcv)
	if err != nil {
		return nil, fmt.Errorf("%w: encoding jpg failed: %w", camera.ErrInvalidImage, err)
	}
	defer image.Close()

	// 把OpenCV的Mat编码成JPEG
	raw := image.GetBytes()
	if len(raw) == 0 {
		return nil, camera.ErrInvalidImage
	}

	// image.Close() 后，NativeByteBuffer 内部内存不能继续依赖，所以必须复制一份
	imageBytes := make([]byte, len(raw))
	copy(imageBytes, raw)

	return imageBytes, nil
}

// openLocalCamera 只支持 macOS 和 Linux
// macOS 使用 AVFoundation Linux 使用 V4L2
func openLocalCamera(deviceID int) (*gocv.VideoCapture, error) {
	api, err := localCameraAPI()
	if err != nil {
		return nil, err
	}

	localcam, err := gocv.OpenVideoCaptureWithAPI(deviceID, api)
	if err != nil {
		return nil, err
	}

	if localcam == nil {
		return nil, fmt.Errorf("camera capture is nil")
	}

	if !localcam.IsOpened() {
		localcam.Close()
		return nil, fmt.Errorf("camera %d is not opened", deviceID)
	}

	return localcam, nil
}

// localCameraAPI 根据当前系统返回明确的摄像头后端
// GOOS=darwin 表示 macOS
func localCameraAPI() (gocv.VideoCaptureAPI, error) {
	switch runtime.GOOS {
	case "darwin":
		return gocv.VideoCaptureAVFoundation, nil
	case "linux":
		return gocv.VideoCaptureV4L2, nil
	default:
		return 0, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}