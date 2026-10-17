package web

import (
	"fmt"
	"net/http"
	"time"
)

// serveLive 将 Go 已处理的画面作为 MJPEG 流发送给订阅者。
func (h *Handler) serveLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式响应", http.StatusInternalServerError)
		return
	}
	frames := make(chan []byte, 1)
	h.mu.Lock()
	h.live[frames] = struct{}{}
	if len(h.latestJPEG) > 0 {
		offer(frames, h.latestJPEG)
	}
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.live, frames); h.mu.Unlock() }()
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store")
	flusher.Flush()
	for {
		select {
		case data := <-frames:
			if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(data)); err != nil {
				return
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			if _, err := w.Write([]byte("\r\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// serveEvents 只在人脸轨迹或身份变化时发送事件。
func (h *Handler) serveEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式响应", http.StatusInternalServerError)
		return
	}
	events := make(chan []byte, 1)
	h.mu.Lock()
	h.events[events] = struct{}{}
	if len(h.lastEvent) > 0 {
		offer(events, h.lastEvent)
	}
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.events, events); h.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := w.Write([]byte(": connected\n\n")); err != nil {
		return
	}
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case data := <-events:
			if _, err := fmt.Fprintf(w, "event: faces\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
