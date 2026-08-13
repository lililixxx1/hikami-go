package recap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"hikami-go/internal/session"
)

// emptyContentResponse 返回 content 空、finish_reason=stop 的 OpenAI 响应,
// 模拟 DeepSeek 等 reasoning 模型偶发的"reasoning 完但正文未输出"(ISSUE-007)。
func emptyContentResponse() string {
	return `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"","reasoning_content":"思考中..."}}]}`
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
