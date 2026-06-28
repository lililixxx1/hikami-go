package recap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"hikami-go/internal/session"
)

const defaultSummarizationThreshold = 30000 // chars

// SummarizedTranscript holds a condensed version of a long transcript.
type SummarizedTranscript struct {
	Summary   string   `json:"summary"`
	KeyQuotes []string `json:"key_quotes"`
	Topics    []string `json:"topics"`
}

// TranscriptSummarizer condenses long transcripts via a lightweight AI call.
type TranscriptSummarizer struct {
	provider Provider
	cfg      summarizerConfig
}

type summarizerConfig struct {
	threshold int // min transcript length (chars) to trigger summarization
}

// NewTranscriptSummarizer creates a summarizer. Nil provider disables summarization.
func NewTranscriptSummarizer(provider Provider) *TranscriptSummarizer {
	return &TranscriptSummarizer{
		provider: provider,
		cfg: summarizerConfig{
			threshold: defaultSummarizationThreshold,
		},
	}
}

// Summarize condenses a long transcript. Returns nil if summarization is skipped.
// The caller should fall back to the original transcript when nil is returned.
func (s *TranscriptSummarizer) Summarize(ctx context.Context, transcript []byte, meta *sessionMetadata) (*SummarizedTranscript, error) {
	if s.provider == nil {
		return nil, nil
	}

	text := string(transcript)
	runeCount := len([]rune(text))
	if runeCount < s.cfg.threshold {
		return nil, nil
	}

	durationInfo := ""
	if meta != nil && meta.DurationMs > 0 {
		durMin := meta.DurationMs / 60000
		durationInfo = fmt.Sprintf("这场直播时长约 %d 小时 %d 分钟。", durMin/60, durMin%60)
	}

	systemPrompt := `你是一个直播转写文本压缩专家。你的任务是将冗长的直播转写文本压缩为精简但信息完整的摘要，用于后续生成直播回顾文档。

要求：
1. 保留所有重要话题和事件的描述，不遗漏关键内容
2. 保留主播的原话引用（尤其是金句、爆笑语录），标注 [原话] 标记
3. 按时间线组织，标注每个话题的大致时间范围
4. 保留具体细节：食物名、价格、数字、歌名、游戏名等
5. 删除重复内容、无意义的语气词和停顿
6. 输出 JSON 格式`

	userPrompt := fmt.Sprintf(`请压缩以下直播转写文本。%s

转写文本（%d 字）：
%s

请输出以下 JSON 格式：
{
  "summary": "压缩后的叙述文本（保留所有话题，按时间线组织，2000-4000字）",
  "key_quotes": ["原话引用1", "原话引用2", ...],
  "topics": ["话题1", "话题2", ...]
	}`, durationInfo, runeCount, text)

	// Use a minimal session info for the summarization call
	dummySession := session.Session{Title: "transcript-summarization"}
	genResult, err := s.provider.Generate(ctx, systemPrompt, userPrompt, dummySession)
	if err != nil {
		return nil, fmt.Errorf("transcript summarization failed: %w", err)
	}
	content := genResult.Content

	// Parse JSON from response
	content = stripAIPreamble(content)
	content = extractJSON(content)

	var result SummarizedTranscript
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		// If JSON parsing fails, use the raw content as summary
		return &SummarizedTranscript{
			Summary:   content,
			KeyQuotes: nil,
			Topics:    nil,
		}, nil
	}

	return &result, nil
}

// extractJSON tries to find a JSON object in the response text.
func extractJSON(text string) string {
	text = strings.TrimSpace(text)

	// If wrapped in markdown code block
	if strings.HasPrefix(text, "```") {
		// Remove opening ```
		text = text[3:]
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx+1:]
		}
		// Remove closing ```
		if idx := strings.LastIndex(text, "```"); idx >= 0 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}

	// Find first { and last }
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}

	return text
}
