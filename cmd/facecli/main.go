package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"lipcoder/face/internal/camera"
	"lipcoder/face/internal/camera/local"
	"lipcoder/face/internal/config"
	"lipcoder/face/internal/recognition"
	"lipcoder/face/internal/recognition/ins"
	"lipcoder/face/internal/record/pgvector"
	"lipcoder/face/internal/service"
	"lipcoder/face/internal/service/example"
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

	cam, err := local.NewLocal(ctx, 0)
	if err != nil {
		logger.Error("init local camera failed", "err", err)
		os.Exit(1)
	}
	defer cam.Close()

	rec, err := newRecognizer(cfg)
	if err != nil {
		logger.Error("init inspireface failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := rec.Close(); err != nil {
			logger.Error("close inspireface failed", "err", err)
		}
	}()

	reqCh := make(chan service.AdminRequest)
	addFaceSem := make(chan int, 2)

	var adminReqWG sync.WaitGroup
	var loopWG sync.WaitGroup

	adminLoop := example.NewAdminLoop(ctx, reqCh, addFaceSem, store, &adminReqWG)
	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		if err := adminLoop.StartAdminLoop(); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("admin loop stopped with error", "err", err)
			return
		}
		logger.Info("admin loop stopped")
	}()

	logger.Info("face cli started")
	var action example.ActionRequest
	adminInputLoop(ctx, reqCh, cam, rec, action)

	signInLoop := example.NewSignInLoop(ctx, cam, rec, store, 500*time.Millisecond, 0.45, store)
	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		if err := signInLoop.StartSignIn(); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("sign in loop stopped with error", "err", err)
			return
		}
		logger.Info("sign in loop stopped")
	}()

	logger.Info("sign in loop started")
	logger.Info("press Ctrl+C to stop")

	<-ctx.Done()
	logger.Info("shutdown signal received")

	close(reqCh)
	loopWG.Wait()
	adminReqWG.Wait()

	logger.Info("face cli stopped")
}

func adminInputLoop(
	ctx context.Context,
	reqCh chan<- service.AdminRequest,
	cam camera.Camera,
	rec recognition.Analyzer,
	action example.ActionRequest,
) {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("请选择操作：")
		fmt.Println("1. 添加人脸")
		fmt.Println("2. 删除人脸")
		fmt.Println("3. 查询人脸")
		fmt.Println("4. 输出所有人姓名列表")
		fmt.Println("5. 检测当前画面人脸情绪")
		fmt.Println("0. 退出管理，开始签到")
		fmt.Print("> ")

		op, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("读取输入失败:", err)
			continue
		}
		op = strings.TrimSpace(op)

		switch op {
		case "0":
			fmt.Println("退出管理模式，开始签到")
			return
		case "1":
			name, err := readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}
			sendAdminRequest(ctx, reqCh, action.AddFace(name, cam, rec), func(result service.AdminResult) {
				fmt.Println("添加成功:", result.Name)
			})
		case "2":
			name, err := readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}
			sendAdminRequest(ctx, reqCh, action.DeleteFace(name), func(result service.AdminResult) {
				fmt.Println("删除成功:", result.Name)
			})
		case "3":
			name, err := readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}
			sendAdminRequest(ctx, reqCh, action.SearchFace(name), func(result service.AdminResult) {
				if result.Exists {
					fmt.Println("查询结果: 存在", result.Name)
					return
				}
				fmt.Println("查询结果: 不存在", result.Name)
			})
		case "4":
			sendAdminRequest(ctx, reqCh, action.ListFaceNames(), printFaceNames)
		case "5":
			if err := detectEmotionOnce(ctx, cam, rec); err != nil {
				fmt.Println("情绪检测失败:", err)
			}
		default:
			fmt.Println("未知操作")
		}
	}
}

func sendAdminRequest(
	ctx context.Context,
	reqCh chan<- service.AdminRequest,
	req service.AdminRequest,
	onSuccess func(service.AdminResult),
) {
	select {
	case reqCh <- req:
	case <-ctx.Done():
		fmt.Println("操作失败:", ctx.Err())
		return
	}

	select {
	case result := <-req.Reply:
		if result.Err != nil {
			fmt.Println("操作失败:", result.Err)
			return
		}
		if onSuccess != nil {
			onSuccess(result)
		}
	case <-ctx.Done():
		fmt.Println("操作失败:", ctx.Err())
	}
}

func printFaceNames(result service.AdminResult) {
	if len(result.Names) == 0 {
		fmt.Println("当前没有已添加的人脸")
		return
	}

	fmt.Println("所有人姓名列表:")
	for i, name := range result.Names {
		fmt.Printf("%d. %s\n", i+1, name)
	}
}

func detectEmotionOnce(ctx context.Context, cam camera.Camera, rec recognition.Analyzer) error {
	imageBytes, err := cam.Capture()
	if err != nil {
		return fmt.Errorf("capture image: %w", err)
	}

	result, err := rec.AnalyzePhotoEmotion(ctx, imageBytes)
	if err != nil {
		return err
	}

	return writeJSON(os.Stdout, summarizeEmotionResult(result))
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

func newRecognizer(cfg config.Config) (*ins.Inspire, error) {
	packPath := strings.TrimSpace(cfg.Inspireface.PackPath)
	if packPath == "" {
		packPath = strings.TrimSpace(os.Getenv("INSPIREFACE_PACK_PATH"))
	}
	if packPath == "" {
		packPath = filepath.Join(".sdk", "models", "Megatron")
	}
	return ins.NewInspire(packPath)
}

type emotionOutput struct {
	FaceCount    int64                 `json:"face_count"`
	Box          []float64             `json:"box,omitempty"`
	Quality      float64               `json:"quality"`
	EmbeddingDim int                   `json:"embedding_dim,omitempty"`
	Emotion      []recognition.Emotion `json:"emotion,omitempty"`
}

func summarizeEmotionResult(result *recognition.EmotionResult) emotionOutput {
	if result == nil {
		return emotionOutput{}
	}

	out := emotionOutput{
		FaceCount: result.FaceCount,
		Box:       result.Box,
		Quality:   result.Quality,
		Emotion:   result.Emotion,
	}
	if len(result.Embedding) > 0 {
		out.EmbeddingDim = len(result.Embedding[0])
	}

	return out
}

func writeJSON(file *os.File, v any) error {
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
