[根目录](../../CLAUDE.md) > **internal/aiprovider**

# internal/aiprovider -- AI Provider 共享返回类型

## 模块职责

定义 AI Provider 的共享返回类型 `GenerateResult`，供 `internal/recap` 和 `internal/glossary` 模块的多种 AI Provider 实现使用。统一了 AI 生成调用的输出格式，包含生成内容、原始响应和完成原因。

## 入口与启动

- **入口文件**: `result.go`
- **测试文件**: `result_test.go`（5 个测试用例）

## 对外接口

### 核心类型

```go
type GenerateResult struct {
    Content      string  // 生成的内容（如直播回顾 Markdown、术语建议 JSON）
    Raw          string  // 原始 API 响应（用于调试和日志）
    FinishReason string  // 完成原因："stop"（正常）、"length"（长度限制）、"max_tokens"（最大 token）、""（未知）
}
```

## 使用场景

1. **回顾生成** (`internal/recap`)
   - OpenAI-compatible Provider
   - Anthropic API Provider
   - Claude CLI Provider
   - Codex CLI Provider
   - Local Placeholder Provider

2. **术语发现** (`internal/glossary`)
   - AI 自动发现术语候选
   - 术语建议生成

## 测试覆盖

| 测试函数 | 说明 |
|---------|------|
| `TestGenerateResult_Fields` | 字段赋值与读取 |
| `TestGenerateResult_JSONRoundtrip` | JSON 序列化与反序列化往返 |
| `TestGenerateResult_ZeroValue` | 零值行为 |
| `TestGenerateResult_FinishReason` | FinishReason 多种取值 |
| `TestGenerateResult_SliceOfResults` | 结果数组操作 |

## 相关文件清单

```
internal/aiprovider/
├── result.go           # GenerateResult 类型定义
└── result_test.go      # 单元测试（5 个用例）
```

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-11 | 文档初始化 | 创建模块级 CLAUDE.md，补全面包屑导航和完整结构 |
