package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
	cam, err := local.NewLocal(ctx, 0)
	if err != nil {
		logger.Error("init local camera failed", "err", err)
		os.Exit(1)
	}

	// 初始化照片处理器
	rec, err := ins.NewInspire(httpClient, ctx, cfg.Inspireface.Host, 20<<20)
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

	// 创建用户行为的创建器
	var r example.ActionRequest
	adminInputLoop(ctx, reqCh, cam, rec, r)

	// 人脸识别循环检测器
	signinloop := example.NewSignInLoop(ctx, cam, rec, store, 500*time.Millisecond, 0.45, store)

	loopWG.Add(1)
	go func() {
		defer loopWG.Done()

		err := signinloop.StartSignIn()
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("sign in loop stopped with error", "err", err)
			return
		}

		logger.Info("sign in loop stopped")
	}()

	logger.Info("face service started")
	logger.Info("press Ctrl+C to stop")

	<-ctx.Done()

	logger.Info("shutdown signal received")

	close(reqCh)

	loopWG.Wait()
	adminReqWG.Wait()

	logger.Info("face service stopped")
}

func adminInputLoop(
	ctx context.Context,
	reqCh chan<- service.AdminRequest,
	cam camera.Camera,
	rec recognition.Analyzer,
	r example.ActionRequest,
) {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("请选择操作：")
		fmt.Println("1. 添加人脸")
		fmt.Println("2. 删除人脸")
		fmt.Println("3. 查询人脸")
		fmt.Println("4. 输出所有人姓名列表")
		fmt.Println("0. 退出管理，开始签到")
		fmt.Print("> ")

		op, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("读取输入失败:", err)
			continue
		}

		op = strings.TrimSpace(op)

		if op == "0" {
			fmt.Println("退出管理模式，开始签到")
			return
		}

		if op != "1" && op != "2" && op != "3" && op != "4" {
			fmt.Println("未知操作")
			continue
		}

		var req service.AdminRequest
		var name string

		switch op {
		case "1":
			name, err = readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}

			req = r.AddFace(name, cam, rec)

		case "2":
			name, err = readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}

			req = r.DeleteFace(name)

		case "3":
			name, err = readName(reader)
			if err != nil {
				fmt.Println(err)
				continue
			}

			req = r.SearchFace(name)

		case "4":
			req = r.ListFaceNames()
		}

		select {
		case reqCh <- req:
		case <-ctx.Done():
			return
		}

		select {
		case result := <-req.Reply:
			if result.Err != nil {
				fmt.Println("操作失败:", result.Err)
				continue
			}

			switch op {
			case "1":
				fmt.Println("添加成功:", name)
			case "2":
				fmt.Println("删除成功:", name)
			case "3":
				if result.Exists {
					fmt.Println("查询结果: 存在", name)
				} else {
					fmt.Println("查询结果: 不存在", name)
				}
			case "4":
				if len(result.Names) == 0 {
					fmt.Println("当前没有已添加的人脸")
					continue
				}

				fmt.Println("所有人姓名列表:")
				for i, faceName := range result.Names {
					fmt.Printf("%d. %s\n", i+1, faceName)
				}
			}

		case <-ctx.Done():
			return
		}
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
