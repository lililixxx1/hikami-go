package download

import (
	"context"
	"errors"
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
		if errors.Is(err, ErrNativeUnsupported) {
			fallback := d.Fallback
			if fallback == nil {
				fallback = YTDLPDownloader{}
			}
			return fallback.Download(ctx, sourceURL, rawDir, cookieFile)
		}
		return err
	}
	return nil
}

// NewConfiguredDownloader 根据配置选择回放下载后端。
func NewConfiguredDownloader(cfg *config.Config) Downloader {
	ytdlp := YTDLPDownloader{Command: cfg.YTDLP, FFprobe: cfg.FFprobe, FFmpeg: cfg.FFmpeg}
	signer := biliutil.NewWBISigner("")
	native := NativeDownloader{Signer: signer, FFprobe: cfg.FFprobe, FFmpeg: cfg.FFmpeg}

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
