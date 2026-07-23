[根目录](../../CLAUDE.md) > **internal/mcp**

# internal/mcp -- MCP 搜索工具集成

## 模块职责

提供 MCP(Model Context Protocol)搜索工具集成,让 recap(回顾生成)与 glossary(术语发现/复核)的 AI 调用能附带工具,使模型主动联网查证人名/游戏名/专有词标准写法。

管理两类工具来源:
- **外部 MCP server**(stdio/http/sse transport,经 mark3labs/mcp-go client 连接)。
- **内置 in-process 搜索工具**(Brave/Tavily,直接 Go 函数实现,不经 mcp-go transport)。

两类工具统一经 `ListTools`/`CallTool` 接口暴露给上层 agent loop。

## 入口与启动

- `NewManager()` 创建空 Manager;`Reload(ctx, cfg)` 按配置建立连接 + 注册内置工具。
- 装配点:`cmd/hikami/main.go` 启动时 `mcpManager.Reload(ctx, cfg.MCP)`,defer Close。
- 注入:recap/glossary 通过包级函数变量 `RunToolsAwareGenerate` 间接调 `RunWithTools`(避免反向导入 mcp)。

## 关键文件

| 文件 | 职责 |
|------|------|
| `manager.go` | Manager:多 server 连接管理 + ListTools/CallTool 路由 + Reload 热重载 + RWMutex 并发安全 |
| `builtin.go` | 内置搜索工具(Brave/Tavily),key 空不注册降级,结果硬上限 1500 字 |
| `loop.go` | `RunWithTools` agent loop:检测 ToolCalls→执行→回传→再调,maxRounds + token 预算 + ctx 取消防护 |

## 依赖关系

- **mcp 只导入 aiprovider**(ToolCapableProvider 类型 + GenerateRequest/Result)与 config,**不导入 recap 或 glossary**(零耦合)。
- recap 和 glossary 各自导入 mcp?**否**——它们用包级函数变量(`recap.RunToolsAwareGenerate` / `glossary.RunToolsAwareGenerate`)注入实现,main.go 装配时设置为 `mcp.RunWithTools`,彻底避免反向依赖。
- 外部依赖:`github.com/mark3labs/mcp-go v0.56.0`(纯 Go 无 cgo)。

## 降级保证(零回归)

三层降级,任一失败都不影响现有 AI 流程:
1. 未配置(enabled=false 或无 key/server)→ Manager 无工具 → 上层走普通 Generate。
2. CLI provider(claude_cli/codex_cli)不实现 ToolCapableProvider → 类型断言失败 → 普通 Generate。
3. 单个外部 server 连接失败 → 该 server 工具不可用(其余正常),只 slog.Warn。

## 并发安全(qoder v1 Important#3)

- `CallTool` 持 RLock(允许多并发)。
- `Reload` 持 Lock(写锁互斥),优雅排空在途调用后关闭旧连接。
- 测试覆盖:并发 CallTool during Reload(TestConcurrentCallToolDuringReload)。

## 测试与质量

- `manager_test.go`(8 用例):注册/路由/降级/热重载/并发/前缀解析/格式化/截断。
- `loop_test.go`(7 用例):无工具直通/正常循环/工具失败回传/maxRounds 耗尽/token 预算/ctx 取消/字符估算。
- 全量 `go test ./internal/mcp/...` 通过(0.075s,纯本地不触发网络)。
- `go vet` 通过。

## 变更记录

- 2026-07-22(二):**新建**(Phase 3)。MCP 搜索工具集成,详见根 AGENTS.md changelog 2026-07-22 条。
