package face

import "embed"

// Files 包含人脸识别模型包。
//
//go:embed all:.sdk/models
var Files embed.FS
