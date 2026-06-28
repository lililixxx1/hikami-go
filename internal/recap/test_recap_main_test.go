package recap

import (
	"context"
	"database/sql"
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

func createInMemoryDB(t *testing.T) (*sql.DB, error) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	// Run glossary migrations
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS glossary_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT NOT NULL DEFAULT '',
			term TEXT NOT NULL,
			canonical TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(channel_id, term)
		);
		CREATE TABLE IF NOT EXISTS glossary_meta (
			channel_id TEXT PRIMARY KEY,
			note TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
	`)
	if err != nil {
		return nil, err
	}
	return db, nil
}

// TestGenerateRecapFromRealData generates a recap using the actual recap module
// with DeepSeek API. Run with: go test -run TestGenerateRecapFromRealData -v -timeout 10m
func TestGenerateRecapFromRealData(t *testing.T) {
	packageDir := "/home/lioi/下载/codex/huizeman/sessions/26.04.22/package"
	outputDir := "/tmp/test-recap-26.04.22"
	deepseekAPIKey := os.Getenv("AI_API_KEY")
	if deepseekAPIKey == "" {
		t.Skip("AI_API_KEY 环境变量未设置")
	}

	if _, err := os.Stat(packageDir); os.IsNotExist(err) {
		t.Skipf("测试数据目录不存在: %s", packageDir)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("创建输出目录失败: %v", err)
	}

	// Set API key in env for provider
	os.Setenv("AI_API_KEY", deepseekAPIKey)
	defer os.Unsetenv("AI_API_KEY")

	cfg := &config.Config{
		RecapAI: config.RecapAIConfig{
			Enabled:             true,
			Provider:            "openai_compatible",
			APIKeyEnv:           "AI_API_KEY",
			BaseURL:             "https://api.deepseek.com",
			Model:               "deepseek-v4-pro",
			MaxTokens:           16384,
			TimeoutSeconds:      600,
			EnableSummarization: true,
		},
	}

	provider := NewConfiguredProvider(cfg)

	// Load transcript
	transcriptPath := filepath.Join(packageDir, "transcript.txt")
	transcript, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("读取转写文件失败: %v", err)
	}
	sessionInfo := session.Session{
		ID:        "26.04.22",
		Slug:      "26.04.22",
		ChannelID: "huizeman",
		Title:     "灰泽满 晚台杂谈",
		StartedAt: "2026-04-22T20:00:00+08:00",
		Status:    "asr_done",
	}
	glossaryDB, _ := createInMemoryDB(t)
	glossaryStore := glossary.NewStore(glossaryDB)
	seedHuizemanGlossary(t, glossaryStore)

	h := &Handler{
		cfg:           cfg,
		glossaryStore: glossaryStore,
	}
	rules, err := buildCorrectionRules(context.Background(), glossaryStore, sessionInfo.ChannelID)
	if err != nil {
		t.Fatalf("构建术语规则失败: %v", err)
	}
	if timed, err := correctedTimedTranscriptFromPackageDir(packageDir, rules); err == nil && len(timed.text) > 0 {
		transcript = timed.text
		if _, _, err := h.writeCorrectedTranscriptArtifacts(outputDir, nil, transcript, timed.report); err != nil {
			t.Fatalf("保存校正转写失败: %v", err)
		}
	} else {
		corrected, applied := correctTextWithRules(string(transcript), rules)
		transcript = []byte(corrected)
		if _, _, err := h.writeCorrectedTranscriptArtifacts(outputDir, nil, transcript, newCorrectionReport("transcript.txt", applied)); err != nil {
			t.Fatalf("保存校正转写失败: %v", err)
		}
	}
	t.Logf("转写文件: %d 字节", len(transcript))

	// Load danmaku and convert format (B站 format -> normalize format)
	danmakuData, err := loadAndConvertDanmaku(filepath.Join(packageDir, "danmaku.json"), outputDir)
	if err != nil {
		t.Logf("弹幕加载失败: %v，跳过弹幕分析", err)
	} else {
		t.Logf("弹幕: %d 条", len(danmakuData))
	}

	// Load metadata
	var durationMs int64
	meta := readSessionMetadata(filepath.Dir(packageDir))
	if meta != nil {
		durationMs = meta.DurationMs
	} else {
		// Fallback: read metadata.json directly
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

	// Analyze danmaku using the original module function
	var stats *danmakuStats
	if danmakuData != nil {
		stats, _ = analyzeDanmaku(danmakuData, durationMs)
	}

	// Build template vars
	resolved := &ResolvedTemplate{
		SystemPrompt: defaultSystemPrompt,
		UserFormat:   defaultUserFormat,
	}
	vars := &TemplateVars{
		ChannelName: "灰泽满",
		ChannelID:   "huizeman",
		Slug:        "26.04.22",
		Date:        "2026.04.22",
		DateTime:    "2026-04-22T20:00:00+08:00",
		Title:       "晚台杂谈",
		FanName:     "绿冻",
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
	if err := os.WriteFile(filepath.Join(outputDir, "live-recap.prompt.md"), []byte(prompt), 0644); err != nil {
		t.Logf("保存 prompt 失败: %v", err)
	}

	// Generate recap using the actual provider
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
	if err := os.WriteFile(filepath.Join(outputDir, "live-recap.raw.json"), []byte(raw), 0644); err != nil {
		t.Logf("保存原始响应失败: %v", err)
	}

	outputFile := filepath.Join(outputDir, "直播回顾_26.04.22.md")
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

func seedHuizemanGlossary(t *testing.T, store *glossary.Store) {
	t.Helper()
	ctx := context.Background()
	entries := []struct {
		term      string
		canonical string
		category  string
	}{
		{"立冬", "绿冻", "粉丝称呼"},
		{"律动", "绿冻", "粉丝称呼"},
		{"绿色果冻", "绿冻", "粉丝称呼"},
		{"辉泽满", "灰泽满", "主播名"},
		{"会泽满", "灰泽满", "主播名"},
		{"辉泽版", "灰泽满", "主播名"},
		{"柜子哥", "柜子歌", "梗"},
		{"弹幕男生", "弹幕男神", "梗"},
		{"灰晨风", "灰泽满", "主播名"},
		{"细节苹果肌", "细节苹果汁", "本场梗"},
	}
	for _, e := range entries {
		if err := store.Upsert(ctx, "huizeman", e.term, e.canonical, e.category); err != nil {
			t.Fatalf("seed glossary %q: %v", e.term, err)
		}
	}
	note := `主播语境：
- 主播名：灰泽满，常用昵称：小满。
- 粉丝称呼：绿冻。结尾不要写“粉丝”或“律动们”，优先写“绿冻们”。
- 文风：粉丝向、懂梗、轻松但不油腻，像直播切片简介加同好repo。
- 重要梗优先保留：柜子歌、弹幕男神、KPI播满、外卖迟到、改动态、好姐妹、萤火虫、乐极生悲、细节苹果汁、乘法口诀、中文大考。
- 时间使用相对直播时间，例如 00:20-00:35，不要使用现实钟表时间。
- 结尾标题用“致绿冻们”。`
	if err := store.SetNote(ctx, "huizeman", note); err != nil {
		t.Fatalf("seed glossary note: %v", err)
	}
}

// loadAndConvertDanmaku reads danmaku JSON and converts to normalized format
// that the recap module expects (rawDanmakuItem with time_ms field).
// Supports three formats:
// 1. normalized: [{time_ms, text, user_id, ...}]
// 2. B站录制原始: [{stime, text, uhash, ...}] (stime in seconds)
// 3. B站 API: {comments: [{progress, content, midHash, ...}]}
func loadAndConvertDanmaku(path string, outputDir string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try format 1: normalized (already has time_ms)
	var normalized []rawDanmakuItem
	if err := json.Unmarshal(data, &normalized); err == nil && len(normalized) > 0 {
		if normalized[0].TimeMS > 0 {
			converted, _ := json.Marshal(normalized)
			_ = os.WriteFile(filepath.Join(outputDir, "danmaku_converted.json"), converted, 0644)
			return converted, nil
		}
	}

	// Try format 2: B站录制原始 [{stime, text, uhash, ...}]
	var biliRaw []struct {
		STime  int    `json:"stime"` // milliseconds
		Text   string `json:"text"`
		UHash  string `json:"uhash"`
		Weight int    `json:"weight"`
	}
	if err := json.Unmarshal(data, &biliRaw); err == nil && len(biliRaw) > 0 {
		items := make([]rawDanmakuItem, 0, len(biliRaw))
		for _, c := range biliRaw {
			items = append(items, rawDanmakuItem{
				TimeMS: int64(c.STime),
				Text:   c.Text,
				UserID: c.UHash,
				Weight: c.Weight,
			})
		}
		converted, _ := json.Marshal(items)
		_ = os.WriteFile(filepath.Join(outputDir, "danmaku_converted.json"), converted, 0644)
		return converted, nil
	}

	// Try format 3: B站 API {comments: [...]}
	var biliAPI struct {
		Comments []struct {
			Progress int    `json:"progress"` // milliseconds
			Content  string `json:"content"`
			MidHash  string `json:"midHash"`
			Weight   int    `json:"weight"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(data, &biliAPI); err != nil {
		return nil, fmt.Errorf("parse danmaku: unsupported format: %w", err)
	}
	items := make([]rawDanmakuItem, 0, len(biliAPI.Comments))
	for _, c := range biliAPI.Comments {
		items = append(items, rawDanmakuItem{
			TimeMS: int64(c.Progress),
			Text:   c.Content,
			UserID: c.MidHash,
			Weight: c.Weight,
		})
	}

	converted, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}

	// Save converted format for reference
	_ = os.WriteFile(filepath.Join(outputDir, "danmaku_converted.json"), converted, 0644)

	return converted, nil
}
