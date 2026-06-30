package biliutil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// coverMaxBytes 限制单张封面大小。超过则视为异常响应，不写出（避免写出截断的坏图）。
const coverMaxBytes = 20 * 1024 * 1024 // 20MB

// coverHTTPClient 是 DownloadCover 在 client 入参为 nil 时使用的默认客户端（带超时），
// 避免无超时的外部请求挂住调用方。
var coverHTTPClient = &http.Client{Timeout: 30 * time.Second}

// DownloadCover 下载 B 站封面图到 rawDir/cover.<ext>。
// url 为空直接返回（跳过）；client 为 nil 用带 30s 超时的默认客户端。
// 复用 setBiliHeaders 设置 UA / Referer(https://www.bilibili.com) / Cookie，满足 B 站封面防盗链。
// 任何失败（HTTP 非 2xx、读取错误、超限）仅 slog.Warn，不返回错误、不阻断调用方主流程。
// 扩展名按 Content-Type(image/png|jpeg|webp) 决定，URL 后缀兜底，默认 .jpg。
func DownloadCover(ctx context.Context, client HTTPDoer, url, cookieHeader, rawDir string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	if client == nil {
		client = coverHTTPClient
	}
	if err := downloadCoverToDir(ctx, client, url, cookieHeader, rawDir); err != nil {
		slog.Warn("download cover failed", "url", url, "raw_dir", rawDir, "error", err)
	}
}

func downloadCoverToDir(ctx context.Context, client HTTPDoer, url, cookieHeader, rawDir string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create cover request: %w", err)
	}
	setBiliHeaders(req, cookieHeader)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cover request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cover http status %d", resp.StatusCode)
	}

	// 读 maxBytes+1：若超过上限则视为异常，不写出截断的坏图。
	data, err := io.ReadAll(io.LimitReader(resp.Body, coverMaxBytes+1))
	if err != nil {
		return fmt.Errorf("read cover: %w", err)
	}
	if len(data) > coverMaxBytes {
		return fmt.Errorf("cover exceeds max size %d bytes (got %d), skipped", coverMaxBytes, len(data))
	}

	ext := coverExt(resp.Header.Get("Content-Type"), url)
	path := filepath.Join(rawDir, "cover"+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write cover: %w", err)
	}
	slog.Info("cover downloaded", "url", url, "path", path, "bytes", len(data))
	return nil
}

// coverExt 按 Content-Type 决定扩展名，缺失或不识别时回退到 URL 路径后缀，最终默认 .jpg。
// 回退时用 url.Parse 取 Path 的扩展名，正确处理 query/fragment
// （如 https://x/cover.webp?token=... 仍取 .webp，而非被 ?token 污染）。codex r18 [P2]。
func coverExt(contentType, rawURL string) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return ".jpg"
	}
	// 从 URL 提取路径后缀：先解析，失败则退回手动剥 ? / #。
	suffix := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		suffix = u.Path
	} else if i := strings.IndexAny(rawURL, "?#"); i >= 0 {
		suffix = rawURL[:i]
	}
	switch strings.ToLower(filepath.Ext(suffix)) {
	case ".png":
		return ".png"
	case ".webp":
		return ".webp"
	case ".jpg", ".jpeg":
		return ".jpg"
	}
	return ".jpg"
}
