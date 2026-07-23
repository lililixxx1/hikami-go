package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"hikami-go/internal/aiprovider"
)

// maxConversationTokenChars 是 agent loop 累积对话的字符上限(近似 token 预算)。
// 超过则停止工具调用,返回已累积内容(审核 Important#2)。
// 注:中文每字符约 1-2 token,此处按字符保守估算(非精确 token 计数),
// 80000 字符 ≈ 多数模型 32k-64k token 上下文的 80% 阈值。
const maxConversationTokenChars = 80000

// RunWithTools 是 MCP 工具调用 agent loop:
//  1. 调 GenerateWithTools(带 tools)
//  2. 若响应含 ToolCalls → 逐个 mgr.CallTool 执行 → 把 assistant(tool_calls)+tool 结果追加到 messages
//  3. 再调 GenerateWithTools(带工具结果,让模型基于结果继续生成)
//  4. 直到模型不再请求工具(finish=stop)或达 maxRounds 上限
//
// 防护:
//   - maxRounds:防死循环(默认建议 5)。
//   - token 预算:每轮前估算累积字符,超 maxConversationTokenChars 停止工具调用。
//   - 工具失败:回传错误内容给模型(非中断,让模型自行处理)。
//   - ctx 取消:每轮检查,及时退出。
func RunWithTools(
	ctx context.Context,
	tcp aiprovider.ToolCapableProvider,
	mgr *Manager,
	req aiprovider.GenerateRequest,
	maxRounds int,
) (aiprovider.GenerateResult, error) {
	if maxRounds <= 0 {
		maxRounds = 5
	}

	// 首轮:用 req 原始 messages + tools 调用。
	result, err := tcp.GenerateWithTools(ctx, req)
	if err != nil {
		return result, err
	}

	// 无工具调用 → 直接返回(最常见路径,首次即完成)。
	if len(result.ToolCalls) == 0 {
		return result, nil
	}

	// agent loop:处理工具调用,最多 maxRounds 轮。
	messages := append([]aiprovider.Message(nil), req.Messages...)

	for round := 0; round < maxRounds; round++ {
		// ctx 取消检查(审核 Important#8)。
		if err := ctx.Err(); err != nil {
			slog.Info("mcp agent loop cancelled", "round", round, "error", err)
			return result, err
		}

		// token 预算检查:估算累积对话字符(审核 Important#2)。
		if estimateChars(messages) > maxConversationTokenChars {
			slog.Info("mcp agent loop stop: token budget exceeded", "round", round, "chars", estimateChars(messages))
			// 停止工具调用,把已累积内容返回(附加提示模型基于已有信息收尾)。
			result.FinishReason = "length"
			return result, nil
		}

		// 把上一轮的 assistant(tool_calls) 消息加入历史。
		messages = append(messages, aiprovider.Message{
			Role:      aiprovider.RoleAssistant,
			Content:   result.Content,
			ToolCalls: result.ToolCalls,
		})

		// 逐个执行工具调用,结果作为 tool 消息追加。
		for _, tc := range result.ToolCalls {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			toolResult, callErr := mgr.CallTool(ctx, tc.Name, tc.Arguments)
			if callErr != nil {
				// 工具失败:回传错误内容给模型(非中断)。
				slog.Warn("mcp tool call failed, returning error to model", "tool", tc.Name, "error", callErr)
				toolResult = fmt.Sprintf("工具调用失败: %v", callErr)
			} else {
				slog.Info("mcp tool called", "tool", tc.Name, "args", tc.Arguments, "result_len", len([]rune(toolResult)))
			}
			messages = append(messages, aiprovider.Message{
				Role:       aiprovider.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    toolResult,
			})
		}

		// 下一轮:带工具结果再调(工具结果已在 messages 里)。
		nextReq := aiprovider.GenerateRequest{
			SystemPrompt: req.SystemPrompt,
			Messages:     messages,
			Tools:        req.Tools,
			MaxTokens:    req.MaxTokens,
			Model:        req.Model,
		}
		result, err = tcp.GenerateWithTools(ctx, nextReq)
		if err != nil {
			return result, err
		}

		// 模型不再请求工具 → 返回最终内容。
		if len(result.ToolCalls) == 0 {
			return result, nil
		}
	}

	// 达 maxRounds 上限仍有工具调用:停止,返回最后内容。
	slog.Info("mcp agent loop stop: max rounds reached", "max_rounds", maxRounds)
	return result, nil
}

// estimateChars 估算 messages 累积字符数(含工具结果)。
// 中文项目保守用 rune 计数(1 字符 ≈ 1-2 token),不做 ASCII/非 ASCII 区分以简化。
func estimateChars(messages []aiprovider.Message) int {
	total := 0
	for _, m := range messages {
		total += utf8.RuneCountInString(m.Content)
		for _, tc := range m.ToolCalls {
			total += utf8.RuneCountInString(tc.Arguments)
			total += utf8.RuneCountInString(tc.Name)
		}
	}
	return total
}
