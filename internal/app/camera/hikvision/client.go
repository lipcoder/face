package hikvision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lipcoder/face/internal/app/camera"
)

// Source adapts a Hikvision HTTP snapshot endpoint into the same continuous
// channel contract as local and RTSP video sources. Prefer type=rtsp when the
// camera exposes RTSP directly.
type Source struct {
	id           string
	url          string
	username     string
	password     string
	interval     time.Duration
	maxFrameSize int64
	queueSize    int
	client       *http.Client

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(id string, options map[string]any) (camera.Source, error) {
	url := camera.StringOption(options, "url", "")
	username := camera.StringOption(options, "username", "")
	password := camera.StringOption(options, "password", "")
	if id == "" || url == "" || username == "" || password == "" {
		return nil, camera.ErrInvalidConfig
	}
	timeout, err := camera.DurationOption(options, "request_timeout", 5*time.Second)
	if err != nil {
		return nil, err
	}
	interval, err := camera.DurationOption(options, "interval", 200*time.Millisecond)
	if err != nil {
		return nil, err
	}
	maxFrameSize, err := camera.IntOption(options, "max_frame_bytes", 20<<20)
	if err != nil || maxFrameSize <= 0 {
		return nil, camera.ErrInvalidConfig
	}
	queueSize, err := camera.IntOption(options, "queue_size", 2)
	if err != nil || queueSize <= 0 {
		return nil, camera.ErrInvalidConfig
	}
	return &Source{
		id:           id,
		url:          url,
		username:     username,
		password:     password,
		interval:     interval,
		maxFrameSize: int64(maxFrameSize),
		queueSize:    queueSize,
		client:       &http.Client{Timeout: timeout},
	}, nil
}

func (s *Source) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *Source) Start(ctx context.Context) (camera.Stream, error) {
	if s == nil || ctx == nil {
		return camera.Stream{}, camera.ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return camera.Stream{}, camera.ErrInvalidState
	}
	runCtx, cancel := context.WithCancel(ctx)
	frames := make(chan camera.Frame, s.queueSize)
	errs := make(chan error, 1)
	s.started = true
	s.cancel = cancel
	s.wg.Add(1)
	go s.run(runCtx, frames, errs)
	return camera.Stream{Frames: frames, Errors: errs}, nil
}

func (s *Source) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	return nil
}

func (s *Source) run(ctx context.Context, frames chan camera.Frame, errs chan error) {
	defer s.wg.Done()
	defer close(frames)
	defer close(errs)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	var sequence atomic.Uint64

	for {
		frame, err := s.fetch(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				select {
				case errs <- err:
				default:
				}
			}
		} else {
			sendLatest(ctx, frames, camera.Frame{
				CameraID:   s.id,
				Sequence:   sequence.Add(1),
				CapturedAt: time.Now(),
				JPEG:       frame,
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Source) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build hikvision request: %v", camera.ErrInvalidConfig, err)
	}
	req.SetBasicAuth(s.username, s.password)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: hikvision request: %v", camera.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: hikvision status %d", camera.ErrUnavailable, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, s.maxFrameSize+1))
	if err != nil || int64(len(data)) > s.maxFrameSize {
		return nil, fmt.Errorf("%w: read hikvision frame", camera.ErrInvalidFrame)
	}
	contentType := http.DetectContentType(data)
	if len(data) == 0 || !strings.HasPrefix(contentType, "image/") {
		return nil, camera.ErrInvalidFrame
	}
	return data, nil
}

func sendLatest(ctx context.Context, output chan camera.Frame, frame camera.Frame) {
	select {
	case output <- frame:
		return
	default:
	}
	select {
	case <-output:
	default:
	}
	select {
	case output <- frame:
	case <-ctx.Done():
	}
}
