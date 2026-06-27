package example

import (
	"context"
	"errors"
	"fmt"
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/record"
	"lipcoder/face/internal/service"
	"time"
)

type EmotionLoop struct {
	ctx        context.Context
	cam        camera.Camera
	rec        recognition.EmotionAnalyzer
	facedb     record.FaceDB
	interval   time.Duration
	similarity float64
	updates    chan service.RecognitionResult
}

func NewEmotionLoop(
	ctx context.Context,
	cam camera.Camera,
	rec recognition.EmotionAnalyzer,
	facedb record.FaceDB,
	interval time.Duration,
	similarity float64,
) *EmotionLoop {
	return &EmotionLoop{
		ctx:        ctx,
		cam:        cam,
		rec:        rec,
		facedb:     facedb,
		interval:   interval,
		similarity: similarity,
		updates:    make(chan service.RecognitionResult, 1),
	}
}

func (l *EmotionLoop) Updates() <-chan service.RecognitionResult {
	if l == nil {
		return nil
	}
	return l.updates
}

func (l *EmotionLoop) Start() error {
	if l == nil {
		return errors.New("emotion loop cannot be nil")
	}
	if l.ctx == nil || l.cam == nil || l.rec == nil || l.facedb == nil || l.updates == nil {
		return errors.New("emotion loop invalid state")
	}
	if l.interval <= 0 {
		return errors.New("interval must be positive")
	}
	if l.similarity <= 0 {
		return errors.New("similarity must be positive")
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

			result, err := DetectFaceEmotions(l.ctx, imageBytes, l.rec, l.facedb, l.similarity)
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

func DetectFaceEmotions(
	ctx context.Context,
	imageBytes []byte,
	rec recognition.EmotionAnalyzer,
	facedb record.FaceDB,
	similarityThreshold float64,
) (service.RecognitionResult, error) {
	if rec == nil {
		return service.RecognitionResult{}, errors.New("recognition cannot be nil")
	}
	if facedb == nil {
		return service.RecognitionResult{}, errors.New("facedb cannot be nil")
	}
	if len(imageBytes) == 0 {
		return service.RecognitionResult{}, recognition.ErrInvalidImage
	}

	result, err := rec.AnalyzeEmotion(ctx, imageBytes)
	if err != nil {
		return service.RecognitionResult{}, fmt.Errorf("analyze emotion: %w", err)
	}

	detections := make([]service.FaceDetection, 0, len(result.Faces))
	for _, face := range result.Faces {
		detection := faceDetection(face)
		if len(face.Embedding) > 0 {
			match, err := facedb.SearchFaceByEmbedding(face.Embedding, similarityThreshold)
			if err == nil {
				detection.FaceID = match.ID
				detection.Name = match.Name
				detection.Similarity = match.Similarity
			} else if !errors.Is(err, record.ErrNotFound) {
				return service.RecognitionResult{}, fmt.Errorf("search face by embedding: %w", err)
			}
		}

		detections = append(detections, detection)
	}

	return service.RecognitionResult{
		ImageWidth:  result.Image.Width,
		ImageHeight: result.Image.Height,
		Faces:       detections,
	}, nil
}
