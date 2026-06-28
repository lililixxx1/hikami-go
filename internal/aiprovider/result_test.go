package aiprovider

import (
	"encoding/json"
	"testing"
)

func TestGenerateResult_Fields(t *testing.T) {
	r := GenerateResult{
		Content:      "回顾内容",
		Raw:          `{"text":"回顾内容"}`,
		FinishReason: "stop",
	}
	if r.Content != "回顾内容" {
		t.Errorf("Content = %q, 期望 %q", r.Content, "回顾内容")
	}
	if r.Raw != `{"text":"回顾内容"}` {
		t.Errorf("Raw = %q, 不匹配", r.Raw)
	}
	if r.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, 期望 %q", r.FinishReason, "stop")
	}
}

func TestGenerateResult_JSONRoundtrip(t *testing.T) {
	original := GenerateResult{
		Content:      "测试内容",
		Raw:          `raw response`,
		FinishReason: "length",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}

	var decoded GenerateResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("反序列化: %v", err)
	}

	if decoded.Content != original.Content {
		t.Errorf("Content 反序列化 = %q, 期望 %q", decoded.Content, original.Content)
	}
	if decoded.Raw != original.Raw {
		t.Errorf("Raw 反序列化 = %q, 期望 %q", decoded.Raw, original.Raw)
	}
	if decoded.FinishReason != original.FinishReason {
		t.Errorf("FinishReason 反序列化 = %q, 期望 %q", decoded.FinishReason, original.FinishReason)
	}
}

func TestGenerateResult_ZeroValue(t *testing.T) {
	var r GenerateResult
	if r.Content != "" {
		t.Errorf("零值 Content = %q, 期望空字符串", r.Content)
	}
	if r.Raw != "" {
		t.Errorf("零值 Raw = %q, 期望空字符串", r.Raw)
	}
	if r.FinishReason != "" {
		t.Errorf("零值 FinishReason = %q, 期望空字符串", r.FinishReason)
	}
}

func TestGenerateResult_FinishReason(t *testing.T) {
	reasons := []string{"stop", "length", "max_tokens", ""}
	for _, reason := range reasons {
		r := GenerateResult{FinishReason: reason}
		if r.FinishReason != reason {
			t.Errorf("FinishReason = %q, 期望 %q", r.FinishReason, reason)
		}
	}
}

func TestGenerateResult_SliceOfResults(t *testing.T) {
	results := []GenerateResult{
		{Content: "第一段", FinishReason: "stop"},
		{Content: "第二段", FinishReason: "length"},
		{Content: "第三段", FinishReason: "stop"},
	}
	if len(results) != 3 {
		t.Fatalf("结果数量 = %d, 期望 3", len(results))
	}
	if results[1].Content != "第二段" {
		t.Errorf("results[1].Content = %q", results[1].Content)
	}
	if results[1].FinishReason != "length" {
		t.Errorf("results[1].FinishReason = %q", results[1].FinishReason)
	}
}
