package simple

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/service"
)

type FaceHandler struct {
	action  service.Action
	reqCh   chan<- service.AdminRequest
	cam     camera.Camera
	rec     recognition.Recognition
	timeout time.Duration
}

func NewFaceHandler(
	action service.Action,
	reqCh chan<- service.AdminRequest,
	cam camera.Camera,
	rec recognition.Recognition,
	timeout time.Duration,
) *FaceHandler {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &FaceHandler{
		action:  action,
		reqCh:   reqCh,
		cam:     cam,
		rec:     rec,
		timeout: timeout,
	}
}

func (h *FaceHandler) AddFace(c *gin.Context) {
	name, ok := getFaceName(c)
	if !ok {
		return
	}

	req := h.action.AddFace(name, h.cam, h.rec)

	result, err := h.sendAdminRequest(c.Request.Context(), req)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"action": result.Action,
		"name":   result.Name,
	})
}

func (h *FaceHandler) DeleteFace(c *gin.Context) {
	name, ok := getFaceName(c)
	if !ok {
		return
	}

	req := h.action.DeleteFace(name)

	result, err := h.sendAdminRequest(c.Request.Context(), req)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"action": result.Action,
		"name":   result.Name,
	})
}

func (h *FaceHandler) SearchFace(c *gin.Context) {
	name, ok := getFaceName(c)
	if !ok {
		return
	}

	req := h.action.SearchFace(name)

	result, err := h.sendAdminRequest(c.Request.Context(), req)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"action": result.Action,
		"name":   result.Name,
		"exists": result.Exists,
	})
}

func (h *FaceHandler) sendAdminRequest(
	parent context.Context,
	req service.AdminRequest,
) (service.AdminResult, error) {
	ctx, cancel := context.WithTimeout(parent, h.timeout)
	defer cancel()

	select {
	case h.reqCh <- req:
	case <-ctx.Done():
		return service.AdminResult{}, ctx.Err()
	}

	select {
	case result := <-req.Reply:
		if result.Err != nil {
			return service.AdminResult{}, result.Err
		}
		return result, nil

	case <-ctx.Done():
		return service.AdminResult{}, ctx.Err()
	}
}

type faceNameRequest struct {
	Name string `json:"name"`
}

func getFaceName(c *gin.Context) (string, bool) {
	var req faceNameRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid json body",
		})
		return "", false
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "name is required",
		})
		return "", false
	}

	return name, true
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		c.JSON(http.StatusGatewayTimeout, gin.H{
			"error": "request timeout",
		})

	case errors.Is(err, context.Canceled):
		c.JSON(http.StatusRequestTimeout, gin.H{
			"error": "request canceled",
		})

	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
	}
}

func NewRouter(faceHandler *FaceHandler) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"ok": true,
		})
	})

	faces := r.Group("/api/faces")
	{
		faces.POST("/add", faceHandler.AddFace)
		faces.POST("/delete", faceHandler.DeleteFace)
		faces.POST("/search", faceHandler.SearchFace)
	}

	return r
}