package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"lipcoder/face/internal/app/camera"
	"lipcoder/face/internal/app/camera/hikvision"
	"lipcoder/face/internal/app/camera/local"
	"lipcoder/face/internal/app/camera/rtsp"
	"lipcoder/face/internal/app/database/pgvector"
	"lipcoder/face/internal/app/recognition/ins"
	"lipcoder/face/internal/config"
	"lipcoder/face/internal/service"
	"lipcoder/face/internal/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	configPath := flag.String("config", defaultConfigPath(), "YAML config path")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := pgvector.Init(ctx, cfg.DB.DSN)
	if err != nil {
		logger.Error("init database failed", "err", err)
		os.Exit(1)
	}
	defer closeWithLog(logger, "database", store.Close)

	recognizer, processorID, err := buildRecognizer(cfg.Processors)
	if err != nil {
		logger.Error("init image processor failed", "err", err)
		os.Exit(1)
	}
	defer closeWithLog(logger, "image processor", recognizer.Close)
	logger.Info("image processor started", "id", processorID)

	hubs, err := buildCameras(ctx, cfg.Cameras)
	if err != nil {
		logger.Error("init camera streams failed", "err", err)
		os.Exit(1)
	}
	defer closeCameraHubs(logger, hubs)
	for id, hub := range hubs {
		logger.Info("camera stream started", "id", id)
		go logCameraErrors(ctx, logger, id, hub.Errors())
	}

	svc, err := service.New(recognizer, store, hubs, service.Config{
		DefaultCamera:        cfg.App.DefaultCamera,
		SimilarityThreshold:  cfg.App.SimilarityThreshold,
		EnrollmentSamples:    cfg.App.EnrollmentSamples,
		EnrollmentMinQuality: cfg.App.EnrollmentMinQuality,
		EnrollmentTimeout:    cfg.App.EnrollmentTimeout,
		SignInInterval:       cfg.App.SignInInterval,
		SignInCooldown:       cfg.App.SignInCooldown,
	})
	if err != nil {
		logger.Error("init service failed", "err", err)
		os.Exit(1)
	}
	for id := range hubs {
		events, err := svc.StartSignIn(ctx, id, 8)
		if err != nil {
			logger.Error("start camera recognition failed", "id", id, "err", err)
			os.Exit(1)
		}
		go logSignInEvents(ctx, logger, events)
	}

	server := &http.Server{
		Addr:              cfg.Server.Address,
		Handler:           web.NewRouter(web.NewHandler(svc, cfg.Server.RequestTimeout)),
		ReadHeaderTimeout: cfg.Server.RequestTimeout,
	}
	go func() {
		logger.Info("web server starting", "addr", cfg.Server.Address)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("web server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("web server shutdown failed", "err", err)
	}
	logger.Info("face service stopped")
}

// buildCameras is intentionally kept in the composition root: config only
// loads maps, while main dispatches every enabled spec to its camera package.
func buildCameras(
	ctx context.Context,
	specs map[string]config.ComponentSpec,
) (map[string]*camera.Hub, error) {
	hubs := make(map[string]*camera.Hub)
	ids := make([]string, 0, len(specs))
	for id := range specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		spec := specs[id]
		if !spec.Enabled {
			continue
		}
		var (
			source camera.Source
			err    error
		)
		switch strings.ToLower(strings.TrimSpace(spec.Type)) {
		case "local", "usb":
			source, err = local.New(id, spec.Options)
		case "rtsp":
			source, err = rtsp.New(id, spec.Options)
		case "hikvision":
			source, err = hikvision.New(id, spec.Options)
		default:
			err = fmt.Errorf("unsupported camera type %q", spec.Type)
		}
		if err != nil {
			closeCameraHubs(nil, hubs)
			return nil, fmt.Errorf("build camera %q: %w", id, err)
		}
		hub, err := camera.NewHub(source)
		if err != nil {
			_ = source.Close()
			closeCameraHubs(nil, hubs)
			return nil, fmt.Errorf("build camera hub %q: %w", id, err)
		}
		if err := hub.Start(ctx); err != nil {
			_ = hub.Close()
			closeCameraHubs(nil, hubs)
			return nil, fmt.Errorf("start camera %q: %w", id, err)
		}
		hubs[id] = hub
	}
	return hubs, nil
}

func buildRecognizer(specs map[string]config.ProcessorSpec) (*ins.Inspire, string, error) {
	ids := make([]string, 0, len(specs))
	for id := range specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		spec := specs[id]
		if !spec.Enabled || !strings.EqualFold(spec.Type, "inspireface") {
			continue
		}
		packPath := optionString(spec.Options, "pack_path", "")
		maxFaces, err := optionInt(spec.Options, "max_faces", 5)
		if err != nil {
			return nil, "", fmt.Errorf("processor %q: %w", id, err)
		}
		detectSize, err := optionInt(spec.Options, "detect_pixel_level", 320)
		if err != nil {
			return nil, "", fmt.Errorf("processor %q: %w", id, err)
		}
		minFacePixels, err := optionInt(spec.Options, "min_face_pixels", 32)
		if err != nil {
			return nil, "", fmt.Errorf("processor %q: %w", id, err)
		}
		sessionCount := spec.Pools.Workers
		if sessionCount <= 0 {
			sessionCount = 1
		}
		sessionCount, err = optionInt(spec.Options, "session_count", sessionCount)
		if err != nil {
			return nil, "", fmt.Errorf("processor %q: %w", id, err)
		}
		pose := optionBool(spec.Options, "pose", true)
		recognizer, err := ins.NewInspire(
			packPath,
			ins.WithMaxFaces(maxFaces),
			ins.WithDetectPixelLevel(detectSize),
			ins.WithMinFacePixels(minFacePixels),
			ins.WithSessionCount(sessionCount),
			ins.WithFeatures(ins.FeatureFlags{
				Recognition: true,
				Quality:     true,
				Pose:        pose,
			}),
		)
		return recognizer, id, err
	}
	return nil, "", errors.New("no enabled inspireface processor")
}

func defaultConfigPath() string {
	if value := strings.TrimSpace(os.Getenv("CONFIG_PATH")); value != "" {
		return value
	}
	return "config.yaml"
}

func optionString(options map[string]any, key, fallback string) string {
	value, ok := options[key]
	if !ok || value == nil {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return fallback
	}
	return text
}

func optionInt(options map[string]any, key string, fallback int) (int, error) {
	value, ok := options[key]
	if !ok || value == nil {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("option %s must be a positive integer", key)
	}
	return parsed, nil
}

func optionBool(options map[string]any, key string, fallback bool) bool {
	value, ok := options[key]
	if !ok || value == nil {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
	if err != nil {
		return fallback
	}
	return parsed
}

func logCameraErrors(ctx context.Context, logger *slog.Logger, id string, errors <-chan error) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errors:
			if !ok {
				return
			}
			logger.Warn("camera stream error", "id", id, "err", err)
		}
	}
}

func logSignInEvents(ctx context.Context, logger *slog.Logger, events <-chan service.SignInEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Err != nil {
				logger.Warn(
					"camera recognition failed",
					"id", event.CameraID,
					"sequence", event.Sequence,
					"err", event.Err,
				)
				continue
			}
			for _, match := range event.Matches {
				logger.Info(
					"face signed in",
					"id", event.CameraID,
					"sequence", event.Sequence,
					"name", match.Name,
					"similarity", match.Similarity,
				)
			}
		}
	}
}

func closeCameraHubs(logger *slog.Logger, hubs map[string]*camera.Hub) {
	for id, hub := range hubs {
		if hub == nil {
			continue
		}
		if err := hub.Close(); err != nil && logger != nil {
			logger.Error("close camera stream failed", "id", id, "err", err)
		}
	}
}

func closeWithLog(logger *slog.Logger, component string, closeFunc func() error) {
	if closeFunc == nil {
		return
	}
	if err := closeFunc(); err != nil {
		logger.Error("close component failed", "component", component, "err", err)
	}
}
