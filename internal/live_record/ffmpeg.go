package live_record

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hikami-go/internal/executil"
)

type FFmpegRecorder struct {
	Command         string
	StopGracePeriod time.Duration
	HTTPClient      *http.Client
}

func (r *FFmpegRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	command := r.Command
	if command == "" {
		command = "ffmpeg"
	}
	args := buildFFmpegArgs(stream, outputPath)
	logFFmpegStarted(ctx, command, args, stream, outputPath)

	response, err := r.openStream(ctx, stream)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	cmd := exec.CommandContext(ctx, command, args...)
	executil.HideWindow(cmd) // 桌面模式下抑制派生子进程的黑色控制台窗口闪现（与下方 cmd.Cancel 正交）
	// 优雅停止：context 取消时发 SIGTERM 而非 SIGKILL，让 ffmpeg 写完容器头
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = r.stopGracePeriod()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open ffmpeg stdin: %w", err)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg record: %w", err)
	}
	copyErrCh := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(stdin, response.Body)
		closeErr := stdin.Close()
		if copyErr != nil {
			copyErrCh <- copyErr
			return
		}
		copyErrCh <- closeErr
	}()

	waitErr := cmd.Wait()
	logFFmpegExited(ctx, outputPath, waitErr)
	_ = response.Body.Close()
	copyErr := <-copyErrCh
	if waitErr != nil {
		return fmt.Errorf("ffmpeg record failed: %w: %s", waitErr, output.String())
	}
	if copyErr != nil && ctx.Err() == nil {
		return fmt.Errorf("copy live stream to ffmpeg: %w", copyErr)
	}
	return nil
}

func buildFFmpegArgs(stream StreamInfo, outputPath string) []string {
	args := []string{
		recordOutputOverwriteFlag(outputPath),
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+discardcorrupt",
		"-err_detect", "ignore_err",
	}
	if isLikelyFLV(stream.URL) {
		args = append(args, "-f", "flv")
	}
	args = append(args,
		"-i", "pipe:0",
		"-avoid_negative_ts", "make_zero",
		"-vn",
		"-c:a", "copy",
		outputPath,
	)
	return args
}

func recordOutputOverwriteFlag(outputPath string) string {
	if strings.Contains(filepath.Base(outputPath), ".part.") {
		return "-n"
	}
	return "-y"
}

func (r *FFmpegRecorder) stopGracePeriod() time.Duration {
	if r.StopGracePeriod > 0 {
		return r.StopGracePeriod
	}
	return 10 * time.Second
}

func (r *FFmpegRecorder) openStream(ctx context.Context, stream StreamInfo) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, stream.URL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range stream.Headers {
		if value == "" {
			continue
		}
		request.Header.Set(key, value)
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "Mozilla/5.0 Hikami-Go")
	}
	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("open live stream: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		return nil, fmt.Errorf("open live stream: http status %d", response.StatusCode)
	}
	return response, nil
}

func logFFmpegStarted(ctx context.Context, command string, args []string, stream StreamInfo, outputPath string) {
	slog.Info("ffmpeg record started",
		"channel_id", ctx.Value(liveRecordChannelIDKey),
		"session_id", ctx.Value(liveRecordSessionIDKey),
		"command", command,
		"args", args,
		"stream_url", redactURL(stream.URL),
		"output_path", outputPath)
}

func logFFmpegExited(ctx context.Context, outputPath string, err error) {
	slog.Info("ffmpeg process exited",
		"channel_id", ctx.Value(liveRecordChannelIDKey),
		"session_id", ctx.Value(liveRecordSessionIDKey),
		"status", ffmpegExitStatus(err),
		"output_path", outputPath)
}

func ffmpegExitStatus(err error) string {
	if err == nil {
		return "success"
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Sprintf("exit_code_%d", exitErr.ExitCode())
	}
	return err.Error()
}

func isLikelyFLV(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err == nil {
		return strings.Contains(strings.ToLower(parsed.Path), ".flv")
	}
	return strings.Contains(strings.ToLower(rawURL), ".flv")
}
