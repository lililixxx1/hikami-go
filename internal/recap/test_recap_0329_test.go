package recap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/glossary"
	"hikami-go/internal/session"

	_ "modernc.org/sqlite"
)

func TestGenerateRecap_0329(t *testing.T) {
	packageDir := "/home/cc/codex/huizeman/sessions/26.03.29/package"
	outputDir := "/home/cc/hikami-go/test-recap"

	// API key 仅从环境变量读取，缺失则跳过测试，避免硬编码凭证。
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skipf("未设置 DEEPSEEK_API_KEY，跳过测试")
	}
	if _, err := os.Stat(packageDir); os.IsNotExist(err) {
		t.Skipf("测试数据目录不存在: %s", packageDir)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("创建输出目录失败: %v", err)
	}

	os.Setenv("AI_API_KEY", apiKey)
	defer os.Unsetenv("AI_API_KEY")

	cfg := &config.Config{
		RecapAI: config.RecapAIConfig{
			Enabled:             true,
			Provider:            "openai_compatible",
			APIKeyEnv:           "AI_API_KEY",
			BaseURL:             "https://api.deepseek.com",
			Model:               "deepseek-chat",
			MaxTokens:           16384,
			TimeoutSeconds:      600,
			EnableSummarization: true,
		},
	}

	provider := NewConfiguredProvider(cfg)

	// Load transcript
	transcript, err := os.ReadFile(filepath.Join(packageDir, "transcript.txt"))
	if err != nil {
		t.Fatalf("读取转写文件失败: %v", err)
	}
	t.Logf("转写文件: %d 字节", len(transcript))

	sessionInfo := session.Session{
		ID:        "26.03.29",
		Slug:      "26.03.29",
		ChannelID: "huizeman",
		Title:     "灰泽满 午台杂谈",
		StartedAt: "2026-03-29T13:00:00+08:00",
		Status:    "asr_done",
	}

	glossaryDB, _ := createInMemoryDB(t)
	glossaryStore := glossary.NewStore(glossaryDB)
	seedHuizemanGlossary(t, glossaryStore)

	h := &Handler{
		cfg:           cfg,
		glossaryStore: glossaryStore,
	}

	// Glossary correction
	rules, err := buildCorrectionRules(context.Background(), glossaryStore, sessionInfo.ChannelID)
	if err != nil {
		t.Fatalf("构建术语规则失败: %v", err)
	}
	if timed, err := correctedTimedTranscriptFromPackageDir(packageDir, rules); err == nil && len(timed.text) > 0 {
		transcript = timed.text
		h.writeCorrectedTranscriptArtifacts(outputDir, nil, transcript, timed.report)
	} else {
		corrected, applied := correctTextWithRules(string(transcript), rules)
		transcript = []byte(corrected)
		h.writeCorrectedTranscriptArtifacts(outputDir, nil, transcript, newCorrectionReport("transcript.txt", applied))
	}
	t.Logf("校正后转写: %d 字节", len(transcript))

	// Load danmaku
	danmakuData, err := loadAndConvertDanmaku(filepath.Join(packageDir, "danmaku.json"), outputDir)
	if err != nil {
		t.Logf("弹幕加载失败: %v，跳过弹幕分析", err)
	} else {
		t.Logf("弹幕加载成功")
	}

	// Load metadata
	var durationMs int64
	meta := readSessionMetadata(filepath.Dir(packageDir))
	if meta != nil {
		durationMs = meta.DurationMs
	} else {
		metaData, err := os.ReadFile(filepath.Join(packageDir, "metadata.json"))
		if err == nil {
			var m struct {
				DurationMs int64 `json:"duration_ms"`
			}
			if json.Unmarshal(metaData, &m) == nil {
				durationMs = m.DurationMs
			}
		}
	}
	if durationMs > 0 {
		durMin := durationMs / 60000
		t.Logf("时长: %d小时%d分钟", durMin/60, durMin%60)
	}

	// Analyze danmaku
	var stats *danmakuStats
	if danmakuData != nil {
		stats, _ = analyzeDanmaku(danmakuData, durationMs)
		if stats != nil {
			t.Logf("弹幕统计: 总数=%d 独立用户=%d 去重=%d 高权重=%d 平均/分钟=%.1f",
				stats.TotalCount, stats.UniqueUsers, stats.UniqueTexts,
				len(stats.HighWeightDanmaku), stats.AvgPerMin)
		}
	}

	// Use 粉丝向精修 preset
	preset := BuiltinPresets[0] // 粉丝向精修
	resolved := &ResolvedTemplate{
		SystemPrompt: preset.SystemPrompt,
		UserFormat:   preset.UserFormat,
		FanName:      "绿冻们",
	}

	vars := &TemplateVars{
		ChannelName: "灰泽满",
		ChannelID:   "huizeman",
		Slug:        "26.03.29",
		Date:        "2026.03.29",
		DateTime:    "2026-03-29T13:00:00+08:00",
		Title:       "午台杂谈",
		FanName:     "绿冻们",
	}
	if durationMs > 0 {
		durMin := int(durationMs / 60000)
		vars.DurationMin = durMin
		vars.Duration = fmt.Sprintf("%d小时%d分钟", durMin/60, durMin%60)
	}
	if stats != nil {
		vars.DanmakuCount = stats.TotalCount
		vars.UniqueUsers = stats.UniqueUsers
		vars.AvgPerMin = fmt.Sprintf("%.1f", stats.AvgPerMin)
	}

	prompt := h.buildPrompt(context.Background(), sessionInfo, transcript, stats, meta, resolved, vars, nil)
	t.Logf("Prompt 长度: %d 字节", len(prompt))

	// Save prompt
	os.WriteFile(filepath.Join(outputDir, "live-recap.prompt.md"), []byte(prompt), 0644)

	// Generate recap
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	t.Log("开始生成回顾 (DeepSeek API)...")
	start := time.Now()
	result, err := provider.Generate(ctx, resolved.SystemPrompt, prompt, sessionInfo)
	if err != nil {
		t.Fatalf("API 调用失败: %v", err)
	}
	recap := result.Content
	raw := result.Raw
	recap = applyGlossaryCorrections(ctx, glossaryStore, sessionInfo.ChannelID, recap)
	recap = ensureFinalAddressSection(recap)
	elapsed := time.Since(start)
	t.Logf("生成完成，耗时 %s，回顾 %d 字节", elapsed, len(recap))

	// Append programmatic danmaku statistics
	if stats != nil && stats.TotalCount > 0 {
		statsSection := FormatDanmakuStats(stats, vars)
		recap = appendDanmakuStats(recap, statsSection)
	}

	// Save outputs
	os.WriteFile(filepath.Join(outputDir, "live-recap.raw.json"), []byte(raw), 0644)

	outputFile := filepath.Join(outputDir, "直播回顾_26.03.29.md")
	if err := os.WriteFile(outputFile, []byte(recap), 0644); err != nil {
		t.Fatalf("保存回顾失败: %v", err)
	}

	// Extract suggested terms
	if recap != "" {
		terms := extractSuggestedTerms(recap)
		if len(terms) > 0 {
			suggData, _ := json.Marshal(terms)
			_ = os.WriteFile(filepath.Join(outputDir, "suggested_terms.json"), suggData, 0644)
			t.Logf("提取术语建议: %d 条", len(terms))
		}
	}

	t.Logf("回顾已保存到: %s", outputFile)
	t.Logf("文件大小: %d 字节", len(recap))
}
