package camera

import (
	"errors"
)

var (
	// 参数或配置错误
	ErrInvalidConfig = errors.New("camera invalid config")

	// 构造状态错误，例如绕过 NewHik 构造
	ErrInvalidState = errors.New("camera invalid state")

	// 摄像头源不可用：本地摄像头打不开、网络摄像头连不上、设备离线等
	ErrUnavailable = errors.New("camera unavailable")

	// 请求失败：网络错误、HTTP 非 200
	ErrRequestFailed = errors.New("camera request failed")

	// 没有拿到有效图片：空图片、读不到帧、图片格式非法、编码失败
	ErrInvalidImage = errors.New("camera invalid image")

	// 图片太大
	ErrImageTooLarge = errors.New("camera image too large")

	// 不支持的图片格式
	ErrUnsupportedImageFormat = errors.New("camera unsupported image format")
)

type Camera interface {
	Capture() ([]byte, error)
}
