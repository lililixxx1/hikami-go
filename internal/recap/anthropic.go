package recap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

type AnthropicProvider struct {
	cfg        *config.Config
	httpClient *http.Client
}

func NewAnthropicProvider(cfg *config.Config) *AnthropicProvider {
	return &AnthropicProvider{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: time.Duration(cfg.RecapAI.TimeoutSeconds) * time.Second},
	}
}

func (p *AnthropicProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	_ = sessionInfo
	// 走 EffectiveAPIKeyEnv:与 probe/工厂一致,空 env 名兜底到 AI_API_KEY(codex 审核中[4])。
	apiKey := os.Getenv(p.cfg.RecapAI.EffectiveAPIKeyEnv())
	if apiKey == "" {
		return aiprovider.GenerateResult{}, errors.New("anthropic api key not set")
	}

	endpoint := p.anthropicEndpoint()
	maxTokens := p.cfg.RecapAI.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}
	// model 走 EffectiveModel(DeepSeek 默认):provider 切到 anthropic 时 model 必须用户显式填,
	// 否则发 deepseek-v4-pro 给 Anthropic 会 400。留空兜底只保证请求不因空 model 失败。
	body := map[string]any{
		"model":      recapModelFromContext(ctx, p.cfg.RecapAI.EffectiveModel()),
		"max_tokens": maxTokens,
		"system":     systemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}
	result, rawData, err := p.doAnthropicRequest(ctx, apiKey, endpoint, body)
	if err != nil {
		return result, err
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return aiprovider.GenerateResult{Raw: string(rawData)}, errors.New("anthropic response missing content")
	}
	result.Content = stripAIPreamble(result.Content)
	result.Raw = string(rawData)
	return result, nil
}

// GenerateWithTools 实现 aiprovider.ToolCapableProvider。
// Anthropic tool calling 与 OpenAI 差异较大:tools 用 input_schema;assistant 的 tool_use 与
// user 的 tool_result 都是 content blocks 数组的元素(而非顶层字段)。空 req.Tools 时等价 Generate。
func (p *AnthropicProvider) GenerateWithTools(ctx context.Context, req aiprovider.GenerateRequest) (aiprovider.GenerateResult, error) {
	apiKey := os.Getenv(p.cfg.RecapAI.EffectiveAPIKeyEnv())
	if apiKey == "" {
		return aiprovider.GenerateResult{}, errors.New("anthropic api key not set")
	}
	endpoint := p.anthropicEndpoint()

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = recapModelFromContext(ctx, p.cfg.RecapAI.EffectiveModel())
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = p.cfg.RecapAI.MaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = 16384
	}

	body := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   anthropicMessages(req.Messages),
	}
	if strings.TrimSpace(req.SystemPrompt) != "" {
		body["system"] = req.SystemPrompt
	}
	// Anthropic tools 格式:[{name, description, input_schema}]。
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		body["tools"] = tools
		// tool_choice:auto 让模型自主决定(Anthropic 默认即 auto,显式更清晰)。
		body["tool_choice"] = map[string]any{"type": "auto"}
	}

	result, rawData, err := p.doAnthropicRequest(ctx, apiKey, endpoint, body)
	if err != nil {
		return result, err
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return aiprovider.GenerateResult{Raw: string(rawData)}, errors.New("anthropic response missing content and tool_use")
	}
	// tool-calling 场景不做 stripAIPreamble(同 OpenAI 理由)。
	result.Raw = string(rawData)
	return result, nil
}

// anthropicMessages 把 aiprovider.Message 切片转成 Anthropic messages API 格式。
// Anthropic 的 message.content 可以是字符串,也可以是 content blocks 数组。
// assistant 带 tool_calls → content 数组含 {type:"text"} 和 {type:"tool_use",id,name,input}。
// role=tool 的工具结果 → 转为 role="user" + content:[{type:"tool_result",tool_use_id,content}]。
func anthropicMessages(msgs []aiprovider.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case aiprovider.RoleTool:
			// 工具结果在 Anthropic 里是 user 消息的 tool_result content block。
			out = append(out, map[string]any{
				"role": "user",
				"content": []map[string]any{
					{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content},
				},
			})
		case aiprovider.RoleAssistant:
			content := make([]map[string]any, 0, 1+len(m.ToolCalls))
			if m.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				// input 是 JSON 字符串,Anthropic 要对象;解析失败则用空对象。
				var input any
				if json.Valid([]byte(tc.Arguments)) {
					_ = json.Unmarshal([]byte(tc.Arguments), &input)
				}
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": input,
				})
			}
			out = append(out, map[string]any{"role": "assistant", "content": content})
		default: // user / system(实际 system 走顶层,这里兜底当 user 文本)
			role := string(m.Role)
			if role != "user" {
				role = "user"
			}
			out = append(out, map[string]any{"role": role, "content": m.Content})
		}
	}
	return out
}

// anthropicEndpoint 解析 Anthropic API URL(带官方兜底)。
func (p *AnthropicProvider) anthropicEndpoint() string {
	baseURL := strings.TrimRight(p.cfg.RecapAI.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return baseURL + "/messages"
}

// doAnthropicRequest 发送 POST /messages 并解析响应,Generate 与 GenerateWithTools 共用。
func (p *AnthropicProvider) doAnthropicRequest(ctx context.Context, apiKey, endpoint string, body map[string]any) (aiprovider.GenerateResult, []byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return aiprovider.GenerateResult{}, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return aiprovider.GenerateResult{}, nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return aiprovider.GenerateResult{}, nil, err
	}
	defer resp.Body.Close()

	rawData, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return aiprovider.GenerateResult{Raw: string(rawData)}, rawData, fmt.Errorf("anthropic http status %d: %s", resp.StatusCode, string(rawData))
	}
	result := parseAnthropicResult(rawData)
	return result, rawData, nil
}

func parseAnthropicContent(data []byte) string {
	return parseAnthropicResult(data).Content
}

func parseAnthropicResult(data []byte) aiprovider.GenerateResult {
	var raw struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return aiprovider.GenerateResult{}
	}
	// 聚合所有 text blocks(模型可能输出多个文本块,与 tool_use 交错)。
	var textParts []string
	var toolCalls []aiprovider.ToolCall
	for _, c := range raw.Content {
		switch c.Type {
		case "text":
			if c.Text != "" {
				textParts = append(textParts, c.Text)
			}
		case "tool_use":
			// input 是对象,转成 JSON 字符串保持与 OpenAI 路径统一(ToolCall.Arguments 为 JSON 字符串)。
			arg := string(c.Input)
			if arg == "" {
				arg = "{}"
			}
			toolCalls = append(toolCalls, aiprovider.ToolCall{
				ID:        c.ID,
				Name:      c.Name,
				Arguments: arg,
			})
		}
	}
	result := aiprovider.GenerateResult{
		Content:      strings.Join(textParts, "\n\n"),
		FinishReason: raw.StopReason,
		ToolCalls:    toolCalls,
	}
	return result
}
