package lab

import (
	"context"
	"errors"
	"fmt"
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/data"
	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/service"
	"strings"
	"sync"
)

type AdminLoop struct {
	ctx        context.Context
	reqCh      <-chan service.AdminRequest
	addFaceSem chan int
	facedb     data.Facedb
	wg         *sync.WaitGroup
}

func NewAdminLoop(
	ctx context.Context,
	reqCh <-chan service.AdminRequest,
	addFaceSem chan int,
	facedb data.Facedb,
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

type Request struct {
}

func (r Request) NewAddFaceRequest(
	name string,
	cam camera.Camera,
	rec recognition.Recognition,
) service.AdminRequest {
	return service.AdminRequest{
		Name:   name,
		Action: "add",
		Cam:    cam,
		Rec:    rec,
		Reply:  make(chan service.AdminResult, 1),
	}
}

func (r Request) NewDeleteFaceRequest(name string) service.AdminRequest {
	return service.AdminRequest{
		Name:   name,
		Action: "delete",
		Reply:  make(chan service.AdminResult, 1),
	}
}

func (r Request) NewSearchFaceRequest(name string) service.AdminRequest {
	return service.AdminRequest{
		Name:   name,
		Action: "search",
		Reply:  make(chan service.AdminResult, 1),
	}
}

func handleAdminRequest(
	ctx context.Context,
	req service.AdminRequest,
	facedb data.Facedb,
	addFaceSem chan int,
) {
	if req.Name == "" {
		sendAdminResult(ctx, req.Reply, service.AdminResult{
			Name:   req.Name,
			Action: req.Action,
			Err:    errors.New("name cannot be empty"),
		})
		return
	}

	switch req.Action {
	case "add":
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
		err := facedb.DeleteFaceByName(ctx, req.Name)
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
		exists, err := facedb.FaceExistsByName(ctx, req.Name)
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
	facedb data.Facedb,
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
	facedb data.Facedb,
	rec recognition.Recognition,
) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	imageBytes, err := cam.Capture(ctx)
	if err != nil {
		return 0, fmt.Errorf("capture image: %w", err)
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	embedding, err := rec.GetFaceEmbedding(ctx, imageBytes, 0)
	if err != nil {
		return 0, fmt.Errorf("get embedding from recognition response: %w", err)
	}

	if len(embedding) == 0 {
		return 0, recognition.ErrNoFaceEmbedding
	}

	id, err := facedb.AddFace(ctx, name, embedding[0])
	if err != nil {
		if errors.Is(err, data.ErrAlreadyExists) {
			return 0, data.ErrAlreadyExists
		}

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
