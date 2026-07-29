package service

import (
	"context"
	"fmt"
	"sync"
)

type AdminAction string

const (
	AdminAdd    AdminAction = "add"
	AdminDelete AdminAction = "delete"
	AdminSearch AdminAction = "search"
	AdminList   AdminAction = "list"
)

type AdminRequest struct {
	Context  context.Context
	Action   AdminAction
	Name     string
	CameraID string
	Reply    chan<- AdminResult
}

type AdminResult struct {
	Action     AdminAction
	Name       string
	Names      []string
	Exists     bool
	Enrollment *EnrollmentResult
	Err        error
}

// RunAdminLoop uses a fixed worker pool. requestQueue should be bounded by the
// caller, while every request owns a buffered one-shot reply channel.
func (s *RecognitionService) RunAdminLoop(
	ctx context.Context,
	requestQueue <-chan AdminRequest,
	workers int,
) error {
	if s == nil || ctx == nil || requestQueue == nil || workers <= 0 {
		return fmt.Errorf("invalid admin loop configuration")
	}
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case request, ok := <-requestQueue:
					if !ok {
						return
					}
					s.handleAdminRequest(ctx, request)
				}
			}
		}()
	}
	wg.Wait()
	return ctx.Err()
}

func (s *RecognitionService) handleAdminRequest(parent context.Context, request AdminRequest) {
	requestCtx := request.Context
	if requestCtx == nil {
		requestCtx = parent
	}
	result := AdminResult{Action: request.Action, Name: request.Name}
	switch request.Action {
	case AdminAdd:
		enrollment, err := s.Enroll(requestCtx, request.Name, request.CameraID)
		result.Err = err
		if err == nil {
			result.Name = enrollment.Name
			result.Enrollment = &enrollment
		}
	case AdminDelete:
		result.Err = s.DeleteFace(request.Name)
	case AdminSearch:
		result.Exists, result.Err = s.FaceExists(request.Name)
	case AdminList:
		result.Names, result.Err = s.ListFaces()
	default:
		result.Err = fmt.Errorf("unknown admin action %q", request.Action)
	}
	sendAdminResult(parent, requestCtx, request.Reply, result)
}

func sendAdminResult(
	parent context.Context,
	requestCtx context.Context,
	reply chan<- AdminResult,
	result AdminResult,
) {
	if reply == nil {
		return
	}
	select {
	case reply <- result:
	case <-requestCtx.Done():
	case <-parent.Done():
	}
}

func SubmitAdmin(
	ctx context.Context,
	requestQueue chan<- AdminRequest,
	action AdminAction,
	name string,
	cameraID string,
) (<-chan AdminResult, error) {
	if ctx == nil || requestQueue == nil {
		return nil, fmt.Errorf("invalid admin request")
	}
	reply := make(chan AdminResult, 1)
	request := AdminRequest{
		Context:  ctx,
		Action:   action,
		Name:     name,
		CameraID: cameraID,
		Reply:    reply,
	}
	select {
	case requestQueue <- request:
		return reply, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
