package ins

/*
//
// CFLAGS 告诉 Go 编译器去哪里找 inspireface.h。
// 这里用的是相对路径，所以这个包默认按当前仓库结构编译：
// 默认使用仓库内 command/build_sdk.sh 生成的 .sdk 目录。
// 从当前包 internal/recognition/ins 往上三级就是项目根目录。
//
// LDFLAGS 告诉 Go 链接器要链接 libInspireFace.so。
// rpath 指向仓库内 .sdk，macOS 下运行 CLI 时不需要额外设置 DYLD_LIBRARY_PATH。
#cgo CFLAGS: -I${SRCDIR}/../../../.sdk/inspireface-sdk/include -I${SRCDIR}/../../../.sdk/inspireface-sdk/include/inspireface
#cgo darwin LDFLAGS: -L${SRCDIR}/../../../.sdk/inspireface-sdk/lib -Wl,-rpath,${SRCDIR}/../../../.sdk/inspireface-sdk/lib -lInspireFace
#cgo linux LDFLAGS: -L${SRCDIR}/../../../.sdk/inspireface-sdk/lib -Wl,-rpath,${SRCDIR}/../../../.sdk/inspireface-sdk/lib -lInspireFace
#include <stdlib.h>
#include "inspireface.h"
*/
import "C"

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"lipcoder/face/internal/recognition"
	"math"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"
)

const frameSetSize = 25

// FeatureFlags 控制创建 InspireFace session 时启用哪些能力。
// 当前三个业务函数至少需要 Recognition 和 Quality；情绪接口还需要 Emotion。
type FeatureFlags struct {
	Emotion     bool
	Quality     bool
	Recognition bool
}

// Config 是 C SDK 初始化和 session 创建参数。
type Config struct {
	// PackPath 是模型包路径，例如 /opt/models/Megatron。
	// NewInspire 传空字符串时，会读取 INSPIREFACE_PACK_PATH 环境变量。
	PackPath string
	// MaxFaces 是单张图最多检测多少张脸。
	MaxFaces int
	// DetectPixelLevel 是检测尺寸，值越大通常越准但越慢。
	DetectPixelLevel int
	// MinFacePixels 过滤过小的人脸。
	MinFacePixels int
	// SessionCount 是底层 InspireFace session 数量。并发高时可以调大。
	SessionCount int
	Features     FeatureFlags
	// QualityThreshold 用于 25 帧接口筛选可用于聚合 embedding 的帧。
	QualityThreshold float64
	// EmbeddingFrameCount 是 25 帧里最多选择多少个高质量 embedding 做聚合。
	EmbeddingFrameCount int
}

type Option func(*Config)

// Inspire 是这个包的主对象。
//
// 用法示例：
//
//	client, err := ins.NewInspire("/opt/models/Megatron")
//	if err != nil { return err }
//	defer client.Close()
//
//	result, err := client.AnalyzePhoto(ctx, imgBytes)
//
// 一个 Inspire 对象内部持有 C SDK 的全局初始化状态和若干 session。
// 用完必须 Close，否则底层 C 资源不会释放。
type Inspire struct {
	cfg           Config
	sessions      []*sessionSlot
	rr            atomic.Uint64
	featureLength int
	closed        atomic.Bool
}

type sessionSlot struct {
	mu sync.Mutex
	// session 是 C.HFSession。这里用 unsafe.Pointer 保存，调用 C API 时再转回去。
	session unsafe.Pointer
}

type decodedImage struct {
	width  int
	height int
	bgr    []byte
}

type analyzeOptions struct {
	emotion bool
}

var _ recognition.Analyzer = (*Inspire)(nil)

// NewInspire 初始化 InspireFace C SDK，并创建一组可复用的 session。
//
// packPath 是模型包路径；传空时使用环境变量 INSPIREFACE_PACK_PATH。
// 如果上层是容器部署，通常在容器里设置：
//
//	ENV INSPIREFACE_PACK_PATH=/opt/models/Megatron
//
// 注意：cgo 需要在构建和运行时都能找到 libInspireFace.so。
// 构建时用 CGO_LDFLAGS，运行时用 LD_LIBRARY_PATH。
func NewInspire(packPath string, opts ...Option) (*Inspire, error) {
	cfg := defaultConfig()
	if packPath == "" {
		packPath = os.Getenv("INSPIREFACE_PACK_PATH")
	}
	cfg.PackPath = packPath
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	// Go string 不能直接传给 C。C.CString 会在 C 堆上复制一份，以 '\0' 结尾。
	// 这块内存需要手动 C.free，否则会泄漏。
	cPath := C.CString(cfg.PackPath)
	defer C.free(unsafe.Pointer(cPath))
	if r := C.HFLaunchInspireFace(cPath); !cOK(r) {
		return nil, fmt.Errorf("%w: HFLaunchInspireFace=%d", recognition.ErrInvalidState, int(r))
	}

	inspire := &Inspire{cfg: cfg}
	if err := inspire.queryFeatureLength(); err != nil {
		_ = inspire.Close()
		return nil, err
	}
	if err := inspire.createSessions(); err != nil {
		_ = inspire.Close()
		return nil, err
	}
	return inspire, nil
}

func WithMaxFaces(v int) Option {
	return func(c *Config) { c.MaxFaces = v }
}

func WithDetectPixelLevel(v int) Option {
	return func(c *Config) { c.DetectPixelLevel = v }
}

func WithMinFacePixels(v int) Option {
	return func(c *Config) { c.MinFacePixels = v }
}

func WithSessionCount(v int) Option {
	return func(c *Config) { c.SessionCount = v }
}

func WithFeatures(v FeatureFlags) Option {
	return func(c *Config) { c.Features = v }
}

func WithQualityThreshold(v float64) Option {
	return func(c *Config) { c.QualityThreshold = v }
}

func WithEmbeddingFrameCount(v int) Option {
	return func(c *Config) { c.EmbeddingFrameCount = v }
}

// Close 释放 session，并终止 InspireFace 全局状态。
// 调用 Close 后，这个 Inspire 对象不能再继续使用。
func (a *Inspire) Close() error {
	if a == nil || a.closed.Swap(true) {
		return nil
	}
	for _, slot := range a.sessions {
		if slot != nil && slot.session != nil {
			C.HFReleaseInspireFaceSession(C.HFSession(slot.session))
			slot.session = nil
		}
	}
	a.sessions = nil
	if r := C.HFTerminateInspireFace(); !cOK(r) {
		return fmt.Errorf("%w: HFTerminateInspireFace=%d", recognition.ErrInvalidState, int(r))
	}
	return nil
}

// AnalyzePhoto 分析单张 jpeg/png 图片。
//
// imgBytes 是完整图片文件内容，不是裸 RGB/BGR 像素。
// 返回的 Box 是扁平数组，每张脸 4 个 float64：[x, y, width, height]。
func (a *Inspire) AnalyzePhoto(ctx context.Context, imgBytes []byte) (*recognition.FaceResult, error) {
	faces, err := a.analyzeImage(ctx, imgBytes, analyzeOptions{})
	if err != nil {
		return nil, err
	}
	return faceResult(faces), nil
}

// AnalyzePhotoEmotion 分析单张 jpeg/png 图片，并额外返回情绪结果。
// Emotion 的顺序与 Box/Embedding 的人脸顺序一致。
func (a *Inspire) AnalyzePhotoEmotion(ctx context.Context, imgBytes []byte) (*recognition.EmotionResult, error) {
	faces, err := a.analyzeImage(ctx, imgBytes, analyzeOptions{emotion: true})
	if err != nil {
		return nil, err
	}
	base := faceResult(faces)
	emotions := make([]recognition.Emotion, 0, len(faces))
	for _, face := range faces {
		emotions = append(emotions, face.Emotion)
	}
	return &recognition.EmotionResult{
		FaceCount: base.FaceCount,
		Box:       base.Box,
		Quality:   base.Quality,
		Embedding: base.Embedding,
		Emotion:   emotions,
	}, nil
}

// AnalyzeFrameSet 分析固定 25 张连续帧。
//
// 约束：
//   - frames 必须正好 25 张；
//   - 每一帧必须刚好检测到 1 张脸；
//   - 只会选择质量分 >= QualityThreshold 的帧参与 embedding 聚合。
//
// 返回值仍然是 FaceResult，但 FaceCount 固定为 1，Embedding 里只有一个聚合后的向量。
func (a *Inspire) AnalyzeFrameSet(ctx context.Context, frames [][]byte) (*recognition.FaceResult, error) {
	if err := a.validate(ctx); err != nil {
		return nil, err
	}
	if len(frames) != frameSetSize {
		return nil, recognition.ErrInvalidFrameCount
	}

	type frameFace struct {
		index int
		face  recognition.Face
	}
	candidates := make([]frameFace, 0, len(frames))
	for i, frame := range frames {
		faces, err := a.analyzeImage(ctx, frame, analyzeOptions{})
		if err != nil {
			return nil, err
		}
		if len(faces) != 1 {
			return nil, recognition.ErrNotOneFace
		}
		candidates = append(candidates, frameFace{index: i, face: faces[0]})
	}

	// 25 帧里可能有模糊、偏头或曝光不好的帧。这里先按质量阈值过滤，再按质量排序。
	selected := make([]frameFace, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.face.Quality >= a.cfg.QualityThreshold {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		return nil, recognition.ErrLowFaceQuality
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].face.Quality > selected[j].face.Quality
	})
	if len(selected) > a.cfg.EmbeddingFrameCount {
		selected = selected[:a.cfg.EmbeddingFrameCount]
	}

	embeddings := make([][]float64, 0, len(selected))
	for _, candidate := range selected {
		if len(candidate.face.Embedding) == 0 {
			return nil, recognition.ErrNoFaceEmbedding
		}
		embeddings = append(embeddings, candidate.face.Embedding)
	}
	// 多帧 embedding 先分别 L2 归一化，再平均，再归一化，得到一个更稳定的向量。
	embedding, ok := aggregateEmbeddings(embeddings)
	if !ok {
		return nil, recognition.ErrInvalidEmbedding
	}

	best := selected[0].face
	return &recognition.FaceResult{
		FaceCount: 1,
		Box:       best.Box,
		Quality:   best.Quality,
		Embedding: [][]float64{embedding},
	}, nil
}

func (a *Inspire) analyzeImage(ctx context.Context, imgBytes []byte, opts analyzeOptions) ([]recognition.Face, error) {
	if err := a.validate(ctx); err != nil {
		return nil, err
	}
	if len(imgBytes) == 0 {
		return nil, recognition.ErrInvalidImage
	}
	img, err := decodeBGR(imgBytes)
	if err != nil {
		return nil, err
	}
	return a.analyzeDecoded(ctx, img, opts)
}

func (a *Inspire) analyzeDecoded(ctx context.Context, img decodedImage, opts analyzeOptions) ([]recognition.Face, error) {
	slot := a.nextSession()
	if slot == nil {
		return nil, recognition.ErrInvalidState
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// HFImageData 会传给 C API。不能把 Go slice 的底层指针放进 C 结构体再传给 C，
	// Go 1.26 会报 "Go pointer to unpinned Go pointer"。这里复制到 C 堆内存，
	// 用完后手动释放，符合 cgo 指针规则。
	cBGR := C.CBytes(img.bgr)
	defer C.free(cBGR)

	var stream unsafe.Pointer
	data := C.HFImageData{
		data:     (*C.uchar)(cBGR),
		width:    C.HInt32(img.width),
		height:   C.HInt32(img.height),
		format:   C.HF_STREAM_BGR,
		rotation: C.HF_CAMERA_ROTATION_0,
	}
	if r := C.HFCreateImageStream(&data, (*unsafe.Pointer)(unsafe.Pointer(&stream))); !cOK(r) {
		return nil, fmt.Errorf("%w: HFCreateImageStream=%d", recognition.ErrRecognitionFailed, int(r))
	}
	// C API 创建出来的 stream 也要手动释放。
	defer C.HFReleaseImageStream(C.HFImageStream(stream))

	var faceData C.HFMultipleFaceData
	if r := C.HFExecuteFaceTrack(C.HFSession(slot.session), C.HFImageStream(stream), &faceData); !cOK(r) {
		return nil, fmt.Errorf("%w: HFExecuteFaceTrack=%d", recognition.ErrRecognitionFailed, int(r))
	}
	n := int(faceData.detectedNum)
	if n <= 0 {
		return nil, nil
	}

	if opts.emotion && a.cfg.Features.Emotion {
		if r := C.HFMultipleFacePipelineProcessOptional(C.HFSession(slot.session), C.HFImageStream(stream), &faceData, C.HOption(C.HF_ENABLE_FACE_EMOTION)); !cOK(r) {
			return nil, fmt.Errorf("%w: HFMultipleFacePipelineProcessOptional=%d", recognition.ErrRecognitionFailed, int(r))
		}
	}

	emotions := make([]int, n)
	for i := range emotions {
		emotions[i] = -1
	}
	if opts.emotion && a.cfg.Features.Emotion {
		var er C.HFFaceEmotionResult
		if r := C.HFGetFaceEmotionResult(C.HFSession(slot.session), &er); !cOK(r) {
			return nil, fmt.Errorf("%w: HFGetFaceEmotionResult=%d", recognition.ErrRecognitionFailed, int(r))
		}
		if er.emotion != nil {
			values := unsafe.Slice((*C.HInt32)(unsafe.Pointer(er.emotion)), min(n, int(er.num)))
			for i, v := range values {
				emotions[i] = int(v)
			}
		}
	}

	// faceData 里的数组由 C SDK 管理。unsafe.Slice 只是把 C 指针包装成 Go slice 方便读取，
	// 不要 append、不要保存到函数外长期使用。
	rects := unsafe.Slice((*C.HFaceRect)(unsafe.Pointer(faceData.rects)), n)
	var tokens []C.HFFaceBasicToken
	if faceData.tokens != nil {
		tokens = unsafe.Slice((*C.HFFaceBasicToken)(unsafe.Pointer(faceData.tokens)), n)
	}
	var detConfidence []C.float
	if faceData.detConfidence != nil {
		detConfidence = unsafe.Slice((*C.float)(unsafe.Pointer(faceData.detConfidence)), n)
	}

	faces := make([]recognition.Face, 0, n)
	for i := 0; i < n; i++ {
		face := recognition.Face{
			Box: []float64{
				float64(rects[i].x),
				float64(rects[i].y),
				float64(rects[i].width),
				float64(rects[i].height),
			},
		}
		if len(detConfidence) > i {
			face.DetScore = float64(detConfidence[i])
		}
		if a.cfg.Features.Quality && len(tokens) > i {
			var q C.HFloat
			if r := C.HFFaceQualityDetect(C.HFSession(slot.session), tokens[i], &q); !cOK(r) {
				return nil, fmt.Errorf("%w: HFFaceQualityDetect=%d", recognition.ErrRecognitionFailed, int(r))
			}
			face.Quality = float64(q)
		}
		if opts.emotion && emotions[i] >= 0 {
			face.Emotion = recognition.Emotion{ID: emotions[i], Label: emotionLabel(emotions[i])}
		}
		if a.cfg.Features.Recognition && len(tokens) > i && a.featureLength > 0 {
			// HFFaceFeatureExtractCpy 会把 C 侧 embedding 拷贝到我们提供的 buffer。
			// 这里先用 []C.float 接收，再转换成上层统一使用的 []float64。
			embedding := make([]C.float, a.featureLength)
			if r := C.HFFaceFeatureExtractCpy(C.HFSession(slot.session), C.HFImageStream(stream), tokens[i], (*C.float)(unsafe.Pointer(&embedding[0]))); !cOK(r) {
				return nil, fmt.Errorf("%w: HFFaceFeatureExtractCpy=%d", recognition.ErrRecognitionFailed, int(r))
			}
			face.Embedding = make([]float64, len(embedding))
			for j, v := range embedding {
				face.Embedding[j] = float64(v)
			}
		}
		faces = append(faces, face)
	}
	return faces, ctx.Err()
}

func (a *Inspire) queryFeatureLength() error {
	var featureLength C.HInt32
	if r := C.HFGetFeatureLength(&featureLength); !cOK(r) || featureLength <= 0 {
		return fmt.Errorf("%w: HFGetFeatureLength=%d", recognition.ErrInvalidState, int(r))
	}
	a.featureLength = int(featureLength)
	return nil
}

func (a *Inspire) createSessions() error {
	for i := 0; i < a.cfg.SessionCount; i++ {
		var session unsafe.Pointer
		if r := C.HFCreateInspireFaceSessionOptional(
			a.cfg.Features.option(),
			C.HF_DETECT_MODE_ALWAYS_DETECT,
			C.HInt32(a.cfg.MaxFaces),
			C.HInt32(a.cfg.DetectPixelLevel),
			-1,
			(*unsafe.Pointer)(unsafe.Pointer(&session)),
		); !cOK(r) {
			return fmt.Errorf("%w: HFCreateInspireFaceSessionOptional=%d", recognition.ErrInvalidState, int(r))
		}
		C.HFSessionSetFilterMinimumFacePixelSize(C.HFSession(session), C.HInt32(a.cfg.MinFacePixels))
		C.HFSessionSetTrackPreviewSize(C.HFSession(session), C.HInt32(a.cfg.DetectPixelLevel))
		a.sessions = append(a.sessions, &sessionSlot{session: session})
	}
	return nil
}

func (a *Inspire) nextSession() *sessionSlot {
	if len(a.sessions) == 0 {
		return nil
	}
	return a.sessions[int(a.rr.Add(1)-1)%len(a.sessions)]
}

func (a *Inspire) validate(ctx context.Context) error {
	if a == nil || a.closed.Load() || len(a.sessions) == 0 {
		return recognition.ErrInvalidState
	}
	if ctx == nil {
		return recognition.ErrInvalidConfig
	}
	return ctx.Err()
}

func (f FeatureFlags) option() C.HOption {
	opt := C.HOption(C.HF_ENABLE_NONE)
	if f.Recognition {
		opt |= C.HOption(C.HF_ENABLE_FACE_RECOGNITION)
	}
	if f.Quality {
		opt |= C.HOption(C.HF_ENABLE_QUALITY)
	}
	if f.Emotion {
		opt |= C.HOption(C.HF_ENABLE_FACE_EMOTION)
	}
	return opt
}

func defaultConfig() Config {
	return Config{
		MaxFaces:            5,
		DetectPixelLevel:    320,
		MinFacePixels:       32,
		SessionCount:        1,
		Features:            FeatureFlags{Recognition: true, Quality: true, Emotion: true},
		QualityThreshold:    0.45,
		EmbeddingFrameCount: 5,
	}
}

func validateConfig(cfg Config) error {
	if cfg.PackPath == "" || cfg.MaxFaces <= 0 || cfg.DetectPixelLevel <= 0 || cfg.MinFacePixels < 0 || cfg.SessionCount <= 0 {
		return recognition.ErrInvalidConfig
	}
	if cfg.EmbeddingFrameCount <= 0 || cfg.QualityThreshold < 0 {
		return recognition.ErrInvalidConfig
	}
	return nil
}

func decodeBGR(data []byte) (decodedImage, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return decodedImage{}, fmt.Errorf("%w: %w", recognition.ErrInvalidImage, err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return decodedImage{}, recognition.ErrInvalidImage
	}
	bgr := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			offset := (y*width + x) * 3
			bgr[offset] = byte(b >> 8)
			bgr[offset+1] = byte(g >> 8)
			bgr[offset+2] = byte(r >> 8)
		}
	}
	return decodedImage{width: width, height: height, bgr: bgr}, nil
}

func faceResult(faces []recognition.Face) *recognition.FaceResult {
	result := &recognition.FaceResult{FaceCount: int64(len(faces))}
	for _, face := range faces {
		result.Box = append(result.Box, face.Box...)
		result.Embedding = append(result.Embedding, face.Embedding)
		if face.Quality > result.Quality {
			result.Quality = face.Quality
		}
	}
	return result
}

func aggregateEmbeddings(embeddings [][]float64) ([]float64, bool) {
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return nil, false
	}
	dim := len(embeddings[0])
	sum := make([]float64, dim)
	for _, embedding := range embeddings {
		if len(embedding) != dim {
			return nil, false
		}
		normalized := l2Normalize(embedding)
		for i, v := range normalized {
			sum[i] += v
		}
	}
	for i := range sum {
		sum[i] /= float64(len(embeddings))
	}
	return l2Normalize(sum), true
}

func l2Normalize(values []float64) []float64 {
	out := append([]float64(nil), values...)
	sum := 0.0
	for _, v := range out {
		sum += v * v
	}
	if sum <= 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
		return out
	}
	inv := 1.0 / math.Sqrt(sum)
	for i := range out {
		out[i] *= inv
	}
	return out
}

func emotionLabel(value int) string {
	switch value {
	case 0:
		return "neutral"
	case 1:
		return "happy"
	case 2:
		return "sad"
	case 3:
		return "surprise"
	case 4:
		return "fear"
	case 5:
		return "disgust"
	case 6:
		return "anger"
	default:
		return "unknown"
	}
}

func cOK(r C.HResult) bool {
	return r == C.HSUCCEED
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
