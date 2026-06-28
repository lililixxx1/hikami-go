package asr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

func newTempAudioServer(t *testing.T) *TempAudioServer {
	t.Helper()
	return NewTempAudioServer(&config.Config{
		ASRTemp: config.ASRTempConfig{
			LocalDir:      t.TempDir(),
			PublicBaseURL: "https://example.com/asr-temp",
		},
	})
}

func TestLocalPath_NormalPath(t *testing.T) {
	// 正常对象路径解析成功
	server := newTempAudioServer(t)
	path, err := server.localPath("ch1/session1/audio.asr.mp3")
	if err != nil {
		t.Fatalf("localPath: %v", err)
	}
	if !strings.HasPrefix(path, server.cfg.ASRTemp.LocalDir) {
		t.Fatalf("path = %q, want under %q", path, server.cfg.ASRTemp.LocalDir)
	}
}

func TestLocalPath_DirectoryTraversal(t *testing.T) {
	// ../ 被 filepath.Clean("/"+path) 规范化，路径安全地落在 localDir 下
	server := newTempAudioServer(t)
	path, err := server.localPath("../audio.asr.mp3")
	if err != nil {
		t.Fatalf("localPath: %v", err)
	}
	if !strings.HasPrefix(path, server.cfg.ASRTemp.LocalDir) {
		t.Fatalf("path = %q, want under %q", path, server.cfg.ASRTemp.LocalDir)
	}
}

func TestLocalPath_AbsoluteTraversal(t *testing.T) {
	// 绝对路径被规范化，安全地落在 localDir 下
	server := newTempAudioServer(t)
	path, err := server.localPath(filepath.Join(string(os.PathSeparator), "tmp", "audio.asr.mp3"))
	if err != nil {
		t.Fatalf("localPath: %v", err)
	}
	if !strings.HasPrefix(path, server.cfg.ASRTemp.LocalDir) {
		t.Fatalf("path = %q, want under %q", path, server.cfg.ASRTemp.LocalDir)
	}
}

func TestPublish_Success(t *testing.T) {
	// 发布成功后目标文件存在，URL 和对象路径格式正确
	server := newTempAudioServer(t)
	source := filepath.Join(t.TempDir(), "audio.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	publicURL, objectPath, err := server.Publish(context.Background(), source, session.Session{
		ID:        "session1",
		ChannelID: "ch1",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if objectPath != "ch1/session1/audio.asr.mp3" {
		t.Fatalf("objectPath = %q", objectPath)
	}
	if publicURL != "https://example.com/asr-temp/ch1/session1/audio.asr.mp3" {
		t.Fatalf("publicURL = %q", publicURL)
	}
	target, err := server.localPath(objectPath)
	if err != nil {
		t.Fatalf("localPath: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("stat target: %v", err)
	}
}

func TestPublish_ContextCancelled(t *testing.T) {
	// 上下文取消应返回错误
	server := newTempAudioServer(t)
	source := filepath.Join(t.TempDir(), "audio.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := server.Publish(ctx, source, session.Session{ID: "session1", ChannelID: "ch1"}); err == nil {
		t.Fatalf("expected error for cancelled context")
	}
}

func TestPublish_SourceNotExist(t *testing.T) {
	// 源文件不存在应返回错误
	server := newTempAudioServer(t)
	source := filepath.Join(t.TempDir(), "missing.mp3")
	if _, _, err := server.Publish(context.Background(), source, session.Session{ID: "session1", ChannelID: "ch1"}); err == nil {
		t.Fatalf("expected error for missing source")
	}
}

func TestDelete_Success(t *testing.T) {
	// 删除成功后文件不存在
	server := newTempAudioServer(t)
	objectPath := "ch1/session1/audio.asr.mp3"
	target, err := server.localPath(objectPath)
	if err != nil {
		t.Fatalf("localPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := server.Delete(context.Background(), objectPath); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected target deleted, got err %v", err)
	}
}

func TestDelete_AlreadyDeleted(t *testing.T) {
	// 文件已不存在不返回错误
	server := newTempAudioServer(t)
	if err := server.Delete(context.Background(), "ch1/session1/audio.asr.mp3"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestDelete_CleanupEmptyParents(t *testing.T) {
	// 删除后空父目录应被清理
	server := newTempAudioServer(t)
	objectPath := "ch1/session1/audio.asr.mp3"
	target, err := server.localPath(objectPath)
	if err != nil {
		t.Fatalf("localPath: %v", err)
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(target, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := server.Delete(context.Background(), objectPath); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("expected empty parent cleanup, got err %v", err)
	}
}

func TestMountHandler_ServesFile(t *testing.T) {
	// HTTP 服务应返回临时音频文件内容
	server := newTempAudioServer(t)
	path := filepath.Join(server.cfg.ASRTemp.LocalDir, "ch1", "session1", "audio.asr.mp3")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir file dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	httpServer := httptest.NewServer(server.MountHandler())
	t.Cleanup(httpServer.Close)

	resp, err := http.Get(httpServer.URL + "/asr-temp/ch1/session1/audio.asr.mp3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "audio" {
		t.Fatalf("body = %q, want audio", string(body))
	}
}
