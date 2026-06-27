package example

import (
	"context"
	"errors"
	"fmt"
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/service"
	"time"
)

type HeadState string

const (
	HeadFacing       HeadState = "facing"
	HeadLookingLeft  HeadState = "looking_left"
	HeadLookingRight HeadState = "looking_right"
	HeadLookingDown  HeadState = "looking_down"
	HeadLookingUp    HeadState = "looking_up"
	HeadTiltedLeft   HeadState = "tilted_left"
	HeadTiltedRight  HeadState = "tilted_right"
)

type HeadPoseDetection struct {
	Box   service.Box      `json:"box"`
	Pose  recognition.Pose `json:"pose"`
	State HeadState        `json:"state"`
}

type HeadPoseResult struct {
	ImageWidth  int                 `json:"image_width,omitempty"`
	ImageHeight int                 `json:"image_height,omitempty"`
	Faces       []HeadPoseDetection `json:"faces"`
}

type HeadStateLoop struct {
	ctx      context.Context
	cam      camera.Camera
	rec      recognition.StateAnalyzer
	interval time.Duration
	updates  chan HeadPoseResult
}

func NewHeadStateLoop(
	ctx context.Context,
	cam camera.Camera,
	rec recognition.StateAnalyzer,
	interval time.Duration,
) *HeadStateLoop {
	return &HeadStateLoop{
		ctx:      ctx,
		cam:      cam,
		rec:      rec,
		interval: interval,
		updates:  make(chan HeadPoseResult, 1),
	}
}

func (l *HeadStateLoop) Updates() <-chan HeadPoseResult {
	if l == nil {
		return nil
	}
	return l.updates
}

func (l *HeadStateLoop) Start() error {
	if l == nil {
		return errors.New("head state loop cannot be nil")
	}
	if l.ctx == nil || l.cam == nil || l.rec == nil || l.updates == nil {
		return errors.New("head state loop invalid state")
	}
	if l.interval <= 0 {
		return errors.New("interval must be positive")
	}

	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return l.ctx.Err()
		case <-ticker.C:
			imageBytes, err := l.cam.Capture()
			if err != nil {
				return fmt.Errorf("capture image: %w", err)
			}

			result, err := DetectHeadState(l.ctx, imageBytes, l.rec)
			if err != nil {
				if errors.Is(err, recognition.ErrNoFace) {
					continue
				}
				return err
			}

			select {
			case l.updates <- result:
			default:
				<-l.updates
				l.updates <- result
			}
		}
	}
}

func DetectHeadState(
	ctx context.Context,
	imageBytes []byte,
	rec recognition.StateAnalyzer,
) (HeadPoseResult, error) {
	if rec == nil {
		return HeadPoseResult{}, errors.New("recognition cannot be nil")
	}
	if len(imageBytes) == 0 {
		return HeadPoseResult{}, recognition.ErrInvalidImage
	}

	result, err := rec.AnalyzeState(ctx, imageBytes)
	if err != nil {
		return HeadPoseResult{}, fmt.Errorf("analyze state: %w", err)
	}

	faces := make([]HeadPoseDetection, 0, len(result.Faces))
	for _, face := range result.Faces {
		faces = append(faces, HeadPoseDetection{
			Box:   faceBox(face.Box),
			Pose:  face.Pose,
			State: classifyHeadState(face.Pose),
		})
	}

	return HeadPoseResult{
		ImageWidth:  result.Image.Width,
		ImageHeight: result.Image.Height,
		Faces:       faces,
	}, nil
}

func classifyHeadState(pose recognition.Pose) HeadState {
	const yawThreshold = 18
	const pitchThreshold = 15
	const rollThreshold = 18

	switch {
	case pose.Pitch > pitchThreshold:
		return HeadLookingDown
	case pose.Pitch < -pitchThreshold:
		return HeadLookingUp
	case pose.Yaw > yawThreshold:
		return HeadLookingRight
	case pose.Yaw < -yawThreshold:
		return HeadLookingLeft
	case pose.Roll > rollThreshold:
		return HeadTiltedRight
	case pose.Roll < -rollThreshold:
		return HeadTiltedLeft
	default:
		return HeadFacing
	}
}
