package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lipcoder/face/internal/app/camera"
	"lipcoder/face/internal/app/database"
)

type SignInEvent struct {
	CameraID string
	Sequence uint64
	Matches  []database.FaceMatch
	Err      error
}

// StartSignIn starts an asynchronous stream processor. Its output is bounded
// and keeps the newest event so a slow UI cannot stall recognition.
func (s *RecognitionService) StartSignIn(
	ctx context.Context,
	cameraID string,
	buffer int,
) (<-chan SignInEvent, error) {
	if s == nil || ctx == nil {
		return nil, fmt.Errorf("invalid sign-in configuration")
	}
	subscription, err := s.Subscribe(cameraID, 1)
	if err != nil {
		return nil, err
	}
	if buffer <= 0 {
		buffer = 4
	}
	events := make(chan SignInEvent, buffer)
	go s.signInLoop(ctx, subscription, events)
	return events, nil
}

func (s *RecognitionService) signInLoop(
	ctx context.Context,
	subscription *camera.Subscription,
	events chan SignInEvent,
) {
	defer subscription.Close()
	defer close(events)
	nextProcess := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-subscription.Frames:
			if !ok {
				sendLatestSignInEvent(events, SignInEvent{Err: camera.ErrClosed})
				return
			}
			now := time.Now()
			if now.Before(nextProcess) {
				continue
			}
			nextProcess = now.Add(s.cfg.SignInInterval)
			faces, err := s.RecognizeFrame(ctx, frame.JPEG, true)
			event := SignInEvent{CameraID: frame.CameraID, Sequence: frame.Sequence, Err: err}
			if err == nil {
				for _, face := range faces {
					if face.Match != nil {
						event.Matches = append(event.Matches, *face.Match)
					}
				}
			} else if errors.Is(err, context.Canceled) {
				return
			}
			sendLatestSignInEvent(events, event)
		}
	}
}

func sendLatestSignInEvent(output chan SignInEvent, event SignInEvent) {
	select {
	case output <- event:
		return
	default:
	}
	select {
	case <-output:
	default:
	}
	select {
	case output <- event:
	default:
	}
}
