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
	"lipcoder/face/internal/service"
	"lipcoder/face/internal/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	rec, err := ins.NewInspire(cfg.Inspireface.PackPath)
	if err != nil {
		logger.Error("init inspireface failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := rec.Close(); err != nil {
			logger.Error("close inspireface failed", "err", err)
		}
	}()

	svc, err := service.New(rec)
	if err != nil {
		logger.Error("init service failed", "err", err)
		os.Exit(1)
	}

	router := web.NewRouter(web.NewHandler(svc, store, 30*time.Second, 0.45))
	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: router,
	}

	go func() {
		logger.Info("web server starting", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("web server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("web server shutdown error", "err", err)
	}

	logger.Info("face service stopped")
}
