package recognition

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"lipcoder/face/internal/adapter/camera"
	"lipcoder/face/internal/adapter/camera/local"
	"lipcoder/face/internal/adapter/inspireface"
	facedb "lipcoder/face/internal/database"
	facejson "lipcoder/face/internal/domain/json"
)

const (
	DefaultAttendanceInterval = 500 * time.Millisecond
	DefaultCosineThreshold    = 0.45
	DefaultMaxAddFaceJobs     = 2
)

var (
	ErrNilCamera         = errors.New("camera cannot be nil")
	ErrImageEmpty        = errors.New("image is empty")
	ErrNoFaceDetected    = errors.New("no face detected")
	ErrMultipleFaces     = errors.New("multiple faces detected")
	ErrEmbeddingEmpty    = errors.New("embedding is empty")
	ErrFaceAlreadyExists = errors.New("face already exists")
)

type AttendanceHandler func(ctx context.Context, name string, similarity float64) error

type FaceVerifyService struct {
	inspire *inspireface.Inspire
	// 控制 add 人脸任务的并发数,主签到不走这个限制，避免录入任务影响签到主流程
	addFaceSem chan struct{}
}

func NewFaceVerifyService(httpClient *http.Client) *FaceVerifyService {
	return &FaceVerifyService{
		inspire:    inspireface.NewInspire(httpClient),
		addFaceSem: make(chan struct{}, DefaultMaxAddFaceJobs),
	}
}

// StartAttendanceLoop 是主签到循环。
// 这条线常驻运行：
// camera.Capture -> InspireFace -> SearchFaceByEmbedding -> onMatched
// go里面函数是一等公民，使用这个StartAttendanceLoop函数的时候AttendanceHandler就是一个函数，要将函数传进去
func (s *FaceVerifyService) StartAttendanceLoop(
	ctx context.Context,
	cam camera.Camera,
	interval time.Duration,
	threshold float64,
	onMatched AttendanceHandler,
) error {
	if cam == nil {
		return ErrNilCamera
	}

	if interval <= 0 {
		interval = DefaultAttendanceInterval
	}

	if threshold <= 0 {
		threshold = DefaultCosineThreshold
	}

	ticker := time.NewTicker(interval) //每隔interval响一次
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			embedding, err := s.ExtractBestEmbeddingFromCamera(ctx, cam)
			if err != nil {
				slog.Warn("attendance extract embedding failed", "err", err)
				continue
			}

			embeddingText := EmbeddingToPGVector(embedding)

			name, similarity, err := facedb.SearchFaceByEmbedding(ctx, embeddingText, threshold)
			if err != nil {
				if !errors.Is(err, facedb.ErrNotFound) {
					slog.Warn("attendance search face failed", "err", err)
				}
				continue
			}

			if onMatched != nil {
				if err := onMatched(ctx, name, similarity); err != nil {
					slog.Warn("attendance handler failed", "name", name, "similarity", similarity, "err", err)
				}
				continue
			}

			slog.Info("attendance matched", "name", name, "similarity", similarity)
		}
	}
}

// ExtractBestEmbeddingFromCamera 给主签到用。
// 如果图片里有多张脸，选质量最高的一张。
func (s *FaceVerifyService) ExtractBestEmbeddingFromCamera(
	ctx context.Context,
	cam camera.Camera,
) ([]float64, error) {
	return s.extractEmbeddingFromCamera(ctx, cam, false)
}

// ExtractSingleEmbeddingFromCamera 给添加人脸用
// 如果图片里有多张脸，直接报错，避免录入错人
func (s *FaceVerifyService) ExtractSingleEmbeddingFromCamera(
	ctx context.Context,
	cam camera.Camera,
) ([]float64, error) {
	return s.extractEmbeddingFromCamera(ctx, cam, true)
}

func (s *FaceVerifyService) extractEmbeddingFromCamera(
	ctx context.Context,
	cam camera.Camera,
	requireSingleFace bool,
) ([]float64, error) {
	if cam == nil {
		return nil, ErrNilCamera
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	imageBytes, err := cam.Capture()
	if err != nil {
		return nil, fmt.Errorf("capture image: %w", err)
	}

	return s.extractEmbeddingFromImage(ctx, imageBytes, requireSingleFace)
}

func (s *FaceVerifyService) extractEmbeddingFromImage(
	ctx context.Context,
	imageBytes []byte,
	requireSingleFace bool,
) ([]float64, error) {
	if len(imageBytes) == 0 {
		return nil, ErrImageEmpty
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	respBody, err := s.inspire.PostImage(imageBytes)
	if err != nil {
		return nil, fmt.Errorf("post image to inspireface: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	embedding, err := getBestEmbeddingFromInspireResponse(respBody, requireSingleFace)
	if err != nil {
		return nil, fmt.Errorf("get embedding from inspireface response: %w", err)
	}

	return embedding, nil
}

func getBestEmbeddingFromInspireResponse(
	respBody []byte,
	requireSingleFace bool,
) ([]float64, error) {
	response, err := facejson.BytesToResponse(respBody)
	if err != nil {
		return nil, err
	}

	if !response.OK {
		return nil, errors.New("inspireface response ok is false")
	}

	if len(response.Faces) == 0 {
		return nil, ErrNoFaceDetected
	}

	if requireSingleFace && len(response.Faces) != 1 {
		return nil, ErrMultipleFaces
	}

	bestFace := response.Faces[0]

	for _, face := range response.Faces[1:] {
		if face.Quality > bestFace.Quality {
			bestFace = face
		}
	}

	if len(bestFace.Embedding) == 0 {
		return nil, ErrEmbeddingEmpty
	}

	return bestFace.Embedding, nil
}

type AdminOp string

const (
	AdminAddFace    AdminOp = "add"
	AdminDeleteFace AdminOp = "delete"
	AdminQueryFace  AdminOp = "query"
)

type AdminRequest struct {
	Op   AdminOp
	Name string

	// add 的时候需要 Cam。
	// delete/query 不需要 Cam。
	Cam camera.Camera

	Reply chan AdminResult
}

type AdminResult struct {
	Op     AdminOp
	Name   string
	ID     int64
	Exists bool
	Err    error
}

// StartAdminLoop 处理管理请求。
// add 会启动新的 goroutine，不阻塞管理循环。
// delete/query 直接按 name 操作数据库。
func (s *FaceVerifyService) StartAdminLoop(
	ctx context.Context,
	reqCh <-chan AdminRequest,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case req, ok := <-reqCh:
			if !ok {
				return nil
			}

			switch req.Op {
			case AdminAddFace:
				go s.handleAddFaceRequest(ctx, req)

			case AdminDeleteFace:
				err := s.DeleteFaceByName(ctx, req.Name)
				sendAdminResult(ctx, req.Reply, AdminResult{
					Op:   req.Op,
					Name: req.Name,
					Err:  err,
				})

			case AdminQueryFace:
				exists, err := s.QueryFaceByName(ctx, req.Name)
				sendAdminResult(ctx, req.Reply, AdminResult{
					Op:     req.Op,
					Name:   req.Name,
					Exists: exists,
					Err:    err,
				})

			default:
				sendAdminResult(ctx, req.Reply, AdminResult{
					Op:   req.Op,
					Name: req.Name,
					Err:  fmt.Errorf("unknown admin op: %s", req.Op),
				})
			}
		}
	}
}

func (s *FaceVerifyService) handleAddFaceRequest(
	ctx context.Context,
	req AdminRequest,
) {
	select {
	case <-ctx.Done():
		sendAdminResult(ctx, req.Reply, AdminResult{
			Op:   req.Op,
			Name: req.Name,
			Err:  ctx.Err(),
		})
		return

	case s.addFaceSem <- struct{}{}:
		defer func() {
			<-s.addFaceSem
		}()
	}

	id, err := s.AddFaceFromCamera(ctx, req.Name, req.Cam)

	sendAdminResult(ctx, req.Reply, AdminResult{
		Op:   req.Op,
		Name: req.Name,
		ID:   id,
		Err:  err,
	})
}

func sendAdminResult(
	ctx context.Context,
	reply chan AdminResult,
	result AdminResult,
) {
	if reply == nil {
		return
	}

	select {
	case <-ctx.Done():
		return

	case reply <- result:
		return
	}
}

// 保留你原来的函数名，避免其他地方已经调用 GetLocalEmbedding 时直接炸。
// 新代码里更建议用 ExtractBestEmbeddingFromCamera(ctx, cam)。
func GetLocalEmbedding(httpClient *http.Client) ([]float64, error) {
	service := NewFaceVerifyService(httpClient)

	return service.ExtractBestEmbeddingFromCamera(
		context.Background(),
		&local.Local{},
	)
}
