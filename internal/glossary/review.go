package glossary

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"hikami-go/internal/session"
)

// reviewBatchSize 是每批 AI 复核的候选项数量(避免单次 prompt 过长)。
const reviewBatchSize = 10

// reviewSystemPrompt 是术语批量复核的 system prompt。
const reviewSystemPrompt = `你是术语校正复核助手。下面是一批待审核的术语候选(可能是 ASR 误识别)。
请用搜索工具(如有)核实每个 term 到 canonical 映射是否正确,返回 JSON 数组,每项含:
- id: 原候选 id
- canonical: 你核实后的正确写法(若原 canonical 正确则原样返回)
- confidence: 你的置信度 [0,1](核实后确认正确则高,不确定则低)
- reasoning: 一句话理由

只返回纯 JSON 数组,不要 markdown 代码块,不要多余文字。
若无法核实,基于已有信息给保守判断。示例:
[{"id":1,"canonical":"原神","confidence":0.95,"reasoning":"游戏名确认无误"}]`

// Review 对 pending 候选项做批量 AI 复核(Phase 5)。
// 每批调 AI(可带搜索工具)核实,更新 ai_review + confidence + canonical。
// status 保持 pending(保留人工把关)。高置信项前端高亮。
//
// 需要 Discoverer 已注入 provider + (可选)mcpToolkit。无 pending 候选时直接返回。
func (d *Discoverer) Review(ctx context.Context, channelID string) error {
	if d == nil || d.store == nil || d.provider == nil {
		return fmt.Errorf("discoverer not fully configured")
	}
	candidates, err := d.store.ListCandidates(ctx, channelID, CandidateStatusPending)
	if err != nil {
		return fmt.Errorf("list pending candidates: %w", err)
	}
	if len(candidates) == 0 {
		slog.Info("glossary review: no pending candidates", "channel_id", channelID)
		return nil
	}

	slog.Info("glossary review start", "channel_id", channelID, "count", len(candidates))
	processed := 0
	for i := 0; i < len(candidates); i += reviewBatchSize {
		end := i + reviewBatchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[i:end]
		if err := d.reviewBatch(ctx, batch); err != nil {
			slog.Warn("glossary review batch failed (continuing)", "channel_id", channelID, "batch_start", i, "error", err)
			continue
		}
		processed += len(batch)
	}
	slog.Info("glossary review done", "channel_id", channelID, "processed", processed, "total", len(candidates))
	return nil
}

// reviewBatch 复核一批候选项:构造 prompt 调 AI(可带工具)后解析 JSON 更新。
func (d *Discoverer) reviewBatch(ctx context.Context, batch []Candidate) error {
	userPrompt := buildReviewUserPrompt(batch)

	// 走 generateChunk 逻辑(自动判断是否用 MCP 工具)。
	// 复核场景 sessionInfo 不被使用(tool-calling 分支走 GenerateWithTools),传零值。
	dummySession := session.Session{ChannelID: batch[0].ChannelID}
	result, err := d.generateChunk(ctx, reviewSystemPrompt, userPrompt, dummySession)
	if err != nil {
		return fmt.Errorf("generate review: %w", err)
	}

	reviews, err := parseReviewResult(result.Content)
	if err != nil {
		slog.Warn("glossary review parse failed (skipping batch)", "error", err, "raw", result.Content)
		return nil
	}

	for _, rv := range reviews {
		if rv.ID == 0 {
			continue
		}
		// 验证 id 在本批内(防 AI 幻觉 id)。
		if !batchContainsID(batch, rv.ID) {
			continue
		}
		if err := d.store.UpdateCandidateReview(ctx, rv.ID, rv.Canonical, rv.Confidence, rv.Reasoning); err != nil {
			slog.Warn("update candidate review failed", "id", rv.ID, "error", err)
		}
	}
	return nil
}

// buildReviewUserPrompt 构造批量复核的 user prompt(合法 JSON 数组)。
func buildReviewUserPrompt(batch []Candidate) string {
	var sb strings.Builder
	sb.WriteString("请复核以下术语候选:\n\n[\n")
	for i, c := range batch {
		fmt.Fprintf(&sb, "  {\"id\":%d,\"term\":%q,\"canonical\":%q,\"category\":%q}", c.ID, c.Term, c.Canonical, c.Category)
		if i < len(batch)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("]\n")
	return sb.String()
}

// reviewItem 是 AI 复核返回的单项。
type reviewItem struct {
	ID         int64   `json:"id"`
	Canonical  string  `json:"canonical"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// parseReviewResult 解析 AI 返回的 JSON 数组(容忍 code block 包裹)。
func parseReviewResult(content string) ([]reviewItem, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty review result")
	}
	var items []reviewItem
	if err := json.Unmarshal([]byte(content), &items); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return items, nil
}

// batchContainsID 检查候选批次是否含指定 id。
func batchContainsID(batch []Candidate, id int64) bool {
	for _, c := range batch {
		if c.ID == id {
			return true
		}
	}
	return false
}
