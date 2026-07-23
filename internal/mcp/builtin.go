package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
)

// maxToolResultChars 是单个工具返回结果的字符上限(审核 Minor#9)。
// 超出则截断并追加截断标记,避免长搜索结果撑爆模型上下文。
const maxToolResultChars = 1500

// maxSearchResults 是单次搜索返回的结果条数上限。
const maxSearchResults = 5

// registerBuiltins 按 builtin 配置注册内置搜索工具。
// key 为空则对应工具不注册(降级);两个 key 都空则无内置工具(仅外部 server 可用)。
func (m *Manager) registerBuiltins(cfg config.MCPBuiltinConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if k := cfg.EffectiveBraveAPIKey(); k != "" {
		m.builtins["web_search"] = builtinEntry{
			tool: aiprovider.Tool{
				Name:        "web_search",
				Description: "搜索网络获取最新信息。用于核实专有名词(人名/游戏名/作品名/术语)的标准写法。参数:query(搜索词,必填),count(结果数,可选,默认5)。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"搜索关键词"},"count":{"type":"integer","description":"返回结果数(最大5)","default":5}},"required":["query"]}`),
			},
			fn: func(ctx context.Context, args string) (string, error) {
				return m.callBraveSearch(ctx, k, args)
			},
		}
		slog.Info("mcp builtin registered", "tool", "web_search", "provider", "brave")
	}
	if k := cfg.EffectiveTavilyAPIKey(); k != "" {
		m.builtins["tavily_search"] = builtinEntry{
			tool: aiprovider.Tool{
				Name:        "tavily_search",
				Description: "AI 优化的网络搜索,适合获取事实性信息。用于核实专有名词标准写法。参数:query(搜索词,必填),max_results(可选,默认5)。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"搜索关键词"},"max_results":{"type":"integer","description":"返回结果数(最大5)","default":5}},"required":["query"]}`),
			},
			fn: func(ctx context.Context, args string) (string, error) {
				return m.callTavilySearch(ctx, k, args)
			},
		}
		slog.Info("mcp builtin registered", "tool", "tavily_search", "provider", "tavily")
	}
}

// braveSearchArgs / tavilySearchArgs 是工具入参的反序列化结构。
type braveSearchArgs struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

// callBraveSearch 调用 Brave Search API,返回紧凑格式文本结果。
func (m *Manager) callBraveSearch(ctx context.Context, apiKey, args string) (string, error) {
	var a braveSearchArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	count := a.Count
	if count <= 0 || count > maxSearchResults {
		count = maxSearchResults
	}

	reqURL := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(a.Query) + fmt.Sprintf("&count=%d", count)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("brave search request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("brave search http %d: %s", resp.StatusCode, truncateBody(body))
	}

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse brave response: %w", err)
	}

	return formatSearchResults(result.Web.Results), nil
}

// tavilySearchArgs Tavily 入参。
type tavilySearchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

// callTavilySearch 调用 Tavily Search API。
func (m *Manager) callTavilySearch(ctx context.Context, apiKey, args string) (string, error) {
	var a tavilySearchArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	maxR := a.MaxResults
	if maxR <= 0 || maxR > maxSearchResults {
		maxR = maxSearchResults
	}

	reqBody, _ := json.Marshal(map[string]any{
		"query":       a.Query,
		"max_results": maxR,
		"api_key":     apiKey,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", strings.NewReader(string(reqBody)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("tavily search request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("tavily search http %d: %s", resp.StatusCode, truncateBody(body))
	}

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse tavily response: %w", err)
	}

	return formatTavilyResults(result.Results), nil
}

// formatSearchResults 把 Brave 结果格式化为紧凑文本(审核 Minor#9:固定格式 + 硬上限)。
func formatSearchResults(results []struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}) string {
	if len(results) == 0 {
		return "未找到相关结果。"
	}
	var sb strings.Builder
	for i, r := range results {
		if i >= maxSearchResults {
			break
		}
		fmt.Fprintf(&sb, "[%d] %s\n%s\n%s\n\n", i+1, r.Title, r.URL, truncate(r.Description, 120))
	}
	return capResult(sb.String())
}

// formatTavilyResults Tavily 结果格式化。
func formatTavilyResults(results []struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}) string {
	if len(results) == 0 {
		return "未找到相关结果。"
	}
	var sb strings.Builder
	for i, r := range results {
		if i >= maxSearchResults {
			break
		}
		fmt.Fprintf(&sb, "[%d] %s\n%s\n%s\n\n", i+1, r.Title, r.URL, truncate(r.Content, 120))
	}
	return capResult(sb.String())
}

// truncate 截断字符串到 max 字符(按 rune),超出加省略号。
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// capResult 把工具结果限制在 maxToolResultChars 内。
func capResult(s string) string {
	if len([]rune(s)) <= maxToolResultChars {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxToolResultChars]) + "\n...(结果已截断)"
}

// truncateBody 截断 HTTP 错误响应体(避免日志过长)。
func truncateBody(body []byte) string {
	s := string(body)
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// 确保导入了 time(可能在超时配置时用,目前 httpClient 在 NewManager 设了超时)。
var _ = time.Second
