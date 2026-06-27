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

	frames := make([][]byte, 0, enrollmentFrameCount)
	for i := 0; i < enrollmentFrameCount; i++ {
		imageBytes, err := cam.Capture()
		if err != nil {
			return 0, fmt.Errorf("capture enrollment frame: %w", err)
		}
		if len(imageBytes) == 0 {
			return 0, recognition.ErrInvalidImage
		}

		frames = append(frames, imageBytes)

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(40 * time.Millisecond):
		}
	}

	return EnrollFaceFromFrames(ctx, name, frames, facedb, rec)
}

func EnrollFaceFromFrames(
	ctx context.Context,
	name string,
	frames [][]byte,
	facedb record.FaceDB,
	rec recognition.FineIdentifier,
) (int64, error) {
	if facedb == nil {
		return 0, errors.New("facedb cannot be nil")
	}
	if rec == nil {
		return 0, errors.New("recognition cannot be nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("name cannot be empty")
	}
	if len(frames) != enrollmentFrameCount {
		return 0, fmt.Errorf("enrollment requires %d frames", enrollmentFrameCount)
	}

	embedding, err := rec.GetLivenessEmbedding(ctx, frames)
	if err != nil {
		return 0, fmt.Errorf("get liveness embedding from recognition response: %w", err)
	}
	if len(embedding) == 0 {
		return 0, recognition.ErrNoFaceEmbedding
	}

	id, err := facedb.AddFace(name, embedding)
	if err != nil {
		return 0, fmt.Errorf("add face to database: %w", err)
	}

	return id, nil
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
