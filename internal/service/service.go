// Package service contains application workflows. Hardware, image processing
// and persistence remain behind internal/app contracts.
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"lipcoder/face/internal/app/camera"
	"lipcoder/face/internal/app/database"
	"lipcoder/face/internal/app/recognition"
)

type Config struct {
	DefaultCamera        string
	SimilarityThreshold  float64
	EnrollmentSamples    int
	EnrollmentMinQuality float64
	EnrollmentTimeout    time.Duration
	SignInInterval       time.Duration
	SignInCooldown       time.Duration
}

type RecognitionService struct {
	rec     recognition.Analyzer
	facedb  database.FaceDB
	signLog database.SignLogWriter
	hubs    map[string]*camera.Hub
	cfg     Config

	signMu       sync.Mutex
	lastSignTime map[int64]time.Time
}

func New(
	rec recognition.Analyzer,
	facedb database.FaceDB,
	hubs map[string]*camera.Hub,
	cfg Config,
) (*RecognitionService, error) {
	if rec == nil || facedb == nil {
		return nil, recognition.ErrInvalidConfig
	}
	if cfg.SimilarityThreshold <= 0 || cfg.SimilarityThreshold > 1 ||
		cfg.EnrollmentSamples <= 0 ||
		cfg.EnrollmentMinQuality < 0 || cfg.EnrollmentMinQuality > 1 ||
		cfg.EnrollmentTimeout <= 0 ||
		cfg.SignInInterval <= 0 ||
		cfg.SignInCooldown <= 0 {
		return nil, fmt.Errorf("%w: invalid service config", recognition.ErrInvalidConfig)
	}
	if len(hubs) > 0 {
		if cfg.DefaultCamera == "" || hubs[cfg.DefaultCamera] == nil {
			return nil, fmt.Errorf("%w: default camera %q is unavailable", camera.ErrInvalidConfig, cfg.DefaultCamera)
		}
	}
	var signLog database.SignLogWriter
	if writer, ok := facedb.(database.SignLogWriter); ok {
		signLog = writer
	}
	return &RecognitionService{
		rec:          rec,
		facedb:       facedb,
		signLog:      signLog,
		hubs:         hubs,
		cfg:          cfg,
		lastSignTime: make(map[int64]time.Time),
	}, nil
}

func (s *RecognitionService) AnalyzeFrame(ctx context.Context, frame []byte) (*recognition.FaceResult, error) {
	if s == nil || s.rec == nil {
		return nil, recognition.ErrInvalidState
	}
	return s.rec.AnalyzeFrame(ctx, frame)
}

func (s *RecognitionService) AnalyzeFramePose(ctx context.Context, frame []byte) (*recognition.FaceResult, error) {
	if s == nil || s.rec == nil {
		return nil, recognition.ErrInvalidState
	}
	return s.rec.AnalyzeFramePose(ctx, frame)
}

type EnrollmentResult struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	CameraID   string  `json:"camera_id"`
	Samples    int     `json:"samples"`
	MaxQuality float64 `json:"max_quality"`
}

// Enroll consumes a camera subscription and aggregates embeddings online.
// Raw frames are released after each AnalyzeFrame call and are never collected
// into an in-memory image set.
func (s *RecognitionService) Enroll(
	ctx context.Context,
	name string,
	cameraID string,
) (EnrollmentResult, error) {
	if s == nil || s.rec == nil || s.facedb == nil {
		return EnrollmentResult{}, recognition.ErrInvalidState
	}
	if ctx == nil {
		return EnrollmentResult{}, recognition.ErrInvalidConfig
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return EnrollmentResult{}, fmt.Errorf("%w: name cannot be empty", database.ErrInvalidInput)
	}
	hub, resolvedID, err := s.cameraHub(cameraID)
	if err != nil {
		return EnrollmentResult{}, err
	}
	subscription, err := hub.Subscribe(1)
	if err != nil {
		return EnrollmentResult{}, err
	}
	defer subscription.Close()

	enrollCtx, cancel := context.WithTimeout(ctx, s.cfg.EnrollmentTimeout)
	defer cancel()
	embeddings := make([][]float64, 0, s.cfg.EnrollmentSamples)
	maxQuality := 0.0
	for len(embeddings) < s.cfg.EnrollmentSamples {
		select {
		case <-enrollCtx.Done():
			return EnrollmentResult{}, fmt.Errorf(
				"%w: accepted %d/%d samples: %v",
				recognition.ErrLowFaceQuality,
				len(embeddings),
				s.cfg.EnrollmentSamples,
				enrollCtx.Err(),
			)
		case frame, ok := <-subscription.Frames:
			if !ok {
				return EnrollmentResult{}, camera.ErrClosed
			}
			result, err := s.rec.AnalyzeFrame(enrollCtx, frame.JPEG)
			if err != nil {
				return EnrollmentResult{}, fmt.Errorf("analyze enrollment frame: %w", err)
			}
			if result == nil || result.FaceCount != 1 || len(result.Embedding) != 1 {
				continue
			}
			if result.Quality < s.cfg.EnrollmentMinQuality || len(result.Embedding[0]) == 0 {
				continue
			}
			embeddings = append(embeddings, append([]float64(nil), result.Embedding[0]...))
			if result.Quality > maxQuality {
				maxQuality = result.Quality
			}
		}
	}

	embedding, err := recognition.AggregateEmbeddings(embeddings)
	if err != nil {
		return EnrollmentResult{}, err
	}
	id, err := s.facedb.AddFace(name, embedding)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("add face to database: %w", err)
	}
	return EnrollmentResult{
		ID:         id,
		Name:       name,
		CameraID:   resolvedID,
		Samples:    len(embeddings),
		MaxQuality: maxQuality,
	}, nil
}

type RecognizedFace struct {
	Box     []float64           `json:"box"`
	Name    string              `json:"name"`
	Quality float64             `json:"quality"`
	Match   *database.FaceMatch `json:"match,omitempty"`
}

func (s *RecognitionService) RecognizeFrame(
	ctx context.Context,
	frame []byte,
	writeSignLog bool,
) ([]RecognizedFace, error) {
	result, err := s.AnalyzeFrame(ctx, frame)
	if err != nil {
		return nil, err
	}
	if result == nil || result.FaceCount == 0 {
		return []RecognizedFace{}, nil
	}
	faces := make([]RecognizedFace, 0, result.FaceCount)
	for index := 0; index < int(result.FaceCount); index++ {
		face := RecognizedFace{
			Box:     boxAt(result.Box, index),
			Name:    "未知",
			Quality: result.Quality,
		}
		if index < len(result.Embedding) && len(result.Embedding[index]) > 0 {
			match, matchErr := s.facedb.SearchFaceByEmbedding(
				result.Embedding[index],
				s.cfg.SimilarityThreshold,
			)
			if matchErr == nil {
				face.Name = match.Name
				face.Match = &match
				if writeSignLog {
					if err := s.recordSign(match); err != nil {
						return nil, err
					}
				}
			} else if !errors.Is(matchErr, database.ErrNotFound) {
				return nil, matchErr
			}
		}
		faces = append(faces, face)
	}
	return faces, nil
}

func (s *RecognitionService) DeleteFace(name string) error {
	if s == nil || s.facedb == nil {
		return database.ErrInvalidState
	}
	return s.facedb.DeleteFaceByName(strings.TrimSpace(name))
}

func (s *RecognitionService) FaceExists(name string) (bool, error) {
	if s == nil || s.facedb == nil {
		return false, database.ErrInvalidState
	}
	return s.facedb.FaceExistsByName(strings.TrimSpace(name))
}

func (s *RecognitionService) ListFaces() ([]string, error) {
	if s == nil || s.facedb == nil {
		return nil, database.ErrInvalidState
	}
	return s.facedb.ListFaceNames()
}

func (s *RecognitionService) Subscribe(cameraID string, buffer int) (*camera.Subscription, error) {
	hub, _, err := s.cameraHub(cameraID)
	if err != nil {
		return nil, err
	}
	return hub.Subscribe(buffer)
}

func (s *RecognitionService) CameraIDs() []string {
	if s == nil {
		return nil
	}
	ids := make([]string, 0, len(s.hubs))
	for id := range s.hubs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *RecognitionService) DefaultCamera() string {
	if s == nil {
		return ""
	}
	return s.cfg.DefaultCamera
}

func (s *RecognitionService) cameraHub(cameraID string) (*camera.Hub, string, error) {
	if s == nil {
		return nil, "", camera.ErrInvalidState
	}
	cameraID = strings.TrimSpace(cameraID)
	if cameraID == "" {
		cameraID = s.cfg.DefaultCamera
	}
	hub := s.hubs[cameraID]
	if hub == nil {
		return nil, "", fmt.Errorf("%w: camera %q is unavailable", camera.ErrInvalidConfig, cameraID)
	}
	return hub, cameraID, nil
}

func (s *RecognitionService) recordSign(match database.FaceMatch) error {
	if s.signLog == nil {
		return nil
	}
	now := time.Now()
	s.signMu.Lock()
	last := s.lastSignTime[match.ID]
	if !last.IsZero() && now.Sub(last) < s.cfg.SignInCooldown {
		s.signMu.Unlock()
		return nil
	}
	s.lastSignTime[match.ID] = now
	s.signMu.Unlock()
	if err := s.signLog.RecordSignLog(match.ID, match.Name, match.Similarity); err != nil {
		s.signMu.Lock()
		delete(s.lastSignTime, match.ID)
		s.signMu.Unlock()
		return fmt.Errorf("write sign log: %w", err)
	}
	return nil
}

func boxAt(values []float64, index int) []float64 {
	offset := index * 4
	if offset < 0 || offset+3 >= len(values) {
		return []float64{0, 0, 0, 0}
	}
	return append([]float64(nil), values[offset:offset+4]...)
}
