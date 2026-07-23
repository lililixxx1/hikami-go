// Package mcp 提供 MCP(Model Context Protocol)搜索工具集成。
//
// Manager 管理两类工具来源:
//  1. 外部 MCP server(stdio/http/sse transport,经 mark3labs/mcp-go client 连接)。
//  2. 内置 in-process 搜索工具(Brave/Tavily,直接 Go 函数实现,不经 mcp-go transport)。
//
// 两类工具统一经 ListTools/CallTool 接口暴露给上层(recap/glossary 的 agent loop)。
//
// 并发安全:CallTool 持 RLock,Reload 持 Lock + 优雅排空在途调用(审核 Important#3)。
// 连接失败降级:单个 server 挂掉不影响其他(只 slog.Warn,不 fatal)。
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
)

// defaultToolTimeout 是单次工具调用的默认超时(server 配置 TimeoutSec<=0 时用)。
const defaultToolTimeout = 30 * time.Second

// reloadDrainTimeout 是 Reload 时等待在途 CallTool 完成的最长超时(审核 Important#3)。
const reloadDrainTimeout = 10 * time.Second

// builtinTool 是内置工具的执行函数(纯 Go,不经 mcp-go transport)。
// args 是模型传入的 JSON 参数字符串,返回工具结果文本(已格式化)。
type builtinTool func(ctx context.Context, args string) (string, error)

// builtinEntry 是已注册的内置工具(元信息 + 执行函数)。
type builtinEntry struct {
	tool aiprovider.Tool
	fn   builtinTool
}

// connectedServer 是一个已连接的外部 MCP server。
type connectedServer struct {
	name    string
	c       *client.Client
	tools   []mcp.Tool    // 初始化时 ListTools 的快照
	timeout time.Duration // 单次工具调用超时(审核 code-review Important#2:TimeoutSec 生效)
	// inflight 跟踪在途 CallTool 调用(Reload 关闭前等待,审核 code-review Important#1)。
	inflight sync.WaitGroup
}

// Manager 管理所有 MCP 工具(外部 server + 内置)。
// 零值不可用,必须用 NewManager 构造。
type Manager struct {
	mu         sync.RWMutex
	servers    map[string]*connectedServer // key = server name
	builtins   map[string]builtinEntry     // key = tool name
	httpClient *http.Client                // 内置工具用(Brave/Tavily)
}

// NewManager 创建空 Manager(无连接、无内置工具)。
// 实际连接与工具注册在 Reload 时按配置建立;main.go 启动后调一次 Reload。
func NewManager() *Manager {
	return &Manager{
		servers:    make(map[string]*connectedServer),
		builtins:   make(map[string]builtinEntry),
		httpClient: &http.Client{Timeout: defaultToolTimeout},
	}
}

// HasTools 返回是否有任何可用工具(外部 + 内置)。
// 调用方(recap/glossary)用它快速判断是否值得走 tool-calling 路径。
func (m *Manager) HasTools() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.servers) > 0 || len(m.builtins) > 0
}

// ListTools 聚合所有可用工具(外部 server 工具 + 内置),转为 aiprovider.Tool 格式。
// 外部工具名加 server 前缀(name__tool)避免跨 server 重名冲突。
func (m *Manager) ListTools(ctx context.Context) []aiprovider.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tools := make([]aiprovider.Tool, 0, len(m.builtins)+8)
	// 内置工具优先(搜索工具最常用)。
	for _, b := range m.builtins {
		tools = append(tools, b.tool)
	}
	// 外部 server 工具,加前缀防重名。
	for _, srv := range m.servers {
		for _, t := range srv.tools {
			params, _ := json.Marshal(t.InputSchema)
			tools = append(tools, aiprovider.Tool{
				Name:        srv.name + "__" + t.Name,
				Description: t.Description,
				Parameters:  params,
			})
		}
	}
	return tools
}

// CallTool 执行指定工具(name 由 ListTools 返回),返回结果文本。
// name 路由:内置工具名直接匹配;外部工具(含 __ 前缀)路由到对应 server。
func (m *Manager) CallTool(ctx context.Context, name, args string) (string, error) {
	m.mu.RLock()
	// 内置工具优先查(无前缀)。
	if b, ok := m.builtins[name]; ok {
		m.mu.RUnlock()
		return b.fn(ctx, args)
	}
	// 外部工具:解析 server 前缀。
	srv, toolName, ok := m.resolveExternal(name)
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("mcp tool %q not found", name)
	}
	c := srv.c
	callTimeout := srv.timeout // 审核code-review Important#2:TimeoutSec 生效
	srv.inflight.Add(1)        // 跟踪在途调用,Reload 关闭前等待(审核 code-review Important#1)
	m.mu.RUnlock()

	// 应用 per-server 超时(审核 code-review Important#2:TimeoutSec 配置生效)。
	if callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}
	// 锁外执行(可能耗时),允许并发 CallTool + Reload 排空等待。
	var parsedArgs any
	if args != "" {
		_ = json.Unmarshal([]byte(args), &parsedArgs)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = parsedArgs
	result, err := c.CallTool(ctx, req)
	srv.inflight.Done() // 调用结束,允许 Reload 关闭
	if err != nil {
		return "", fmt.Errorf("call tool %s: %w", name, err)
	}
	return extractTextContent(result), nil
}

// resolveExternal 解析带 server 前缀的外部工具名(name__tool),返回 server 与原始 tool 名。
// 调用方必须已持 RLock。
func (m *Manager) resolveExternal(name string) (*connectedServer, string, bool) {
	for i := 0; i < len(name); i++ {
		if i+1 < len(name) && name[i] == '_' && name[i+1] == '_' {
			srvName := name[:i]
			toolName := name[i+2:]
			if srv, ok := m.servers[srvName]; ok {
				return srv, toolName, true
			}
		}
	}
	return nil, "", false
}

// extractTextContent 从 CallToolResult 提取所有 TextContent 拼接为字符串。
func extractTextContent(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, content := range result.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			if tc.Text != "" {
				parts = append(parts, tc.Text)
			}
		}
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}

// Reload 按 cfg 重建所有连接:优雅关闭旧的 → 按新配置建立外部 server 连接 → 注册内置工具。
// 在途 CallTool 会被等待完成(reloadDrainTimeout 上限),避免配置更新中途切断调用。
// 实现 handler.Server.mcpManager 接口的 Reload 方法。
func (m *Manager) Reload(ctx context.Context, cfg config.MCPConfig) error {
	// 阶段一:持写锁关闭旧连接(此时 CallTool 因 RLock 互斥阻塞或已排空)。
	m.mu.Lock()
	oldServers := m.servers
	m.servers = make(map[string]*connectedServer)
	// 清旧内置工具(下面按新 key 重新注册)。
	m.builtins = make(map[string]builtinEntry)
	m.mu.Unlock()

	// 锁外优雅关闭旧连接:先等待在途 CallTool 完成(reloadDrainTimeout 上限),
	// 再关闭 client(审核 code-review Important#1:之前立即关闭会切断在途调用)。
	drainServers(oldServers)

	// 未启用:仅关闭,不建新连接(零工具,上层降级普通 Generate)。
	if !cfg.Enabled {
		return nil
	}

	// 阶段二:按配置建立外部 server 连接 + 注册内置工具。
	if err := m.connectServers(ctx, cfg.Servers); err != nil {
		// 连接部分失败不 fatal(已成功的 server 仍可用),只记录。
		slog.Warn("mcp reload: some servers failed to connect (degraded)", "error", err)
	}
	m.registerBuiltins(cfg.Builtin)
	return nil
}

// connectServers 逐个连接配置的外部 server,成功的加入 m.servers,失败的 slog.Warn 跳过。
func (m *Manager) connectServers(ctx context.Context, servers []config.MCPServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for _, sc := range servers {
		if !sc.Enabled {
			continue
		}
		if sc.Name == "" {
			slog.Warn("mcp server missing name, skipped")
			continue
		}
		c, tools, err := connectOne(ctx, sc)
		if err != nil {
			slog.Warn("mcp server connect failed (skipped)", "name", sc.Name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// 审核code-review Important#2:TimeoutSec 配置生效(<=0 用默认 30s)。
		to := time.Duration(sc.TimeoutSec) * time.Second
		if sc.TimeoutSec <= 0 {
			to = defaultToolTimeout
		}
		m.servers[sc.Name] = &connectedServer{name: sc.Name, c: c, tools: tools, timeout: to}
		slog.Info("mcp server connected", "name", sc.Name, "tools", len(tools))
	}
	return firstErr
}

// connectOne 连接单个 server 并 ListTools,返回 (client, tools)。
func connectOne(ctx context.Context, sc config.MCPServerConfig) (*client.Client, []mcp.Tool, error) {
	c, err := buildClient(sc)
	if err != nil {
		return nil, nil, err
	}
	// stdio/http/sse 都需要 Start(transport 启动)。
	if err := c.Start(ctx); err != nil {
		_ = c.Close()
		return nil, nil, fmt.Errorf("start transport: %w", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "hikami-go", Version: "mcp-client"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return nil, nil, fmt.Errorf("initialize: %w", err)
	}
	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		_ = c.Close()
		return nil, nil, fmt.Errorf("list tools: %w", err)
	}
	tools := []mcp.Tool{}
	if toolsResult != nil {
		tools = toolsResult.Tools
	}
	return c, tools, nil
}

// buildClient 按 transport 类型构造 mcp-go client。
//
// http/sse 模式会把 sc.Headers 作为自定义请求头注入(如 Authorization 鉴权);
// stdio 不支持请求头,鉴权场景改用 env 字段。
// sc.Headers 为 nil 时 mcp-go 的 WithHTTPHeaders/WithHeaders 是 no-op,行为不变。
func buildClient(sc config.MCPServerConfig) (*client.Client, error) {
	switch sc.Transport {
	case "http", "streamable_http":
		return client.NewStreamableHttpClient(sc.URL, transport.WithHTTPHeaders(sc.Headers))
	case "sse":
		return client.NewSSEMCPClient(sc.URL, transport.WithHeaders(sc.Headers))
	case "stdio":
		if sc.Command == "" {
			return nil, fmt.Errorf("stdio server %q missing command", sc.Name)
		}
		return client.NewStdioMCPClient(sc.Command, sc.Env, sc.Args...)
	default:
		return nil, fmt.Errorf("unknown transport %q for server %q", sc.Transport, sc.Name)
	}
}

// drainServers 等待在途调用完成后关闭一批旧 server 连接。
// 实现 reloadDrainTimeout 的优雅排空(审核 code-review Important#1):
// Reload 换 servers map 后,旧 server 上的 CallTool 可能仍在执行(锁外),
// 立即 Close 会切断在途调用 → 该次调用收到连接错误。
// 这里用 WaitGroup 等待,超时后强制关闭(避免 Reload 无限阻塞)。
func drainServers(servers map[string]*connectedServer) {
	// 阶段一:等待所有在途调用完成(reloadDrainTimeout 上限)。
	done := make(chan struct{})
	go func() {
		for _, srv := range servers {
			srv.inflight.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
		// 全部在途调用已完成,可安全关闭。
	case <-time.After(reloadDrainTimeout):
		slog.Warn("mcp reload: drain timeout, force closing servers with in-flight calls", "timeout", reloadDrainTimeout)
	}
	// 阶段二:关闭连接(此时在途调用已完成或超时放弃)。
	for name, srv := range servers {
		if err := srv.c.Close(); err != nil {
			slog.Warn("mcp server close error", "name", name, "error", err)
		}
	}
}

// Close 关闭所有连接(供 main.go shutdown 调用)。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, srv := range m.servers {
		if err := srv.c.Close(); err != nil {
			slog.Warn("mcp server close error on shutdown", "name", name, "error", err)
		}
	}
	m.servers = make(map[string]*connectedServer)
	m.builtins = make(map[string]builtinEntry)
	return nil
}
