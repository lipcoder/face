package main

import (
	"context"
	"errors"
	"log"
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
	labservice "lipcoder/face/internal/service/lab"
	labweb "lipcoder/face/internal/web/lab"
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
	adminloop := labservice.NewAdminLoop(ctx, reqCh, addFaceSem, store, &adminReqWG)

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

	var action labservice.ActionRequest

	// web
	faceHandler := labweb.NewFaceHandler(
		action,
		reqCh,
		cam,
		rec,
		10*time.Second,
	)

	router := labweb.NewRouter(faceHandler)

	if err := router.Run(":5090"); err != nil {
		log.Fatal(err)
	}

	logger.Info("face service started")
	logger.Info("press Ctrl+C to stop")

	<-ctx.Done()

	logger.Info("shutdown signal received")

	close(reqCh)

	loopWG.Wait()
	adminReqWG.Wait()

	logger.Info("face service stopped")
}
