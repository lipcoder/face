package service

import (
	"context"
	"errors"
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/recognition"
	"strings"
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
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req, ok := <-reqCh:
			if !ok {
				return nil
			}
			req.name = strings.TrimSpace(req.name)
			if req.name == "" {
				return errors.New("name cannot be empty")
			}
			if req.cam == nil {
				return errors.New("camera cannot be nil")
			}

			switch req.action {
			case "add":
				go handleAddFaceRequest(ctx, req, addFaceSem)

			case "delete":
				exists, err := DeleteFace(ctx, req.name)
				sendAdminResult(ctx, req.Reply, AdminResult{
					name:   req.name,
					action: req.action,
					exists: exists,
					err:    err,
				})

			case "search":
				exists, err := QueryFace(ctx, req.name)
				sendAdminResult(ctx, req.Reply, AdminResult{
					name:   req.name,
					action: req.action,
					exists: exists,
					err:    err,
				})
			}
		}
	}
}

// 管理addFace的并发数量
func handleAddFaceRequest(
	ctx context.Context,
	req AdminRequest,
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
	_, err := AddFaceFromCamera(ctx, req.name, req.cam, req.rec)
	sendAdminResult(ctx, req.Reply, AdminResult{
		name:   req.name,
		action: req.action,
		err:    err,
	})
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
