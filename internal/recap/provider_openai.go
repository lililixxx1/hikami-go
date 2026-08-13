package recap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

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
	// ISSUE-007 空 content 兜底重试见 doOpenAIRequestWithRetry(Generate 与 GenerateWithTools 共用)。
	result, rawData, err := p.doOpenAIRequestWithRetry(ctx, endpoint, data)
	if err != nil {
		return aiprovider.GenerateResult{Raw: string(rawData)}, err
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
	// 与 Generate 共用 ISSUE-007 空 content 兜底重试(MCP 工具开启时 recap 也走本路径,
	// 空 content 同样需要兜底,而非硬失败)。空 content 的判定含 tool_calls:工具路径下
	// "content 空 且 无 tool_calls" 才算空响应。
	result, rawData, err := p.doOpenAIRequestWithRetry(ctx, endpoint, data)
	if err != nil {
		return aiprovider.GenerateResult{Raw: string(rawData)}, err
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

// doOpenAIRequestWithRetry 在 doOpenAIRequest 之上叠加 ISSUE-007 空 content 兜底重试,
// 由 Generate 与 GenerateWithTools 共用(避免两条路径重试逻辑分叉)。
//
// DeepSeek 等 reasoning 模型可能返回 HTTP 200 但正文为空(message.reasoning_content 有内容,
// 正文未输出)。空 content 成因有不同类别,finish_reason 无法完全区分,按下述策略处理:
//   - finish_reason="length" 或 "content_filter":确定性失败(前者 token 预算耗尽,后者内容安全
//     过滤),同输入同结果,重试只会重复同样的失败并多花付费调用,立即终止不重试。
//   - 其余(stop+空、空 finish_reason 等):可能为真·间歇抖动(重试可救),也可能是 max_tokens
//     不足的确定性失败——DeepSeek 在该情形常报 stop 而非 length,是其行为特性,代码层无法可靠
//     区分。对其做有界重试(共 3 次尝试)兜底真·间歇;若根因实为配置不足,重试耗尽后返回带
//     finish_reason 的错误,治本仍需调大 max_tokens / 换 model(见 docs/KNOWN_ISSUES.md ISSUE-007)。
//
// HTTP/网络错误不重试(doOpenAIRequest 返回的 err 直传)。重试无退避(真·间歇通常下次即恢复,
// 加退避只增延迟,有意为之)。重试受 ctx 取消保护(doOpenAIRequest 用 ctx)。
// 注:成功判定用 TrimSpace——纯空白 content(如 "   ")也视为空,避免下游拿空白回顾继续流水线。
func (p *OpenAICompatibleProvider) doOpenAIRequestWithRetry(ctx context.Context, endpoint string, data []byte) (aiprovider.GenerateResult, []byte, error) {
	const maxEmptyContentRetries = 2
	var lastResult aiprovider.GenerateResult
	var lastRaw []byte
	for attempt := 0; attempt <= maxEmptyContentRetries; attempt++ {
		result, rawData, err := p.doOpenAIRequest(ctx, endpoint, data)
		if err != nil {
			return result, rawData, err
		}
		if strings.TrimSpace(result.Content) != "" || len(result.ToolCalls) > 0 {
			return result, rawData, nil
		}
		// 空 content:记录最近一次,供耗尽时的诊断日志与错误使用(ISSUE-007 原诊断缺陷:丢弃 Raw 不打日志)。
		lastResult = result
		lastRaw = rawData
		// 确定性 finish_reason(length=token 预算耗尽 / content_filter=内容安全过滤):
		// 同输入同结果,重试无效,立即终止(错误信息提示对应治本方向)。
		if fr := result.FinishReason; fr == "length" || fr == "content_filter" {
			slog.WarnContext(ctx, "recap provider returned empty content, deterministic finish_reason (not retrying)",
				"finish_reason", fr, "raw_len", len(rawData), "raw_head", truncateForLog(string(rawData), 800))
			return result, rawData, fmt.Errorf("recap provider response missing content, finish_reason=%s (deterministic, not retried: length => increase max_tokens/switch model; content_filter => check transcript content)", fr)
		}
		if attempt < maxEmptyContentRetries {
			slog.WarnContext(ctx, "recap provider returned empty content, retrying",
				"attempt", attempt+1, "max_attempts", maxEmptyContentRetries+1,
				"finish_reason", result.FinishReason, "raw_len", len(rawData))
			continue
		}
	}
	// 重试耗尽(各次均为 stop/空 等非 length 空响应)。
	slog.WarnContext(ctx, "recap provider returned empty content, retries exhausted",
		"finish_reason", lastResult.FinishReason, "raw_len", len(lastRaw),
		"raw_head", truncateForLog(string(lastRaw), 800))
	return lastResult, lastRaw, fmt.Errorf("recap provider response missing content after %d attempts (finish_reason=%s)", maxEmptyContentRetries+1, lastResult.FinishReason)
}

// truncateForLog 截断字符串到约 max 字节的日志预览,在 rune 边界截断,
// 避免切断多字节 UTF-8 字符(如中文)产生含替换符的 invalid UTF-8 日志。
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
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
