package download

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"hikami-go/internal/config"
)

func TestNewConfiguredDownloader(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		want    any
	}{
		{name: "auto", backend: "auto", want: AutoDownloader{}},
		{name: "native", backend: "native", want: NativeDownloader{}},
		{name: "ytdlp", backend: "ytdlp", want: YTDLPDownloader{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewConfiguredDownloader(&config.Config{
				YTDLP:      "yt-dlp-custom",
				FFprobe:    "ffprobe-custom",
				FFmpeg:     "ffmpeg-custom",
				Downloader: config.DownloaderConfig{Backend: tt.backend},
			})
			switch tt.want.(type) {
			case AutoDownloader:
				if _, ok := got.(AutoDownloader); !ok {
					t.Fatalf("got %T, want AutoDownloader", got)
				}
			case NativeDownloader:
				if _, ok := got.(NativeDownloader); !ok {
					t.Fatalf("got %T, want NativeDownloader", got)
				}
			case YTDLPDownloader:
				ytdlp, ok := got.(YTDLPDownloader)
				if !ok {
					t.Fatalf("got %T, want YTDLPDownloader", got)
				}
				if ytdlp.Command != "yt-dlp-custom" || ytdlp.FFprobe != "ffprobe-custom" || ytdlp.FFmpeg != "ffmpeg-custom" {
					t.Fatalf("unexpected ytdlp config: %+v", ytdlp)
				}
			}
		})
	}
}

func TestAutoDownloaderFallbackOnNativeUnsupported(t *testing.T) {
	native := &stubDownloader{err: ErrNativeUnsupported}
	fallback := &stubDownloader{writer: func(rawDir string) error {
		return os.WriteFile(filepath.Join(rawDir, "audio.m4a"), []byte("fallback"), 0o644)
	}}
	rawDir := t.TempDir()
	err := (AutoDownloader{Native: native, Fallback: fallback}).Download(context.Background(), "source", rawDir, "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if native.calls != 1 || fallback.calls != 1 {
		t.Fatalf("calls native=%d fallback=%d", native.calls, fallback.calls)
	}
	assertFileContent(t, filepath.Join(rawDir, "audio.m4a"), "fallback")
}

func TestAutoDownloaderNoFallbackOnOtherError(t *testing.T) {
	nativeErr := fmt.Errorf("native failed")
	native := &stubDownloader{err: nativeErr}
	fallback := &stubDownloader{}
	err := (AutoDownloader{Native: native, Fallback: fallback}).Download(context.Background(), "source", t.TempDir(), "")
	if !errors.Is(err, nativeErr) {
		t.Fatalf("err = %v, want nativeErr", err)
	}
	if native.calls != 1 || fallback.calls != 0 {
		t.Fatalf("calls native=%d fallback=%d", native.calls, fallback.calls)
	}
}

type stubDownloader struct {
	err    error
	calls  int
	writer func(rawDir string) error
}

func (d *stubDownloader) Download(_ context.Context, _ string, rawDir string, _ string) error {
	d.calls++
	if d.writer != nil {
		if err := d.writer(rawDir); err != nil {
			return err
		}
	}
	return d.err
}
