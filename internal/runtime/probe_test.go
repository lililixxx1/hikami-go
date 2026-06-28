package runtime

import (
	"testing"

	"hikami-go/internal/config"
)

func TestProbeReportsASRModelAndRequestMode(t *testing.T) {
	cfg := &config.Config{
		FFmpeg:  "ffmpeg",
		FFprobe: "ffprobe",
		YTDLP:   "yt-dlp",
		Rclone:  "rclone",
		DashScope: config.DashScopeConfig{
			Model: "qwen-asr",
		},
	}

	status := Probe(cfg)
	if status.Capabilities.ASRModel != "qwen3-asr-flash-filetrans" {
		t.Fatalf("asr model = %s", status.Capabilities.ASRModel)
	}
	if status.Capabilities.ASRRequestMode != "file_url" {
		t.Fatalf("asr request mode = %s", status.Capabilities.ASRRequestMode)
	}
}

// TestProbeRecapEmptyProviderFallsBack 验证 provider 留空兜底:
// RecapAI.Provider 为空时,probe 视为 openai_compatible,只要有密钥就判可用。
// 这保证用户清空 provider 字段后,运行时已用 DeepSeek 默认,首页能力显示仍绿(codex 审核第 2 条)。
func TestProbeRecapEmptyProviderFallsBack(t *testing.T) {
	t.Setenv("AI_API_KEY", "sk-test")
	cfg := &config.Config{
		FFmpeg:  "ffmpeg",
		FFprobe: "ffprobe",
		YTDLP:   "yt-dlp",
		RecapAI: config.RecapAIConfig{
			Enabled:  true,
			Provider: "", // 留空
			BaseURL:  "", // 留空
			Model:    "", // 留空
		},
	}

	status := Probe(cfg)
	if !status.Capabilities.RecapGenerate {
		t.Fatalf("RecapGenerate = false, want true (empty provider/base_url/model should fall back to DeepSeek default)")
	}
}

// TestProbeRecapEmptyFieldsNoKey 验证留空兜底下,密钥未配置时仍判不可用。
func TestProbeRecapEmptyFieldsNoKey(t *testing.T) {
	t.Setenv("AI_API_KEY", "")
	cfg := &config.Config{
		FFmpeg:  "ffmpeg",
		FFprobe: "ffprobe",
		YTDLP:   "yt-dlp",
		RecapAI: config.RecapAIConfig{Enabled: true},
	}
	status := Probe(cfg)
	if status.Capabilities.RecapGenerate {
		t.Fatalf("RecapGenerate = true, want false (no API key)")
	}
}
