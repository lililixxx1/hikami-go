package biliutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadCover(t *testing.T) {
	t.Run("png by content-type", func(t *testing.T) {
		srv := newCoverServer(t, "image/png", "PNG-DATA")
		dir := t.TempDir()
		DownloadCover(context.Background(), srv.Client(), srv.URL, "", dir)
		assertCover(t, dir, ".png", "PNG-DATA")
	})

	t.Run("jpg by content-type", func(t *testing.T) {
		srv := newCoverServer(t, "image/jpeg", "JPG-DATA")
		dir := t.TempDir()
		DownloadCover(context.Background(), srv.Client(), srv.URL, "", dir)
		assertCover(t, dir, ".jpg", "JPG-DATA")
	})

	t.Run("webp by content-type", func(t *testing.T) {
		srv := newCoverServer(t, "image/webp", "WEBP")
		dir := t.TempDir()
		DownloadCover(context.Background(), srv.Client(), srv.URL, "", dir)
		assertCover(t, dir, ".webp", "WEBP")
	})

	t.Run("fallback to url ext when no content-type", func(t *testing.T) {
		srv := newCoverServer(t, "", "DATA")
		dir := t.TempDir()
		DownloadCover(context.Background(), srv.Client(), srv.URL+"/cover.png", "", dir)
		assertCover(t, dir, ".png", "DATA")
	})

	t.Run("empty url skipped", func(t *testing.T) {
		dir := t.TempDir()
		DownloadCover(context.Background(), nil, "  ", "", dir)
		if files, _ := os.ReadDir(dir); len(files) != 0 {
			t.Fatalf("expected no file, got %d", len(files))
		}
	})

	t.Run("non-2xx skipped", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()
		dir := t.TempDir()
		DownloadCover(context.Background(), srv.Client(), srv.URL, "", dir)
		if files, _ := os.ReadDir(dir); len(files) != 0 {
			t.Fatalf("403 should not write file, got %d", len(files))
		}
	})

	t.Run("oversized not written", func(t *testing.T) {
		big := strings.Repeat("x", coverMaxBytes+10)
		srv := newCoverServer(t, "image/png", big)
		dir := t.TempDir()
		DownloadCover(context.Background(), srv.Client(), srv.URL, "", dir)
		if files, _ := os.ReadDir(dir); len(files) != 0 {
			t.Fatalf("oversized cover should not be written, got %d", len(files))
		}
	})
}

func TestCoverExt(t *testing.T) {
	cases := map[string]string{ // key = "contentType|url" -> ext
		"image/png|":          ".png",
		"IMAGE/PNG|":          ".png",
		"image/webp|":         ".webp",
		"image/jpeg|":         ".jpg",
		"|http://x/a.PNG":     ".png",
		"|http://x/a.webp":    ".webp",
		"|http://x/a.jpeg":    ".jpg",
		"|http://x/a.unknown": ".jpg",
		"||":                  ".jpg",
		// codex r18 [P2] 回归：URL 后缀兜底要正确处理 query / fragment，不被 ?token / #frag 污染。
		"|https://x/cover.png?token=abc":      ".png",
		"|https://x/cover.webp?expires=1&k=2": ".webp",
		"|https://x/cover.jpeg#frag":          ".jpg",
		"|https://x/cover.jpg?t=1":            ".jpg",
		"|https://x/path/cover.m4a?x=1":       ".jpg", // 非图片后缀 → 默认 jpg
	}
	for input, want := range cases {
		parts := strings.SplitN(input, "|", 2)
		if got := coverExt(parts[0], parts[1]); got != want {
			t.Errorf("coverExt(%q,%q) = %q, want %q", parts[0], parts[1], got, want)
		}
	}
}

func newCoverServer(t *testing.T, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func assertCover(t *testing.T, dir, ext, wantContent string) {
	t.Helper()
	path := filepath.Join(dir, "cover"+ext)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if string(data) != wantContent {
		t.Errorf("cover content = %q, want %q", string(data), wantContent)
	}
}
