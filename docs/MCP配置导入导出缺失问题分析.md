# MCP 配置导入导出缺失问题分析

> **状态**:✅ 已修复(2026-07-23)· **发现日期**:2026-07-23 · **严重程度**:中(功能遗漏,数据无损坏)
> **报告人**:用户(实测配置备份功能时发现)
> 本文为完整分析文档;`docs/KNOWN_ISSUES.md` 中登记简短条目。
> **修复计划**:`plans/plan-mcp-config-export-import-2026-07-23.md`(qoder 计划审核 Ready with fixes + 执行后复审)。

---

## 一、问题现象

用户在 Web 设置页使用「配置备份」(导出/导入)功能时,期望所有配置段都能完整迁移到新机器。实测发现:

1. **导出的备份文件不含 MCP 配置**。导出的 JSON bundle(`GET /api/config/export`,约 40KB)顶层字段只有:
   `version / exported_at / recap_ai / publish / webdav / asr_s3 / dashscope / archive / secrets / channels / glossary / templates / bili_accounts`
   —— **没有 `mcp` 字段**。

2. **导入不会破坏现有 MCP 配置**(merge/overwrite 都不碰 MCP),但**也恢复不了**。换机器后,MCP 段(Servers 列表、Brave/Tavily key、enabled 开关、max_tool_rounds)需要全部重新手动配置。

### 实测数据(2026-07-23)

| 操作 | 结果 |
|------|------|
| 导出 40KB 文件,Python 解析顶层 keys | `是否含 mcp 字段: False` |
| 导入(merge)返回 details | `config_sections_count: 6`(只有 6 段,无 MCP) |
| 导入后查 DB `runtime_settings` 的 mcp 段 | 仍保留(enabled=true、stepfun server、Tavily key 都在) |

---

## 二、根因分析

### 直接原因:`ConfigExportBundle` 结构没有 MCP 字段

`internal/handler/config_export.go:34-48` 定义的导出 bundle 结构:

```go
type ConfigExportBundle struct {
    Version      string                  `json:"version"`
    ExportedAt   string                  `json:"exported_at"`
    RecapAI      *config.RecapAIConfig   `json:"recap_ai,omitempty"`
    Publish      *config.PublishConfig   `json:"publish,omitempty"`
    WebDAV       *WebDAVExportSection    `json:"webdav,omitempty"`
    ASRS3        *ASRS3ExportSection     `json:"asr_s3,omitempty"`
    DashScope    *config.DashScopeConfig `json:"dashscope,omitempty"`
    Archive      *config.ArchiveConfig   `json:"archive,omitempty"`
    Secrets      map[string]string       `json:"secrets"`
    Channels     []channel.UpsertInput   `json:"channels"`
    Glossary     GlossaryExportSection   `json:"glossary"`
    Templates    TemplateExportSection   `json:"templates"`
    BiliAccounts []BiliAccountExportItem `json:"bili_accounts"`
    // ❌ 缺失:MCP 配置段
}
```

导出填充逻辑(`handleExportConfig`,`config_export.go:124+`)也只填充上述字段,从未读取 `s.cfg.MCP`。

### 深层原因:MCP 是后期新增段,导入导出未同步更新

- **导入导出功能完成于 2026-07-05**(commit `6a2bb18`,当时 6 个全局段 + secrets + channels + glossary + templates + bili_accounts)。
- **MCP 配置段是 2026-07-22 新增**(commit `5b84b63`,MCP 搜索工具集成的 6 phase 实施),通过 DB v36 迁移加入 `runtime_settings` 白名单。
- MCP 引入时更新了 `config.go`(MCPConfig/MCPSectionDTO)、`handler`(GET/PUT /api/config/mcp)、前端(MCPCardV10)、OpenAPI,**但漏掉了 `config_export.go`**——典型的"新功能上线、周边设施未同步"型遗漏。

### 导入侧为何不破坏 MCP(保护性副作用)

导入逻辑(`handleImportConfig`,`config_export.go:239+`)收集待写入的 section 时,有一句关键注释:

> `// 收集待持久化的 section DTO（仅 bundle 携带的段才写）。`

即导入**只处理 bundle 里存在的段**(`config_export.go:290+` 的 `sections = append(...)` 仅枚举 6 段)。因为 bundle 没有 MCP,导入循环根本不会生成 mcp 的 section DTO,也就不会对 MCP 段做任何 UPDATE。

这产生了一个**保护性副作用**:即使是 `overwrite` 策略,也不会清空 MCP(overwrite 只清 bundle 携带的段及其关联数据)。所以现状是"MCP 既不导出也不导入,但也不会被破坏"。

### 关键代码位置

| 文件 | 位置 | 问题 |
|------|------|------|
| `internal/handler/config_export.go` | `:34-48` `ConfigExportBundle` | 结构缺 MCP 字段 |
| `internal/handler/config_export.go` | `:124-165` `handleExportConfig` | 导出填充未读 `s.cfg.MCP` |
| `internal/handler/config_export.go` | `:290-330` `handleImportConfig` section 收集 | 只枚举 6 段,无 mcp |

---

## 三、影响评估

### 数据安全性:✅ 无损坏风险

- 导出**只读**,不影响任何配置。
- 导入(merge/overwrite)**不碰 MCP 段**,现有 MCP 配置安全。
- round-trip 实测:导入前后 MCP 段完整保留(enabled、stepfun server、Tavily key 均在)。

### 功能完整性:⚠️ MCP 配置无法迁移

换机器/重装/从备份恢复时,以下 MCP 配置**丢失,需手动重建**:

| 配置项 | 影响 |
|--------|------|
| `enabled`(MCP 总开关) | 需重新打开 |
| `max_tool_rounds` | 需重新设置(默认 5) |
| `servers[]`(外部 MCP server 列表) | **需重新填写所有 server**——名称/transport/URL/command/args/headers(含鉴权 token) |
| `builtin.BraveAPIKey` | 需重新填写 |
| `builtin.TavilyAPIKey` | 需重新填写 |

其中 `servers[].Headers.Authorization`(鉴权 token)丢失影响最大——外部 MCP server(如 stepfun)需要重新获取并配置 token。

### 严重程度评估:**中**

- 不损坏数据、不影响运行中的服务。
- 但违背用户对"配置备份"的合理预期(备份应覆盖全部配置)。
- 多 server / 复杂鉴权场景下,手动重建成本较高。

---

## 四、推荐修复方案

### 推荐方案:MCP 段纳入导入导出 bundle

**改动文件**:仅 `internal/handler/config_export.go`(后端单文件)。

修复核心是把 MCP 段加入 `ConfigExportBundle`,与现有 6 个全局段同等对待——导出时读取 `s.cfg.MCP`,导入时通过现有 `ApplyOverrides` 的 mcp case 写入。

#### 1. 导出 bundle 加 MCP 字段

`ConfigExportBundle`(`config_export.go:34-48`)新增字段:

```go
type ConfigExportBundle struct {
    // ...existing fields...
    MCP *config.MCPConfig `json:"mcp,omitempty"`   // 新增
}
```

直接复用 `config.MCPConfig`(已含完整结构:Enabled/Servers/Builtin/MaxToolRounds)。

#### 2. 导出填充

`handleExportConfig`(`config_export.go:124+`)中,与现有各段填充并列新增:

```go
mcp := s.cfg.MCP
bundle.MCP = &mcp
```

#### 3. 导入恢复

`handleImportConfig`(`config_export.go:290+`)的 section 收集循环新增 mcp case:

```go
if bundle.MCP != nil {
    sections = append(sections, sectionDTO{"mcp", mcpConfigToDTO(*bundle.MCP)})
}
```

`mcpConfigToDTO` 把 `config.MCPConfig` 转成 `MCPSectionDTO`(已有,`config.go:604`),复用现有 `ApplyOverrides` 的 mcp case 写入 `runtime_settings`。该 DTO 与 `PUT /api/config/mcp` 走同一写入路径,行为一致。

> **注意**:MCPConfig 含 Builtin 段的 Brave/Tavily key。导出时这些字段原样进 bundle(与其他全局段如 RecapAI 含 `api_key_env` 的处理粒度一致)。如后续需要区分密钥处理,可参照 WebDAV/ASRS3 的投影 DTO 模式(`config_export.go:50-91`)剥离密钥走 Secrets 段——当前不处理。

#### 4. 验证

- 导出文件含 `mcp` 字段(含 enabled/servers/builtin/max_tool_rounds)。
- 导入 merge/overwrite 后,MCP 段完整恢复。
- round-trip 测试:导出 → 清空 MCP → 导入 → 验证 MCP 配置还原。
- 复用现有 `TestConfigExportRoundTrip` / `TestConfigImportMerge` 模式补测试。
- 后端单文件改动,前端/OpenAPI 无需动(GET/PUT /api/config/mcp 路径不变)。

---

## 五、附录

### 涉及文件清单

| 文件 | 角色 |
|------|------|
| `internal/handler/config_export.go` | 导出/导入主逻辑,修复核心文件 |
| `internal/config/config.go` | MCPConfig / MCPSectionDTO 定义(已有,复用) |
| `docs/KNOWN_ISSUES.md` | 问题登记(简短条目) |

### 相关历史

- 2026-07-05 `6a2bb18`:导入导出功能完成(6 段)。
- 2026-07-22 `5b84b63`:MCP 段新增(6 phase),漏更新 config_export.go。
- 2026-07-23:本文档创建,问题确认。

---

## 六、修复实施(2026-07-23)

> 已按第四节「推荐方案」实施并扩展。**最终方案**:用户在 plan 阶段选定**投影 DTO + 密钥走 Secrets**(第四节原文是「直接嵌 MCPConfig」的简单方案,实施时按用户决策改为投影 DTO,与 WebDAV/ASRS3 范式一致、密钥可独立轮换)。

### 实际改动(`internal/handler/config_export.go` 单文件)

1. **新增投影 DTO**:`MCPExportSection`(enabled/servers/builtin/max_tool_rounds)+ `mcpServerExport`(headers 不含 Authorization)+ `mcpBuiltinExport`(只留 env 名字段)。剔明明文密钥 `Builtin.BraveAPIKey`/`TavilyAPIKey` 与 `Servers[].Headers["Authorization"]`。
2. **3 个 helper**:
   - `mcpToExport(c, secrets)` — cfg→投影 DTO,密钥写入传入的 secrets map;headers 仅在原值非空时分配(保持 nil 语义);Authorization 按大小写无关匹配抽取。
   - `mcpServerSecretKey(index, name)` — `MCP_SERVER_{idx}_{NAME}_AUTHORIZATION`,**「下标+名」双键防归一化碰撞**(qoder Important#1 修订:原计划仅用 name,会被 `my-server`/`my_server` 归一化碰撞静默覆盖;双键后 index 区分,export/import 同序遍历可逆)。
   - `mcpFromExport(e, secrets)` — 投影→cfg,从 secrets 回填密钥到明文字段(BraveAPIKey 而非 env 字段,语义清晰)。
3. **`ConfigExportBundle` 加 `MCP *MCPExportSection json:"mcp,omitempty"`**(指针+omitempty,旧备份缺段为 nil)。
4. **导出填充**:`handleExportConfig` 在 RLock 内取 `s.cfg.MCP` 拷贝,RLock 后调 `mcpToExport`(在 Secrets 收集段之前,因它写 `bundle.Secrets`)。
5. **导入恢复**:`handleImportConfig` 段收集加 mcp case(`mcpFromExport` → `MCPSectionDTO`,与 `updateMCPConfig` 同构,走同一 `ApplyOverrides` mcp case 落盘);内存提交 `s.cfg.MCP = nextMCP`(基线拷贝 `nextMCP = s.cfg.MCP` 保证旧 bundle 零回归);锁外 `mcpManager.Reload`(bundle.MCP 非 nil 时,与 PUT handler 一致)。
6. **`validateImportedSections` 不扩展**:`Config.Validate()` 不校验 MCP,MCP 无格式约束(server name/url 自由文本、max_tool_rounds 由 `EffectiveMaxToolRounds` 兜底)。

### 密钥命名约定

| 密钥来源 | bundle.Secrets key |
|---------|---------------------|
| 内置 Brave key 明文 | `MCP_BRAVE_API_KEY`(固定键名) |
| 内置 Tavily key 明文 | `MCP_TAVILY_API_KEY`(固定键名) |
| server `i` 的 Authorization 头 | `MCP_SERVER_{i}_{NAME大写非字母数字转_}_AUTHORIZATION` |

### 测试(`config_export_test.go` +6,共 17)

- `TestExportBundleOmitsMCPPlaintextSecrets` — 明文密钥绝不进 mcp 配置段(只 marshal 投影段,不 marshal 含 secrets 的整个 bundle)。
- `TestExportBundleMCPIsOmittable` — MCP 为 nil 时 omitempty 省略。
- `TestMCPExportImportRoundTrip` — `mcpToExport`→`mcpFromExport` 完全可逆(`reflect.DeepEqual`),含 marshal/unmarshal 落盘往返。
- `TestMCPExportImportRoundTrip_NameCollision` — 同名/归一化碰撞双键防串台(`my-server` vs `my_server`)。
- `TestImportConfigPersistsMCPSection` — merge 导入后 runtime_settings 有 mcp section、内存 cfg.MCP 恢复(含密钥回填)。
- `TestImportConfigOldBundleLeavesMCPUntouched` — 旧 bundle 无 mcp 段→MCP 配置不被破坏(保护性副作用,零回归)。

### 验证

- handler/config/mcp 包测试全过、`go vet` 通过、`gofmt` 通过。
- 零回归:旧 bundle 导入后 MCP 段不动(`TestImportConfigOldBundleLeavesMCPUntouched` 钉死)。
- (worker/live_record 包的失败是 Windows 进程检测预存 flake,与本改动无关。)

### OpenAPI / 前端

核实结论:**config-export bundle 不在 OpenAPI spec 范围**(`docs/api/` 无 ConfigExportBundle/bundle 相关定义),导出/导入端点早于 OpenAPI 工作。因此**无需 OpenAPI/前端改动**——纯后端单文件修复。
