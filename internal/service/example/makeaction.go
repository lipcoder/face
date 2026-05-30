package example

import (
	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/service"
)

type ActionRequest struct {
}

func (a ActionRequest) AddFace(
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

func (a ActionRequest) DeleteFace(name string) service.AdminRequest {
	return service.AdminRequest{
		Name:   name,
		Action: "delete",
		Reply:  make(chan service.AdminResult, 1),
	}
}

func (a ActionRequest) SearchFace(name string) service.AdminRequest {
	return service.AdminRequest{
		Name:   name,
		Action: "search",
		Reply:  make(chan service.AdminResult, 1),
	}
}
