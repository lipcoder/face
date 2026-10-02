//go:build darwin

package local

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"

	"github.com/lipcoder/face/internal/media"
)

type FrameOptions struct {
	Device    string //  设备id
	Width     int    // 视频宽度
	Height    int    // 视频高度
	Framerate int    // 视频帧率
}

type Client struct {
	cmd *exec.Cmd

	width     int
	height    int
	frameSize int

	mu sync.Mutex

	write   []byte // 写入缓冲区
	latest  []byte // 最新帧缓冲区
	current []byte // 当前帧缓冲区

	seq     uint64 // 帧序列号
	readSeq uint64 // 读取帧序列号

	err    error // 错误信息
	closed bool  // 是否关闭

	notify chan struct{} // 通知通道
	done   chan struct{} // 结束通道
}

// 调用ffmpeg打开摄像头
//
// 启动后台goroutine来处理视频帧
func Open(options FrameOptions) (*Client, error) {
	if options.Device == "" {
		options.Device = "default"
	}

	if options.Width == 0 {
		options.Width = 1280
	}

	if options.Height == 0 {
		options.Height = 720
	}

	if options.Framerate == 0 {
		options.Framerate = 30
	}

	frameSize := options.Width * options.Height * 3 // BGR24格式每个像素占3个字节

	args := []string{
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",

		"-f", "avfoundation",
		"-framerate", strconv.Itoa(options.Framerate),
		"-video_size", fmt.Sprintf(
			"%dx%d",
			options.Width,
			options.Height,
		),

		"-i", options.Device + ":none",

		"-an",
		"-pix_fmt", "bgr24",
		"-f", "rawvideo",
		"pipe:1", // 输出到标准输出
	}

	cmd := exec.Command("ffmpeg", args...)

	stdout, err := cmd.StdoutPipe() // 给 Go 返回一个可以读取 stdout 的对象
	if err != nil {
		return nil, fmt.Errorf("获取标准输出管道错误: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动ffmpeg命令错误: %w", err)
	}

	c := &Client{
		cmd: cmd,

		width:     options.Width,
		height:    options.Height,
		frameSize: frameSize,

		write:   make([]byte, frameSize),
		latest:  make([]byte, frameSize),
		current: make([]byte, frameSize),

		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}

	//
	// Open()
	//   │
	//   ├── go c.run(stdout) ───────────→ run(stdout)
	//   │                                  │
	//   │                                  ├─ 读取帧
	//   │                                  ├─ 读取帧
	//   ↓                                  ├─ 读取帧
	// return c                             └─ ...
	//   │
	//   ↓
	// 调用者继续执行

	// run()
	// 生产者
	// 负责不停地从摄像头生产帧

	// Read()
	// 消费者
	// 负责在需要的时候取最新帧
	go c.run(stdout)

	return c, nil
}

func (c *Client) run(stdout io.Reader) {
	defer close(c.done)

	for {
		if _, err := io.ReadFull(stdout, c.write); err != nil {
			c.mu.Lock()

			if !c.closed && c.err == nil {
				c.err = err
			}

			c.mu.Unlock()

			// 唤醒 Read，让它读取 c.err / c.closed。
			select {
			case c.notify <- struct{}{}:
			default:
			}

			// stdout 已经无法继续提供完整帧，
			// 不再等待 FFmpeg 自己决定什么时候退出。
			if c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}

			break
		}

		c.mu.Lock()

		// write 变成最新帧
		//
		// 旧 latest 重新拿去给 FFmpeg 写
		c.write, c.latest = c.latest, c.write
		c.seq++

		c.mu.Unlock()

		// 当c.notify已经有时，跳转位default，避免阻塞
		select {
		case c.notify <- struct{}{}:
		default:
		}
	}

	// 此时 FFmpeg 要么已经退出，要么刚被 Kill
	// Wait 主要负责等待退出并回收进程资源
	_ = c.cmd.Wait()
}

func (c *Client) Read(ctx context.Context) (*media.Frame, error) {
	for {
		c.mu.Lock()

		// 如果有错误，返回错误
		if c.err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("读取帧错误: %w", c.err)
		}

		// 如果客户端已经关闭则返回
		if c.closed {
			c.mu.Unlock()
			return nil, fmt.Errorf("客户端已关闭")
		}

		// 如果有新的帧，返回最新帧
		if c.seq != c.readSeq {
			// 取最新帧
			c.current, c.latest = c.latest, c.current
			c.readSeq = c.seq

			frame := &media.Frame{
				Data:     c.current,
				Size:     c.frameSize,
				Width:    c.width,
				Height:   c.height,
				Format:   media.PixelBGR,
				Rotation: media.Rotation0,
			}
			c.mu.Unlock()

			// 清理可能残留的旧唤醒信号
			select {
			case <-c.notify:
			default:
			}

			return frame, nil
		}

		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-c.notify:
		}
	}
}

func (c *Client) Close() error {
	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()
		return nil
	}

	c.closed = true

	// 唤醒可能正在等待新帧的 Read
	select {
	case c.notify <- struct{}{}:
	default:
	}

	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}

	// 等待后台 goroutine 退出，确保资源被释放
	<-c.done

	return nil
}

/*
run goroutine                              Read goroutine
     │                                          │
     │                                          │
     ├─ io.ReadFull(...)                        │
     │   读到一整帧                       	      │
     │                                          │
     ├─ write ↔ latest                          │
     │                                          │
     ├─ seq++                                   │
     │                                          │
     ├─ notify <- struct{}{} ─────────────────► │
     │                                          │
     │                                      <- notify
     │                                          │
     │                                          ↓
     │                                  从等待状态醒来
     │                                          │
     │                                  重新检查：
     │                                  seq != readSeq ?
     │                                          │
     │                                          ├─ 是
     │                                          │   ↓
     │                                          │ current ↔ latest
     │                                          │   ↓
     │                                          │ readSeq = seq
     │                                          │   ↓
     │                                          │ return Frame
     │                                          │
     │                                          └─ 否
     │                                              ↓
     │                                           继续等
     │                                          <- notify
     │
     ├─ 继续读取下一帧
     ↓
*/

/*
run goroutine                              Read / 处理 goroutine
     │                                             │
     ├─ 读到 Frame 1                                │
     │                                             │
     ├─ write ↔ latest                             │
     ├─ seq = 1                                    │
     ├─ notify <- struct{}{} ────────────────────► │
     │                                             │
     │                                             ↓
     │                                         Read()
     │                                             │
     │                                      拿到 Frame 1
     │                                             │
     │                                      开始处理 Frame 1
     │                                      （处理很慢）
     │                                             │
     ├─ 读到 Frame 2                  	            │
     │                                             │
     ├─ write ↔ latest                             │
     ├─ seq = 2                                    │
     ├─ notify <- struct{}{}                       │
     │                                             │
     ├─ 读到 Frame 3                                │
     │                                             │
     ├─ write ↔ latest                             │
     ├─ seq = 3                                    │
     ├─ notify 尝试发送                             │
     │   channel 已经有通知                          │
     │   → default，直接丢掉这个通知                  │
     │                                             │
     ├─ 读到 Frame 4                                │
     │                                             │
     ├─ write ↔ latest                             │
     ├─ seq = 4                                    │
     ├─ notify 尝试发送                             │
     │   → 仍然不阻塞                                │
     │                                             │
     ├─ 读到 Frame 5                                │
     │                                             │
     ├─ latest = Frame 5                           │
     ├─ seq = 5                                    │
     │                                             │
     │                                      Frame 1 处理完成
     │                                             │
     │                                             ↓
     │                                         再次 Read()
     │                                             │
     │                                      seq = 5
     │                                      readSeq = 1
     │                                             │
     │                                      5 != 1
     │                                             │
     │                                      current ↔ latest
     │                                             │
     │                                             ↓
     │                                      直接拿 Frame 5
     │
     ├─ Frame 6
     ├─ Frame 7
     ├─ Frame 8
     ↓
*/
