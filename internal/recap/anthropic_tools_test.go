package recap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

// newAnthropicToolTestProvider 构造指向 mock server 的 AnthropicProvider。
func newAnthropicToolTestProvider(t *testing.T, server *httptest.Server) *AnthropicProvider {
	t.Helper()
	cfg := &config.Config{}
	cfg.RecapAI.BaseURL = server.URL
	cfg.RecapAI.Model = "claude-test"
	cfg.RecapAI.MaxTokens = 1024
	t.Setenv("AI_API_KEY", "test-key")
	return NewAnthropicProvider(cfg)
}

// TestParseAnthropicResult_ToolUse 验证 tool_use content block 被提取为 ToolCalls,
// 且所有 text blocks 被聚合(而非只取首个)。
func TestParseAnthropicResult_ToolUse(t *testing.T) {
	resp := `{"stop_reason":"tool_use","content":[
		{"type":"text","text":"我来查一下"},
		{"type":"tool_use","id":"toolu_1","name":"web_search","input":{"query":"辉子版"}},
		{"type":"text","text":"再补充"}
	]}`
	got := parseAnthropicResult([]byte(resp))
	// 聚合两个 text block。
	if got.Content != "我来查一下\n\n再补充" {
		t.Errorf("Content = %q, 期望聚合两个 text block", got.Content)
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("ToolCalls 数量 = %d, 期望 1", len(got.ToolCalls))
	}
	tc := got.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "web_search" {
		t.Errorf("ToolCall = %+v", tc)
	}
	// input 对象转为 JSON 字符串。
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		t.Fatalf("Arguments 不是合法 JSON: %v", err)
	}
	if args["query"] != "辉子版" {
		t.Errorf("Arguments.query = %v", args["query"])
	}
	if got.FinishReason != "tool_use" {
		t.Errorf("FinishReason = %q, 期望 tool_use", got.FinishReason)
	}
}

// TestGenerateWithTools_AnthropicRequestConstruction 验证 Anthropic 请求体:
// tools 用 input_schema;assistant tool_calls 转 content blocks 的 tool_use;
// role=tool 工具结果转 user + tool_result content block。
func TestGenerateWithTools_AnthropicRequestConstruction(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &capturedBody)
		_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}`))
	}))
	defer server.Close()

	p := newAnthropicToolTestProvider(t, server)
	req := aiprovider.GenerateRequest{
		SystemPrompt: "你是助手",
		Messages: []aiprovider.Message{
			{Role: aiprovider.RoleUser, Content: "查辉子版"},
			{Role: aiprovider.RoleAssistant, ToolCalls: []aiprovider.ToolCall{
				{ID: "toolu_1", Name: "web_search", Arguments: `{"query":"辉子版"}`},
			}},
			{Role: aiprovider.RoleTool, ToolCallID: "toolu_1", Content: "辉子版=灰泽满"},
		},
		Tools: []aiprovider.Tool{
			{Name: "web_search", Description: "搜索", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	}
	result, err := p.GenerateWithTools(context.Background(), req)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	if result.Content != "done" {
		t.Errorf("Content = %q, 期望 done", result.Content)
	}

	// system 走顶层字段。
	if capturedBody["system"] != "你是助手" {
		t.Errorf("system 字段 = %v", capturedBody["system"])
	}
	// tools 用 input_schema。
	tools, _ := capturedBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools 数量 = %d, 期望 1", len(tools))
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["name"] != "web_search" {
		t.Errorf("tool name = %v", tool0["name"])
	}
	if _, ok := tool0["input_schema"]; !ok {
		t.Error("Anthropic tool 应使用 input_schema 字段")
	}

	msgs, _ := capturedBody["messages"].([]any)
	// Anthropic: user 原文 + assistant(tool_use) + user(tool_result) = 3 条
	if len(msgs) != 3 {
		t.Fatalf("messages 数量 = %d, 期望 3", len(msgs))
	}
	// 第 2 条是 assistant,content 含 tool_use block。
	assistantMsg, _ := msgs[1].(map[string]any)
	if assistantMsg["role"] != "assistant" {
		t.Errorf("第2条 role = %v, 期望 assistant", assistantMsg["role"])
	}
	// 第 3 条是工具结果,转成 user + tool_result。
	toolResultMsg, _ := msgs[2].(map[string]any)
	if toolResultMsg["role"] != "user" {
		t.Errorf("tool_result 应转为 user role, 实际 = %v", toolResultMsg["role"])
	}
	content, _ := toolResultMsg["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("tool_result content 数量 = %d, 期望 1", len(content))
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "tool_result" || block["tool_use_id"] != "toolu_1" {
		t.Errorf("tool_result block 错误: %+v", block)
	}
}

// TestGenerateWithTools_AnthropicToolUseResponse 解析 tool_use 响应。
func TestGenerateWithTools_AnthropicToolUseResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"stop_reason":"tool_use","content":[
			{"type":"text","text":"搜索中"},
			{"type":"tool_use","id":"toolu_99","name":"web_search","input":{"query":"test"}}
		]}`))
	}))
	defer server.Close()

	p := newAnthropicToolTestProvider(t, server)
	req := aiprovider.GenerateRequest{
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "hi"}},
		Tools:    []aiprovider.Tool{{Name: "web_search"}},
	}
	result, err := p.GenerateWithTools(context.Background(), req)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "toolu_99" {
		t.Fatalf("ToolCalls 解析错误: %+v", result.ToolCalls)
	}
}

// TestGenerateWithTools_AnthropicEmptyToolsEquivalent 零回归契约:空 Tools 时无 tools 字段。
func TestGenerateWithTools_AnthropicEmptyToolsEquivalent(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &capturedBody)
		_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"hello"}]}`))
	}))
	defer server.Close()

	p := newAnthropicToolTestProvider(t, server)
	req := aiprovider.GenerateRequest{
		SystemPrompt: "sys",
		Messages:     []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "prompt"}},
	}
	result, err := p.GenerateWithTools(context.Background(), req)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	if result.Content != "hello" {
		t.Errorf("Content = %q, 期望 hello", result.Content)
	}
	if _, hasTools := capturedBody["tools"]; hasTools {
		t.Error("空 Tools 时不应发送 tools 字段")
	}
}

// TestParseAnthropicResult_OnlyTextBackwardCompat 验证重构后的 parse 对纯文本响应
// (无 tool_use)行为不变,保证向后兼容。
func TestParseAnthropicResult_OnlyTextBackwardCompat(t *testing.T) {
	resp := `{"stop_reason":"stop","content":[{"type":"text","text":"纯文本回复"}]}`
	got := parseAnthropicResult([]byte(resp))
	if got.Content != "纯文本回复" {
		t.Errorf("Content = %q", got.Content)
	}
	if len(got.ToolCalls) != 0 {
		t.Errorf("纯文本响应不应有 ToolCalls, 得到 %d 个", len(got.ToolCalls))
	}
}

// --- M8(2026-08-15 全项目审核):Anthropic 空 content 分流重试,镜像 provider_openai_test 关键用例 ---

// anthropicEmptyResponse 返回 stop_reason 可配的空正文响应(仅空 text block)。
func anthropicEmptyResponse(stopReason string) string {
	return `{"stop_reason":"` + stopReason + `","content":[{"type":"text","text":""}]}`
}

func TestAnthropicGenerate_RetriesEmptyContentThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			_, _ = w.Write([]byte(anthropicEmptyResponse("end_turn")))
			return
		}
		_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"有效回顾"}]}`))
	}))
	defer server.Close()

	p := newAnthropicToolTestProvider(t, server)
	result, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if result.Content != "有效回顾" {
		t.Fatalf("Content = %q", result.Content)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls (1 empty + 1 success), got %d", got)
	}
}

func TestAnthropicGenerate_NoRetryOnStopReasonMaxTokens(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(anthropicEmptyResponse("max_tokens")))
	}))
	defer server.Close()

	p := newAnthropicToolTestProvider(t, server)
	_, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err == nil {
		t.Fatal("expected deterministic error on stop_reason=max_tokens")
	}
	if !strings.Contains(err.Error(), "max_tokens") || !strings.Contains(err.Error(), "not retried") {
		t.Fatalf("error should mention max_tokens + not retried, got: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("deterministic empty content must not retry, calls = %d", got)
	}
}

func TestAnthropicGenerate_FirstCallSucceedsDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	p := newAnthropicToolTestProvider(t, server)
	if _, err := p.Generate(context.Background(), "sys", "user", session.Session{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("success must not retry, calls = %d", got)
	}
}

func TestAnthropicGenerate_EmptyContentRetriesExhausted(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(anthropicEmptyResponse("end_turn")))
	}))
	defer server.Close()

	p := newAnthropicToolTestProvider(t, server)
	_, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err == nil {
		t.Fatal("expected error when all attempts return empty content")
	}
	if !strings.Contains(err.Error(), "after 3 attempts") || !strings.Contains(err.Error(), "stop_reason=end_turn") {
		t.Fatalf("error should mention 3 attempts + stop_reason, got: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 retries), got %d", got)
	}
}

func TestAnthropicGenerateWithTools_EmptyContentWithToolUseNotRetried(t *testing.T) {
	// agent loop 中间轮:空正文 + tool_use 是协议正常态,应放行不重试。
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"web_search","input":{"query":"q"}}]}`))
	}))
	defer server.Close()

	p := newAnthropicToolTestProvider(t, server)
	req := aiprovider.GenerateRequest{
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "hi"}},
		Tools:    []aiprovider.Tool{{Name: "web_search"}},
	}
	result, err := p.GenerateWithTools(context.Background(), req)
	if err != nil {
		t.Fatalf("tool_use-only response must pass through: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1", len(result.ToolCalls))
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("tool_use pass-through must not retry, calls = %d", got)
	}
}
