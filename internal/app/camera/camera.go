// Package camera defines the streaming boundary used by the application.
package camera

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidConfig = errors.New("camera invalid config")
	ErrInvalidState  = errors.New("camera invalid state")
	ErrUnavailable   = errors.New("camera unavailable")
	ErrInvalidFrame  = errors.New("camera invalid frame")
	ErrClosed        = errors.New("camera stream closed")
)

// Frame is one immutable frame from a video source. JPEG contains the encoded
// frame used by the recognizer and MJPEG HTTP transport; callers must not
// modify it.
type Frame struct {
	CameraID   string
	Sequence   uint64
	CapturedAt time.Time
	JPEG       []byte
}

type Stream struct {
	Frames <-chan Frame
	Errors <-chan error
}

// Source continuously produces frames until ctx is cancelled or Close is
// called. A source can only be started once.
type Source interface {
	ID() string
	Start(ctx context.Context) (Stream, error)
	Close() error
}

func StringOption(options map[string]any, key, fallback string) string {
	value, ok := options[key]
	if !ok || value == nil {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return fallback
	}
	return text
}

func IntOption(options map[string]any, key string, fallback int) (int, error) {
	value, ok := options[key]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		return int(typed), nil
	case uint64:
		return int(typed), nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("%w: option %s must be an integer", ErrInvalidConfig, key)
		}
		return int(typed), nil
	default:
		parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
		if err != nil {
			return 0, fmt.Errorf("%w: option %s must be an integer: %v", ErrInvalidConfig, key, value)
		}
		return parsed, nil
	}
}

func DurationOption(options map[string]any, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := options[key]
	if !ok || value == nil {
		return fallback, nil
	}
	duration, err := time.ParseDuration(strings.TrimSpace(fmt.Sprint(value)))
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%w: option %s must be a positive duration", ErrInvalidConfig, key)
	}
	return duration, nil
}
