package upload

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"hikami-go/internal/config"
)

func TestJoinWebDAVPath_MultipleParts(t *testing.T) {
	// 多部分路径应使用 / 拼接
	got := joinWebDAVPath("base", "channel", "session", "file.mp4")
	if got != "base/channel/session/file.mp4" {
		t.Fatalf("path = %q", got)
	}
}

func TestJoinWebDAVPath_Slashes(t *testing.T) {
	// 前后斜杠应被清理
	got := joinWebDAVPath("/base/", "/channel/", "session/")
	if got != "base/channel/session" {
		t.Fatalf("path = %q", got)
	}
}

func TestJoinWebDAVPath_EmptyParts(t *testing.T) {
	// 空部分应被过滤
	got := joinWebDAVPath("", "base", "", "file.mp4")
	if got != "base/file.mp4" {
		t.Fatalf("path = %q", got)
	}
}

func TestPathDir_WithSlash(t *testing.T) {
	// 含斜杠路径应返回父目录
	got := pathDir("base/channel/file.mp4")
	if got != "base/channel" {
		t.Fatalf("dir = %q", got)
	}
}

func TestPathDir_NoSlash(t *testing.T) {
	// 无斜杠路径应返回空目录
	got := pathDir("file.mp4")
	if got != "" {
		t.Fatalf("dir = %q", got)
	}
}

func TestRelativeTarget_Normal(t *testing.T) {
	// 正常路径应去除 basePath 前缀
	copier := &WebDAVCopier{basePath: "base"}
	got := copier.relativeTarget("base/channel/session")
	if got != "channel/session" {
		t.Fatalf("target = %q", got)
	}
}

func TestRelativeTarget_EmptyBasePath(t *testing.T) {
	// basePath 为空时返回清理后的目标路径
	copier := &WebDAVCopier{}
	got := copier.relativeTarget("/channel/session/")
	if got != "channel/session" {
		t.Fatalf("target = %q", got)
	}
}

func TestRelativeTarget_MatchingBasePath(t *testing.T) {
	// target 等于 basePath 时返回空路径
	copier := &WebDAVCopier{basePath: "base"}
	got := copier.relativeTarget("base")
	if got != "" {
		t.Fatalf("target = %q", got)
	}
}

func TestIsWebDAVNotExist_OSNotExist(t *testing.T) {
	// os.ErrNotExist 返回 true
	if !isWebDAVNotExist(os.ErrNotExist) {
		t.Fatalf("expected true for os.ErrNotExist")
	}
	if !isWebDAVNotExist(&os.PathError{Op: "stat", Path: "missing", Err: os.ErrNotExist}) {
		t.Fatalf("expected true for path error")
	}
}

func TestIsWebDAVNotExist_OtherError(t *testing.T) {
	// 其他错误返回 false
	if isWebDAVNotExist(errors.New("other")) {
		t.Fatalf("expected false for other error")
	}
}

func TestWebDAVCopierTimeoutApplied(t *testing.T) {
	// M13:服务端挂起无响应时,客户端必须在注入的超时处中断,不再永久阻塞。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.WebDAVConfig{URL: srv.URL, Username: "u", Password: "p"}
	copier := newWebDAVCopierWithTimeout(cfg, 50*time.Millisecond)

	start := time.Now()
	err := copier.Fetch(context.Background(), "slug", t.TempDir())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error from slow server")
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("request should abort at ~50ms injected timeout, took %v", elapsed)
	}
}
