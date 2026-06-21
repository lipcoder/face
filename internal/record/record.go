package record

import (
	"errors"
	"time"
)

var (
	// 参数或配置错误
	ErrInvalidConfig = errors.New("record invalid config")

	// 构造状态错误，例如绕过 Init 构造
	ErrInvalidState = errors.New("record invalid state")

	// 输入参数无效：空姓名、非法 face id、非法阈值等
	ErrInvalidInput = errors.New("record invalid input")

	// 人脸特征无效：空 embedding、维度不匹配、包含 NaN/Inf
	ErrInvalidEmbedding = errors.New("record invalid embedding")

	// 存储不可用：数据库打不开、连不上、Ping 失败等
	ErrUnavailable = errors.New("record unavailable")

	// 存储请求失败：SQL 执行、扫描、迭代、写入失败
	ErrRequestFailed = errors.New("record request failed")

	// 未找到人脸：按姓名删除不存在、相似度低于阈值、没有可匹配数据
	ErrNotFound = errors.New("record not found")
)

type FaceDB interface {
	AddFace(name string, embedding []float64) (int64, error)
	DeleteFaceByName(name string) error
	FaceExistsByName(name string) (bool, error)
	ListFaceNames() ([]string, error)
	SearchFaceByEmbedding(
		embedding []float64,
		threshold float64,
	) (FaceMatch, error)
}

type FaceMatch struct {
	ID         int64
	Name       string
	Similarity float64
}

type SignLog struct {
	FaceID         int64
	FaceSimilarity float64
	RecognizedAt   time.Time
}

type Record interface {
	RecordSignLog(faceID int64, faceSimilarity float64) error
}
