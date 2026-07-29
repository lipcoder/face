package local

import (
	"fmt"
	"runtime"
	"strconv"
	"time"

	"lipcoder/face/internal/app/camera"
	"lipcoder/face/internal/app/camera/ffmpeg"
)

// New parses local-camera options and returns a continuous video source.
// Supported options: ffmpeg, device/device_id, width, height, fps,
// queue_size, max_frame_bytes and jpeg_quality.
func New(id string, options map[string]any) (camera.Source, error) {
	inputArgs, err := inputArgs(options)
	if err != nil {
		return nil, err
	}
	queueSize, err := camera.IntOption(options, "queue_size", 2)
	if err != nil {
		return nil, err
	}
	jpegQuality, err := camera.IntOption(options, "jpeg_quality", 85)
	if err != nil {
		return nil, err
	}
	maxFrameBytes, err := camera.IntOption(options, "max_frame_bytes", 20<<20)
	if err != nil {
		return nil, err
	}
	retryDelay, err := camera.DurationOption(options, "retry_delay", time.Second)
	if err != nil {
		return nil, err
	}
	return ffmpeg.New(id, ffmpeg.Config{
		Binary:        camera.StringOption(options, "ffmpeg", "ffmpeg"),
		InputArgs:     inputArgs,
		QueueSize:     queueSize,
		JPEGQuality:   jpegQuality,
		MaxFrameBytes: maxFrameBytes,
		RetryDelay:    retryDelay,
	})
}

func inputArgs(options map[string]any) ([]string, error) {
	deviceID, err := camera.IntOption(options, "device_id", 0)
	if err != nil || deviceID < 0 {
		return nil, camera.ErrInvalidConfig
	}
	fps, err := camera.IntOption(options, "fps", 15)
	if err != nil || fps <= 0 {
		return nil, camera.ErrInvalidConfig
	}
	width, err := camera.IntOption(options, "width", 0)
	if err != nil {
		return nil, err
	}
	height, err := camera.IntOption(options, "height", 0)
	if err != nil {
		return nil, err
	}
	if (width == 0) != (height == 0) {
		return nil, fmt.Errorf("%w: width and height must be configured together", camera.ErrInvalidConfig)
	}
	sizeArgs := []string{}
	if width > 0 && height > 0 {
		sizeArgs = []string{"-video_size", fmt.Sprintf("%dx%d", width, height)}
	}
	switch runtime.GOOS {
	case "darwin":
		args := []string{"-f", "avfoundation", "-framerate", strconv.Itoa(fps)}
		args = append(args, sizeArgs...)
		device := camera.StringOption(options, "device", strconv.Itoa(deviceID))
		return append(args, "-i", device+":none"), nil
	case "linux":
		args := []string{"-f", "v4l2", "-framerate", strconv.Itoa(fps)}
		args = append(args, sizeArgs...)
		device := camera.StringOption(options, "device", "/dev/video"+strconv.Itoa(deviceID))
		return append(args, "-i", device), nil
	default:
		return nil, fmt.Errorf("%w: local camera is unsupported on %s", camera.ErrInvalidConfig, runtime.GOOS)
	}
}
