package rtsp

import (
	"fmt"
	"strings"
	"time"

	"lipcoder/face/internal/app/camera"
	"lipcoder/face/internal/app/camera/ffmpeg"
)

func New(id string, options map[string]any) (camera.Source, error) {
	url := camera.StringOption(options, "url", "")
	if !strings.HasPrefix(strings.ToLower(url), "rtsp://") {
		return nil, fmt.Errorf("%w: rtsp camera %q needs an rtsp url", camera.ErrInvalidConfig, id)
	}
	fps, err := camera.IntOption(options, "fps", 0)
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
	inputArgs := []string{"-rtsp_transport", "tcp", "-i", url}
	if fps > 0 {
		inputArgs = append(inputArgs, "-vf", fmt.Sprintf("fps=%d", fps))
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
