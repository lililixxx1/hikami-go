package recap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

// emptyContentResponse 返回 content 空、finish_reason=stop 的 OpenAI 响应,
// 模拟 DeepSeek 等 reasoning 模型偶发的"reasoning 完但正文未输出"(ISSUE-007)。
func emptyContentResponse() string {
	return `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"","reasoning_content":"思考中..."}}]}`
}

// lengthEmptyContentResponse 返回 content 空、finish_reason=length 的 OpenAI 响应,
// 模拟确定性的 token 预算耗尽(reasoning+completion 超过 max_tokens)。
func lengthEmptyContentResponse() string {
	return `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"","reasoning_content":"思考中..."}}]}`
}

// contentFilterEmptyContentResponse 返回 content 空、finish_reason=content_filter 的 OpenAI 响应,
// 模拟确定性的内容安全过滤(同输入同结果,不应重试,与 length 同类)。
func contentFilterEmptyContentResponse() string {
	return `{"choices":[{"finish_reason":"content_filter","message":{"role":"assistant","content":""}}]}`
}

func validContentResponse() string {
	return `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"# 回顾\n\n正文内容"}}]}`
}

// TestGenerate_RetriesEmptyContentThenSucceeds 验证空 content 触发重试,第二次成功则返回正文。
func TestGenerate_RetriesEmptyContentThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_, _ = w.Write([]byte(emptyContentResponse()))
			return
		}
		_, _ = w.Write([]byte(validContentResponse()))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	result, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty content after retry")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls (1 empty + 1 success), got %d", got)
	}
}

// TestGenerate_EmptyContentRetriesExhausted 验证持续空 content 时重试耗尽,返回带 finish_reason 与次数的错误。
func TestGenerate_EmptyContentRetriesExhausted(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(emptyContentResponse()))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	_, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err == nil {
		t.Fatal("expected error when all attempts return empty content")
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("expected error mentioning 3 attempts, got: %v", err)
	}
	if !strings.Contains(err.Error(), "finish_reason=stop") {
		t.Fatalf("expected error containing finish_reason, got: %v", err)
	}
	// maxEmptyContentRetries=2 → 共 3 次尝试(1 次首调 + 2 次重试)
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 retries), got %d", got)
	}
}

// TestGenerate_NoRetryOnHTTPError 验证非 2xx HTTP 错误不触发空 content 重试,直接返回。
func TestGenerate_NoRetryOnHTTPError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server error"}`))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	_, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected no retry on HTTP error (1 call), got %d", got)
	}
}

// TestGenerate_NoRetryOnFinishReasonLength 验证 finish_reason=length 的空 content 是确定性
// token 预算耗尽,不重试(否则只会重复失败并多花付费调用),直接返回带 length 提示的错误。
func TestGenerate_NoRetryOnFinishReasonLength(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(lengthEmptyContentResponse()))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	_, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err == nil {
		t.Fatal("expected error on finish_reason=length empty content")
	}
	if !strings.Contains(err.Error(), "finish_reason=length") {
		t.Fatalf("expected error mentioning finish_reason=length, got: %v", err)
	}
	if strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("length 应立即终止不带重试次数, got: %v", err)
	}
	// 确定性失败:只调一次,不重试。
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected no retry on finish_reason=length (1 call), got %d", got)
	}
}

// TestGenerate_NoRetryOnFinishReasonContentFilter 验证 finish_reason=content_filter 同属确定性
// (同输入同结果),不重试直接返回,与 length 一致(避免对确定性失败白烧付费调用)。
func TestGenerate_NoRetryOnFinishReasonContentFilter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(contentFilterEmptyContentResponse()))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	_, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err == nil {
		t.Fatal("expected error on finish_reason=content_filter empty content")
	}
	if !strings.Contains(err.Error(), "finish_reason=content_filter") {
		t.Fatalf("expected error mentioning finish_reason=content_filter, got: %v", err)
	}
	// 确定性失败:只调一次,不重试。
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected no retry on content_filter (1 call), got %d", got)
	}
}

// TestGenerate_FirstCallSucceedsDoesNotRetry 验证首调即返回正文时不触发重试(恰好 1 次调用)。
func TestGenerate_FirstCallSucceedsDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validContentResponse()))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	result, err := p.Generate(context.Background(), "sys", "user", session.Session{})
	if err != nil {
		t.Fatalf("expected success on first call, got error: %v", err)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty content")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call (no retry on success), got %d", got)
	}
}

// TestGenerateWithTools_RetriesEmptyContentThenSucceeds 验证工具路径(GenerateWithTools)同样
// 对空 content 做兜底重试(ISSUE-007:I-2 对称性——MCP 工具开启时 recap 走本路径)。
func TestGenerateWithTools_RetriesEmptyContentThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_, _ = w.Write([]byte(emptyContentResponse()))
			return
		}
		_, _ = w.Write([]byte(validContentResponse()))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	req := aiprovider.GenerateRequest{
		SystemPrompt: "sys",
		Messages:     []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "user"}},
	}
	result, err := p.GenerateWithTools(context.Background(), req)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty content after retry")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls (1 empty + 1 success), got %d", got)
	}
}

// TestApplyV4ThinkingControls 验证 V4 思考控制参数的合入规则:缺省零发送(零回归)、
// 显式 false/true 映射 thinking.type、reasoning_effort 去空白后非空才发送。
// 背景(2026-08-22 十人联动场次):DeepSeek 非流式请求思考超 ~180s 服务端时间墙会被掐断
// 返回空 content,thinking_enabled=false 跳过思考直接作答。
func TestApplyV4ThinkingControls(t *testing.T) {
	t.Run("缺省零发送", func(t *testing.T) {
		body := map[string]any{}
		applyV4ThinkingControls(body, config.RecapAIConfig{})
		if len(body) != 0 {
			t.Fatalf("缺省配置必须零发送, got %v", body)
		}
	})
	t.Run("false 映射 disabled", func(t *testing.T) {
		body := map[string]any{}
		off := false
		applyV4ThinkingControls(body, config.RecapAIConfig{ThinkingEnabled: &off})
		th, ok := body["thinking"].(map[string]any)
		if !ok || th["type"] != "disabled" {
			t.Fatalf("thinking = %v, want type=disabled", body["thinking"])
		}
	})
	t.Run("true 映射 enabled", func(t *testing.T) {
		body := map[string]any{}
		on := true
		applyV4ThinkingControls(body, config.RecapAIConfig{ThinkingEnabled: &on})
		th, ok := body["thinking"].(map[string]any)
		if !ok || th["type"] != "enabled" {
			t.Fatalf("thinking = %v, want type=enabled", body["thinking"])
		}
	})
	t.Run("effort 去空白后发送", func(t *testing.T) {
		body := map[string]any{}
		applyV4ThinkingControls(body, config.RecapAIConfig{ReasoningEffort: " low "})
		if body["reasoning_effort"] != "low" {
			t.Fatalf("reasoning_effort = %v, want low", body["reasoning_effort"])
		}
	})
	t.Run("effort 空白不发送", func(t *testing.T) {
		body := map[string]any{}
		applyV4ThinkingControls(body, config.RecapAIConfig{ReasoningEffort: "  "})
		if _, ok := body["reasoning_effort"]; ok {
			t.Fatalf("空白 effort 不应发送, got %v", body)
		}
	})
}

// TestGenerate_SendsThinkingControlsWhenConfigured 端到端钉死请求体契约:
// 未配置时不含 thinking/reasoning_effort(与历史请求体一致);配置后携带对应参数。
func TestGenerate_SendsThinkingControlsWhenConfigured(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validContentResponse()))
	}))
	defer server.Close()

	p := newToolTestProvider(t, server)
	if _, err := p.Generate(context.Background(), "sys", "user", session.Session{}); err != nil {
		t.Fatalf("缺省配置 generate: %v", err)
	}
	off := false
	p.cfg.RecapAI.ThinkingEnabled = &off
	p.cfg.RecapAI.ReasoningEffort = "low"
	if _, err := p.Generate(context.Background(), "sys", "user", session.Session{}); err != nil {
		t.Fatalf("配置后 generate: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(bodies))
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &first); err != nil {
		t.Fatalf("unmarshal first body: %v", err)
	}
	if err := json.Unmarshal([]byte(bodies[1]), &second); err != nil {
		t.Fatalf("unmarshal second body: %v", err)
	}
	if _, ok := first["thinking"]; ok {
		t.Fatalf("缺省请求体不应含 thinking: %s", bodies[0])
	}
	if _, ok := first["reasoning_effort"]; ok {
		t.Fatalf("缺省请求体不应含 reasoning_effort: %s", bodies[0])
	}
	th, ok := second["thinking"].(map[string]any)
	if !ok || th["type"] != "disabled" {
		t.Fatalf("配置后 thinking = %v, want type=disabled: %s", second["thinking"], bodies[1])
	}
	if second["reasoning_effort"] != "low" {
		t.Fatalf("配置后 reasoning_effort = %v, want low: %s", second["reasoning_effort"], bodies[1])
	}
}

// TestTruncateForLog 验证 rune 边界截断:不在多字节字符(如中文)中间切断,
// 全 ASCII 按字节精确切,超短/空/max=0 直返,结果恒为合法 UTF-8。
func TestTruncateForLog(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short passthrough", "abc", 800, "abc"},
		{"empty", "", 800, ""},
		{"max zero", "abc", 0, ""},
		{"ascii exact", strings.Repeat("a", 800), 800, strings.Repeat("a", 800)},
		{"ascii truncate", strings.Repeat("a", 1000), 800, strings.Repeat("a", 800)},
		// 中文每字符 3 字节:"你好世界你好" = 18 字节,max=10 应回退到最近的 rune 边界 9(3 个中文字符)。
		{"utf8 boundary", "你好世界你好", 10, "你好世"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateForLog(tc.in, tc.max)
			if got != tc.want {
				t.Fatalf("truncateForLog(%q, %d) = %q (len %d), want %q", tc.in, tc.max, got, len(got), tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("result not valid UTF-8: %q", got)
			}
		})
	}
}
