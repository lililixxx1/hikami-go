package recap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
)

// newToolTestProvider 构造一个指向 mock server 的 OpenAICompatibleProvider,
// 用于 tool calling 测试(避免依赖真实 AI API)。
func newToolTestProvider(t *testing.T, server *httptest.Server) *OpenAICompatibleProvider {
	t.Helper()
	cfg := &config.Config{}
	cfg.RecapAI.BaseURL = server.URL
	cfg.RecapAI.Model = "test-model"
	cfg.RecapAI.MaxTokens = 1024
	t.Setenv("AI_API_KEY", "test-key")
	return &OpenAICompatibleProvider{cfg: cfg, httpClient: &http.Client{Timeout: 5 * time.Second}}
}

// TestParseChatCompletionResult_ToolCalls 验证 OpenAI 响应的 tool_calls 被正确解析为 ToolCall 切片。
func TestParseChatCompletionResult_ToolCalls(t *testing.T) {
	resp := `{"choices":[{"finish_reason":"tool_calls","message":{"content":null,"tool_calls":[
		{"id":"call_1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"辉子版\"}"}},
		{"id":"call_2","type":"function","function":{"name":"web_search","arguments":"{}"}}
	]}}]}`
	got := parseChatCompletionResult([]byte(resp))
	if len(got.ToolCalls) != 2 {
		t.Fatalf("ToolCalls 数量 = %d, 期望 2", len(got.ToolCalls))
	}
	if got.ToolCalls[0].ID != "call_1" || got.ToolCalls[0].Name != "web_search" {
		t.Errorf("ToolCalls[0] = %+v", got.ToolCalls[0])
	}
	if got.ToolCalls[0].Arguments != `{"query":"辉子版"}` {
		t.Errorf("ToolCalls[0].Arguments = %q", got.ToolCalls[0].Arguments)
	}
	if got.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, 期望 tool_calls", got.FinishReason)
	}
}

// TestParseChatCompletionResult_ToolCallsFillsMissingFinishReason 部分兼容端点不返回 finish_reason,
// 但有 tool_calls 时应补齐为 "tool_calls"。
func TestParseChatCompletionResult_ToolCallsFillsMissingFinishReason(t *testing.T) {
	resp := `{"choices":[{"message":{"tool_calls":[
		{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}`
	got := parseChatCompletionResult([]byte(resp))
	if got.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, 期望 tool_calls(补齐)", got.FinishReason)
	}
}

// TestGenerateWithTools_OpenAIRequestConstruction 用 httptest 验证请求体包含 tools/tool_choice,
// 且 messages 正确转换(assistant tool_calls + tool 结果带 tool_call_id)。
func TestGenerateWithTools_OpenAIRequestConstruction(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &capturedBody)
		// 返回一个普通文本响应(非工具调用),结束循环。
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	req := aiprovider.GenerateRequest{
		SystemPrompt: "你是助手",
		Messages: []aiprovider.Message{
			{Role: aiprovider.RoleUser, Content: "查一下辉子版"},
			{Role: aiprovider.RoleAssistant, Content: "", ToolCalls: []aiprovider.ToolCall{
				{ID: "call_1", Name: "web_search", Arguments: `{"query":"辉子版"}`},
			}},
			{Role: aiprovider.RoleTool, ToolCallID: "call_1", Content: "辉子版是灰泽满的俗称"},
		},
		Tools: []aiprovider.Tool{
			{Name: "web_search", Description: "搜索", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)},
		},
	}
	result, err := p.GenerateWithTools(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateWithTools 错误: %v", err)
	}
	if result.Content != "done" {
		t.Errorf("Content = %q, 期望 done", result.Content)
	}

	// 验证请求体结构。
	msgs, _ := capturedBody["messages"].([]any)
	if len(msgs) != 4 { // system + user + assistant + tool
		t.Fatalf("messages 数量 = %d, 期望 4", len(msgs))
	}
	// system 消息。
	sysMsg, _ := msgs[0].(map[string]any)
	if sysMsg["role"] != "system" || sysMsg["content"] != "你是助手" {
		t.Errorf("system 消息错误: %+v", sysMsg)
	}
	// assistant 消息带 tool_calls。
	assistantMsg, _ := msgs[2].(map[string]any)
	calls, _ := assistantMsg["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("assistant tool_calls 数量 = %d, 期望 1", len(calls))
	}
	// tool 消息带 tool_call_id。
	toolMsg, _ := msgs[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("tool 消息错误: %+v", toolMsg)
	}
	// tools 声明 + tool_choice。
	tools, _ := capturedBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools 数量 = %d, 期望 1", len(tools))
	}
	if capturedBody["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, 期望 auto", capturedBody["tool_choice"])
	}
}

// TestGenerateWithTools_OpenAIToolCallsResponse 解析 tool_calls 响应,验证多轮循环的第一步。
func TestGenerateWithTools_OpenAIToolCallsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{
			"content":null,
			"tool_calls":[{"id":"call_42","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"test\"}"}}]
		}}]}`))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	req := aiprovider.GenerateRequest{
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "hi"}},
		Tools:    []aiprovider.Tool{{Name: "web_search"}},
	}
	result, err := p.GenerateWithTools(context.Background(), req)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call_42" {
		t.Fatalf("ToolCalls 解析错误: %+v", result.ToolCalls)
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, 期望 tool_calls", result.FinishReason)
	}
}

// TestGenerateWithTools_OpenAIEmptyToolsEquivalentToGenerate 零回归契约:
// 空 Tools 时 GenerateWithTools 行为应等价于普通 Generate(无 tools/tool_choice 字段)。
func TestGenerateWithTools_OpenAIEmptyToolsEquivalentToGenerate(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &capturedBody)
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"hello"}}]}`))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	req := aiprovider.GenerateRequest{
		SystemPrompt: "sys",
		Messages:     []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "prompt"}},
		// Tools 为空。
	}
	result, err := p.GenerateWithTools(context.Background(), req)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	if result.Content != "hello" {
		t.Errorf("Content = %q, 期望 hello", result.Content)
	}
	// 空 Tools 时不应有 tools/tool_choice 字段。
	if _, hasTools := capturedBody["tools"]; hasTools {
		t.Error("空 Tools 时不应发送 tools 字段")
	}
	if _, hasChoice := capturedBody["tool_choice"]; hasChoice {
		t.Error("空 Tools 时不应发送 tool_choice 字段")
	}
}
