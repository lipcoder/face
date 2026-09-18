// Package contrast defines the local face-feature repository contract.
package database

import (
	"errors"
)

const (
	StudentIDLength         = 10
	FeatureLength           = 512
	FaceSimilarityThreshold = 0.8
)

var (
	ErrNotFound         = errors.New("人员不存在")
	ErrInvalidStudentID = errors.New("学号必须为10位数字")
	ErrInvalidName      = errors.New("姓名不能为空")
	ErrInvalidFeature   = errors.New("人脸特征必须为512维")
)

type Person struct {
	ID       int64
	PersonID string
	Name     string
	Feature  []float32
}
