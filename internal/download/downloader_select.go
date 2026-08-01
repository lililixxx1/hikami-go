package download

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/config"
)

// AutoDownloader 在 native 不支持当前链接时自动回退到 yt-dlp。
type AutoDownloader struct {
	Native   Downloader
	Fallback Downloader
}

func (d AutoDownloader) Download(ctx context.Context, sourceURL string, rawDir string, cookieFile string) error {
	native := d.Native
	if native == nil {
		native = NativeDownloader{}
	}
	if err := native.Download(ctx, sourceURL, rawDir, cookieFile); err != nil {
		// native 失败后 fallback 到 yt-dlp 的条件：
		//   ① ErrNativeUnsupported（原行为，链接不适合 native）；
		//   ② 瞬态网络/超时类错误（2026-07-31 新增）：native 链路本身出问题（节点卡死、context 超时等），
		//      yt-dlp 是独立链路，换它重试有成功可能。不匹配 404 等业务错误（换链路也没用）。
		if errors.Is(err, ErrNativeUnsupported) || isTransientNetErr(err) {
			fallback := d.Fallback
			if fallback == nil {
				fallback = YTDLPDownloader{}
			}
			// 脱敏:不记录原始 err.Error()——native 的网络错误(net/url.Error)常含
			// 带签名/期限的 CDN URL,写入 journald 会泄露敏感参数。只记分类后的原因。
			slog.Info("download fallback to yt-dlp after native failure",
				"reason", classifyNativeErr(err))
			return fallback.Download(ctx, sourceURL, rawDir, cookieFile)
		}
		return err
	}
	return nil
}

// classifyNativeErr 把 native 错误归类为简短原因字符串(用于日志,不含 URL 等敏感信息)。
func classifyNativeErr(err error) string {
	switch {
	case errors.Is(err, errStalled):
		return "stalled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, ErrNativeUnsupported):
		return "unsupported_source"
	default:
		return "transient_network_error"
	}
}

// isTransientNetErr 判断是否为可重试的瞬态网络/超时类错误（值得换 yt-dlp 再试一次）。
// 排除业务错误（如 http status 404、cookie 缺失）——这些换链路也无解。
func isTransientNetErr(err error) bool {
	if err == nil {
		return false
	}
	// 优先用 errors.Is 穿透 cause 链(配合 downloadAudio 的 %w: %w 包装)。
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, errStalled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	// 网络中断/连接重置/EOF 等
	if strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "transport connection") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline exceeded") {
		return true
	}
	return false
}

// NewConfiguredDownloader 根据配置选择回放下载后端。
func NewConfiguredDownloader(cfg *config.Config) Downloader {
	ytdlp := YTDLPDownloader{Command: cfg.YTDLP, FFprobe: cfg.FFprobe, FFmpeg: cfg.FFmpeg}
	signer := biliutil.NewWBISigner("")
	native := NativeDownloader{
		Signer:             signer,
		FFprobe:            cfg.FFprobe,
		FFmpeg:             cfg.FFmpeg,
		PerURLStallSeconds: cfg.Downloader.PerURLStallSeconds,
		PerURLMaxMinutes:   cfg.Downloader.PerURLMaxMinutes,
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Downloader.Backend)) {
	case "", "auto":
		return AutoDownloader{Native: native, Fallback: ytdlp}
	case "native":
		return native
	case "ytdlp":
		return ytdlp
	default:
		return AutoDownloader{Native: native, Fallback: ytdlp}
	}
}
