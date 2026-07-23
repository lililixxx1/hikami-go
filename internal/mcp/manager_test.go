package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
)

// TestBuiltinRegister_BraveKey 验证有 Brave key 时注册 web_search 工具。
func TestBuiltinRegister_BraveKey(t *testing.T) {
	m := NewManager()
	m.registerBuiltins(config.MCPBuiltinConfig{BraveAPIKey: "test-key"})

	tools := m.ListTools(context.Background())
	found := false
	for _, tool := range tools {
		if tool.Name == "web_search" {
			found = true
			if !strings.Contains(tool.Description, "搜索") {
				t.Errorf("web_search description 缺少搜索说明: %q", tool.Description)
			}
		}
	}
	if !found {
		t.Fatal("有 Brave key 时应注册 web_search 工具")
	}
	if !m.HasTools() {
		t.Error("HasTools 应为 true")
	}
}

// TestBuiltinRegister_NoKey 验证无 key 时不注册内置工具(降级)。
func TestBuiltinRegister_NoKey(t *testing.T) {
	m := NewManager()
	m.registerBuiltins(config.MCPBuiltinConfig{}) // 两个 key 都空

	if m.HasTools() {
		t.Error("无 key 时不应有内置工具")
	}
	tools := m.ListTools(context.Background())
	if len(tools) != 0 {
		t.Errorf("无 key 时工具数 = %d, 期望 0", len(tools))
	}
}

// TestCallBraveSearch_Format 验证 Brave 搜索结果格式化逻辑。
func TestCallBraveSearch_Format(t *testing.T) {
	// 直接测 formatSearchResults 的格式化逻辑(实际 HTTP 调用需真实 key,不在此测)。
	result := formatSearchResults([]struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}{
		{Title: "辉子版", URL: "https://example.com", Description: "灰泽满的俗称"},
	})
	if !strings.Contains(result, "[1] 辉子版") {
		t.Errorf("结果应含序号+标题: %q", result)
	}
	if !strings.Contains(result, "https://example.com") {
		t.Errorf("结果应含 URL: %q", result)
	}
	if !strings.Contains(result, "灰泽满的俗称") {
		t.Errorf("结果应含描述: %q", result)
	}
}

// TestCallTavilySearch_Format 验证 Tavily 搜索结果格式化。
func TestCallTavilySearch_Format(t *testing.T) {
	result := formatTavilyResults([]struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	}{
		{Title: "测试结果", URL: "http://example.com", Content: "这是内容"},
	})
	if !strings.Contains(result, "测试结果") {
		t.Errorf("Tavily 结果格式化错误: %q", result)
	}
}

// TestCapResult_Truncation 验证工具结果超长被截断。
func TestCapResult_Truncation(t *testing.T) {
	long := strings.Repeat("a", maxToolResultChars+100)
	capped := capResult(long)
	if !strings.Contains(capped, "结果已截断") {
		t.Error("超长结果应被截断并标记")
	}
	if len([]rune(capped)) > maxToolResultChars+50 { // 加上截断标记的余量
		t.Errorf("截断后长度异常: %d", len([]rune(capped)))
	}
}

// TestCallTool_BuiltinRoute 验证 CallTool 路由到内置工具。
func TestCallTool_BuiltinRoute(t *testing.T) {
	m := NewManager()
	// 注册一个假内置工具,直接测路由。
	m.mu.Lock()
	m.builtins["echo"] = builtinEntry{
		tool: aiprovider.Tool{Name: "echo", Description: "回显工具"},
		fn: func(ctx context.Context, args string) (string, error) {
			return "echo:" + args, nil
		},
	}
	m.mu.Unlock()

	result, err := m.CallTool(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatalf("CallTool 错误: %v", err)
	}
	if result != "echo:hello" {
		t.Errorf("结果 = %q, 期望 echo:hello", result)
	}
}

// TestCallTool_NotFound 验证未知工具名返回错误。
func TestCallTool_NotFound(t *testing.T) {
	m := NewManager()
	_, err := m.CallTool(context.Background(), "nonexistent", "{}")
	if err == nil {
		t.Fatal("未知工具应返回错误")
	}
}

// TestReload_Disabled 验证 Enabled=false 时 Reload 清空工具(零回归)。
func TestReload_Disabled(t *testing.T) {
	m := NewManager()
	// 先注册一个内置工具
	m.registerBuiltins(config.MCPBuiltinConfig{BraveAPIKey: "key"})
	if !m.HasTools() {
		t.Fatal("前置:应有工具")
	}
	// Reload 时禁用
	if err := m.Reload(context.Background(), config.MCPConfig{Enabled: false}); err != nil {
		t.Fatalf("Reload 错误: %v", err)
	}
	if m.HasTools() {
		t.Error("Enabled=false 时 Reload 后不应有工具")
	}
}

// TestReload_BuiltinReregister 验证 Reload 按新配置重新注册内置工具。
func TestReload_BuiltinReregister(t *testing.T) {
	m := NewManager()
	// 第一次:只有 Brave
	if err := m.Reload(context.Background(), config.MCPConfig{
		Enabled: true,
		Builtin: config.MCPBuiltinConfig{BraveAPIKey: "k1"},
	}); err != nil {
		t.Fatalf("Reload 1 错误: %v", err)
	}
	tools := m.ListTools(context.Background())
	if len(tools) != 1 || tools[0].Name != "web_search" {
		t.Fatalf("Reload 1 后工具: %+v", tools)
	}
	// 第二次:换成 Tavily
	if err := m.Reload(context.Background(), config.MCPConfig{
		Enabled: true,
		Builtin: config.MCPBuiltinConfig{TavilyAPIKey: "k2"},
	}); err != nil {
		t.Fatalf("Reload 2 错误: %v", err)
	}
	tools = m.ListTools(context.Background())
	if len(tools) != 1 || tools[0].Name != "tavily_search" {
		t.Fatalf("Reload 2 后应只剩 tavily_search, got: %+v", tools)
	}
}

// TestConcurrentCallToolDuringReload 验证并发 CallTool 与 Reload 不崩溃(审核 Important#8)。
// 这是 RWMutex 设计的核心场景:Reload 持写锁,CallTool 持读锁。
// 用纯本地 echo 工具(不触发网络),专注测并发安全。
func TestConcurrentCallToolDuringReload(t *testing.T) {
	m := NewManager()
	// 注册本地 echo 工具(不依赖网络)。
	m.mu.Lock()
	m.builtins["echo"] = builtinEntry{
		tool: aiprovider.Tool{Name: "echo", Description: "回显"},
		fn: func(ctx context.Context, args string) (string, error) {
			return "echo:" + args, nil
		},
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	done := make(chan struct{})
	// 持续 CallTool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_, _ = m.CallTool(context.Background(), "echo", "x")
			}
		}
	}()
	// 并发 Reload 多次(Reload 会清空 builtins 再重建,需重新注入 echo)
	for i := 0; i < 10; i++ {
		_ = m.Reload(context.Background(), config.MCPConfig{Enabled: false})
		// Reload 后重新注入 echo(Enabled=false 会清空 builtins)
		m.mu.Lock()
		m.builtins["echo"] = builtinEntry{
			tool: aiprovider.Tool{Name: "echo"},
			fn:   func(ctx context.Context, args string) (string, error) { return "echo:" + args, nil },
		}
		m.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	close(done)
	wg.Wait()
	// 若有 race,go test -race 会捕获(手动跑加 -race)
}

// TestResolveExternalPrefix 验证外部工具名前缀解析(server__tool)。
func TestResolveExternalPrefix(t *testing.T) {
	m := NewManager()
	m.mu.Lock()
	m.servers["myserver"] = &connectedServer{name: "myserver"}
	m.mu.Unlock()

	srv, tool, ok := m.resolveExternal("myserver__search")
	if !ok || srv == nil || tool != "search" {
		t.Errorf("解析失败: srv=%v tool=%q ok=%v", srv, tool, ok)
	}
	// 未知 server
	_, _, ok = m.resolveExternal("unknown__tool")
	if ok {
		t.Error("未知 server 前缀应返回 ok=false")
	}
}

// TestDrainServers_WaitGroupSemantics 验证 inflight WaitGroup 的排空语义
// (审核code-review Important#1:Reload 等待在途调用)。
// 注:不调完整 drainServers(它会 Close nil client panic),只验证 WaitGroup 行为。
func TestDrainServers_WaitGroupSemantics(t *testing.T) {
	srv := &connectedServer{name: "test"}
	// 模拟一个在途调用。
	srv.inflight.Add(1)
	released := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond) // 模拟调用耗时
		srv.inflight.Done()
		close(released)
	}()

	// Wait 应阻塞直到 Done 被调用。
	done := make(chan struct{})
	go func() {
		srv.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Wait 返回,说明 Done 已调用。
		select {
		case <-released:
			// 正确
		default:
			t.Error("Wait 应在 Done 之后返回")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait 超时,Done 未被调用")
	}
}
