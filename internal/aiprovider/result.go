package aiprovider

import "encoding/json"

// GenerateResult holds the output of an AI generation call.
type GenerateResult struct {
	Content      string
	Raw          string
	FinishReason string // "stop", "length", "max_tokens", "tool_calls", or "" for unknown
	// ToolCalls 为模型请求调用的工具列表(finish_reason=="tool_calls" 时非空)。
	// 由 ToolCapableProvider.GenerateWithTools 填充;普通 Provider.Generate 永远为 nil。
	ToolCalls []ToolCall
}

// Role 表示对话消息的角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // OpenAI 风格工具结果消息(带 ToolCallID)
)

// Message 表示结构化对话消息(tool-calling 多轮循环使用)。
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // 仅 assistant 消息,模型发起的工具调用
	ToolCallID string     `json:"tool_call_id,omitempty"` // 仅 role=tool,对应被响应的 ToolCall.ID
	// Name 仅 role=tool 时可选,标识工具名(部分 provider 用,OpenAI 非必需)。
	Name string `json:"name,omitempty"`
}

// Tool 描述一个可供模型调用的工具(JSON Schema 参数)。
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Parameters 是 JSON Schema(如 {"type":"object","properties":{...}}),原样传给 provider。
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall 表示模型发起的一次工具调用。
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments 是模型生成的参数 JSON 字符串(原样,未反序列化),由执行方解析。
	Arguments string `json:"arguments"`
}

// GenerateRequest 是 ToolCapableProvider.GenerateWithTools 的结构化输入,
// 承载多轮对话消息 + 可用工具列表。
type GenerateRequest struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
	MaxTokens    int    // <=0 表示不传(由 provider 兜底)
	Model        string // 空则由 provider 用配置默认
}
