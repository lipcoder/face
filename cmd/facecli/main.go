package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
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
	defer store.Close()

	recognizer, processorSpec, err := buildRecognizer(cfg.Processors)
	if err != nil {
		logger.Error("init image processor failed", "err", err)
		os.Exit(1)
	}
	defer recognizer.Close()
	hubs, err := buildCameras(ctx, cfg.Cameras)
	if err != nil {
		logger.Error("init camera streams failed", "err", err)
		os.Exit(1)
	}
	defer closeHubs(hubs)
	if len(hubs) == 0 {
		logger.Error("facecli requires at least one enabled camera")
		os.Exit(1)
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

	workers := processorSpec.Pools.Workers
	if workers <= 0 {
		workers = 1
	}
	queueSize := processorSpec.Pools.QueueSize
	if queueSize <= 0 {
		queueSize = 16
	}
	requestQueue := make(chan service.AdminRequest, queueSize)
	adminDone := make(chan error, 1)
	go func() {
		adminDone <- svc.RunAdminLoop(ctx, requestQueue, workers)
	}()

	logger.Info("face cli started", "camera", cfg.App.DefaultCamera)
	adminInputLoop(ctx, requestQueue, cfg.App.DefaultCamera)
	close(requestQueue)
	if err := <-adminDone; err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("admin loop failed", "err", err)
	}

	events, err := svc.StartSignIn(ctx, cfg.App.DefaultCamera, 8)
	if err != nil {
		logger.Error("start sign-in stream failed", "err", err)
		os.Exit(1)
	}
	logger.Info("sign-in stream started; press Ctrl+C to stop")
	for {
		select {
		case <-ctx.Done():
			logger.Info("face cli stopped")
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Err != nil {
				logger.Warn("sign-in frame failed", "err", event.Err)
				continue
			}
			for _, match := range event.Matches {
				logger.Info(
					"face signed in",
					"name", match.Name,
					"similarity", match.Similarity,
					"camera", event.CameraID,
					"sequence", event.Sequence,
				)
			}
		}
	}
}

func adminInputLoop(
	ctx context.Context,
	requestQueue chan<- service.AdminRequest,
	defaultCamera string,
) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("请选择操作：")
		fmt.Println("1. 从视频流录入人脸")
		fmt.Println("2. 删除人脸")
		fmt.Println("3. 查询人脸")
		fmt.Println("4. 输出所有人姓名列表")
		fmt.Println("0. 退出管理，开始签到")
		fmt.Print("> ")

		option, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			fmt.Println("读取输入失败:", err)
			continue
		}
		switch strings.TrimSpace(option) {
		case "0":
			return
		case "1":
			name, err := readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}
			result := submitAndWait(ctx, requestQueue, service.AdminAdd, name, defaultCamera)
			if result.Err == nil && result.Enrollment != nil {
				fmt.Printf(
					"添加成功: %s（ID %d，有效样本 %d）\n",
					result.Name,
					result.Enrollment.ID,
					result.Enrollment.Samples,
				)
			}
		case "2":
			name, err := readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}
			result := submitAndWait(ctx, requestQueue, service.AdminDelete, name, "")
			if result.Err == nil {
				fmt.Println("删除成功:", result.Name)
			}
		case "3":
			name, err := readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}
			result := submitAndWait(ctx, requestQueue, service.AdminSearch, name, "")
			if result.Err == nil {
				fmt.Printf("查询结果: %s 存在=%v\n", name, result.Exists)
			}
		case "4":
			result := submitAndWait(ctx, requestQueue, service.AdminList, "", "")
			if result.Err == nil {
				fmt.Println("所有人姓名列表:")
				for index, name := range result.Names {
					fmt.Printf("%d. %s\n", index+1, name)
				}
			}
		default:
			fmt.Println("未知操作")
		}
	}
}

func submitAndWait(
	ctx context.Context,
	requestQueue chan<- service.AdminRequest,
	action service.AdminAction,
	name string,
	cameraID string,
) service.AdminResult {
	reply, err := service.SubmitAdmin(ctx, requestQueue, action, name, cameraID)
	if err != nil {
		fmt.Println("操作失败:", err)
		return service.AdminResult{Err: err}
	}
	select {
	case result := <-reply:
		if result.Err != nil {
			fmt.Println("操作失败:", result.Err)
		}
		return result
	case <-ctx.Done():
		fmt.Println("操作失败:", ctx.Err())
		return service.AdminResult{Err: ctx.Err()}
	}
}

func readName(reader *bufio.Reader) (string, error) {
	fmt.Print("请输入姓名: ")
	name, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("读取姓名失败: %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("姓名不能为空")
	}
	return name, nil
}

func buildCameras(
	ctx context.Context,
	specs map[string]config.ComponentSpec,
) (map[string]*camera.Hub, error) {
	hubs := make(map[string]*camera.Hub)
	ids := sortedCameraIDs(specs)
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
			closeHubs(hubs)
			return nil, fmt.Errorf("build camera %q: %w", id, err)
		}
		hub, err := camera.NewHub(source)
		if err != nil {
			closeHubs(hubs)
			return nil, err
		}
		if err := hub.Start(ctx); err != nil {
			_ = hub.Close()
			closeHubs(hubs)
			return nil, err
		}
		hubs[id] = hub
	}
	return hubs, nil
}

func buildRecognizer(
	specs map[string]config.ProcessorSpec,
) (*ins.Inspire, config.ProcessorSpec, error) {
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
		maxFaces, err := optionInt(spec.Options, "max_faces", 5)
		if err != nil {
			return nil, spec, err
		}
		detectSize, err := optionInt(spec.Options, "detect_pixel_level", 320)
		if err != nil {
			return nil, spec, err
		}
		minPixels, err := optionInt(spec.Options, "min_face_pixels", 32)
		if err != nil {
			return nil, spec, err
		}
		sessions := spec.Pools.Workers
		if sessions <= 0 {
			sessions = 1
		}
		sessions, err = optionInt(spec.Options, "session_count", sessions)
		if err != nil {
			return nil, spec, err
		}
		instance, err := ins.NewInspire(
			optionString(spec.Options, "pack_path", ""),
			ins.WithMaxFaces(maxFaces),
			ins.WithDetectPixelLevel(detectSize),
			ins.WithMinFacePixels(minPixels),
			ins.WithSessionCount(sessions),
			ins.WithFeatures(ins.FeatureFlags{Recognition: true, Quality: true, Pose: true}),
		)
		return instance, spec, err
	}
	return nil, config.ProcessorSpec{}, errors.New("no enabled inspireface processor")
}

func sortedCameraIDs(specs map[string]config.ComponentSpec) []string {
	ids := make([]string, 0, len(specs))
	for id := range specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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

func defaultConfigPath() string {
	if value := strings.TrimSpace(os.Getenv("CONFIG_PATH")); value != "" {
		return value
	}
	return "config.yaml"
}

func closeHubs(hubs map[string]*camera.Hub) {
	for _, hub := range hubs {
		if hub != nil {
			_ = hub.Close()
		}
	}
}
