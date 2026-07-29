// Package ffmpeg implements continuous video capture through an FFmpeg
// image2pipe process. It keeps OpenCV/C++ version details out of the Go build.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"lipcoder/face/internal/app/camera"
)

type Config struct {
	Binary        string
	InputArgs     []string
	QueueSize     int
	JPEGQuality   int
	MaxFrameBytes int
	RetryDelay    time.Duration
}

type Source struct {
	id  string
	cfg Config

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(id string, cfg Config) (*Source, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(cfg.InputArgs) == 0 {
		return nil, camera.ErrInvalidConfig
	}
	if strings.TrimSpace(cfg.Binary) == "" {
		cfg.Binary = "ffmpeg"
	}
	if _, err := exec.LookPath(cfg.Binary); err != nil {
		return nil, fmt.Errorf("%w: find ffmpeg binary %q: %v", camera.ErrUnavailable, cfg.Binary, err)
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 2
	}
	if cfg.JPEGQuality <= 0 || cfg.JPEGQuality > 100 {
		cfg.JPEGQuality = 85
	}
	if cfg.MaxFrameBytes <= 0 {
		cfg.MaxFrameBytes = 20 << 20
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = time.Second
	}
	return &Source{id: id, cfg: cfg}, nil
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
	frames := make(chan camera.Frame, s.cfg.QueueSize)
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
	var sequence uint64

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		err := s.runProcess(ctx, frames, &sequence)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			sendLatestError(errs, err)
		}
		if !waitContext(ctx, s.cfg.RetryDelay) {
			return
		}
	}
}

func (s *Source) runProcess(
	ctx context.Context,
	frames chan camera.Frame,
	sequence *uint64,
) error {
	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, s.cfg.InputArgs...)
	args = append(args,
		"-an",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-q:v", ffmpegJPEGQuality(s.cfg.JPEGQuality),
		"pipe:1",
	)
	command := exec.CommandContext(processCtx, s.cfg.Binary, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%w: create ffmpeg stdout: %v", camera.ErrUnavailable, err)
	}
	stderr := &tailBuffer{limit: 8 << 10}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("%w: start ffmpeg: %v", camera.ErrUnavailable, err)
	}

	decodeErr := decodeMJPEG(stdout, s.cfg.MaxFrameBytes, func(data []byte) error {
		(*sequence)++
		sendLatestFrame(ctx, frames, camera.Frame{
			CameraID:   s.id,
			Sequence:   *sequence,
			CapturedAt: time.Now(),
			JPEG:       data,
		})
		return ctx.Err()
	})
	if decodeErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	detail := strings.TrimSpace(stderr.String())
	if decodeErr != nil && !errors.Is(decodeErr, context.Canceled) {
		return fmt.Errorf("%w: decode %s stream: %v", camera.ErrInvalidFrame, s.id, decodeErr)
	}
	if waitErr != nil {
		if detail != "" {
			return fmt.Errorf("%w: ffmpeg %s exited: %v: %s", camera.ErrUnavailable, s.id, waitErr, detail)
		}
		return fmt.Errorf("%w: ffmpeg %s exited: %v", camera.ErrUnavailable, s.id, waitErr)
	}
	return fmt.Errorf("%w: ffmpeg %s stream ended", camera.ErrClosed, s.id)
}

func decodeMJPEG(reader io.Reader, maxFrameBytes int, emit func([]byte) error) error {
	buffer := make([]byte, 32<<10)
	frame := make([]byte, 0, 256<<10)
	inFrame := false
	var previous byte
	for {
		count, err := reader.Read(buffer)
		for _, value := range buffer[:count] {
			if !inFrame {
				if previous == 0xff && value == 0xd8 {
					frame = append(frame[:0], 0xff, 0xd8)
					inFrame = true
				}
				previous = value
				continue
			}

			frame = append(frame, value)
			if len(frame) > maxFrameBytes {
				frame = frame[:0]
				inFrame = false
				return camera.ErrInvalidFrame
			}
			if previous == 0xff && value == 0xd9 {
				if err := emit(append([]byte(nil), frame...)); err != nil {
					return err
				}
				frame = frame[:0]
				inFrame = false
			}
			previous = value
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if inFrame {
					return io.ErrUnexpectedEOF
				}
				return nil
			}
			return err
		}
	}
}

func ffmpegJPEGQuality(quality int) string {
	// FFmpeg q:v uses 2 (best) through 31 (worst).
	value := 31 - (quality * 29 / 100)
	if value < 2 {
		value = 2
	}
	if value > 31 {
		value = 31
	}
	return fmt.Sprint(value)
}

func sendLatestFrame(ctx context.Context, output chan camera.Frame, frame camera.Frame) {
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

func sendLatestError(output chan error, err error) {
	select {
	case output <- err:
		return
	default:
	}
	select {
	case <-output:
	default:
	}
	select {
	case output <- err:
	default:
	}
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type tailBuffer struct {
	limit int
	data  []byte
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.data = append(b.data, data...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(data), nil
}

func (b *tailBuffer) String() string {
	return string(bytes.TrimSpace(b.data))
}
