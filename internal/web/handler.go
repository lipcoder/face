package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lipcoder/face/internal/app/camera"
	"lipcoder/face/internal/app/database"
	"lipcoder/face/internal/app/recognition"
	"lipcoder/face/internal/service"
)

const (
	maxFrameBytes  = 20 << 20
	frameFieldName = "frame"
	defaultTimeout = 30 * time.Second
	mjpegBoundary  = "face-frame"
)

//go:embed templates/*
var webFS embed.FS

type Handler struct {
	svc     *service.RecognitionService
	timeout time.Duration
}

func NewHandler(svc *service.RecognitionService, timeout time.Duration) *Handler {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Handler{svc: svc, timeout: timeout}
}

func NewRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	router.GET("/", handler.Index)
	router.GET("/action", handler.TemplatePage("action.html"))
	router.GET("/main", handler.TemplatePage("main.html"))
	router.GET("/manager", handler.TemplatePage("manager.html"))
	router.GET("/api/health", handler.Health)
	router.GET("/api/cameras", handler.ListCameras)
	router.GET("/api/cameras/:id/stream", handler.CameraStream)
	router.POST("/api/cameras/:id/head-state", handler.AnalyzeCameraHeadState)
	router.POST("/api/cameras/:id/recognize", handler.RecognizeCameraFaces)

	router.POST("/api/frame", handler.AnalyzeFrame)
	router.POST("/api/photo", handler.AnalyzeFrame) // compatibility alias
	router.POST("/api/head-state", handler.AnalyzeHeadState)
	router.POST("/api/faces/add", handler.AddFace)
	router.POST("/api/faces/delete", handler.DeleteFace)
	router.POST("/api/faces/search", handler.SearchFace)
	router.GET("/api/faces/list", handler.ListFaces)
	router.POST("/api/faces/recognize", handler.RecognizeFaces)
	router.POST("/api/recognize", handler.RecognizeFaces)
	return router
}

func (h *Handler) Index(c *gin.Context) {
	c.Redirect(http.StatusFound, "/main")
}

func (h *Handler) TemplatePage(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := webFS.ReadFile("templates/" + name)
		if err != nil {
			writeError(c, err)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":      h != nil && h.svc != nil,
		"cameras": h.svc.CameraIDs(),
	})
}

func (h *Handler) ListCameras(c *gin.Context) {
	ids := h.svc.CameraIDs()
	sort.Strings(ids)
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"cameras": ids,
		"default": h.svc.DefaultCamera(),
	})
}

// CameraStream exposes any configured camera subscription as MJPEG. The HTTP
// client gets the newest available frame and cannot backpressure capture.
func (h *Handler) CameraStream(c *gin.Context) {
	subscription, err := h.svc.Subscribe(c.Param("id"), 1)
	if err != nil {
		writeError(c, err)
		return
	}
	defer subscription.Close()

	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Connection", "close")
	c.Header("Content-Type", "multipart/x-mixed-replace; boundary="+mjpegBoundary)
	c.Status(http.StatusOK)
	flusher, canFlush := c.Writer.(http.Flusher)

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case frame, ok := <-subscription.Frames:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(
				c.Writer,
				"--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n",
				mjpegBoundary,
				len(frame.JPEG),
			); err != nil {
				return
			}
			if _, err := c.Writer.Write(frame.JPEG); err != nil {
				return
			}
			if _, err := c.Writer.Write([]byte("\r\n")); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

func (h *Handler) AnalyzeFrame(c *gin.Context) {
	frame, err := readFrame(c)
	if err != nil {
		writeError(c, err)
		return
	}
	ctx, cancel := h.requestContext(c)
	defer cancel()
	result, err := h.svc.AnalyzeFrame(ctx, frame)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "result": summarizeFaceResult(result)})
}

func (h *Handler) AnalyzeHeadState(c *gin.Context) {
	frame, err := readFrame(c)
	if err != nil {
		writeError(c, err)
		return
	}
	ctx, cancel := h.requestContext(c)
	defer cancel()
	result, err := h.svc.AnalyzeFramePose(ctx, frame)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": gin.H{"faces": headStateFaces(result)},
	})
}

func (h *Handler) AnalyzeCameraHeadState(c *gin.Context) {
	ctx, cancel := h.requestContext(c)
	defer cancel()
	frame, err := h.nextCameraFrame(ctx, c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := h.svc.AnalyzeFramePose(ctx, frame.JPEG)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": gin.H{"faces": headStateFaces(result)},
	})
}

type enrollmentRequest struct {
	Name     string `json:"name"`
	CameraID string `json:"camera_id"`
}

func (h *Handler) AddFace(c *gin.Context) {
	var request enrollmentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeError(c, fmt.Errorf("%w: invalid json body", database.ErrInvalidInput))
		return
	}
	ctx, cancel := h.requestContext(c)
	defer cancel()
	result, err := h.svc.Enroll(ctx, request.Name, request.CameraID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "result": result, "id": result.ID, "name": result.Name})
}

type nameRequest struct {
	Name string `json:"name"`
}

func (h *Handler) DeleteFace(c *gin.Context) {
	name, err := readName(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if err := h.svc.DeleteFace(name); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "name": name})
}

func (h *Handler) SearchFace(c *gin.Context) {
	name, err := readName(c)
	if err != nil {
		writeError(c, err)
		return
	}
	exists, err := h.svc.FaceExists(name)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "name": name, "exists": exists})
}

func (h *Handler) ListFaces(c *gin.Context) {
	names, err := h.svc.ListFaces()
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "names": names, "count": len(names)})
}

func (h *Handler) RecognizeFaces(c *gin.Context) {
	frame, err := readFrame(c)
	if err != nil {
		writeError(c, err)
		return
	}
	ctx, cancel := h.requestContext(c)
	defer cancel()
	faces, err := h.svc.RecognizeFrame(ctx, frame, true)
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeRecognizedFaces(c, faces)
}

func (h *Handler) RecognizeCameraFaces(c *gin.Context) {
	ctx, cancel := h.requestContext(c)
	defer cancel()
	frame, err := h.nextCameraFrame(ctx, c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	faces, err := h.svc.RecognizeFrame(ctx, frame.JPEG, false)
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeRecognizedFaces(c, faces)
}

func (h *Handler) writeRecognizedFaces(c *gin.Context, faces []service.RecognizedFace) {
	output := make([]gin.H, 0, len(faces))
	for _, face := range faces {
		item := gin.H{
			"box":     boxObject(face.Box),
			"name":    face.Name,
			"quality": face.Quality,
		}
		if face.Match != nil {
			item["match"] = face.Match
		}
		output = append(output, item)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "result": gin.H{"faces": output}})
}

func (h *Handler) nextCameraFrame(ctx context.Context, cameraID string) (camera.Frame, error) {
	subscription, err := h.svc.Subscribe(cameraID, 1)
	if err != nil {
		return camera.Frame{}, err
	}
	defer subscription.Close()
	select {
	case <-ctx.Done():
		return camera.Frame{}, ctx.Err()
	case frame, ok := <-subscription.Frames:
		if !ok {
			return camera.Frame{}, camera.ErrClosed
		}
		return frame, nil
	}
}

func (h *Handler) requestContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), h.timeout)
}

func readFrame(c *gin.Context) ([]byte, error) {
	if strings.HasPrefix(c.ContentType(), "multipart/form-data") {
		file, err := c.FormFile(frameFieldName)
		if err != nil {
			// Keep accepting the old field name during migration.
			file, err = c.FormFile("image")
		}
		if err != nil {
			return nil, fmt.Errorf("%w: multipart field %q is required", recognition.ErrInvalidImage, frameFieldName)
		}
		return readMultipartFile(file)
	}
	body := http.MaxBytesReader(c.Writer, c.Request.Body, maxFrameBytes)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil || len(data) == 0 {
		return nil, recognition.ErrInvalidImage
	}
	return data, nil
}

func readMultipartFile(file *multipart.FileHeader) ([]byte, error) {
	if file == nil || file.Size <= 0 || file.Size > maxFrameBytes {
		return nil, recognition.ErrInvalidImage
	}
	input, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open uploaded frame: %v", recognition.ErrInvalidImage, err)
	}
	defer input.Close()
	data, err := io.ReadAll(io.LimitReader(input, maxFrameBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxFrameBytes {
		return nil, recognition.ErrInvalidImage
	}
	return data, nil
}

func readName(c *gin.Context) (string, error) {
	var request nameRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		return "", fmt.Errorf("%w: invalid json body", database.ErrInvalidInput)
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		return "", fmt.Errorf("%w: name cannot be empty", database.ErrInvalidInput)
	}
	return request.Name, nil
}

type faceOutput struct {
	FaceCount    int64              `json:"face_count"`
	Box          []float64          `json:"box,omitempty"`
	Quality      float64            `json:"quality"`
	EmbeddingDim int                `json:"embedding_dim,omitempty"`
	Pose         []recognition.Pose `json:"pose,omitempty"`
}

func summarizeFaceResult(result *recognition.FaceResult) faceOutput {
	if result == nil {
		return faceOutput{}
	}
	output := faceOutput{
		FaceCount: result.FaceCount,
		Box:       result.Box,
		Quality:   result.Quality,
		Pose:      result.Pose,
	}
	if len(result.Embedding) > 0 {
		output.EmbeddingDim = len(result.Embedding[0])
	}
	return output
}

func headStateFaces(result *recognition.FaceResult) []gin.H {
	if result == nil || result.FaceCount <= 0 {
		return []gin.H{}
	}
	faces := make([]gin.H, 0, result.FaceCount)
	for index := 0; index < int(result.FaceCount); index++ {
		pose := poseAt(result.Pose, index)
		faces = append(faces, gin.H{
			"box":   boxObject(boxAt(result.Box, index)),
			"state": headState(pose),
			"pose":  pose,
		})
	}
	return faces
}

func boxAt(values []float64, index int) []float64 {
	offset := index * 4
	if offset < 0 || offset+3 >= len(values) {
		return []float64{0, 0, 0, 0}
	}
	return values[offset : offset+4]
}

func boxObject(box []float64) gin.H {
	if len(box) != 4 {
		box = []float64{0, 0, 0, 0}
	}
	return gin.H{"x": box[0], "y": box[1], "width": box[2], "height": box[3]}
}

func poseAt(values []recognition.Pose, index int) recognition.Pose {
	if index < 0 || index >= len(values) {
		return recognition.Pose{}
	}
	return values[index]
}

func headState(pose recognition.Pose) string {
	switch {
	case pose.Yaw >= 20:
		return "looking_left"
	case pose.Yaw <= -20:
		return "looking_right"
	case pose.Pitch >= 15:
		return "looking_up"
	case pose.Pitch <= -15:
		return "looking_down"
	case pose.Roll >= 15:
		return "tilted_right"
	case pose.Roll <= -15:
		return "tilted_left"
	default:
		return "facing"
	}
}

func writeError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, recognition.ErrInvalidConfig),
		errors.Is(err, recognition.ErrInvalidImage),
		errors.Is(err, recognition.ErrNotOneFace),
		errors.Is(err, database.ErrInvalidConfig),
		errors.Is(err, database.ErrInvalidInput),
		errors.Is(err, database.ErrInvalidEmbedding),
		errors.Is(err, camera.ErrInvalidConfig):
		status = http.StatusBadRequest
	case errors.Is(err, recognition.ErrNoFace),
		errors.Is(err, database.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, recognition.ErrLowFaceQuality):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, camera.ErrUnavailable),
		errors.Is(err, camera.ErrClosed):
		status = http.StatusServiceUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
	}
	c.JSON(status, gin.H{"ok": false, "error": err.Error()})
}
