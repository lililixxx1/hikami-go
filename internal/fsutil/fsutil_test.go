package fsutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomic_Success(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := WriteFileAtomic(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("perm = %o, want 0o644", perm)
	}
}

func TestWriteFileAtomic_NoTmpResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFileAtomic(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}

func TestWriteJSONAtomic_Success(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	value := map[string]any{"a": 1, "b": []int{2, 3}}
	if err := WriteJSONAtomic(path, value, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasSuffix(string(got), "\n") {
		t.Errorf("缺少结尾换行")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["a"] != float64(1) {
		t.Errorf("decoded[a] = %v, want 1", decoded["a"])
	}
}

func TestWriteJSONAtomic_MarshalError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	// chan 不可 JSON 序列化，触发 marshal 错误
	if err := WriteJSONAtomic(path, make(chan int), 0o644); err == nil {
		t.Fatal("期望 marshal 错误，得到 nil")
	}
	// marshal 失败不应创建目标文件
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("marshal 失败不应创建目标文件")
	}
}
