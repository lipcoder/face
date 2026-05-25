package service

import (
	"context"
	"errors"
	"fmt"
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/data"
	"lipcoder/face/internal/recognition"
	"strings"
	"sync"
)

type AdminRequest struct {
	name   string
	action string
	cam    camera.Camera
	rec    recognition.Recognition
	Reply  chan AdminResult
}

type AdminResult struct {
	Action string
	Name   string
	Exists bool
	Err    error
}

func NewAddFaceRequest(
	name string,
	cam camera.Camera,
	rec recognition.Recognition,
) AdminRequest {
	return AdminRequest{
		name:   name,
		action: "add",
		cam:    cam,
		rec:    rec,
		Reply:  make(chan AdminResult, 1),
	}
}

func NewDeleteFaceRequest(name string) AdminRequest {
	return AdminRequest{
		name:   name,
		action: "delete",
		Reply:  make(chan AdminResult, 1),
	}
}

func NewSearchFaceRequest(name string) AdminRequest {
	return AdminRequest{
		name:   name,
		action: "search",
		Reply:  make(chan AdminResult, 1),
	}
}

func StartAdminLoop(
	ctx context.Context,
	reqCh <-chan AdminRequest,
	addFaceSem chan int,
	facedb data.Facedb,
	wg *sync.WaitGroup,
) error {
	if reqCh == nil {
		return errors.New("reqCh cannot be nil")
	}
	if addFaceSem == nil {
		return errors.New("addFaceSem cannot be nil")
	}
	if cap(addFaceSem) == 0 {
		return errors.New("addFaceSem must be buffered")
	}
	if facedb == nil {
		return errors.New("facedb cannot be nil")
	}
	if wg == nil {
		return errors.New("wait group cannot be nil")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case req, ok := <-reqCh:
			if !ok {
				return nil
			}
			req.name = strings.TrimSpace(req.name)

			wg.Add(1)
			go func(req AdminRequest) {
				defer wg.Done()
				handleAdminRequest(ctx, req, facedb, addFaceSem)
			}(req)
		}
	}
}

func handleAdminRequest(
	ctx context.Context,
	req AdminRequest,
	facedb data.Facedb,
	addFaceSem chan int,
) {
	if req.name == "" {
		sendAdminResult(ctx, req.Reply, AdminResult{
			Name:   req.name,
			Action: req.action,
			Err:    errors.New("name cannot be empty"),
		})
		return
	}
	switch req.action {
	case "add":
		if req.cam == nil {
			sendAdminResult(ctx, req.Reply, AdminResult{
				Name:   req.name,
				Action: req.action,
				Err:    errors.New("camera cannot be nil"),
			})
			return
		}

		if req.rec == nil {
			sendAdminResult(ctx, req.Reply, AdminResult{
				Name:   req.name,
				Action: req.action,
				Err:    errors.New("recognition cannot be nil"),
			})
			return
		}
		handleAddFaceRequest(ctx, req, facedb, addFaceSem)
	case "delete":
		err := facedb.DeleteFaceByName(ctx, req.name)
		if err != nil {
			sendAdminResult(ctx, req.Reply, AdminResult{
				Name:   req.name,
				Action: req.action,
				Err:    err,
			})
			return
		}

		sendAdminResult(ctx, req.Reply, AdminResult{
			Name:   req.name,
			Action: req.action,
			Exists: true,
			Err:    nil,
		})

	case "search":
		exists, err := facedb.FaceExistsByName(ctx, req.name)
		if err != nil {
			sendAdminResult(ctx, req.Reply, AdminResult{
				Name:   req.name,
				Action: req.action,
				Exists: false,
				Err:    err,
			})
			return
		}

		sendAdminResult(ctx, req.Reply, AdminResult{
			Name:   req.name,
			Action: req.action,
			Exists: exists,
			Err:    nil,
		})

	default:
		sendAdminResult(ctx, req.Reply, AdminResult{
			Name:   req.name,
			Action: req.action,
			Err:    fmt.Errorf("unknown admin action: %s", req.action),
		})
	}
}

// 管理addFace的并发数量
func handleAddFaceRequest(
	ctx context.Context,
	req AdminRequest,
	facedb data.Facedb,
	addFaceSem chan int,
) {
	select {
	case <-ctx.Done():
		sendAdminResult(ctx, req.Reply, AdminResult{
			Name:   req.name,
			Action: req.action,
			Err:    ctx.Err(),
		})
		return
	case addFaceSem <- 1:
		defer func() {
			<-addFaceSem
		}()
	}
	_, err := addFaceFromCamera(ctx, req.name, req.cam, facedb, req.rec)
	sendAdminResult(ctx, req.Reply, AdminResult{
		Name:   req.name,
		Action: req.action,
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
	reply chan AdminResult,
	result AdminResult,
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
