package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lipcoder/face"
)

// prepareRuntime 释放内嵌模型，供按路径加载模型的 SDK 使用。
func prepareRuntime(model string) (dir, modelPath string, err error) {
	if model == "" || filepath.Base(model) != model {
		return "", "", fmt.Errorf("模型参数必须为内嵌模型名称")
	}
	info, err := fs.Stat(face.Files, ".sdk/models/"+model)
	if err != nil || info.IsDir() {
		return "", "", fmt.Errorf("内嵌模型 %q 不存在", model)
	}
	dir, err = os.MkdirTemp("", "face-runtime-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	err = fs.WalkDir(face.Files, ".sdk", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(".sdk", path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		input, err := face.Files.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return dir, "", err
	}
	return dir, filepath.Join(dir, "models", model), nil
}
