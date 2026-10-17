package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lipcoder/face/frontend"
	"github.com/lipcoder/face/internal/media"
	"github.com/lipcoder/face/internal/source/browser"
	"github.com/lipcoder/face/server"
)

type Handler struct {
	role          string
	done          <-chan struct{}
	requests      chan<- Request
	browserConfig chan browser.VideoInfo
	browserFrames chan []byte
	inputInfo     *browser.VideoInfo
	mu            sync.RWMutex
	snapshot      snapshot
	latestJPEG    []byte
	lastEvent     []byte
	lastSignature string
	live          map[chan []byte]struct{}
	events        map[chan []byte]struct{}
}

type snapshot struct {
	Image string                   `json:"image"`
	Faces []server.ServiceFaceInfo `json:"faces"`
}

func NewDisplay(done <-chan struct{}, requests chan<- Request) *Handler {
	return &Handler{role: "display", done: done, requests: requests,
		live: make(map[chan []byte]struct{}), events: make(map[chan []byte]struct{})}
}

func NewManager(done <-chan struct{}, requests chan<- Request, browserConfig chan browser.VideoInfo, browserFrames chan []byte) *Handler {
	return &Handler{role: "manager", done: done, requests: requests, browserConfig: browserConfig, browserFrames: browserFrames,
		live: make(map[chan []byte]struct{}), events: make(map[chan []byte]struct{})}
}

func (h *Handler) Publish(result server.Result) error {
	frame := result.Frame
	if frame == nil || frame.Format != media.PixelBGR || frame.Width <= 0 || frame.Height <= 0 || len(frame.Data) < frame.Width*frame.Height*3 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, frame.Width, frame.Height))
	for y := 0; y < frame.Height; y++ {
		for x := 0; x < frame.Width; x++ {
			src := (y*frame.Width + x) * 3
			dst := y*img.Stride + x*4
			img.Pix[dst], img.Pix[dst+1], img.Pix[dst+2], img.Pix[dst+3] = frame.Data[src+2], frame.Data[src+1], frame.Data[src], 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
		return err
	}
	h.mu.Lock()
	h.snapshot = snapshot{Image: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), Faces: result.Faces}
	h.latestJPEG = append(h.latestJPEG[:0], buf.Bytes()...)
	for subscriber := range h.live {
		offer(subscriber, h.latestJPEG)
	}
	var signature strings.Builder
	for _, face := range result.Faces {
		signature.WriteString(strconv.FormatInt(face.TrackID, 10))
		signature.WriteByte(':')
		signature.WriteString(face.PersonID)
		signature.WriteByte(':')
		signature.WriteString(face.Name)
		signature.WriteByte(';')
	}
	if signature.String() != h.lastSignature {
		h.lastSignature = signature.String()
		h.lastEvent, _ = json.Marshal(struct {
			Time  string                   `json:"time"`
			Faces []server.ServiceFaceInfo `json:"faces"`
		}{Time: time.Now().Format(time.RFC3339Nano), Faces: result.Faces})
		for subscriber := range h.events {
			offer(subscriber, h.lastEvent)
		}
	}
	h.mu.Unlock()
	return nil
}

func offer(ch chan []byte, value []byte) {
	copyValue := append([]byte(nil), value...)
	select {
	case ch <- copyValue:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- copyValue:
		default:
		}
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.role == "display" {
		switch r.URL.Path {
		case "/", "/display.css", "/display.js", "/api/state", "/api/live", "/api/events", "/api/attendance", "/api/status":
		default:
			http.NotFound(w, r)
			return
		}
	} else {
		switch r.URL.Path {
		case "/", "/manager.css", "/manager.js", "/api/source/config", "/api/source/frame", "/api/preview", "/api/people", "/api/status", "/api/refresh", "/api/enroll":
		default:
			if !strings.HasPrefix(r.URL.Path, "/api/people/") {
				http.NotFound(w, r)
				return
			}
		}
	}
	switch r.URL.Path {
	case "/":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := h.role + ".html"
		data, err := frontend.Files.ReadFile(name)
		if err != nil {
			http.Error(w, "页面加载失败", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	case "/display.css", "/display.js", "/manager.css", "/manager.js":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data, err := frontend.Files.ReadFile(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		}
		w.Write(data)
	case "/api/source/config":
		if h.browserConfig == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256)
		var config browser.VideoInfo
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			http.Error(w, "无效的视频参数", http.StatusBadRequest)
			return
		}
		if config.Width <= 0 || config.Height <= 0 || config.Width > 16384 || config.Height > 16384 ||
			int64(config.Width)*int64(config.Height)*3 > 256<<20 || config.Framerate <= 0 || config.Framerate > 240 ||
			math.IsNaN(config.Framerate) || math.IsInf(config.Framerate, 0) {
			http.Error(w, "无效的浏览器视频参数", http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		if h.inputInfo != nil && *h.inputInfo != config {
			h.mu.Unlock()
			http.Error(w, "视频源已初始化，请保持原有规格", http.StatusConflict)
			return
		}
		if h.inputInfo == nil {
			h.inputInfo = &config
			h.browserConfig <- config
		}
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "/api/source/frame":
		if h.browserFrames == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Type") != "image/jpeg" {
			http.Error(w, "需要 JPEG 视频帧", http.StatusUnsupportedMediaType)
			return
		}
		h.mu.RLock()
		info := h.inputInfo
		h.mu.RUnlock()
		if info == nil {
			http.Error(w, "浏览器尚未提交视频规格", http.StatusConflict)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "视频帧过大或读取失败", http.StatusBadRequest)
			return
		}
		jpegInfo, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || jpegInfo.Width != info.Width || jpegInfo.Height != info.Height {
			http.Error(w, "视频帧尺寸与初始化规格不一致", http.StatusBadRequest)
			return
		}
		select {
		case h.browserFrames <- data:
		default:
			select {
			case <-h.browserFrames:
			default:
			}
			select {
			case h.browserFrames <- data:
			default:
			}
		}
		h.mu.Lock()
		h.latestJPEG = append(h.latestJPEG[:0], data...)
		for subscriber := range h.live {
			offer(subscriber, h.latestJPEG)
		}
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "/api/state":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.mu.RLock()
		state := h.snapshot
		h.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(state)
	case "/api/live":
		h.serveLive(w, r)
	case "/api/preview":
		h.serveLive(w, r)
	case "/api/events":
		h.serveEvents(w, r)
	case "/api/attendance":
		h.handleAttendance(w, r)
	case "/api/people":
		h.handlePeople(w, r)
	case "/api/status":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.dispatch(w, r, GetStatus, "", "", "")
	case "/api/refresh":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.dispatch(w, r, RefreshRecognition, "", "", "")
	case "/api/enroll":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id, name, err := parsePerson(r, w)
		if err != nil || !validID(id) || name == "" {
			http.Error(w, "请输入 10 位数字学号和姓名", http.StatusBadRequest)
			return
		}
		h.dispatch(w, r, CreatePerson, id, name, "")
	default:
		if strings.HasPrefix(r.URL.Path, "/api/people/") {
			h.handlePerson(w, r)
			return
		}
		http.NotFound(w, r)
	}
}
