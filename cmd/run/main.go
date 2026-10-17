package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lipcoder/face/cmd/web"
	sqlitegorm "github.com/lipcoder/face/internal/database/sqlite-gorm"
	"github.com/lipcoder/face/internal/media"
	"github.com/lipcoder/face/internal/reco"
	"github.com/lipcoder/face/internal/reco/inspire"
	"github.com/lipcoder/face/internal/source/browser"
	"github.com/lipcoder/face/server"
)

// frameSource 给每个会话保留最新的一帧，慢会话不会阻塞摄像头。
type frameSource struct{ frames chan *media.Frame }

func newFrameSource() *frameSource { return &frameSource{frames: make(chan *media.Frame, 1)} }

func (s *frameSource) Read(ctx context.Context) (*media.Frame, error) {
	select {
	case frame := <-s.frames:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *frameSource) Close() error { return nil }

func (s *frameSource) publish(frame *media.Frame) {
	copyFrame := *frame
	copyFrame.Data = append([]byte(nil), frame.Data...)
	select {
	case s.frames <- &copyFrame:
	default:
		select {
		case <-s.frames:
		default:
		}
		s.frames <- &copyFrame
	}
}

func browserURL(scheme, addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return scheme + "://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func run() error {
	var model, dbPath, addr, manageAddr, manageCert, manageKey, envPath string
	flag.StringVar(&model, "model", "Megatron", "内嵌 InspireFace 模型名称")
	flag.StringVar(&dbPath, "db", "data/face.db", "SQLite 数据库路径")
	flag.StringVar(&envPath, "env", ".env", "识别视频源配置路径")
	flag.StringVar(&addr, "addr", ":8080", "识别展示端口")
	flag.StringVar(&manageAddr, "manage-addr", ":8081", "管理端口")
	flag.StringVar(&manageCert, "manage-cert", "", "管理端 HTTPS 证书")
	flag.StringVar(&manageKey, "manage-key", "", "管理端 HTTPS 私钥")
	flag.Parse()
	recognition, err := loadRecognitionConfig(envPath)
	if err != nil {
		return err
	}
	if (manageCert == "") != (manageKey == "") {
		return fmt.Errorf("管理端 HTTPS 需要同时提供证书和私钥")
	}
	if addr == manageAddr {
		return fmt.Errorf("展示和管理端口不能相同")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtimeDir, modelPath, err := prepareRuntime(model)
	if err != nil {
		return fmt.Errorf("准备内嵌资源: %w", err)
	}
	defer os.RemoveAll(runtimeDir)
	if err := inspire.Init(modelPath); err != nil {
		return fmt.Errorf("初始化识别模型: %w", err)
	}
	defer inspire.Terminate()

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return fmt.Errorf("创建数据库目录: %w", err)
	}
	db, err := sqlitegorm.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	config := inspire.SessionConfig{
		Mode: inspire.ModeVideo, MaxFaces: 5, DetectPixelLevel: 320,
		EnableRecognition: true, EnableQuality: true,
	}
	recognizer, err := inspire.NewSession(config)
	if err != nil {
		return err
	}
	defer recognizer.Close()
	enroller, err := inspire.NewSession(config)
	if err != nil {
		return err
	}
	defer enroller.Close()

	camera, err := openRecognitionSource(recognition)
	if err != nil {
		return fmt.Errorf("打开识别视频源: %w", err)
	}
	defer camera.Close()
	browserConfig := make(chan browser.VideoInfo, 1)
	browserFrames := make(chan []byte, 1)

	enrollmentFrames := newFrameSource()
	results := make(chan server.Result, 1)
	requests := make(chan web.Request)
	refresh := make(chan struct{}, 1)
	displayUI := web.NewDisplay(ctx.Done(), requests)
	managerUI := web.NewManager(ctx.Done(), requests, browserConfig, browserFrames)

	var workers sync.WaitGroup
	workerErr := make(chan error, 2)
	workers.Add(4)
	go func() {
		defer workers.Done()
		statusFPS := recognition.FPS
		if recognition.Source == "rtsp" {
			statusFPS = 0 // RTSP 的实际帧率由摄像头决定，当前拉流器不提供该值。
		}
		serveCommands(ctx, requests, db, enroller, enrollmentFrames, refresh,
			recognition.Source, recognition.Width, recognition.Height, statusFPS)
	}()
	go func() {
		defer workers.Done()
		var info browser.VideoInfo
		select {
		case info = <-browserConfig:
		case <-ctx.Done():
			return
		}
		client, err := browser.Open(browser.FrameOptions{VideoInfo: info, Frames: browserFrames})
		if err != nil {
			log.Printf("管理摄像头: %v", err)
			return
		}
		defer client.Close()
		for {
			frame, err := client.Read(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("管理摄像头: %v", err)
				}
				return
			}
			enrollmentFrames.publish(frame)
		}
	}()
	go func() {
		defer workers.Done()
		err := server.Run(ctx, db, recognizer, camera, results,
			reco.CyclicFeatureConfig{MinReadyFrames: 1, MinSearchFaceQuality: 0.5, FaceEdgeMargin: 0.05}, refresh)
		if err != nil && !errors.Is(err, context.Canceled) {
			workerErr <- fmt.Errorf("识别: %w", err)
			stop()
		}
	}()
	go func() {
		defer workers.Done()
		for {
			select {
			case result := <-results:
				if err := displayUI.Publish(result); err != nil {
					log.Printf("画面编码: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	displayServer := &http.Server{Addr: addr, Handler: displayUI, ReadHeaderTimeout: 5 * time.Second}
	manageServer := &http.Server{Addr: manageAddr, Handler: managerUI, ReadHeaderTimeout: 5 * time.Second}
	serverErr := make(chan error, 2)
	go func() { serverErr <- displayServer.ListenAndServe() }()
	go func() {
		if manageCert != "" {
			serverErr <- manageServer.ListenAndServeTLS(manageCert, manageKey)
		} else {
			serverErr <- manageServer.ListenAndServe()
		}
	}()
	log.Printf("识别展示 %s（监听 %s）", browserURL("http", addr), addr)
	if manageCert != "" {
		log.Printf("人员管理 %s（监听 %s）", browserURL("https", manageAddr), manageAddr)
	} else {
		log.Printf("人员管理 %s（监听 %s）", browserURL("http", manageAddr), manageAddr)
	}
	var runErr error
	select {
	case err := <-serverErr:
		stop()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case runErr = <-workerErr:
		stop()
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = displayServer.Shutdown(shutdownCtx)
	_ = manageServer.Shutdown(shutdownCtx)
	workers.Wait()
	if runErr == nil {
		select {
		case runErr = <-workerErr:
		default:
		}
	}
	return runErr
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
