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
	action string
	name   string
	exists bool
	err    error
}

func StartAdminLoop(
	ctx context.Context,
	reqCh <-chan AdminRequest,
	addFaceSem chan struct{},
	facedb data.Facedb,
	wg *sync.WaitGroup,
) error {
	for {
		if wg == nil {
			return errors.New("wait group cannot be nil")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req, ok := <-reqCh:
			if !ok {
				return nil
			}
			req.name = strings.TrimSpace(req.name)
			if req.name == "" {
				sendAdminResult(ctx, req.Reply, AdminResult{
					name:   req.name,
					action: req.action,
					exists: false,
					err:    errors.New("no name"),
				})
				continue
			}
			if req.rec == nil {
				sendAdminResult(ctx, req.Reply, AdminResult{
					name:   req.name,
					action: req.action,
					exists: false,
					err:    errors.New("Recognition cannot be nil"),
				})
				continue
			}

			switch req.action {
			case "add":
				if req.cam == nil {
					sendAdminResult(ctx, req.Reply, AdminResult{
						name:   req.name,
						action: req.action,
						exists: false,
						err:    errors.New("camera cannot be nil"),
					})
					continue
				}
				go handleAddFaceRequest(ctx, req, facedb, addFaceSem)

			case "delete":
				if err := facedb.DeleteFaceByName(ctx, req.name); err != nil {
					sendAdminResult(ctx, req.Reply, AdminResult{
						name:   req.name,
						action: req.action,
						exists: false,
						err:    err,
					})
					continue
				}
				sendAdminResult(ctx, req.Reply, AdminResult{
					name:   req.name,
					action: req.action,
					exists: true,
					err:    nil,
				})

			case "search":
				exists, err := facedb.FaceExistsByName(ctx, req.name)
				if err != nil {
					sendAdminResult(ctx, req.Reply, AdminResult{
						name:   req.name,
						action: req.action,
						exists: exists,
						err:    err,
					})
					continue
				}
				sendAdminResult(ctx, req.Reply, AdminResult{
					name:   req.name,
					action: req.action,
					exists: exists,
					err:    nil,
				})
			default:
				sendAdminResult(ctx, req.Reply, AdminResult{
					name:   req.name,
					action: req.action,
					err:    fmt.Errorf("unknown admin action: %s", req.action),
				})
				continue
			}
		}
	}
}

// 管理addFace的并发数量
func handleAddFaceRequest(
	ctx context.Context,
	req AdminRequest,
	facedb data.Facedb,
	addFaceSem chan struct{},
) {
	select {
	case <-ctx.Done():
		sendAdminResult(ctx, req.Reply, AdminResult{
			name:   req.name,
			action: req.action,
			err:    ctx.Err(),
		})
		return
	case addFaceSem <- struct{}{}:
		defer func() {
			<-addFaceSem
		}()
	}
	_, err := addFaceFromCamera(ctx, req.name, req.cam, facedb, req.rec)
	sendAdminResult(ctx, req.Reply, AdminResult{
		name:   req.name,
		action: req.action,
		err:    err,
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
		return 0, fmt.Errorf("mainAction get image failed,%w", err)
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	embedding, err := rec.GetFaceEmbedding(imageBytes, 0)
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

// 返回请求
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
