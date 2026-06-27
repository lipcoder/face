package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/record"
	"lipcoder/face/internal/service/example"
)

const maxUploadSize = 16 << 20

//go:embed templates/*.html
var templateFS embed.FS

type FaceHandler struct {
	facedb     record.FaceDB
	rec        recognition.Analyzer
	timeout    time.Duration
	similarity float64
}

func NewFaceHandler(
	facedb record.FaceDB,
	rec recognition.Analyzer,
	timeout time.Duration,
	similarity float64,
) *FaceHandler {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if similarity <= 0 {
		similarity = 0.45
	}

	return &FaceHandler{
		facedb:     facedb,
		rec:        rec,
		timeout:    timeout,
		similarity: similarity,
	}
}

// MainPage 返回签到识别页面。
func (h *FaceHandler) MainPage(c *gin.Context) {
	writeHTML(c, "templates/main.html")
}

// ActionPage 返回头部姿态检测页面。
func (h *FaceHandler) ActionPage(c *gin.Context) {
	writeHTML(c, "templates/action.html")
}

// EmotionPage 返回情感识别页面。
func (h *FaceHandler) EmotionPage(c *gin.Context) {
	writeHTML(c, "templates/emotion.html")
}

// ManagerPage 返回人脸库管理页面。
func (h *FaceHandler) ManagerPage(c *gin.Context) {
	writeHTML(c, "templates/manager.html")
}

// Recognize 识别单帧图片里的人脸，并返回人脸框和匹配到的人名。
func (h *FaceHandler) Recognize(c *gin.Context) {
	imageBytes, ok := readFormFile(c, "image")
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	result, err := example.RecognizeFaces(ctx, imageBytes, h.rec, h.facedb, h.similarity)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": result,
	})
}

// DetectHeadState 识别单帧图片里的人头朝向状态。
func (h *FaceHandler) DetectHeadState(c *gin.Context) {
	imageBytes, ok := readFormFile(c, "image")
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	result, err := example.DetectHeadState(ctx, imageBytes, h.rec)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": result,
	})
}

// DetectEmotion 识别单帧图片里的人脸情感，并用 embedding 匹配人名。
func (h *FaceHandler) DetectEmotion(c *gin.Context) {
	imageBytes, ok := readFormFile(c, "image")
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	result, err := example.DetectFaceEmotions(ctx, imageBytes, h.rec, h.facedb, h.similarity)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": result,
	})
}

// AddFace 使用浏览器上传的 25 帧图片做精细录入。
func (h *FaceHandler) AddFace(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart body"})
		return
	}

	files := form.File["frames"]
	if len(files) == 0 {
		files = form.File["frame"]
	}
	if len(files) != 25 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "exactly 25 frames are required"})
		return
	}

	frames := make([][]byte, 0, len(files))
	for _, file := range files {
		f, err := file.Open()
		if err != nil {
			writeError(c, err)
			return
		}

		frameBytes, err := io.ReadAll(io.LimitReader(f, maxUploadSize+1))
		_ = f.Close()
		if err != nil {
			writeError(c, err)
			return
		}
		if len(frameBytes) == 0 || len(frameBytes) > maxUploadSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid frame"})
			return
		}

		frames = append(frames, frameBytes)
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	id, err := example.EnrollFaceFromFrames(ctx, name, frames, h.facedb, h.rec)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"action":  "add",
		"id":      id,
		"name":    name,
		"message": "face enrolled",
	})
}

func (h *FaceHandler) DeleteFace(c *gin.Context) {
	name, ok := getFaceName(c)
	if !ok {
		return
	}

	if err := h.facedb.DeleteFaceByName(name); err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"action": "delete",
		"name":   name,
	})
}

func (h *FaceHandler) SearchFace(c *gin.Context) {
	name, ok := getFaceName(c)
	if !ok {
		return
	}

	exists, err := h.facedb.FaceExistsByName(name)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"action": "search",
		"name":   name,
		"exists": exists,
	})
}

func (h *FaceHandler) ListFaceNames(c *gin.Context) {
	names, err := h.facedb.ListFaceNames()
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"action": "list",
		"names":  names,
		"count":  len(names),
	})
}

type faceNameRequest struct {
	Name string `json:"name"`
}

func NewRouter(faceHandler *FaceHandler) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/main")
	})
	r.GET("/main", faceHandler.MainPage)
	r.GET("/emotion", faceHandler.EmotionPage)
	r.GET("/action", faceHandler.ActionPage)
	r.GET("/manager", faceHandler.ManagerPage)
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	r.POST("/api/recognize", faceHandler.Recognize)
	r.POST("/api/head-state", faceHandler.DetectHeadState)
	r.POST("/api/emotion", faceHandler.DetectEmotion)

	faces := r.Group("/api/faces")
	{
		faces.POST("/add", faceHandler.AddFace)
		faces.POST("/delete", faceHandler.DeleteFace)
		faces.POST("/search", faceHandler.SearchFace)
		faces.GET("/list", faceHandler.ListFaceNames)
	}

	return r
}

func writeHTML(c *gin.Context, path string) {
	content, err := templateFS.ReadFile(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
}

func readFormFile(c *gin.Context, name string) ([]byte, bool) {
	file, err := c.FormFile(name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s is required", name)})
		return nil, false
	}

	f, err := file.Open()
	if err != nil {
		writeError(c, err)
		return nil, false
	}
	defer f.Close()

	fileBytes, err := io.ReadAll(io.LimitReader(f, maxUploadSize+1))
	if err != nil {
		writeError(c, err)
		return nil, false
	}
	if len(fileBytes) == 0 || len(fileBytes) > maxUploadSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid image"})
		return nil, false
	}

	return fileBytes, true
}

func getFaceName(c *gin.Context) (string, bool) {
	var req faceNameRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return "", false
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return "", false
	}

	return name, true
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "request timeout"})
	case errors.Is(err, context.Canceled):
		c.JSON(http.StatusRequestTimeout, gin.H{"error": "request canceled"})
	case errors.Is(err, record.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, recognition.ErrNoFace):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
