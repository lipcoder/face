package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"lipcoder/face/internal/camera/local"
	"lipcoder/face/internal/config"
	"lipcoder/face/internal/data/pgvector"
	"lipcoder/face/internal/recognition/inspireface"
	"lipcoder/face/internal/service"
	"lipcoder/face/internal/service/example"
	"lipcoder/face/internal/web/simple"
)

func main() {

	// 初始化日志器
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// 初始化配置
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	// 初始化ctx
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	// 初始化数据库
	store, err := pgvector.Init(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("init database failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Error("close database failed", "err", err)
		}
	}()

	// 初始化连接器
	httpClient := &http.Client{
		Timeout: 8 * time.Second,
	}

	// 初始化摄像头
	cam, err := local.NewLocalCamera(0)
	if err != nil {
		logger.Error("init local camera failed", "err", err)
		os.Exit(1)
	}

	// 初始化照片处理器
	rec, err := inspireface.NewInspire(inspireface.Config{
		Host: cfg.Inspireface.Host,
	}, httpClient)
	if err != nil {
		logger.Error("init inspireface failed", "err", err)
		os.Exit(1)
	}

	// 创建一个管理请求通道
	reqCh := make(chan service.AdminRequest)
	// add请求的并发限制器
	addFaceSem := make(chan int, 2)

	var adminReqWG sync.WaitGroup
	var loopWG sync.WaitGroup

	// 创建检测用户行为的检测器
	adminloop := example.NewAdminLoop(ctx, reqCh, addFaceSem, store, &adminReqWG)

	loopWG.Add(1)
	go func() {
		defer loopWG.Done()

		err := adminloop.StartAdminLoop()
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("admin loop stopped with error", "err", err)
			return
		}

		logger.Info("admin loop stopped")
	}()

	var action example.ActionRequest

	// web
	faceHandler := simple.NewFaceHandler(
		action,
		reqCh,
		cam,
		rec,
		10*time.Second,
	)

	router := simple.NewRouter(faceHandler)

	srv := &http.Server{
		Addr:    ":5090",
		Handler: router,
	}

	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		logger.Info("web server starting", "addr", ":5090")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("web server failed", "err", err)
		}
	}()

	logger.Info("face service started")
	logger.Info("press Ctrl+C to stop")

	<-ctx.Done()

	logger.Info("shutdown signal received")

	// 优雅关闭 web 服务器
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("web server shutdown error", "err", err)
	}

	close(reqCh)

	loopWG.Wait()
	adminReqWG.Wait()

	logger.Info("face service stopped")
}
