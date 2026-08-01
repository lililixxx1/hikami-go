package download

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// TestAutoDownloaderFallbackOnDeadlineExceeded 验证 2026-07-31 修正:
// native 返回超时类错误（context.DeadlineExceeded）时也应 fallback 到 yt-dlp,
// 而非直接把错误抛给用户（原实现只在 ErrNativeUnsupported 时 fallback）。
func TestAutoDownloaderFallbackOnDeadlineExceeded(t *testing.T) {
	native := &stubDownloader{err: context.DeadlineExceeded}
	fallback := &stubDownloader{writer: func(rawDir string) error {
		return os.WriteFile(filepath.Join(rawDir, "audio.m4a"), []byte("fallback"), 0o644)
	}}
	rawDir := t.TempDir()
	err := (AutoDownloader{Native: native, Fallback: fallback}).Download(context.Background(), "source", rawDir, "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if native.calls != 1 || fallback.calls != 1 {
		t.Fatalf("calls native=%d fallback=%d, want 1/1", native.calls, fallback.calls)
	}
	assertFileContent(t, filepath.Join(rawDir, "audio.m4a"), "fallback")
}

// TestAutoDownloaderFallbackOnStalled 验证 native 返回 errStalled（节点卡死无进度）时 fallback。
func TestAutoDownloaderFallbackOnStalled(t *testing.T) {
	native := &stubDownloader{err: errStalled}
	fallback := &stubDownloader{writer: func(rawDir string) error {
		return os.WriteFile(filepath.Join(rawDir, "audio.m4a"), []byte("fallback"), 0o644)
	}}
	rawDir := t.TempDir()
	err := (AutoDownloader{Native: native, Fallback: fallback}).Download(context.Background(), "source", rawDir, "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if native.calls != 1 || fallback.calls != 1 {
		t.Fatalf("calls native=%d fallback=%d, want 1/1", native.calls, fallback.calls)
	}
}

// TestAutoDownloaderNoFallbackOnBusinessError 验证非瞬态错误（含 "http status 404"）不 fallback。
// 这是关键防护：404 这类业务错误换链路也没用，不能无脑 fallback。
func TestAutoDownloaderNoFallbackOnBusinessError(t *testing.T) {
	nativeErr := fmt.Errorf("audio download failed: http status 404")
	native := &stubDownloader{err: nativeErr}
	fallback := &stubDownloader{}
	err := (AutoDownloader{Native: native, Fallback: fallback}).Download(context.Background(), "source", t.TempDir(), "")
	if err == nil {
		t.Fatal("err = nil, want nativeErr")
	}
	if !errors.Is(err, nativeErr) && err.Error() != nativeErr.Error() {
		t.Fatalf("err = %v, want nativeErr %v", err, nativeErr)
	}
	if fallback.calls != 0 {
		t.Fatalf("fallback.calls = %d, want 0 (business error must not fallback)", fallback.calls)
	}
}

// TestIsTransientNetErr 表驱动验证瞬态错误判定。
func TestIsTransientNetErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadline_exceeded", context.DeadlineExceeded, true},
		{"stalled", errStalled, true},
		{"connection_reset", fmt.Errorf("read tcp: connection reset by peer"), true},
		{"timeout", fmt.Errorf("dial tcp: i/o timeout"), true},
		{"eof", fmt.Errorf("unexpected EOF"), true},
		{"http_404", fmt.Errorf("http status 404"), false},
		{"generic", fmt.Errorf("native failed"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientNetErr(tt.err); got != tt.want {
				t.Errorf("isTransientNetErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
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

// TestDownloadAudioErrPreservesCause 验证 Codex #1(走真实 downloadAudio 函数路径):
// downloadAudio 用 %w: %w 包装后,上层能用 errors.Is 穿透 cause 链识别 errStalled,
// 而非依赖脆弱的字符串匹配。用真实 httptest.Server 返回卡住的 body 触发 stall。
func TestDownloadAudioErrPreservesCause(t *testing.T) {
	abort := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("x")) // 发一点数据让 header 写出
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-abort:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(abort)
		srv.Close()
	}()

	// 用真实 audio HTTP client(走 DefaultTransport),让 downloadAudio 经完整链路触发 stall。
	// downloadAudio 内部用 audioHTTPClientOrDefault(nil),故 HTTPClient 留 nil 即用默认 client。
	// 配短 stall(1s)加速测试,仍验证真实函数路径的 cause 链穿透。
	d := NativeDownloader{PerURLStallSeconds: 1, PerURLMaxMinutes: -1}
	targetPath := filepath.Join(t.TempDir(), "audio.m4a")

	// downloadAudio 需要直接拿音频 URL;这里把 server URL 当音频直链(绕过 metadata 解析,
	// 直接测 downloadAudio 的下载+stall 逻辑)。
	err := d.downloadAudio(context.Background(), []string{srv.URL}, "", targetPath)
	if err == nil {
		t.Fatal("downloadAudio should fail with stall, got nil")
	}
	// 核心断言:外层 ErrAudioDownloadFailed + 内层 errStalled 都能被 errors.Is 穿透。
	if !errors.Is(err, ErrAudioDownloadFailed) {
		t.Errorf("errors.Is(ErrAudioDownloadFailed) = false, err=%v", err)
	}
	if !errors.Is(err, errStalled) {
		t.Errorf("errors.Is(errStalled) = false (cause 链丢失), err=%v", err)
	}
}

// TestClassifyNativeErr 验证 Codex #3 的日志脱敏分类函数。
func TestClassifyNativeErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"stalled", errStalled, "stalled"},
		{"deadline", context.DeadlineExceeded, "deadline_exceeded"},
		{"unsupported", ErrNativeUnsupported, "unsupported_source"},
		{"other", fmt.Errorf("connection reset"), "transient_network_error"},
		{"nil", nil, "transient_network_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyNativeErr(tt.err); got != tt.want {
				t.Errorf("classifyNativeErr(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
