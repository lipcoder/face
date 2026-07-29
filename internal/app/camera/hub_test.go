package camera

import (
	"context"
	"testing"
	"time"
)

type fakeSource struct {
	frames chan Frame
	errs   chan error
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		frames: make(chan Frame),
		errs:   make(chan error),
	}
}

func (s *fakeSource) ID() string { return "test" }

func (s *fakeSource) Start(context.Context) (Stream, error) {
	return Stream{Frames: s.frames, Errors: s.errs}, nil
}

func (s *fakeSource) Close() error { return nil }

func TestHubBroadcastsAndKeepsLatestFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := newFakeSource()
	hub, err := NewHub(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer hub.Close()

	fast, err := hub.Subscribe(4)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()
	slow, err := hub.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()

	for sequence := uint64(1); sequence <= 3; sequence++ {
		source.frames <- Frame{CameraID: "test", Sequence: sequence, JPEG: []byte{byte(sequence)}}
	}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		select {
		case frame := <-fast.Frames:
			if frame.Sequence != sequence {
				t.Fatalf("fast sequence = %d, want %d", frame.Sequence, sequence)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for fast subscriber")
		}
	}
	select {
	case frame := <-slow.Frames:
		if frame.Sequence != 3 {
			t.Fatalf("slow sequence = %d, want latest 3", frame.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for slow subscriber")
	}
}
