package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/record"
	"lipcoder/face/internal/service"
)

const (
	maxUploadBytes   = 20 << 20
	frameFieldName   = "frames"
	imageFieldName   = "image"
	defaultTimeout   = 10 * time.Second
	defaultThreshold = 0.45
)

//go:embed templates/*
var webFS embed.FS

type Handler struct {
	svc       *service.RecognitionService
	facedb    record.FaceDB
	timeout   time.Duration
	threshold float64
}

func NewHandler(
	svc *service.RecognitionService,
	facedb record.FaceDB,
	timeout time.Duration,
	threshold float64,
) *Handler {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if threshold <= 0 || threshold > 1 {
		threshold = defaultThreshold
	}
	return &Handler{
		svc:       svc,
		facedb:    facedb,
		timeout:   timeout,
		threshold: threshold,
	}
}

func NewRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/", handler.Index)
	r.GET("/action", handler.TemplatePage("action.html"))
	r.GET("/emotion", handler.TemplatePage("emotion.html"))
	r.GET("/main", handler.TemplatePage("main.html"))
	r.GET("/manager", handler.TemplatePage("manager.html"))
	r.GET("/api/health", handler.Health)

	r.POST("/api/photo", handler.AnalyzePhoto)
	r.POST("/api/photo/emotion", handler.AnalyzePhotoEmotion)
	r.POST("/api/emotion", handler.AnalyzeEmotionFaces)
	r.POST("/api/head-state", handler.AnalyzeHeadState)
	r.POST("/api/frames", handler.AnalyzeFrameSet)
	r.POST("/api/faces/add", handler.AddFace)
	r.POST("/api/faces/delete", handler.DeleteFace)
	r.POST("/api/faces/search", handler.SearchFace)
	r.GET("/api/faces/list", handler.ListFaces)
	r.POST("/api/faces/recognize", handler.RecognizeFace)
	r.POST("/api/recognize", handler.RecognizeFaces)

	return r
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
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) AnalyzePhoto(c *gin.Context) {
	imgBytes, err := readSingleImage(c, imageFieldName)
	if err != nil {
		writeError(c, err)
		return
	}

	ctx, cancel := h.requestContext(c)
	defer cancel()

	result, err := h.svc.AnalyzePhoto(ctx, imgBytes)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "result": summarizeFaceResult(result)})
}

func (h *Handler) AnalyzePhotoEmotion(c *gin.Context) {
	imgBytes, err := readSingleImage(c, imageFieldName)
	if err != nil {
		writeError(c, err)
		return
	}

	ctx, cancel := h.requestContext(c)
	defer cancel()

	result, err := h.svc.AnalyzePhotoEmotion(ctx, imgBytes)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "result": summarizeEmotionResult(result)})
}

func (h *Handler) AnalyzeEmotionFaces(c *gin.Context) {
	imgBytes, err := readSingleImage(c, imageFieldName)
	if err != nil {
		writeError(c, err)
		return
	}

	ctx, cancel := h.requestContext(c)
	defer cancel()

	result, err := h.svc.AnalyzePhotoEmotion(ctx, imgBytes)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"result": gin.H{
			"faces": h.emotionFaces(result),
		},
	})
}

func (h *Handler) AnalyzeHeadState(c *gin.Context) {
	imgBytes, err := readSingleImage(c, imageFieldName)
	if err != nil {
		writeError(c, err)
		return
	}

	ctx, cancel := h.requestContext(c)
	defer cancel()

	result, err := h.svc.AnalyzePhotoPose(ctx, imgBytes)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"result": gin.H{
			"faces": headStateFaces(result),
		},
	})
}

func (h *Handler) AnalyzeFrameSet(c *gin.Context) {
	frames, err := readFrameSet(c, frameFieldName)
	if err != nil {
		writeError(c, err)
		return
	}

	ctx, cancel := h.requestContext(c)
	defer cancel()

	result, err := h.svc.AnalyzeFrameSet(ctx, frames)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "result": summarizeFaceResult(result)})
}

func (h *Handler) AddFace(c *gin.Context) {
	if h.facedb == nil {
		writeError(c, record.ErrInvalidState)
		return
	}

	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		writeError(c, fmt.Errorf("%w: name cannot be empty", record.ErrInvalidInput))
		return
	}

	frames, err := readFrameSet(c, frameFieldName)
	if err != nil {
		writeError(c, err)
		return
	}

	ctx, cancel := h.requestContext(c)
	defer cancel()

	result, err := h.svc.AnalyzeFrameSet(ctx, frames)
	if err != nil {
		writeError(c, err)
		return
	}
	embedding, err := oneEmbedding(result)
	if err != nil {
		writeError(c, err)
		return
	}

	id, err := h.facedb.AddFace(name, embedding)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"id":   id,
		"name": name,
		"result": gin.H{
			"id":            id,
			"name":          name,
			"quality":       result.Quality,
			"box":           result.Box,
			"embedding_dim": len(embedding),
		},
	})
}

func (h *Handler) DeleteFace(c *gin.Context) {
	if h.facedb == nil {
		writeError(c, record.ErrInvalidState)
		return
	}

	name, err := readNameJSON(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if err := h.facedb.DeleteFaceByName(name); err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "name": name})
}

func (h *Handler) SearchFace(c *gin.Context) {
	if h.facedb == nil {
		writeError(c, record.ErrInvalidState)
		return
	}

	name, err := readNameJSON(c)
	if err != nil {
		writeError(c, err)
		return
	}
	exists, err := h.facedb.FaceExistsByName(name)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "name": name, "exists": exists})
}

func (h *Handler) ListFaces(c *gin.Context) {
	if h.facedb == nil {
		writeError(c, record.ErrInvalidState)
		return
	}

	names, err := h.facedb.ListFaceNames()
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "names": names, "count": len(names)})
}

func (h *Handler) RecognizeFace(c *gin.Context) {
	if h.facedb == nil {
		writeError(c, record.ErrInvalidState)
		return
	}

	imgBytes, err := readSingleImage(c, imageFieldName)
	if err != nil {
		writeError(c, err)
		return
	}

	threshold := h.threshold
	if raw := strings.TrimSpace(c.PostForm("threshold")); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			writeError(c, fmt.Errorf("%w: invalid threshold", record.ErrInvalidInput))
			return
		}
		threshold = value
	}

	ctx, cancel := h.requestContext(c)
	defer cancel()

	result, err := h.svc.AnalyzePhoto(ctx, imgBytes)
	if err != nil {
		writeError(c, err)
		return
	}
	embedding, err := firstEmbedding(result)
	if err != nil {
		writeError(c, err)
		return
	}
	match, err := h.facedb.SearchFaceByEmbedding(embedding, threshold)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"result": gin.H{
			"match":      match,
			"face_count": result.FaceCount,
			"box":        result.Box,
			"quality":    result.Quality,
		},
	})
}

func (h *Handler) RecognizeFaces(c *gin.Context) {
	if h.facedb == nil {
		writeError(c, record.ErrInvalidState)
		return
	}

	imgBytes, err := readSingleImage(c, imageFieldName)
	if err != nil {
		writeError(c, err)
		return
	}

	ctx, cancel := h.requestContext(c)
	defer cancel()

	result, err := h.svc.AnalyzePhoto(ctx, imgBytes)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"result": gin.H{
			"faces": h.recognizedFaces(result),
		},
	})
}

func (h *Handler) requestContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), h.timeout)
}

func readSingleImage(c *gin.Context, field string) ([]byte, error) {
	if c.ContentType() == "multipart/form-data" {
		file, err := c.FormFile(field)
		if err != nil {
			return nil, fmt.Errorf("%w: multipart field %q is required", recognition.ErrInvalidImage, field)
		}
		return readMultipartFile(file)
	}

	body := http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("%w: read request body: %w", recognition.ErrInvalidImage, err)
	}
	if len(data) == 0 {
		return nil, recognition.ErrInvalidImage
	}
	return data, nil
}

func readFrameSet(c *gin.Context, field string) ([][]byte, error) {
	if err := c.Request.ParseMultipartForm(maxUploadBytes * 25); err != nil {
		return nil, fmt.Errorf("%w: parse multipart form: %w", recognition.ErrInvalidImage, err)
	}
	if c.Request.MultipartForm == nil || c.Request.MultipartForm.File == nil {
		return nil, recognition.ErrInvalidImage
	}
	files := c.Request.MultipartForm.File[field]
	if len(files) != 25 {
		return nil, recognition.ErrInvalidFrameCount
	}

	frames := make([][]byte, 0, len(files))
	for _, file := range files {
		data, err := readMultipartFile(file)
		if err != nil {
			return nil, err
		}
		frames = append(frames, data)
	}
	return frames, nil
}

func readMultipartFile(file *multipart.FileHeader) ([]byte, error) {
	if file == nil || file.Size <= 0 {
		return nil, recognition.ErrInvalidImage
	}
	if file.Size > maxUploadBytes {
		return nil, fmt.Errorf("%w: file too large", recognition.ErrInvalidImage)
	}

	f, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open uploaded file: %w", recognition.ErrInvalidImage, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read uploaded file: %w", recognition.ErrInvalidImage, err)
	}
	if len(data) == 0 || len(data) > maxUploadBytes {
		return nil, recognition.ErrInvalidImage
	}
	return data, nil
}

type nameRequest struct {
	Name string `json:"name"`
}

func readNameJSON(c *gin.Context) (string, error) {
	var req nameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return "", fmt.Errorf("%w: invalid json body", record.ErrInvalidInput)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return "", fmt.Errorf("%w: name cannot be empty", record.ErrInvalidInput)
	}
	return name, nil
}

type faceOutput struct {
	FaceCount    int64              `json:"face_count"`
	Box          []float64          `json:"box,omitempty"`
	Quality      float64            `json:"quality"`
	EmbeddingDim int                `json:"embedding_dim,omitempty"`
	Pose         []recognition.Pose `json:"pose,omitempty"`
}

type emotionOutput struct {
	FaceCount    int64                 `json:"face_count"`
	Box          []float64             `json:"box,omitempty"`
	Quality      float64               `json:"quality"`
	EmbeddingDim int                   `json:"embedding_dim,omitempty"`
	Emotion      []recognition.Emotion `json:"emotion,omitempty"`
	Pose         []recognition.Pose    `json:"pose,omitempty"`
}

func (h *Handler) emotionFaces(result *recognition.EmotionResult) []gin.H {
	if result == nil || result.FaceCount <= 0 {
		return []gin.H{}
	}

	count := int(result.FaceCount)
	faces := make([]gin.H, 0, count)
	for i := 0; i < count; i++ {
		face := gin.H{
			"box":     boxAt(result.Box, i),
			"name":    "未知",
			"emotion": emotionAt(result.Emotion, i),
		}
		if match, ok := h.matchEmbedding(embeddingAt(result.Embedding, i)); ok {
			face["name"] = match.Name
			face["match"] = match
		}
		faces = append(faces, face)
	}
	return faces
}

func (h *Handler) recognizedFaces(result *recognition.FaceResult) []gin.H {
	if result == nil || result.FaceCount <= 0 {
		return []gin.H{}
	}

	count := int(result.FaceCount)
	faces := make([]gin.H, 0, count)
	for i := 0; i < count; i++ {
		face := gin.H{
			"box":  boxAt(result.Box, i),
			"name": "未知",
		}
		if match, ok := h.matchEmbedding(embeddingAt(result.Embedding, i)); ok {
			face["name"] = match.Name
			face["match"] = match
		}
		faces = append(faces, face)
	}
	return faces
}

func (h *Handler) matchEmbedding(embedding []float64) (record.FaceMatch, bool) {
	if h == nil || h.facedb == nil || len(embedding) == 0 {
		return record.FaceMatch{}, false
	}
	match, err := h.facedb.SearchFaceByEmbedding(embedding, h.threshold)
	if err != nil {
		return record.FaceMatch{}, false
	}
	return match, true
}

func headStateFaces(result *recognition.FaceResult) []gin.H {
	if result == nil || result.FaceCount <= 0 {
		return []gin.H{}
	}

	count := int(result.FaceCount)
	faces := make([]gin.H, 0, count)
	for i := 0; i < count; i++ {
		faces = append(faces, gin.H{
			"box":   boxAt(result.Box, i),
			"state": headState(poseAt(result.Pose, i)),
			"pose":  poseAt(result.Pose, i),
		})
	}
	return faces
}

func boxAt(values []float64, index int) gin.H {
	offset := index * 4
	if offset+3 >= len(values) {
		return gin.H{"x": 0, "y": 0, "width": 0, "height": 0}
	}
	return gin.H{
		"x":      values[offset],
		"y":      values[offset+1],
		"width":  values[offset+2],
		"height": values[offset+3],
	}
}

func embeddingAt(values [][]float64, index int) []float64 {
	if index < 0 || index >= len(values) {
		return nil
	}
	return values[index]
}

func emotionAt(values []recognition.Emotion, index int) recognition.Emotion {
	if index < 0 || index >= len(values) {
		return recognition.Emotion{ID: -1, Label: "unknown"}
	}
	return values[index]
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

func summarizeFaceResult(result *recognition.FaceResult) faceOutput {
	if result == nil {
		return faceOutput{}
	}
	out := faceOutput{
		FaceCount: result.FaceCount,
		Box:       result.Box,
		Quality:   result.Quality,
		Pose:      result.Pose,
	}
	if len(result.Embedding) > 0 {
		out.EmbeddingDim = len(result.Embedding[0])
	}
	return out
}

func summarizeEmotionResult(result *recognition.EmotionResult) emotionOutput {
	if result == nil {
		return emotionOutput{}
	}
	out := emotionOutput{
		FaceCount: result.FaceCount,
		Box:       result.Box,
		Quality:   result.Quality,
		Emotion:   result.Emotion,
		Pose:      result.Pose,
	}
	if len(result.Embedding) > 0 {
		out.EmbeddingDim = len(result.Embedding[0])
	}
	return out
}

func oneEmbedding(result *recognition.FaceResult) ([]float64, error) {
	if result == nil || result.FaceCount == 0 {
		return nil, recognition.ErrNoFace
	}
	if result.FaceCount != 1 || len(result.Embedding) != 1 {
		return nil, recognition.ErrNotOneFace
	}
	if len(result.Embedding[0]) == 0 {
		return nil, recognition.ErrNoFaceEmbedding
	}
	return result.Embedding[0], nil
}

func firstEmbedding(result *recognition.FaceResult) ([]float64, error) {
	if result == nil || result.FaceCount == 0 {
		return nil, recognition.ErrNoFace
	}
	for _, embedding := range result.Embedding {
		if len(embedding) > 0 {
			return embedding, nil
		}
	}
	return nil, recognition.ErrNoFaceEmbedding
}

func writeError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, recognition.ErrInvalidConfig),
		errors.Is(err, recognition.ErrInvalidImage),
		errors.Is(err, recognition.ErrInvalidFrameCount),
		errors.Is(err, recognition.ErrNotOneFace),
		errors.Is(err, record.ErrInvalidConfig),
		errors.Is(err, record.ErrInvalidInput),
		errors.Is(err, record.ErrInvalidEmbedding):
		status = http.StatusBadRequest
	case errors.Is(err, recognition.ErrNoFace),
		errors.Is(err, record.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
	}

	c.JSON(status, gin.H{
		"ok":    false,
		"error": err.Error(),
	})
}
