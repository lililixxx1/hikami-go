package glossary

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/session"
)

const (
	defaultDiscoveryChunkChars = 12000
	defaultDiscoveryMaxChunks  = 8
)

// DiscoveryProvider 与 recap.Provider 方法签名一致，避免 glossary 反向导入 recap。
type DiscoveryProvider interface {
	Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error)
}

// DiscoveryResult is the top-level JSON object returned by AI.
type DiscoveryResult struct {
	Items []DiscoveryItem `json:"items"`
}

// DiscoveryItem is one AI-extracted glossary candidate.
type DiscoveryItem struct {
	Term            string  `json:"term"`
	Canonical       string  `json:"canonical"`
	Category        string  `json:"category"`
	Confidence      float64 `json:"confidence"`
	OccurrenceCount int     `json:"occurrence_count"`
	Reason          string  `json:"reason"`
}

type TranscriptSegment struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

type DiscoveryChunk struct {
	Index   int
	StartMS int64
	EndMS   int64
	Text    string
}

type Discoverer struct {
	store    *Store
	provider DiscoveryProvider
	sessions *session.Store

	chunkChars int
	maxChunks  int
	timeout    time.Duration

	maxRetries int
	retryDelay time.Duration
}

type DiscovererOption func(*Discoverer)

func NewDiscoverer(store *Store, provider DiscoveryProvider, sessions *session.Store, opts ...DiscovererOption) *Discoverer {
	d := &Discoverer{
		store:      store,
		provider:   provider,
		sessions:   sessions,
		chunkChars: defaultDiscoveryChunkChars,
		maxChunks:  defaultDiscoveryMaxChunks,
		timeout:    2 * time.Minute,
		maxRetries: 2,
		retryDelay: 3 * time.Second,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func WithDiscoveryChunkChars(n int) DiscovererOption {
	return func(d *Discoverer) {
		if n > 1000 {
			d.chunkChars = n
		}
	}
}

func WithDiscoveryMaxChunks(n int) DiscovererOption {
	return func(d *Discoverer) {
		if n > 0 {
			d.maxChunks = n
		}
	}
}

func WithDiscoveryTimeout(timeout time.Duration) DiscovererOption {
	return func(d *Discoverer) {
		if timeout > 0 {
			d.timeout = timeout
		}
	}
}

func WithDiscoveryMaxRetries(n int) DiscovererOption {
	return func(d *Discoverer) {
		if n >= 0 {
			d.maxRetries = n
		}
	}
}

func WithDiscoveryRetryDelay(delay time.Duration) DiscovererOption {
	return func(d *Discoverer) {
		if delay > 0 {
			d.retryDelay = delay
		}
	}
}

func (d *Discoverer) Discover(ctx context.Context, channelID string, sessionID string, transcript []byte, segments []TranscriptSegment, existingGlossary string) error {
	if d == nil || d.store == nil || d.provider == nil {
		return nil
	}
	if strings.TrimSpace(channelID) == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%w: channel_id and session_id are required", ErrInvalidCandidate)
	}

	chunks := d.buildChunks(transcript, segments)
	if len(chunks) == 0 {
		return nil
	}
	if len(chunks) > d.maxChunks {
		chunks = chunks[:d.maxChunks]
	}

	sessionInfo := session.Session{ID: sessionID, ChannelID: channelID, Title: "Glossary Discovery"}
	if d.sessions != nil {
		if got, err := d.sessions.Get(ctx, sessionID); err == nil {
			sessionInfo = got
		}
	}

	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	for _, chunk := range chunks {
		userPrompt := buildDiscoveryUserPrompt(existingGlossary, chunk)
		var result aiprovider.GenerateResult
		var err error
		for attempt := 0; attempt <= d.maxRetries; attempt++ {
			result, err = d.provider.Generate(ctx, discoverySystemPrompt, userPrompt, sessionInfo)
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return fmt.Errorf("generate discovery chunk %d: %w", chunk.Index, ctx.Err())
			}
			if attempt == d.maxRetries {
				return fmt.Errorf("generate discovery chunk %d: %w", chunk.Index, err)
			}
			slog.Warn("glossary discovery generate failed",
				"channel_id", channelID,
				"session_id", sessionID,
				"chunk_index", chunk.Index,
				"attempt", attempt,
				"error", err,
			)
			select {
			case <-ctx.Done():
				return fmt.Errorf("generate discovery chunk %d: %w", chunk.Index, ctx.Err())
			case <-time.After(d.retryDelay):
			}
		}
		if err != nil {
			return fmt.Errorf("generate discovery chunk %d: %w", chunk.Index, err)
		}
		content := result.Content
		items, err := parseDiscoveryResult(content)
		if err != nil {
			return fmt.Errorf("parse discovery chunk %d: %w", chunk.Index, err)
		}
		if err := d.mergeCandidates(ctx, channelID, items, sessionID); err != nil {
			return fmt.Errorf("merge discovery chunk %d: %w", chunk.Index, err)
		}
	}
	return nil
}

func (d *Discoverer) buildChunks(transcript []byte, segments []TranscriptSegment) []DiscoveryChunk {
	if len(segments) > 0 {
		return d.buildChunksFromSegments(segments)
	}
	return d.buildChunksFromText(string(transcript))
}

func (d *Discoverer) buildChunksFromSegments(segments []TranscriptSegment) []DiscoveryChunk {
	var chunks []DiscoveryChunk
	var b strings.Builder
	var startMS, endMS int64
	flush := func() {
		text := strings.TrimSpace(b.String())
		if text == "" {
			return
		}
		chunks = append(chunks, DiscoveryChunk{
			Index:   len(chunks) + 1,
			StartMS: startMS,
			EndMS:   endMS,
			Text:    text,
		})
		b.Reset()
		startMS = 0
		endMS = 0
	}

	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" || seg.StartMS < 0 {
			continue
		}
		line := fmt.Sprintf("[%s] %s\n", formatDiscoveryTimestamp(seg.StartMS), text)
		if b.Len() > 0 && b.Len()+len(line) > d.chunkChars {
			flush()
		}
		if b.Len() == 0 {
			startMS = seg.StartMS
		}
		endMS = seg.EndMS
		b.WriteString(line)
	}
	flush()
	return chunks
}

func (d *Discoverer) buildChunksFromText(text string) []DiscoveryChunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	paragraphs := strings.Split(text, "\n\n")
	var chunks []DiscoveryChunk
	var b strings.Builder
	flush := func() {
		chunkText := strings.TrimSpace(b.String())
		if chunkText == "" {
			return
		}
		chunks = append(chunks, DiscoveryChunk{Index: len(chunks) + 1, Text: chunkText})
		b.Reset()
	}
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		if utf8.RuneCountInString(paragraph) > d.chunkChars {
			flush()
			for _, part := range splitRunes(paragraph, d.chunkChars) {
				chunks = append(chunks, DiscoveryChunk{Index: len(chunks) + 1, Text: part})
			}
			continue
		}
		if b.Len() > 0 && b.Len()+len(paragraph)+2 > d.chunkChars {
			flush()
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(paragraph)
	}
	flush()
	return chunks
}

func parseDiscoveryResult(raw string) ([]DiscoveryItem, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result DiscoveryResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}

	items := make([]DiscoveryItem, 0, len(result.Items))
	for _, item := range result.Items {
		item.Term = strings.TrimSpace(item.Term)
		item.Canonical = strings.TrimSpace(item.Canonical)
		item.Category = strings.TrimSpace(item.Category)
		item.Reason = strings.TrimSpace(item.Reason)
		item.Confidence = clamp01(item.Confidence)
		if item.OccurrenceCount <= 0 {
			item.OccurrenceCount = 1
		}
		if item.Term == "" || item.Canonical == "" {
			continue
		}
		if normalizeKey(item.Term, item.Canonical) == "" {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func (d *Discoverer) mergeCandidates(ctx context.Context, channelID string, items []DiscoveryItem, sessionID string) error {
	for _, item := range items {
		if err := d.store.UpsertCandidate(ctx, channelID, item, sessionID); err != nil {
			return err
		}
	}
	return nil
}

const discoverySystemPrompt = `你是直播转写文本的术语发现助手。你的任务是从转写片段中发现值得加入术语表审核队列的候选项，用于后续人工审核。

你只提取以下类型：
- 主播、嘉宾、粉丝群体、社群称呼
- 直播中反复出现的梗、口头禅、活动名、栏目名
- 游戏、角色、作品、歌曲、品牌、专有名词
- 明显可能被 ASR 误识别的词，以及它对应的正确写法

排除以下内容：
- 普通日常词汇、泛泛的话题词、情绪词
- 单次出现且没有专有含义的词
- 已在“已有术语表”中出现的 term 或 canonical
- 无法判断正确写法的候选
- 人身攻击、隐私信息、联系方式、广告
- 过长句子；term 和 canonical 都应是短词或短语

输出要求：
- 只输出纯 JSON，不要输出 markdown code block，不要解释。
- JSON 顶层对象固定为 {"items":[...]}。
- items 最多 12 条。
- 没有候选时输出 {"items":[]}。
- confidence 必须是 0 到 1 的数字。
- occurrence_count 是该候选在当前片段中的估计出现次数，至少为 1。

每个 item 字段：
- term：转写中出现的疑似写法或待收录写法。
- canonical：建议的正式写法。如果 term 本身就是正式写法，canonical 与 term 相同。
- category：简短分类，例如 人名、游戏、角色、作品、歌曲、梗、粉丝称呼、活动。
- confidence：你对该候选值得进入人工审核的置信度。
- occurrence_count：当前片段内估计出现次数。
- reason：一句话说明依据，必须简短。`

func buildDiscoveryUserPrompt(existingGlossary string, chunk DiscoveryChunk) string {
	if strings.TrimSpace(existingGlossary) == "" {
		existingGlossary = "（空）"
	}
	start := "unknown"
	end := "unknown"
	if chunk.StartMS > 0 || chunk.EndMS > 0 {
		start = formatDiscoveryTimestamp(chunk.StartMS)
		end = formatDiscoveryTimestamp(chunk.EndMS)
	}
	return fmt.Sprintf(`# Glossary Discovery 输入

## 已有术语表

%s

如果已有术语表为空，表示当前没有可参考的正式术语。

## 转写片段

片段序号：%d
时间范围：%s - %s

%s

## 输出 JSON Schema

{
  "items": [
    {
      "term": "转写中出现的写法",
      "canonical": "建议正式写法",
      "category": "分类",
      "confidence": 0.82,
      "occurrence_count": 2,
      "reason": "简短依据"
    }
  ]
}
`, existingGlossary, chunk.Index, start, end, chunk.Text)
}

func formatDiscoveryTimestamp(ms int64) string {
	totalSec := ms / 1000
	hour := totalSec / 3600
	minute := (totalSec % 3600) / 60
	second := totalSec % 60
	if hour > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second)
	}
	return fmt.Sprintf("%02d:%02d", minute, second)
}

func splitRunes(text string, size int) []string {
	if size <= 0 {
		return []string{text}
	}
	var parts []string
	runes := []rune(text)
	for len(runes) > 0 {
		n := size
		if len(runes) < n {
			n = len(runes)
		}
		parts = append(parts, string(runes[:n]))
		runes = runes[n:]
	}
	return parts
}
