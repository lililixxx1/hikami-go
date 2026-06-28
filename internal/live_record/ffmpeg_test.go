package live_record

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFFmpegRecorderPipesRemoteStreamWithHeaders(t *testing.T) {
	var gotCookie string
	var gotReferer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotReferer = r.Header.Get("Referer")
		_, _ = w.Write([]byte("flv-bytes"))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	ffmpegPath := filepath.Join(tempDir, "fake-ffmpeg")
	argsPath := filepath.Join(tempDir, "args.txt")
	script := `#!/bin/sh
if [ -n "$HIKAMI_FFMPEG_ARGS_FILE" ]; then
  printf '%s\n' "$@" > "$HIKAMI_FFMPEG_ARGS_FILE"
fi
out=""
for arg in "$@"; do
  out="$arg"
done
cat > "$out"
`
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	t.Setenv("HIKAMI_FFMPEG_ARGS_FILE", argsPath)

	outputPath := filepath.Join(tempDir, "audio.m4a")
	recorder := &FFmpegRecorder{Command: ffmpegPath}
	err := recorder.Record(context.Background(), StreamInfo{
		URL: server.URL + "/live.flv?token=1",
		Headers: map[string]string{
			"Cookie":  "SESSDATA=test",
			"Referer": "https://live.bilibili.com/",
		},
	}, outputPath)
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(content) != "flv-bytes" {
		t.Fatalf("output = %q", string(content))
	}
	if gotCookie != "SESSDATA=test" {
		t.Fatalf("cookie header = %q", gotCookie)
	}
	if gotReferer != "https://live.bilibili.com/" {
		t.Fatalf("referer header = %q", gotReferer)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	argsText := string(args)
	if !strings.Contains(argsText, "-i\npipe:0\n") {
		t.Fatalf("ffmpeg args do not use stdin pipe:\n%s", argsText)
	}
	if !strings.Contains(argsText, "-f\nflv\n") {
		t.Fatalf("ffmpeg args do not force flv demuxer:\n%s", argsText)
	}
	if strings.Contains(argsText, server.URL) {
		t.Fatalf("ffmpeg args should not contain remote url:\n%s", argsText)
	}
	for _, want := range []string{
		"-fflags\n+discardcorrupt\n",
		"-err_detect\nignore_err\n",
		"-avoid_negative_ts\nmake_zero\n",
	} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("ffmpeg args missing %q:\n%s", want, argsText)
		}
	}
}

func TestBuildFFmpegArgsDoesNotForceFLVForNonFLV(t *testing.T) {
	args := strings.Join(buildFFmpegArgs(StreamInfo{URL: "https://example.com/live.m3u8"}, "audio.m4a"), "\n")
	if strings.Contains(args, "-f\nflv") {
		t.Fatalf("ffmpeg args should not force flv for non-flv stream:\n%s", args)
	}
}

func TestBuildFFmpegArgsDoesNotOverwritePartFiles(t *testing.T) {
	args := buildFFmpegArgs(StreamInfo{URL: "https://example.com/live.flv"}, "audio.part.1.m4a")
	if len(args) == 0 || args[0] != "-n" {
		t.Fatalf("part file overwrite flag = %q, want -n", args[0])
	}

	args = buildFFmpegArgs(StreamInfo{URL: "https://example.com/live.flv"}, "audio.m4a")
	if len(args) == 0 || args[0] != "-y" {
		t.Fatalf("primary file overwrite flag = %q, want -y", args[0])
	}
}

func TestFFmpegRecorderStopGracePeriod(t *testing.T) {
	recorder := &FFmpegRecorder{StopGracePeriod: 3 * time.Second}
	if got := recorder.stopGracePeriod(); got != 3*time.Second {
		t.Fatalf("stop grace = %v, want 3s", got)
	}
	recorder = &FFmpegRecorder{}
	if got := recorder.stopGracePeriod(); got != 10*time.Second {
		t.Fatalf("default stop grace = %v, want 10s", got)
	}
}
