package media

import "unsafe"

type PixelFormat uint8

const (
	PixelRGB PixelFormat = iota // PixelRGB 表示每个像素按 R、G、B 顺序连续存放，每像素 3 字节。

	PixelBGR  // PixelBGR 表示每个像素按 B、G、R 顺序连续存放，每像素 3 字节。
	PixelRGBA // PixelRGBA 表示每个像素按 R、G、B、A 顺序连续存放，每像素 4 字节。
	PixelBGRA // PixelBGRA 表示每个像素按 B、G、R、A 顺序连续存放，每像素 4 字节。
	PixelNV12 // PixelNV12 表示 Y 平面后跟 UV 交错平面的 YUV420 图像。
	PixelNV21 // PixelNV21 表示 Y 平面后跟 VU 交错平面的 YUV420 图像。
	PixelI420 // PixelI420 表示 Y、U、V 三个平面依次存放的 YUV420 图像。
	PixelGray // PixelGray 表示单通道 8 位灰度图像。
)

type Rotation uint8

const (
	Rotation0 Rotation = iota
	Rotation90
	Rotation180
	Rotation270
)

type Frame struct {
	// Data 是由 Go 管理的图像内存。使用 Data 时 Buffer 必须为 nil。
	Data []byte
	// Buffer 指向外部 source 拥有的 native/C 图像内存,不可指向 Go 管理的内存指针
	//
	// Frame 和 Session 都不拥有、也不能释放这块内存；
	// 调用方必须保证 GetFacePlace/GetFaceFeature 返回前内存始终有效且不会被覆盖
	// 使用 Buffer 时 Data 必须为 nil
	Buffer unsafe.Pointer
	// Size 是 Buffer 指向的有效数据大小；Data 模式下使用 len(Data)。
	Size int

	Width     int
	Height    int
	Framerate float64 // 视频源报告的帧率；未提供时为 0

	Format   PixelFormat
	Rotation Rotation
}
