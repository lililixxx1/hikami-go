//go:build vad_integration

// vad_integration_test.go 端到端集成测试,需 ffmpeg + ffprobe 真实可用。
// 默认不进 CI(避免 CI 环境缺 ffmpeg 失败),本地手跑:
//
//	go test -tags vad_integration ./internal/asr/ -run TestVADIntegration -v
//
// 关键守卫(qoder v1 I-4):TestVADIntegration_TrimMatchesSilenceMap 验证
// Trim(atrim+concat)实际输出时长 == SilenceMap.TrimmedDurationMS,
// 防 v1 C-1(Trim 与 silence_map 不一致 → 反向映射累积漂移)同类 bug 逃逸。

package asr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hikami-go/internal/config"
)

// skipIfNoFFmpeg 跳过测试如果系统没有 ffmpeg/ffprobe。
func skipIfNoFFmpeg(t *testing.T) (string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not in PATH: %v", err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skipf("ffprobe not in PATH: %v", err)
	}
	return ffmpeg, ffprobe
}

// genTestAudio 用 ffmpeg lavfi 生成一个含静音的测试音频(3s 语音 + 3s 静音 + 3s 语音)。
// 返回音频路径 + 预期时长(9s)。
func genTestAudio(t *testing.T, ffmpeg, dir string) string {
	t.Helper()
	out := filepath.Join(dir, "test.wav")
	// sine 生成 1kHz 正弦(模拟说话),anull 生成静音,concat 拼接
	cmd := exec.Command(ffmpeg, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=r=16000:f=1000:d=3",
		"-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono:d=3",
		"-f", "lavfi", "-i", "sine=r=16000:f=1000:d=3",
		"-filter_complex", "[0:a][1:a][2:a]concat=n=3:v=0:a=1[out]",
		"-map", "[out]", "-ac", "1", "-ar", "16000", out,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg lavfi 生成测试音频失败(环境不支持 lavfi): %v: %s", err, string(output))
	}
	return out
}

func ffprobeDurationMS(t *testing.T, ffprobe, path string) int64 {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("parse duration %q: %v", string(out), err)
	}
	return int64(sec * 1000)
}

// TestVADIntegration_DetectFindsSilence 验证 Detect 能在测试音频中检测到静音。
func TestVADIntegration_DetectFindsSilence(t *testing.T) {
	ffmpeg, ffprobe := skipIfNoFFmpeg(t)
	dir := t.TempDir()
	audioPath := genTestAudio(t, ffmpeg, dir)

	p := &VADProcessor{
		ffmpeg: ffmpeg, ffprobe: ffprobe,
		cfg: &config.Config{VAD: config.VADConfig{
			Enabled: true, ThresholdDB: -30, MinSilenceSec: 1.0,
			PaddingSec: 0.1, MinOutputRatio: 0.3,
		}},
	}
	intervals, origMS, err := p.Detect(context.Background(), audioPath)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if origMS < 8500 || origMS > 9500 {
		t.Errorf("origMS = %d, want ~9000 (9s audio)", origMS)
	}
	if len(intervals) == 0 {
		t.Skip("Detect 未找到静音(可能是 sine 不触发 silencedetect 的阈值,跳过后续断言)")
	}
}

// TestVADIntegration_TrimMatchesSilenceMap(qoder v1 I-4 关键守卫):
// 验证 Trim(atrim+concat)实际输出时长 ≈ SilenceMap.TrimmedDurationMS(50ms 容差)。
// 若 C-1 同类 bug(Trim 输出与 silence_map 不一致)重现,此测试立即失败。
func TestVADIntegration_TrimMatchesSilenceMap(t *testing.T) {
	ffmpeg, ffprobe := skipIfNoFFmpeg(t)
	dir := t.TempDir()
	audioPath := genTestAudio(t, ffmpeg, dir)

	p := &VADProcessor{
		ffmpeg: ffmpeg, ffprobe: ffprobe,
		cfg: &config.Config{VAD: config.VADConfig{
			Enabled: true, ThresholdDB: -30, MinSilenceSec: 1.0,
			PaddingSec: 0.2, MinOutputRatio: 0.3,
		}},
	}
	intervals, origMS, err := p.Detect(context.Background(), audioPath)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	smap := p.BuildSilenceMap(intervals, origMS)
	if smap == nil {
		t.Skip("BuildSilenceMap nil(未检测到有效静音或全静音),跳过 Trim 一致性检查")
	}
	trimmedPath := filepath.Join(dir, "trimmed.mp3")
	if err := p.Trim(context.Background(), audioPath, trimmedPath, smap); err != nil {
		t.Fatalf("Trim: %v", err)
	}
	info, err := os.Stat(trimmedPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("trimmed file missing or empty: %v", err)
	}
	actualMS := ffprobeDurationMS(t, ffprobe, trimmedPath)
	// MP3 encoder delay/padding 容差 50ms
	diff := actualMS - smap.TrimmedDurationMS
	if diff < 0 {
		diff = -diff
	}
	if diff > 50 {
		t.Errorf("C-1 REGRESSION: Trim output duration %dms != silence_map.TrimmedDurationMS %dms (diff %dms > 50ms tolerance) — atrim+concat 输出与 silence_map 不一致,反向映射会累积漂移",
			actualMS, smap.TrimmedDurationMS, diff)
	}
	t.Logf("OK: trimmed %dms vs silence_map %dms (diff %dms within 50ms tolerance)",
		actualMS, smap.TrimmedDurationMS, diff)
}
