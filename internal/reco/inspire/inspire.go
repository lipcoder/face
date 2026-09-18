package inspire

/*
#cgo darwin CFLAGS: -I${SRCDIR}/../../../.sdk/inspireface-static-darwin-arm64/include -I${SRCDIR}/../../../.sdk/inspireface-static-darwin-arm64/include/inspireface
#cgo darwin LDFLAGS: -L${SRCDIR}/../../../.sdk/inspireface-static-darwin-arm64/lib -lInspireFace -lMNN -lc++
#cgo linux CFLAGS: -I${SRCDIR}/../../../.sdk/inspireface-static-linux-amd64/include -I${SRCDIR}/../../../.sdk/inspireface-static-linux-amd64/include/inspireface
#cgo linux LDFLAGS: -L${SRCDIR}/../../../.sdk/inspireface-static-linux-amd64/lib -lInspireFace -lMNN -lstdc++ -ldl -lpthread

#include <stdlib.h>
#include <string.h>
#include "inspireface.h"
*/
import "C"
import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/lipcoder/face/internal/media"
	"github.com/lipcoder/face/internal/reco"
)

var (
	InitMu        sync.Mutex // sdk初始化锁
	InitStatus    bool       // sdk初始化状态
	FeatureLength int        // sdk特征长度
)

const maxFrameDimension = 16384

// Init 初始化sdk
func Init(packPath string) error {
	if packPath == "" {
		return fmt.Errorf("模型包路径为空")
	}

	InitMu.Lock()
	defer InitMu.Unlock()

	if InitStatus {
		return fmt.Errorf("重复初始化sdk")
	}

	cPath := C.CString(packPath)
	if cPath == nil {
		return fmt.Errorf("模型包路径转换失败")
	}
	defer C.free(unsafe.Pointer(cPath))

	// C.HFLaunchInspireFace 初始化 sdk
	if ret := C.HFLaunchInspireFace(cPath); !cOK(ret) {
		return cError("HFLaunchInspireFace", ret)
	}

	var n C.HInt32 // 人脸特征向量长度
	if ret := C.HFGetFeatureLength(&n); !cOK(ret) {
		_ = C.HFTerminateInspireFace() // 终止sdk，释放资源
		return cError("HFGetFeatureLength", ret)
	}
	if n <= 0 {
		_ = C.HFTerminateInspireFace()
		return fmt.Errorf("无效的特征长度: %d", n)
	}

	FeatureLength = int(n)
	InitStatus = true
	return nil
}

// 摄像头模式
type SessionMode uint8

const (
	ModeImage SessionMode = iota
	ModeVideo
	ModeMonitor
)

// SessionConfig 表示创建一条识别 Session 时的通用配置
type SessionConfig struct {
	Mode              SessionMode // Mode 指定静态图片、普通视频或监控视频场景
	MaxFaces          int         // 单帧最多处理的人脸数量，必须大于 0
	DetectPixelLevel  int         // 检测器输入分辨率等级，-1 表示使用 SDK 默认值 320 ,常见值为 160 / 320 / 640
	MinFacePixels     int         // 需要处理的最小人脸边长，0 表示使用 SDK 默认值
	InputFPS          int         // 实际送入 Session 的帧率,ModeMonitor 必须提供大于 0 的值,0 表示让 SDK 使用默认值（-1，通常按 30 FPS 处理）
	EnableRecognition bool        // 是否启用人脸特征提取能力
	EnableQuality     bool        // 是否计算人脸质量分
}

type Session struct {
	handle            C.HFSession     // inspire的session句柄
	stream            C.HFImageStream // inspire描述当前输入图像的对象,图像数据、宽高、格式、旋转
	buffer            unsafe.Pointer  // Session 自己拥有的 C 图片内存，仅供复制 Go Frame.Data 使用
	bufferCap         int             // Session 自有 buffer 的容量
	maxFaces          int             // 单帧最多处理的人脸数量
	enableRecognition bool            // 是否启用人脸特征提取能力
	enableQuality     bool            // 是否计算人脸质量分
	closed            bool            // 是否已经关闭
}

// NewSession 创建一个新的识别会话
func NewSession(config SessionConfig) (*Session, error) {
	InitMu.Lock()
	if !InitStatus {
		InitMu.Unlock()
		return nil, fmt.Errorf("sdk未初始化")
	}
	InitMu.Unlock()

	if config.MaxFaces <= 0 || config.MinFacePixels < 0 {
		return nil, fmt.Errorf("无效的配置参数")
	}

	// 0 表示由 Go 层使用 SDK 默认值；正数必须是 160 的倍数。
	if config.DetectPixelLevel < 0 ||
		(config.DetectPixelLevel > 0 && config.DetectPixelLevel%160 != 0) {
		return nil, fmt.Errorf("检测器输入分辨率等级必须是 160 的倍数")
	}

	if config.Mode == ModeMonitor && config.InputFPS <= 0 {
		return nil, fmt.Errorf("监控模式下必须提供大于 0 的帧率")
	}

	var detectMode C.HFDetectMode // 检测模式
	switch config.Mode {
	case ModeImage:
		detectMode = C.HF_DETECT_MODE_ALWAYS_DETECT
	case ModeVideo:
		detectMode = C.HF_DETECT_MODE_LIGHT_TRACK
	case ModeMonitor:
		detectMode = C.HF_DETECT_MODE_TRACK_BY_DETECTION
	default:
		return nil, fmt.Errorf("无效的模式: %d", config.Mode)
	}

	// SDK 使用 -1 表示使用默认检测分辨率等级，当前默认值为 320。
	detectPixelLevel := config.DetectPixelLevel
	if detectPixelLevel == 0 {
		detectPixelLevel = -1
	}

	// InputFPS 只对监控模式有效，其余模式直接使用 SDK 默认值。
	inputFPS := -1
	if config.Mode == ModeMonitor {
		inputFPS = config.InputFPS
	}

	// 检测和跟踪是 Session 的基础能力，这里只配置需要额外启用的算法功能。
	option := C.HOption(C.HF_ENABLE_NONE)
	if config.EnableRecognition { // 启用人脸识别功能
		option |= C.HOption(C.HF_ENABLE_FACE_RECOGNITION)
	}
	if config.EnableQuality { // 启用人脸质量分计算功能
		option |= C.HOption(C.HF_ENABLE_QUALITY)
	}

	var handle C.HFSession // inspire的session句柄
	if ret := C.HFCreateInspireFaceSessionOptional(
		option,
		detectMode,
		C.HInt32(config.MaxFaces),
		C.HInt32(detectPixelLevel),
		C.HInt32(inputFPS),
		(*unsafe.Pointer)(unsafe.Pointer(&handle)),
	); !cOK(ret) {
		return nil, cError("HFCreateInspireFaceSessionOptional", ret)
	}

	// InspireFace 描述当前输入图像的对象，
	// 用于保存图像数据、宽高、像素格式、旋转方向等信息。
	var stream C.HFImageStream
	if ret := C.HFCreateImageStreamEmpty(
		(*unsafe.Pointer)(unsafe.Pointer(&stream)),
	); !cOK(ret) {
		_ = C.HFReleaseInspireFaceSession(handle)
		return nil, cError("HFCreateImageStreamEmpty", ret)
	}

	s := &Session{
		handle:            handle,
		stream:            stream,
		maxFaces:          config.MaxFaces,
		enableRecognition: config.EnableRecognition,
		enableQuality:     config.EnableQuality,
	}

	if config.MinFacePixels > 0 {
		// 设置最小人脸尺寸，小于该尺寸的人脸会被过滤
		if ret := C.HFSessionSetFilterMinimumFacePixelSize(
			handle,
			C.HInt32(config.MinFacePixels),
		); !cOK(ret) {
			_ = s.Close()
			return nil, cError("HFSessionSetFilterMinimumFacePixelSize", ret)
		}
	}

	return s, nil
}

// Close 关闭识别会话，释放资源
func (s *Session) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true

	var firsterr error

	// 释放资源的顺序：stream -> buffer -> handle
	if s.stream != nil {
		if ret := C.HFReleaseImageStream(s.stream); !cOK(ret) {
			firsterr = cError("HFReleaseImageStream", ret)
		}
		s.stream = nil
	}

	if s.buffer != nil {
		C.free(s.buffer)
		s.buffer = nil
		s.bufferCap = 0
	}

	if s.handle != nil {
		// firsterr 只记录第一个错误，后续错误会被忽略
		if ret := C.HFReleaseInspireFaceSession(s.handle); !cOK(ret) && firsterr == nil {
			firsterr = cError("HFReleaseInspireFaceSession", ret)
		}
		s.handle = nil
	}

	return firsterr
}

type Face struct {
	TrackID             int64        // 人脸跟踪 ID，唯一标识一张人脸
	TrackCount          int64        // 人脸跟踪帧数，表示该人脸已经连续被跟踪的帧数
	Box                 media.Rect   // 人脸框，表示人脸在图像中的位置和大小
	Angles              media.Angles // 人脸角度，包括翻滚角、偏航角和俯仰角
	DetectionConfidence float32      // 人脸检测置信度，范围 [0, 1]，值越大表示检测结果越可靠
	Quality             float32      // 人脸质量分，范围 [0, 1]，值越大表示人脸质量越好
	Feature             []float32    // 人脸特征向量，长度为 FeatureLength，只有在启用人脸识别功能时才会返回
}

// Process 处理一帧图像。
//
// InspireFace 的 C API 不支持中断正在执行的单次调用，因此 ctx 采用协作式取消：
// 在进入耗时调用前后以及逐张人脸处理时检查取消状态。
func (s *Session) process(ctx context.Context, frame *media.Frame, quality bool, feature bool) ([]Face, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.closed {
		return nil, fmt.Errorf("session已关闭")
	}
	if frame == nil {
		return nil, fmt.Errorf("无效的帧数据")
	}

	// Go 层验证帧数据的有效性，避免传入非法数据给 C 层
	if err := validateFrame(*frame); err != nil {
		return nil, err
	}

	// 把 Go 的像素格式转换成 InspireFace 的枚举
	format, err := cPixelFormat(frame.Format)
	if err != nil {
		return nil, err
	}
	// 把 Go 的旋转角度转换成 InspireFace 的枚举
	rotation, err := cRotation(frame.Rotation)
	if err != nil {
		return nil, err
	}

	// Data 会复制到 Session 自有的 C 内存；外部 native Buffer 则直接使用。
	imageBuffer, err := s.frameBuffer(frame)
	if err != nil {
		return nil, err
	}
	// frameBuffer 不会保存外部 Buffer；它只在本次同步处理返回前使用。
	defer runtime.KeepAlive(frame)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 设置 InspireFace 的图像流对象的属性，包括图像数据、宽高、像素格式和旋转角度
	if ret := C.HFImageStreamSetFormat(s.stream, format); !cOK(ret) {
		return nil, cError("HFImageStreamSetFormat", ret)
	}
	// 设置图像流对象的宽度和高度
	if ret := C.HFImageStreamSetRotation(s.stream, rotation); !cOK(ret) {
		return nil, cError("HFImageStreamSetRotation", ret)
	}

	// 通知图像流对象的图像数据和尺寸，供 InspireFace 进行人脸检测和识别
	if ret := C.HFImageStreamSetBuffer(
		s.stream,
		(*C.uchar)(imageBuffer),
		C.HInt32(frame.Width),
		C.HInt32(frame.Height),
	); !cOK(ret) {
		return nil, cError("HFImageStreamSetBuffer", ret)
	}

	// 开始人脸检测和识别，返回检测到的人脸数据

	// C.HFExecuteFaceTrack 的参数说明：
	//
	//   - detectedNum：检测到的人脸数量
	//   - rects：data.rects 原本只是一个 C 指针，知道后面连续放了 n 个 HFaceRect，所以包装成可以下标访问的 slice , rects[i].x、rects[i].y、rects[i].width、rects[i].height 分别表示第 i 张人脸的左上角坐标和宽高
	//   - trackIds：指向一组 HInt32 的指针，表示每张人脸的跟踪 ID
	//   - trackCounts：指向一组 HInt32 的指针，表示每张人脸的跟踪帧数
	//   - detConfidence：每张人脸的检测置信度
	//   - angles：每张人脸的角度信息，包含偏航角、俯仰角和滚转角, angles.roll[0],angles.yaw[0],angles.pitch[0]
	//   - tokens：每张人脸的 SDK token，用于后续的特征提取和识别
	var data C.HFMultipleFaceData
	if ret := C.HFExecuteFaceTrack(s.handle, s.stream, &data); !cOK(ret) {
		return nil, cError("HFExecuteFaceTrack", ret)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	n := int(data.detectedNum) // 检测到的人脸数量
	if n < 0 {
		return nil, fmt.Errorf("检测到的人脸数量非法: %d", n)
	}
	if n > s.maxFaces {
		return nil, fmt.Errorf("%w: %d", reco.ErrTooManyFaces, n)
	}
	if n == 0 {
		return nil, nil // 没有检测到人脸，直接返回空结果
	}

	if data.rects == nil || data.trackIds == nil ||
		data.angles.roll == nil || data.angles.yaw == nil || data.angles.pitch == nil {
		return nil, fmt.Errorf("检测到的人脸数据非法")
	}
	if (s.enableQuality && quality || s.enableRecognition && feature) && data.tokens == nil {
		return nil, fmt.Errorf("inspireface: face tokens are unavailable")
	}

	if s.enableQuality && quality {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ret := C.HFMultipleFacePipelineProcessOptional(
			s.handle,
			s.stream,
			&data,
			C.HOption(C.HF_ENABLE_QUALITY),
		); !cOK(ret) {
			return nil, cError("HFMultipleFacePipelineProcessOptional", ret)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	rects := unsafe.Slice((*C.HFaceRect)(unsafe.Pointer(data.rects)), n)
	trackIDs := unsafe.Slice((*C.HInt32)(unsafe.Pointer(data.trackIds)), n)

	var trackCounts []C.HInt32
	if data.trackCounts != nil {
		trackCounts = unsafe.Slice((*C.HInt32)(unsafe.Pointer(data.trackCounts)), n)
	}

	var confidences []C.HFloat
	if data.detConfidence != nil {
		confidences = unsafe.Slice((*C.HFloat)(unsafe.Pointer(data.detConfidence)), n)
	}

	var tokens []C.HFFaceBasicToken
	if data.tokens != nil {
		tokens = unsafe.Slice((*C.HFFaceBasicToken)(unsafe.Pointer(data.tokens)), n)
	}

	rolls := unsafe.Slice(
		(*C.HFloat)(unsafe.Pointer(data.angles.roll)),
		n,
	)

	yaws := unsafe.Slice(
		(*C.HFloat)(unsafe.Pointer(data.angles.yaw)),
		n,
	)

	pitches := unsafe.Slice(
		(*C.HFloat)(unsafe.Pointer(data.angles.pitch)),
		n,
	)

	faces := make([]Face, n)

	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		face := Face{
			TrackID: int64(trackIDs[i]),
			Box: media.Rect{
				X:      int(rects[i].x),
				Y:      int(rects[i].y),
				Width:  int(rects[i].width),
				Height: int(rects[i].height),
			},
			Angles: media.Angles{
				Roll:  float32(rolls[i]),
				Yaw:   float32(yaws[i]),
				Pitch: float32(pitches[i]),
			},
		}

		// 如果 trackCounts 里面确实存在第 i 个元素，就把它填进 face.TrackCount
		if i < len(trackCounts) {
			face.TrackCount = int64(trackCounts[i])
		}
		if i < len(confidences) {
			face.DetectionConfidence = float32(confidences[i])
		}

		if s.enableQuality && quality {
			var quality C.HFloat
			if ret := C.HFFaceQualityDetect(s.handle, tokens[i], &quality); !cOK(ret) {
				return nil, cError("HFFaceQualityDetect", ret)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			face.Quality = float32(quality)
		}

		if s.enableRecognition && feature {
			feature := make([]float32, FeatureLength)
			if ret := C.HFFaceFeatureExtractCpy(
				s.handle,
				s.stream,
				tokens[i],
				(*C.float)(unsafe.Pointer(&feature[0])),
			); !cOK(ret) {
				return nil, cError("HFFaceFeatureExtractCpy", ret)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			face.Feature = feature
		}

		faces[i] = face
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return faces, nil
}

// validateFrame 验证帧数据的有效性 Go侧的检查
func validateFrame(frame media.Frame) error {
	if frame.Width <= 0 || frame.Height <= 0 ||
		frame.Width > maxFrameDimension || frame.Height > maxFrameDimension {
		return fmt.Errorf("帧数据非法")
	}
	if frame.Data != nil && frame.Buffer != nil {
		return fmt.Errorf("帧数据非法: Data 和 Buffer 不能同时使用")
	}
	if len(frame.Data) == 0 && frame.Buffer == nil {
		return fmt.Errorf("帧数据非法: Data 和 Buffer 不能同时为空")
	}
	// 非 nil 的空 Data 也不能与 Buffer 混用，避免数据来源语义不明确。
	if frame.Data != nil && len(frame.Data) == 0 {
		return fmt.Errorf("帧数据非法: Data 为空")
	}

	var expected int

	switch frame.Format {
	case media.PixelRGB, media.PixelBGR:
		expected = frame.Width * frame.Height * 3

	case media.PixelRGBA, media.PixelBGRA:
		expected = frame.Width * frame.Height * 4

	case media.PixelNV12, media.PixelNV21, media.PixelI420:
		if frame.Width%2 != 0 || frame.Height%2 != 0 {
			return fmt.Errorf("帧数据非法")
		}
		expected = frame.Width * frame.Height * 3 / 2

	case media.PixelGray:
		expected = frame.Width * frame.Height

	default:
		return fmt.Errorf("不支持的像素格式: %d", frame.Format)
	}

	actual := len(frame.Data)
	if frame.Buffer != nil {
		actual = frame.Size
	}
	if actual != expected {
		return fmt.Errorf(
			"帧数据非法: got=%d want=%d",
			actual,
			expected,
		)
	}
	if _, err := cRotation(frame.Rotation); err != nil {
		return err
	}

	return nil
}

// frameBuffer 返回本次同步处理使用的 native 图像地址。
//
// Buffer 属于外部 source，Session 不保存也不释放；Data 则复制到 Session
// 自己拥有的 C buffer，该内存只会由 Session.Close 释放。
func (s *Session) frameBuffer(frame *media.Frame) (unsafe.Pointer, error) {
	if frame.Buffer != nil {
		return frame.Buffer, nil
	}

	if err := s.ensureBuffer(len(frame.Data)); err != nil {
		return nil, err
	}
	C.memcpy(s.buffer, unsafe.Pointer(&frame.Data[0]), C.size_t(len(frame.Data)))
	runtime.KeepAlive(frame.Data)
	return s.buffer, nil
}

// cPixelFormat 将 Go 的像素格式转换为 InspireFace 的枚举
func cPixelFormat(format media.PixelFormat) (C.HFImageFormat, error) {
	switch format {
	case media.PixelRGB:
		return C.HF_STREAM_RGB, nil
	case media.PixelBGR:
		return C.HF_STREAM_BGR, nil
	case media.PixelRGBA:
		return C.HF_STREAM_RGBA, nil
	case media.PixelBGRA:
		return C.HF_STREAM_BGRA, nil
	case media.PixelNV12:
		return C.HF_STREAM_YUV_NV12, nil
	case media.PixelNV21:
		return C.HF_STREAM_YUV_NV21, nil
	case media.PixelI420:
		return C.HF_STREAM_I420, nil
	case media.PixelGray:
		return C.HF_STREAM_GRAY, nil
	default:
		return 0, fmt.Errorf("不支持的像素格式: %d", format)
	}
}

// cRotation 将 Go 的旋转角度转换为 InspireFace 的枚举
func cRotation(rotation media.Rotation) (C.HFRotation, error) {
	switch rotation {
	case media.Rotation0:
		return C.HF_CAMERA_ROTATION_0, nil
	case media.Rotation90:
		return C.HF_CAMERA_ROTATION_90, nil
	case media.Rotation180:
		return C.HF_CAMERA_ROTATION_180, nil
	case media.Rotation270:
		return C.HF_CAMERA_ROTATION_270, nil
	default:
		return 0, fmt.Errorf("不支持的旋转角度: %d", rotation)
	}
}

// ensureBuffer 确保 给c使用的 buffer 的容量足够大，如果不够大则重新分配
func (s *Session) ensureBuffer(size int) error {
	if size <= 0 {
		return fmt.Errorf("无效大小: %d", size)
	}
	if s.buffer != nil && s.bufferCap >= size {
		return nil // 现有 buffer 足够大，无需重新分配
	}

	// 调用c标准库的函数， realloc 来分配或扩展内存
	p := C.realloc(s.buffer, C.size_t(size))
	if p == nil {
		return fmt.Errorf("内存分配失败: %d 字节", size)
	}
	s.buffer = p
	s.bufferCap = size

	return nil
}

// cOK 判断 InspireFace 的返回值是否表示成功
func cOK(result C.HResult) bool {
	return result == C.HSUCCEED
}

// cError 将 InspireFace 的返回值转换为 Go 的错误类型
func cError(operation string, result C.HResult) error {
	return fmt.Errorf("inspireface: %s failed with code %d", operation, int64(result))
}
