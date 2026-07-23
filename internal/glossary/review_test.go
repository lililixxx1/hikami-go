package glossary

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestUpdateCandidateReview 验证 UpdateCandidateReview 写入 ai_review 并更新 canonical/confidence。
func TestUpdateCandidateReview(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item := DiscoveryItem{Term: "辉子版", Canonical: "灰泽满", Category: "称呼", Confidence: 0.5, OccurrenceCount: 2, Reason: "测试"}
	if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
		t.Fatal(err)
	}
	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	if len(candidates) != 1 {
		t.Fatalf("前置:应 1 个候选, got %d", len(candidates))
	}
	cid := candidates[0].ID
	if candidates[0].AIReview != "" {
		t.Errorf("初始 ai_review 应为空, got %q", candidates[0].AIReview)
	}

	if err := s.UpdateCandidateReview(ctx, cid, "灰泽满", 0.95, "搜索确认辉子版是灰泽满的俗称"); err != nil {
		t.Fatalf("UpdateCandidateReview: %v", err)
	}

	after, _ := s.ListCandidates(ctx, "ch1", "pending")
	if len(after) != 1 {
		t.Fatalf("复核后应仍 1 个候选, got %d", len(after))
	}
	if after[0].Canonical != "灰泽满" {
		t.Errorf("Canonical = %q", after[0].Canonical)
	}
	if after[0].Confidence != 0.95 {
		t.Errorf("Confidence = %v, 期望 0.95", after[0].Confidence)
	}
	if after[0].AIReview != "搜索确认辉子版是灰泽满的俗称" {
		t.Errorf("AIReview = %q", after[0].AIReview)
	}
	if after[0].Status != CandidateStatusPending {
		t.Errorf("Status = %q, 应仍 pending", after[0].Status)
	}
}

// TestUpdateCandidateReview_NotFound 验证不存在的 id 返回 ErrCandidateNotFound(审核code-review Minor#4)。
func TestUpdateCandidateReview_NotFound(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()
	// 不存在的 id(空库)。
	err := s.UpdateCandidateReview(ctx, 99999, "x", 0.5, "理由")
	if err == nil {
		t.Fatal("不存在的 id 应返回错误")
	}
	if err != ErrCandidateNotFound {
		t.Errorf("应返回 ErrCandidateNotFound, got %v", err)
	}
}

// TestParseReviewResult_ValidJSON 验证复核结果 JSON 解析。
func TestParseReviewResult_ValidJSON(t *testing.T) {
	input := `[{"id":1,"canonical":"原神","confidence":0.95,"reasoning":"游戏名确认"},
{"id":2,"canonical":"崩坏星穹铁道","confidence":0.9,"reasoning":"全名核实"}]`
	items, err := parseReviewResult(input)
	if err != nil {
		t.Fatalf("解析错误: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应解析 2 项, got %d", len(items))
	}
	if items[0].ID != 1 || items[0].Canonical != "原神" {
		t.Errorf("items[0] = %+v", items[0])
	}
	if items[0].Confidence != 0.95 {
		t.Errorf("items[0].Confidence = %v", items[0].Confidence)
	}
}

// TestParseReviewResult_CodeBlockWrapped 验证容忍 json code block 包裹。
func TestParseReviewResult_CodeBlockWrapped(t *testing.T) {
	input := "```json\n[{\"id\":1,\"canonical\":\"测试\",\"confidence\":0.8,\"reasoning\":\"ok\"}]\n```"
	items, err := parseReviewResult(input)
	if err != nil {
		t.Fatalf("应容忍 code block: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应解析 1 项, got %d", len(items))
	}
}

func TestParseReviewResult_Empty(t *testing.T) {
	_, err := parseReviewResult("")
	if err == nil {
		t.Fatal("空内容应返回错误")
	}
}

func TestParseReviewResult_InvalidJSON(t *testing.T) {
	_, err := parseReviewResult("not json at all")
	if err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
}

// TestBatchContainsID 验证 id 归属检查(防 AI 幻觉 id)。
func TestBatchContainsID(t *testing.T) {
	batch := []Candidate{{ID: 1}, {ID: 5}, {ID: 10}}
	if !batchContainsID(batch, 5) {
		t.Error("5 应在批次内")
	}
	if batchContainsID(batch, 99) {
		t.Error("99 不应在批次内")
	}
}

// TestBuildReviewUserPrompt 验证 prompt 构造格式。
func TestBuildReviewUserPrompt(t *testing.T) {
	batch := []Candidate{
		{ID: 1, Term: "辉子版", Canonical: "灰泽满", Category: "称呼"},
	}
	prompt := buildReviewUserPrompt(batch)
	if !strings.Contains(prompt, "辉子版") || !strings.Contains(prompt, "灰泽满") {
		t.Errorf("prompt 应含 term 和 canonical: %q", prompt)
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(extractJSONArray(prompt)), &raw); err != nil {
		t.Errorf("prompt 内的 JSON 应可解析: %v (prompt=%q)", err, prompt)
	}
}

// extractJSONArray 从 prompt 提取方括号包裹的 JSON 数组(测试辅助)。
func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || end <= start {
		return "[]"
	}
	return s[start : end+1]
}
