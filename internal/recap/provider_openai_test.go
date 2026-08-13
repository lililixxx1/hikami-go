package recap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"hikami-go/internal/aiprovider"
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
