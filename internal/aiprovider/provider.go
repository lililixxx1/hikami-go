package aiprovider

import "context"

// ToolCapableProvider 是支持 function/tool calling 的可选 provider 能力接口。
//
// 设计说明(qoder-code-review v1 Critical#1 修订):
// 定义在 aiprovider 包(而非 recap),因为 recap、glossary、mcp 三个包都已(或将)导入 aiprovider,
// 在此定义不引入任何新耦合,也不破坏 glossary↔recap 的 duck-typing 隔离(discovery.go:21)。
//
// 消费方(recap.Handler / glossary.Discoverer)用类型断言判断 provider 是否支持工具调用:
//
//	if tcp, ok := p.(ToolCapableProvider); ok { ... }
//
// 不支持(如 LocalProvider / disabledProvider / claude_cli / codex_cli)时类型断言失败,
// 调用方降级为普通 Provider.Generate,保证零回归。
type ToolCapableProvider interface {
	GenerateWithTools(ctx context.Context, req GenerateRequest) (GenerateResult, error)
}
