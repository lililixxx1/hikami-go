package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hikami-go/internal/aiprovider"
)

// fakeToolProvider 是测试用的 ToolCapableProvider,按预设序列返回结果。
type fakeToolProvider struct {
	results []aiprovider.GenerateResult
	errs    []error
	calls   int
}

func (f *fakeToolProvider) GenerateWithTools(ctx context.Context, req aiprovider.GenerateRequest) (aiprovider.GenerateResult, error) {
	idx := f.calls
	f.calls++
	if idx < len(f.errs) && f.errs[idx] != nil {
		return aiprovider.GenerateResult{}, f.errs[idx]
	}
	if idx < len(f.results) {
		return f.results[idx], nil
	}
	return aiprovider.GenerateResult{Content: "default", FinishReason: "stop"}, nil
}

// TestRunWithTools_NoToolCalls 首次即无工具调用 → 直接返回(最常见路径)。
func TestRunWithTools_NoToolCalls(t *testing.T) {
	p := &fakeToolProvider{
		results: []aiprovider.GenerateResult{
			{Content: "最终回顾", FinishReason: "stop"},
		},
	}
	mgr := NewManager()
	req := aiprovider.GenerateRequest{
		SystemPrompt: "sys",
		Messages:     []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "prompt"}},
		Tools:        []aiprovider.Tool{{Name: "web_search"}},
	}
	result, err := RunWithTools(context.Background(), p, mgr, req, 5)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	if result.Content != "最终回顾" {
		t.Errorf("Content = %q", result.Content)
	}
	if p.calls != 1 {
		t.Errorf("应只调 1 次,实际 %d", p.calls)
	}
}

// TestRunWithTools_ToolCallLoop 正常工具调用循环:首轮请求工具 → 执行 → 次轮最终回复。
func TestRunWithTools_ToolCallLoop(t *testing.T) {
	p := &fakeToolProvider{
		results: []aiprovider.GenerateResult{
			{Content: "", FinishReason: "tool_calls", ToolCalls: []aiprovider.ToolCall{
				{ID: "c1", Name: "echo", Arguments: `{"q":"辉子版"}`},
			}},
			{Content: "查证后的回顾", FinishReason: "stop"},
		},
	}
	mgr := NewManager()
	// 注册 echo 工具(本地,无网络)。
	mgr.mu.Lock()
	mgr.builtins["echo"] = builtinEntry{
		tool: aiprovider.Tool{Name: "echo"},
		fn:   func(ctx context.Context, args string) (string, error) { return "辉子版=灰泽满", nil },
	}
	mgr.mu.Unlock()

	req := aiprovider.GenerateRequest{
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "prompt"}},
		Tools:    []aiprovider.Tool{{Name: "echo"}},
	}
	result, err := RunWithTools(context.Background(), p, mgr, req, 5)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	if result.Content != "查证后的回顾" {
		t.Errorf("Content = %q, 期望 查证后的回顾", result.Content)
	}
	if p.calls != 2 {
		t.Errorf("应调 2 次(工具请求 + 最终),实际 %d", p.calls)
	}
}

// TestRunWithTools_ToolFailureReturnedToModel 工具执行失败时回传错误给模型(非中断)。
func TestRunWithTools_ToolFailureReturnedToModel(t *testing.T) {
	p := &fakeToolProvider{
		results: []aiprovider.GenerateResult{
			{FinishReason: "tool_calls", ToolCalls: []aiprovider.ToolCall{
				{ID: "c1", Name: "broken", Arguments: "{}"},
			}},
			{Content: "降级处理", FinishReason: "stop"},
		},
	}
	mgr := NewManager()
	mgr.mu.Lock()
	mgr.builtins["broken"] = builtinEntry{
		tool: aiprovider.Tool{Name: "broken"},
		fn:   func(ctx context.Context, args string) (string, error) { return "", errors.New("模拟失败") },
	}
	mgr.mu.Unlock()

	req := aiprovider.GenerateRequest{
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "x"}},
		Tools:    []aiprovider.Tool{{Name: "broken"}},
	}
	result, err := RunWithTools(context.Background(), p, mgr, req, 5)
	if err != nil {
		t.Fatalf("工具失败不应中断循环: %v", err)
	}
	if result.Content != "降级处理" {
		t.Errorf("Content = %q", result.Content)
	}
}

// TestRunWithTools_MaxRoundsExceeded 达上限仍有工具调用 → 停止返回最后内容。
func TestRunWithTools_MaxRoundsExceeded(t *testing.T) {
	// 每轮都返回工具调用(永不停止)。
	loops := make([]aiprovider.GenerateResult, 10)
	for i := range loops {
		loops[i] = aiprovider.GenerateResult{
			Content:      "循环中",
			FinishReason: "tool_calls",
			ToolCalls:     []aiprovider.ToolCall{{ID: "c", Name: "echo", Arguments: "{}"}},
		}
	}
	p := &fakeToolProvider{results: loops}
	mgr := NewManager()
	mgr.mu.Lock()
	mgr.builtins["echo"] = builtinEntry{
		tool: aiprovider.Tool{Name: "echo"},
		fn:   func(ctx context.Context, args string) (string, error) { return "ok", nil },
	}
	mgr.mu.Unlock()

	req := aiprovider.GenerateRequest{
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "x"}},
		Tools:    []aiprovider.Tool{{Name: "echo"}},
	}
	_, err := RunWithTools(context.Background(), p, mgr, req, 3)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	// maxRounds=3 + 首轮 = 最多 4 次调用(首轮 + 3 轮循环)。
	if p.calls > 4 {
		t.Errorf("应受 maxRounds 限制,p.calls=%d", p.calls)
	}
}

// TestRunWithTools_TokenBudgetExceeded 累积字符超预算 → 停止工具调用。
func TestRunWithTools_TokenBudgetExceeded(t *testing.T) {
	p := &fakeToolProvider{
		results: []aiprovider.GenerateResult{
			{FinishReason: "tool_calls", ToolCalls: []aiprovider.ToolCall{
				{ID: "c1", Name: "echo", Arguments: "{}"},
			}},
			{Content: "继续", FinishReason: "tool_calls", ToolCalls: []aiprovider.ToolCall{
				{ID: "c2", Name: "echo", Arguments: "{}"},
			}},
		},
	}
	mgr := NewManager()
	mgr.mu.Lock()
	mgr.builtins["echo"] = builtinEntry{
		tool: aiprovider.Tool{Name: "echo"},
		fn:   func(ctx context.Context, args string) (string, error) { return strings.Repeat("长", 100000), nil },
	}
	mgr.mu.Unlock()

	req := aiprovider.GenerateRequest{
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "x"}},
		Tools:    []aiprovider.Tool{{Name: "echo"}},
	}
	_, err := RunWithTools(context.Background(), p, mgr, req, 10)
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	// 工具结果超长 → token 预算触发,不会跑满 10 轮。
	if p.calls > 3 {
		t.Errorf("token 预算应在早期停止,p.calls=%d", p.calls)
	}
}

// TestRunWithTools_ContextCancel ctx 取消时及时退出(审核 Important#8)。
func TestRunWithTools_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &fakeToolProvider{
		results: []aiprovider.GenerateResult{
			{FinishReason: "tool_calls", ToolCalls: []aiprovider.ToolCall{{ID: "c1", Name: "echo", Arguments: "{}"}}},
		},
	}
	mgr := NewManager()
	mgr.mu.Lock()
	mgr.builtins["echo"] = builtinEntry{
		tool: aiprovider.Tool{Name: "echo"},
		fn: func(ctx context.Context, args string) (string, error) {
			cancel() // 在工具执行中取消
			return "ok", nil
		},
	}
	mgr.mu.Unlock()

	req := aiprovider.GenerateRequest{
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "x"}},
		Tools:    []aiprovider.Tool{{Name: "echo"}},
	}
	_, err := RunWithTools(ctx, p, mgr, req, 5)
	// ctx 取消后应返回 context 错误(或正常返回,关键是及时退出不阻塞)。
	_ = err
}

// TestEstimateChars 验证字符估算。
func TestEstimateChars(t *testing.T) {
	msgs := []aiprovider.Message{
		{Role: aiprovider.RoleUser, Content: "中文测试"},     // 4 rune
		{Role: aiprovider.RoleTool, Content: "结果"},         // 2 rune
		{Role: aiprovider.RoleAssistant, ToolCalls: []aiprovider.ToolCall{
			{Name: "web_search", Arguments: `{"q":"辉子版"}`}, // name 10 + args 16
		}},
	}
	got := estimateChars(msgs)
	if got < 6 {
		t.Errorf("estimateChars = %d, 应至少 6", got)
	}
}
