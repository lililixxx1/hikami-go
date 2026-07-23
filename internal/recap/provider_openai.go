package recap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

type OpenAICompatibleProvider struct {
	cfg        *config.Config
	httpClient *http.Client
}

func (p *OpenAICompatibleProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	_ = sessionInfo
	// base_url / model / api_key_env 留空兜底:经 Effective* 解析,空值回落 DeepSeek 官方默认,
	// 避免空 base_url 拼出无 host 的 /chat/completions 或空 model 触发 400。
	endpoint := strings.TrimRight(p.cfg.RecapAI.EffectiveBaseURL(), "/") + "/chat/completions"
	body := map[string]any{
		"model": recapModelFromContext(ctx, p.cfg.RecapAI.EffectiveModel()),
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": prompt},
		},
	}
	if p.cfg.RecapAI.MaxTokens > 0 {
		body["max_tokens"] = p.cfg.RecapAI.MaxTokens
	}
	data, err := json.Marshal(body)
	if err != nil {
		return aiprovider.GenerateResult{}, err
	}
	result, rawData, err := p.doOpenAIRequest(ctx, endpoint, data)
	if err != nil {
		return result, err
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return aiprovider.GenerateResult{Raw: string(rawData)}, fmt.Errorf("recap provider response missing content")
	}
	result.Content = stripAIPreamble(result.Content)
	result.Raw = string(rawData)
	return result, nil
}

// GenerateWithTools 实现 aiprovider.ToolCapableProvider,支持 function calling 多轮对话。
// 请求体加 tools/tool_choice,messages 支持 assistant(tool_calls)/tool(tool_call_id) 角色,
// 响应解析提取 message.tool_calls。空 req.Tools 时等价于 Generate(零回归契约,有测试保护)。
func (p *OpenAICompatibleProvider) GenerateWithTools(ctx context.Context, req aiprovider.GenerateRequest) (aiprovider.GenerateResult, error) {
	endpoint := strings.TrimRight(p.cfg.RecapAI.EffectiveBaseURL(), "/") + "/chat/completions"

	// model 解析:优先 req.Model,其次配置 EffectiveModel;与 Generate 一致走 ctx 注入的覆盖。
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = recapModelFromContext(ctx, p.cfg.RecapAI.EffectiveModel())
	}

	// messages:system(systemPrompt 非空时)+ req.Messages 原样转 OpenAI 格式。
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.SystemPrompt})
	}
	for _, m := range req.Messages {
		messages = append(messages, openAIMessage(m))
	}

	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	} else if p.cfg.RecapAI.MaxTokens > 0 {
		body["max_tokens"] = p.cfg.RecapAI.MaxTokens
	}
	// tools 非空时声明工具 + tool_choice:auto(让模型自主决定是否调用)。
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  params,
				},
			})
		}
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}

	data, err := json.Marshal(body)
	if err != nil {
		return aiprovider.GenerateResult{}, err
	}
	result, rawData, err := p.doOpenAIRequest(ctx, endpoint, data)
	if err != nil {
		return result, err
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return aiprovider.GenerateResult{Raw: string(rawData)}, fmt.Errorf("recap provider response missing content and tool_calls")
	}
	// tool-calling 场景不做 stripAIPreamble(模型可能在工具调用前输出结构化中间内容,
	// 剥离会破坏 agent loop 的 message 拼接)。
	result.Raw = string(rawData)
	return result, nil
}

// openAIMessage 把 aiprovider.Message 转成 OpenAI chat completion 的 message 对象。
// assistant 带 ToolCalls 时输出 tool_calls 数组;tool 角色带 tool_call_id。
func openAIMessage(m aiprovider.Message) map[string]any {
	out := map[string]any{"role": string(m.Role), "content": m.Content}
	if len(m.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			calls = append(calls, map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			})
		}
		out["tool_calls"] = calls
		// OpenAI: assistant 发起 tool_calls 时 content 可为 null。
		if m.Content == "" {
			out["content"] = nil
		}
	}
	if m.ToolCallID != "" {
		out["tool_call_id"] = m.ToolCallID
	}
	return out
}

// doOpenAIRequest 发送 POST /chat/completions 并解析响应,返回 (结果,原始响应字节,错误)。
// Generate 与 GenerateWithTools 共用,避免请求逻辑重复。
func (p *OpenAICompatibleProvider) doOpenAIRequest(ctx context.Context, endpoint string, data []byte) (aiprovider.GenerateResult, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return aiprovider.GenerateResult{}, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+os.Getenv(p.cfg.RecapAI.EffectiveAPIKeyEnv()))
	request.Header.Set("Content-Type", "application/json")
	response, err := p.httpClient.Do(request)
	if err != nil {
		return aiprovider.GenerateResult{}, nil, err
	}
	defer response.Body.Close()
	rawData, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return aiprovider.GenerateResult{Raw: string(rawData)}, rawData, fmt.Errorf("recap provider http status %d: %s", response.StatusCode, string(rawData))
	}
	result := parseChatCompletionResult(rawData)
	return result, rawData, nil
}

func parseChatCompletionContent(data []byte) string {
	return parseChatCompletionResult(data).Content
}

func parseChatCompletionResult(data []byte) aiprovider.GenerateResult {
	var raw struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return aiprovider.GenerateResult{}
	}
	if len(raw.Choices) == 0 {
		return aiprovider.GenerateResult{}
	}
	ch := raw.Choices[0]
	result := aiprovider.GenerateResult{
		Content:      ch.Message.Content,
		FinishReason: ch.FinishReason,
	}
	// OpenAI:模型请求调用工具时 finish_reason="tool_calls",message.tool_calls 非空。
	// 规范化为统一 ToolCall 切片,供 agent loop 执行。
	if len(ch.Message.ToolCalls) > 0 {
		result.ToolCalls = make([]aiprovider.ToolCall, 0, len(ch.Message.ToolCalls))
		for _, tc := range ch.Message.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, aiprovider.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		// finish_reason 为空时(部分兼容端点不返回),按 tool_calls 存在性补齐。
		if result.FinishReason == "" {
			result.FinishReason = "tool_calls"
		}
	}
	return result
}
