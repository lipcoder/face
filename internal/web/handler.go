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

//go:embed 	frontend/*
var frontendFS embed.FS

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
	r.GET("/app.js", handler.AppJS)
	r.GET("/api/health", handler.Health)

	r.POST("/api/photo", handler.AnalyzePhoto)
	r.POST("/api/photo/emotion", handler.AnalyzePhotoEmotion)
	r.POST("/api/frames", handler.AnalyzeFrameSet)
	r.POST("/api/faces/add", handler.AddFace)
	r.POST("/api/faces/delete", handler.DeleteFace)
	r.POST("/api/faces/search", handler.SearchFace)
	r.GET("/api/faces/list", handler.ListFaces)
	r.POST("/api/faces/recognize", handler.RecognizeFace)

	return r
}

func (h *Handler) Index(c *gin.Context) {
	data, err := frontendFS.ReadFile("frontend/index.html")
	if err != nil {
		writeError(c, err)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func (h *Handler) AppJS(c *gin.Context) {
	data, err := frontendFS.ReadFile("frontend/app.js")
	if err != nil {
		writeError(c, err)
		return
	}
	c.Data(http.StatusOK, "text/javascript; charset=utf-8", data)
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
		"ok": true,
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
	FaceCount    int64     `json:"face_count"`
	Box          []float64 `json:"box,omitempty"`
	Quality      float64   `json:"quality"`
	EmbeddingDim int       `json:"embedding_dim,omitempty"`
}

type emotionOutput struct {
	FaceCount    int64                 `json:"face_count"`
	Box          []float64             `json:"box,omitempty"`
	Quality      float64               `json:"quality"`
	EmbeddingDim int                   `json:"embedding_dim,omitempty"`
	Emotion      []recognition.Emotion `json:"emotion,omitempty"`
}

func summarizeFaceResult(result *recognition.FaceResult) faceOutput {
	if result == nil {
		return faceOutput{}
	}
	out := faceOutput{
		FaceCount: result.FaceCount,
		Box:       result.Box,
		Quality:   result.Quality,
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
