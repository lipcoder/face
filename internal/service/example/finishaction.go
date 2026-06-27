package example

import (
	"context"
	"errors"
	"fmt"
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/record"
	"lipcoder/face/internal/service"
	"strings"
	"sync"
	"time"
)

const enrollmentFrameCount = 25

type AdminLoop struct {
	ctx        context.Context
	reqCh      <-chan service.AdminRequest
	addFaceSem chan int
	facedb     record.FaceDB
	wg         *sync.WaitGroup
}

func NewAdminLoop(
	ctx context.Context,
	reqCh <-chan service.AdminRequest,
	addFaceSem chan int,
	facedb record.FaceDB,
	wg *sync.WaitGroup,
) *AdminLoop {
	return &AdminLoop{
		ctx:        ctx,
		reqCh:      reqCh,
		addFaceSem: addFaceSem,
		facedb:     facedb,
		wg:         wg,
	}
}

func (l *AdminLoop) StartAdminLoop() error {
	if l == nil {
		return fmt.Errorf("admin loop is nil")
	}
	if l.ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if l.reqCh == nil {
		return fmt.Errorf("admin request channel is nil")
	}
	if l.addFaceSem == nil {
		return fmt.Errorf("add face semaphore channel is nil")
	}
	if l.wg == nil {
		return fmt.Errorf("wait group is nil")
	}

	for {
		select {
		case <-l.ctx.Done():
			return l.ctx.Err()

		case req, ok := <-l.reqCh:
			if !ok {
				return nil
			}
			req.Name = strings.TrimSpace(req.Name)

			l.wg.Add(1)
			go func(req service.AdminRequest) {
				defer l.wg.Done()
				handleAdminRequest(l.ctx, req, l.facedb, l.addFaceSem)
			}(req)
		}
	}
}

func handleAdminRequest(
	ctx context.Context,
	req service.AdminRequest,
	facedb record.FaceDB,
	addFaceSem chan int,
) {
	if facedb == nil {
		sendAdminResult(ctx, req.Reply, service.AdminResult{
			Name:   req.Name,
			Action: req.Action,
			Err:    errors.New("facedb cannot be nil"),
		})
		return
	}

	switch req.Action {
	case "list":
		names, err := facedb.ListFaceNames()
		if err != nil {
			sendAdminResult(ctx, req.Reply, service.AdminResult{
				Action: req.Action,
				Err:    err,
			})
			return
		}

		sendAdminResult(ctx, req.Reply, service.AdminResult{
			Action: req.Action,
			Names:  names,
			Err:    nil,
		})

	case "add":
		if req.Name == "" {
			sendAdminResult(ctx, req.Reply, service.AdminResult{
				Name:   req.Name,
				Action: req.Action,
				Err:    errors.New("name cannot be empty"),
			})
			return
		}

		if req.Cam == nil {
			sendAdminResult(ctx, req.Reply, service.AdminResult{
				Name:   req.Name,
				Action: req.Action,
				Err:    errors.New("camera cannot be nil"),
			})
			return
		}

		if req.Rec == nil {
			sendAdminResult(ctx, req.Reply, service.AdminResult{
				Name:   req.Name,
				Action: req.Action,
				Err:    errors.New("recognition cannot be nil"),
			})
			return
		}

		handleAddFaceRequest(ctx, req, facedb, addFaceSem)

	case "delete":
		if req.Name == "" {
			sendAdminResult(ctx, req.Reply, service.AdminResult{
				Name:   req.Name,
				Action: req.Action,
				Err:    errors.New("name cannot be empty"),
			})
			return
		}

		err := facedb.DeleteFaceByName(req.Name)
		if err != nil {
			sendAdminResult(ctx, req.Reply, service.AdminResult{
				Name:   req.Name,
				Action: req.Action,
				Err:    err,
			})
			return
		}

		sendAdminResult(ctx, req.Reply, service.AdminResult{
			Name:   req.Name,
			Action: req.Action,
			Exists: true,
			Err:    nil,
		})

	case "search":
		if req.Name == "" {
			sendAdminResult(ctx, req.Reply, service.AdminResult{
				Name:   req.Name,
				Action: req.Action,
				Err:    errors.New("name cannot be empty"),
			})
			return
		}

		exists, err := facedb.FaceExistsByName(req.Name)
		if err != nil {
			sendAdminResult(ctx, req.Reply, service.AdminResult{
				Name:   req.Name,
				Action: req.Action,
				Exists: false,
				Err:    err,
			})
			return
		}

		sendAdminResult(ctx, req.Reply, service.AdminResult{
			Name:   req.Name,
			Action: req.Action,
			Exists: exists,
			Err:    nil,
		})

	default:
		sendAdminResult(ctx, req.Reply, service.AdminResult{
			Name:   req.Name,
			Action: req.Action,
			Err:    fmt.Errorf("unknown admin action: %s", req.Action),
		})
	}
}

// 管理 addFace 的并发数量
func handleAddFaceRequest(
	ctx context.Context,
	req service.AdminRequest,
	facedb record.FaceDB,
	addFaceSem chan int,
) {
	select {
	case <-ctx.Done():
		sendAdminResult(ctx, req.Reply, service.AdminResult{
			Name:   req.Name,
			Action: req.Action,
			Err:    ctx.Err(),
		})
		return

	case addFaceSem <- 1:
		defer func() {
			<-addFaceSem
		}()
	}

	_, err := addFaceFromCamera(ctx, req.Name, req.Cam, facedb, req.Rec)
	sendAdminResult(ctx, req.Reply, service.AdminResult{
		Name:   req.Name,
		Action: req.Action,
		Err:    err,
	})
}

func addFaceFromCamera(
	ctx context.Context,
	name string,
	cam camera.Camera,
	facedb record.FaceDB,
	rec recognition.Analyzer,
) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	frames, err := captureFrameSet(ctx, cam, enrollmentFrameCount)
	if err != nil {
		return 0, err
	}

	result, err := rec.AnalyzeFrameSet(ctx, frames)
	if err != nil {
		return 0, fmt.Errorf("analyze 25 face frames: %w", err)
	}

	if len(result.Embedding) != 1 || len(result.Embedding[0]) == 0 {
		return 0, recognition.ErrNoFaceEmbedding
	}

	id, err := facedb.AddFace(name, result.Embedding[0])
	if err != nil {
		return 0, fmt.Errorf("add face to database: %w", err)
	}

	return id, nil
}

func captureFrameSet(ctx context.Context, cam camera.Camera, count int) ([][]byte, error) {
	if count <= 0 {
		return nil, recognition.ErrInvalidFrameCount
	}

	frames := make([][]byte, 0, count)
	for len(frames) < count {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		imageBytes, err := cam.Capture()
		if err != nil {
			return nil, fmt.Errorf("capture frame %d: %w", len(frames)+1, err)
		}
		frames = append(frames, imageBytes)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}

	return frames, nil
}

func sendAdminResult(
	ctx context.Context,
	reply chan service.AdminResult,
	result service.AdminResult,
) {
	if reply == nil {
		return
	}

	select {
	case reply <- result:
		return
	default:
	}

	select {
	case reply <- result:
		return
	case <-ctx.Done():
		return
	}
}
