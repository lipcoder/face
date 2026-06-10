package recognition

import (
	"context"
	"errors"
)
var (
	// 参数或配置错误
	ErrInvalidConfig = errors.New("recognition invalid config")
	// 构造状态错误
	ErrInvalidState = errors.New("recognition invalid state")
	
	// 输入图片无效 例如图片 bytes 为空、图片损坏、无法被识别服务解析
	ErrInvalidImage = errors.New("recognition invalid image")

	// 图片太大
	ErrImageTooLarge = errors.New("recognition image too large")

	// 不支持的图片格式
	ErrUnsupportedImageFormat = errors.New("recognition unsupported image format")

	// 构建图片请求失败：
	// 例如 multipart form 创建失败、写入图片 bytes 失败、创建 HTTP request 失败
	ErrBuildImageRequest = errors.New("recognition build image request failed")

	// 请求识别服务失败：
	// 例如网络错误、连接超时、DNS 失败、服务不可达
	ErrRequestFailed = errors.New("recognition request failed")

	// 识别服务响应无效 例如 HTTP 非 200、响应 body 为空、JSON 解析失败、响应结构不符合预期
	ErrInvalidResponse = errors.New("recognition invalid response")

	// 识别服务处理失败 例如服务正常返回 JSON，但 ok=false，表示服务内部识别失败
	ErrRecognitionFailed = errors.New("recognition failed")

	// 没有检测到人脸
	ErrNoFace = errors.New("recognition no face")

	// 人脸数量不符合要求
	ErrNotOneFace = errors.New("recognition not one face")

	// 没有拿到人脸特征 检测到了人脸，但 embedding 为空
	ErrNoFaceEmbedding = errors.New("recognition no face embedding")

	// 人脸特征无效 例如 embedding 维度为 0，或者 embedding 长度和 embedding_dim 不一致
	ErrInvalidEmbedding = errors.New("recognition invalid embedding")

	// 人脸选择策略无效 例如 rank 不是 -1、0、1 这些当前支持的值
	ErrInvalidRank = errors.New("recognition invalid rank")

	// 人脸质量过低
	ErrLowFaceQuality = errors.New("recognition low face quality")
)

type Recognition interface {
	GetFaceEmbedding(ctx context.Context, imgBytes []byte, rank int) ([][]float64, error)
}
