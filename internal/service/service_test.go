package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"lipcoder/face/internal/app/camera"
	"lipcoder/face/internal/app/database"
	"lipcoder/face/internal/app/recognition"
)

type testAnalyzer struct{}

func (testAnalyzer) AnalyzeFrame(context.Context, []byte) (*recognition.FaceResult, error) {
	return &recognition.FaceResult{
		FaceCount: 1,
		Quality:   0.9,
		Embedding: [][]float64{{3, 4}},
	}, nil
}

func (testAnalyzer) AnalyzeFramePose(ctx context.Context, frame []byte) (*recognition.FaceResult, error) {
	return testAnalyzer{}.AnalyzeFrame(ctx, frame)
}

type testDatabase struct {
	name      string
	embedding []float64
}

func (d *testDatabase) AddFace(name string, embedding []float64) (int64, error) {
	d.name = name
	d.embedding = append([]float64(nil), embedding...)
	return 7, nil
}

func (*testDatabase) DeleteFaceByName(string) error         { return nil }
func (*testDatabase) FaceExistsByName(string) (bool, error) { return true, nil }
func (*testDatabase) ListFaceNames() ([]string, error)      { return []string{"test"}, nil }
func (*testDatabase) SearchFaceByEmbedding([]float64, float64) (database.FaceMatch, error) {
	return database.FaceMatch{}, database.ErrNotFound
}

type testVideoSource struct {
	frames chan camera.Frame
	errs   chan error
}

func (s *testVideoSource) ID() string { return "local" }
func (s *testVideoSource) Start(context.Context) (camera.Stream, error) {
	return camera.Stream{Frames: s.frames, Errors: s.errs}, nil
}
func (*testVideoSource) Close() error { return nil }

func TestEnrollAggregatesStreamEmbeddingsWithoutFrameSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &testVideoSource{
		frames: make(chan camera.Frame),
		errs:   make(chan error),
	}
	hub, err := camera.NewHub(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer hub.Close()

	store := &testDatabase{}
	svc, err := New(testAnalyzer{}, store, map[string]*camera.Hub{"local": hub}, Config{
		DefaultCamera:        "local",
		SimilarityThreshold:  0.45,
		EnrollmentSamples:    2,
		EnrollmentMinQuality: 0.45,
		EnrollmentTimeout:    time.Second,
		SignInInterval:       time.Second,
		SignInCooldown:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		source.frames <- camera.Frame{CameraID: "local", Sequence: 1, JPEG: []byte{1}}
		source.frames <- camera.Frame{CameraID: "local", Sequence: 2, JPEG: []byte{2}}
	}()
	result, err := svc.Enroll(ctx, " Alice ", "local")
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != 7 || result.Samples != 2 || store.name != "Alice" {
		t.Fatalf("unexpected enrollment result: %+v, stored name %q", result, store.name)
	}
	if len(store.embedding) != 2 ||
		math.Abs(store.embedding[0]-0.6) > 1e-9 ||
		math.Abs(store.embedding[1]-0.8) > 1e-9 {
		t.Fatalf("aggregated embedding = %v", store.embedding)
	}
}

func TestRecognizeFrameLeavesUnknownFaceWithoutDatabaseMatch(t *testing.T) {
	svc, err := New(testAnalyzer{}, &testDatabase{}, nil, Config{
		SimilarityThreshold:  0.45,
		EnrollmentSamples:    2,
		EnrollmentMinQuality: 0.45,
		EnrollmentTimeout:    time.Second,
		SignInInterval:       time.Second,
		SignInCooldown:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	faces, err := svc.RecognizeFrame(context.Background(), []byte{1}, false)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		t.Fatal(err)
	}
	if len(faces) != 1 || faces[0].Name != "未知" {
		t.Fatalf("faces = %+v", faces)
	}
}
