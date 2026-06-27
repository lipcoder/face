package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lipcoder/face/internal/config"
	"lipcoder/face/internal/recognition/ins"
	"lipcoder/face/internal/record/pgvector"
	"lipcoder/face/internal/web"
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

	// 初始化照片处理器
	rec, err := ins.NewInspire(httpClient, ctx, cfg.Inspireface.Host, 20<<20)
	if err != nil {
		logger.Error("init inspireface failed", "err", err)
		os.Exit(1)
	}

	// web
	faceHandler := web.NewFaceHandler(
		store,
		rec,
		10*time.Second,
		0.45,
	)

	router := web.NewRouter(faceHandler)

	srv := &http.Server{
		Addr:    ":5090",
		Handler: router,
	}

	go func() {
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

	logger.Info("face service stopped")
}
