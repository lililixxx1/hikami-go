// Package fsutil 提供文件原子写入辅助，采用临时文件 + rename 策略，
// 避免异常中断留下半成品产物（符合项目"标准产物原子写入"规范）。
package fsutil

import (
	"encoding/json"
	"os"
)

// WriteFileAtomic 以临时文件 + rename 原子写入字节数据。
// 写入或 rename 失败时清理临时文件，避免残留。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// WriteJSONAtomic 将 value 序列化为缩进 JSON 后原子写入。
// 序列化错误会被返回（不再被吞），确保异常值不会写出空/损坏文件。
func WriteJSONAtomic(path string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return WriteFileAtomic(path, data, perm)
}
