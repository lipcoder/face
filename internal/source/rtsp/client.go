package rtsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/lipcoder/face/internal/media"
)

// FrameConfig 配置 RTSP 拉流和解码后的画面尺寸。
type FrameConfig struct {
	URL            string
	Width          int
	Height         int
	Transport      string        // 传输协议，默认使用 tcp，也可使用 udp
	ReadTimeout    time.Duration // 最长等待完整视频帧的时间，默认 10 秒
	ReconnectDelay time.Duration // 断线后重连间隔，默认 2 秒
}

type Client struct {
	options   FrameConfig
	ffmpeg    string
	frameSize int

	mu      sync.Mutex
	cmd     *exec.Cmd
	write   []byte
	latest  []byte
	current []byte
	seq     uint64
	readSeq uint64
	closed  bool

	notify chan struct{}
	stop   chan struct{}
	done   chan struct{}
}

// Open 启动后台拉流；连接中断后持续重试，直到关闭客户端。
func Open(options FrameConfig) (*Client, error) {
	u, err := url.Parse(options.URL)
	if err != nil || u.Host == "" || (u.Scheme != "rtsp" && u.Scheme != "rtsps") {
		return nil, errors.New("无效的 RTSP URL")
	}
	if options.Width <= 0 || options.Height <= 0 || options.Width > 16384 || options.Height > 16384 ||
		int64(options.Width)*int64(options.Height)*3 > 256<<20 {
		return nil, errors.New("无效的视频尺寸")
	}
	if options.Transport == "" {
		options.Transport = "tcp"
	}
	if options.Transport != "tcp" && options.Transport != "udp" {
		return nil, errors.New("RTSP transport 必须为 tcp 或 udp")
	}
	if options.ReadTimeout == 0 {
		options.ReadTimeout = 10 * time.Second
	}
	if options.ReconnectDelay == 0 {
		options.ReconnectDelay = 2 * time.Second
	}
	if options.ReadTimeout < time.Millisecond || options.ReconnectDelay < time.Millisecond {
		return nil, errors.New("超时和重连间隔必须至少为 1 毫秒")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("查找 ffmpeg 失败: %w", err)
	}
	size := options.Width * options.Height * 3
	c := &Client{
		options: options, ffmpeg: ffmpeg, frameSize: size,
		write: make([]byte, size), latest: make([]byte, size), current: make([]byte, size),
		notify: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go c.run()
	return c, nil
}

func (c *Client) command() *exec.Cmd {
	o := c.options
	return exec.Command(c.ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-rtsp_transport", o.Transport,
		"-timeout", strconv.FormatInt(o.ReadTimeout.Microseconds(), 10),
		"-rw_timeout", strconv.FormatInt(o.ReadTimeout.Microseconds(), 10),
		"-i", o.URL,
		"-map", "0:v:0", "-an", "-sn", "-dn",
		"-vf", fmt.Sprintf("scale=%d:%d", o.Width, o.Height),
		"-pix_fmt", "bgr24", "-f", "rawvideo", "pipe:1",
	)
}

func (c *Client) run() {
	defer close(c.done)
	for {
		select {
		case <-c.stop:
			return
		default:
		}

		cmd := c.command()
		stdout, err := cmd.StdoutPipe()
		if err == nil {
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}
			err = cmd.Start()
			if err == nil {
				c.cmd = cmd
			}
			c.mu.Unlock()
		}
		if err == nil {
			c.readStream(cmd, stdout)
			// 进程退出后立即丢弃旧帧，避免并发读取拿到断线前的画面。
			c.mu.Lock()
			c.readSeq = c.seq
			c.mu.Unlock()
			_ = cmd.Process.Kill() // 中断可能阻塞的管道读取
			_ = cmd.Wait()
			c.mu.Lock()
			c.cmd = nil
			c.mu.Unlock()
		}

		// 每次重连前清除上一条连接留下的帧。
		c.mu.Lock()
		c.readSeq = c.seq
		c.mu.Unlock()
		select {
		case <-c.stop:
			return
		case <-time.After(c.options.ReconnectDelay):
		}
	}
}

func (c *Client) readStream(cmd *exec.Cmd, stdout io.Reader) {
	// 摄像头可能保持连接却不再输出帧；超时后终止 ffmpeg 以触发重连。
	watchdog := time.AfterFunc(c.options.ReadTimeout, func() { _ = cmd.Process.Kill() })
	defer watchdog.Stop()
	for {
		if _, err := io.ReadFull(stdout, c.write); err != nil {
			return
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		// 交换缓冲区，只发布完整帧；读取方始终拿到最近的一帧。
		c.write, c.latest = c.latest, c.write
		c.seq++
		c.mu.Unlock()
		watchdog.Reset(c.options.ReadTimeout)
		select {
		case c.notify <- struct{}{}:
		default:
		}
	}
}

// Read 等待最新的完整帧。返回的数据只保证在下一次 Read 前有效，调用方应单独读取。
func (c *Client) Read(ctx context.Context) (*media.Frame, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, errors.New("客户端已关闭")
		}
		if c.seq != c.readSeq {
			c.current, c.latest = c.latest, c.current
			c.readSeq = c.seq
			frame := &media.Frame{
				Data: c.current, Size: c.frameSize,
				Width: c.options.Width, Height: c.options.Height,
				Format: media.PixelBGR, Rotation: media.Rotation0,
			}
			c.mu.Unlock()
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
	if !c.closed {
		c.closed = true
		close(c.stop)
		select {
		case c.notify <- struct{}{}:
		default:
		}
		if c.cmd != nil {
			_ = c.cmd.Process.Kill()
		}
	}
	c.mu.Unlock()
	<-c.done
	return nil
}
