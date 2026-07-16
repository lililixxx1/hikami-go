[根目录](../../CLAUDE.md) > **internal/recap**

# internal/recap -- AI 直播回顾生成

## 模块职责

使用 AI Provider 生成直播回顾文档。支持多种 Provider 实现：OpenAI-compatible API、Anthropic API、CLI Provider 和本地占位。生成 Markdown 回顾和 B 站专栏适配文本。支持指定时间段回顾、弹幕分析、术语建议闭环、回顾前术语校正转写和最终 Markdown 术语兜底。支持自定义回顾模板（全局+主播级别）、模板变量插值引擎、弹幕统计程序化生成、模板导入导出和内置模板预设。

## 入口与启动

- **入口文件**: `handler.go`, `provider_util.go`, `provider_openai.go`, `anthropic.go`, `claude_cli.go`, `codex_cli.go`, `prompt.go`, `transcript_correction.go`, `glossary_correction.go`, `danmaku.go`, `danmaku_stats.go`, `filter.go`, `segmentation.go`, `transcript_summarizer.go`, `template.go`, `render.go`, `presets.go`
- **任务类型**: `recap`

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewHandler(cfg, sessions, states, provider, glossaryStore, templateStore)` | 创建 Handler（注入 templateStore） |
| `CreateTask(ctx, pool, sessionID)` | 校验前置条件并创建任务 |
| `CreateTaskWithRange(ctx, pool, sessionID, startSec, endSec)` | 创建指定时间段回顾任务 |
| `CreateRegenTask(ctx, pool, sessionID)` | 重新生成整场回顾（覆盖本地 md，不碰 B站）；守卫 recap_done/published；入队带 `BypassFailState=true`，失败不降级主状态 |
| `Register(pool)` | 注册 recap 任务处理器 |
| `SetNotifyManager(m)` | 注入通知管理器，回顾完成发送 `recap_done` |
| `SetGlossaryDiscoverer(d)` | 注入术语发现器，回顾完成后自动术语发现 |
| `SetOnSuccess(fn)` | 注入成功回调 |
| `SetCapabilityChecker(c)` | 注入运行时能力检查器（设计 4.5）；`CreateTask`/`CreateTaskWithRange` 据此判定回顾能力，不可用时返回 `ErrRecapUnavailable`。未注入时不校验（向后兼容） |

**接口：**

```go
type Provider interface {
    Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error)
}
```

**API 端点：**
- `POST /api/sessions/:sid/recap/generate` -- 生成回顾
- `POST /api/sessions/:sid/recap-partial` -- 指定 `start_time`/`end_time` 生成局部回顾
- `POST /api/sessions/:sid/recap-with-range` -- `recap-partial` 兼容别名
- `GET /api/sessions/:sid/recap` -- 获取回顾内容（markdown, prompt, raw_response, suggested_terms）
- `GET /api/recap/templates` -- 列出全局回顾模板
- `PUT /api/recap/templates` -- 新增/更新全局回顾模板
- `GET /api/recap/templates/export` -- 导出全局回顾模板 JSON
- `POST /api/recap/templates/import` -- 导入全局回顾模板 JSON
- `GET /api/recap/presets` -- 列出内置回顾模板预设
- `GET /api/channels/:id/recap-template` -- 获取主播回顾模板（含全局/主播/合并结果）
- `PUT /api/channels/:id/recap-template` -- 新增/更新主播回顾模板
- `DELETE /api/channels/:id/recap-template` -- 删除主播回顾模板
- `GET /api/channels/:id/recap-template/export` -- 导出主播回顾模板 JSON
- `POST /api/channels/:id/recap-template/import` -- 导入主播回顾模板 JSON

**前置条件：**
- 场次状态必须为 `asr_done`
- `local_available=true`（上传 `all` 策略清理本地目录后置 `false`，需先 `Fetch` 取回）
- `package/transcript.txt` 文件必须存在
- 不能有同场次的活跃回顾任务
- 回顾生成能力必须可用

## Provider 实现

| Provider | 配置值 | 文件位置 | 说明 |
|----------|--------|----------|------|
| `OpenAICompatibleProvider` | `openai_compatible` | `provider_openai.go` | HTTP API，兼容 OpenAI 格式 |
| `AnthropicProvider` | `anthropic` | `anthropic.go` | Anthropic Claude API (Messages) |
| `ClaudeCLIProvider` | `claude_cli` | `claude_cli.go` | Claude CLI 本地调用，stdin/stdout JSON |
| `CodexCLIProvider` | `codex_cli` | `codex_cli.go` | Codex CLI 本地调用，命令行参数传 prompt |
| `LocalProvider` | 其他 / 未配置 API Key | `provider_util.go` | 本地占位，生成模板文本 |
| `disabledProvider` | `recap_ai.enabled=false` | `provider_util.go` | `Generate` 永远返回 `ErrRecapDisabled`（设计 4.1：禁用即禁用，不再退回 LocalProvider 占位，避免自动链误产占位回顾） |

## 回顾模板系统

### template.go -- TemplateStore CRUD + Resolve 合并

**核心类型：**

| 类型 | 说明 |
|------|------|
| `Template` | 数据库行模型（id, channel_id, name, system_prompt, user_format, fan_name, extra_vars, enabled, is_default） |
| `TemplateExport` | JSON 导出结构体（channel_id, templates, exported_at） |
| `ResolvedTemplate` | 合并后的最终模板（system_prompt, user_format, fan_name, extra_vars map[string]string） |
| `TemplateStore` | CRUD 操作封装 |

**TemplateStore 方法：**

| 方法 | 说明 |
|------|------|
| `NewTemplateStore(db)` | 创建实例 |
| `GetGlobal(ctx, name)` | 获取全局模板（channel_id=''） |
| `GetByChannel(ctx, channelID, name)` | 获取主播级模板 |
| `Resolve(ctx, channelID, name)` | 合并全局+主播模板为最终模板 |
| `Upsert(ctx, t)` | 插入或替换模板（按 channel_id+name 唯一索引） |
| `Delete(ctx, id)` | 删除模板（内置模板不可删除，返回 ErrTemplateBuiltIn） |
| `ListGlobal(ctx)` | 列出全局模板（按 name 排序） |
| `ListByChannel(ctx, channelID)` | 列出主播模板（按 name 排序） |
| `ExportJSON(ctx, channelID)` | 导出全局或主播模板 JSON |
| `ImportJSON(ctx, channelID, data)` | 导入全局或主播模板 JSON |
| `CopyFromChannel(ctx, src, dst)` | 复制主播模板 |
| `ClearCustom(ctx)` | 清除所有非内置模板（DELETE FROM recap_templates WHERE is_default = 0），用于配置导入 overwrite 策略 |

**Resolve 合并逻辑：**
1. 获取全局模板，不存在则使用内置默认
2. 获取主播级模板
3. 若主播模板存在且 enabled：
   - system_prompt: 非空且非 `__builtin__` 则覆盖全局
   - user_format: 同上
   - fan_name: 非空则覆盖
   - extra_vars: 合并（全局为 base，主播覆盖）
4. 否则直接使用全局模板
5. 剩余 `__builtin__` 标记替换为内置常量

**内置默认值：**
- `defaultSystemPrompt` -- 详细的直播回顾编辑指令（标题格式、必含章节、原话引用、弹幕热度、术语校正规则）
- `defaultUserFormat` -- 输出格式要求（7 个章节结构定义）

**哨兵错误：**
- `ErrTemplateNotFound` -- 模板不存在
- `ErrTemplateBuiltIn` -- 尝试删除内置模板

### presets.go -- 内置模板预设库

**输出格式约定（符号化纯文本）：** 自 2026-06-21（`481a3eb`）起，默认与各预设模板改用**符号化（emoji 前缀）纯文本**输出，对齐 B 站专栏编辑器渲染——表格转为 emoji 前缀分行（不再输出 Markdown 表格），高亮/引用/弹幕/观看建议等章节统一用 emoji 前缀行表达。这使得生成的回顾在 B 站专栏中渲染更自然，避免 Markdown 表格被编辑器破坏。

**BuiltinPresets -- 5 个内置预设：**

| 预设名称 | 描述 | 风格 |
|----------|------|------|
| 粉丝向精修 | 更接近可发布成稿的粉丝向回顾 | 温暖、克制、少模板腔，要求"致{{fan_name}}"位于最后 |
| 正式详实 | 结构化详细回顾，适合专业内容 | 客观专业，7 个章节 |
| 粉丝向 | 轻松活泼粉丝风格 | 热情亲切，5 个章节 |
| 简洁摘要 | 精简版回顾 | 高效简洁，3 个章节 |
| 弹幕聚焦 | 侧重弹幕互动分析 | 弹幕分析师视角，5 个章节 |

### render.go -- 模板变量插值引擎

**TemplateVars 标准变量：** ChannelName, ChannelID, Date, DateTime, Title, Duration, DurationMin, FanName, DanmakuCount, UniqueUsers, AvgPerMin, Slug

### danmaku_stats.go -- 弹幕统计程序化生成

| 函数 | 说明 |
|------|------|
| `FormatDanmakuStats(stats, vars)` | 生成弹幕统计 Markdown 段落 |
| `appendDanmakuStats(recap, statsSection)` | 将统计段插入回顾 Markdown 的弹幕互动章节 |

## 关键依赖与配置

- `recap_ai.provider`: Provider 类型
- `recap_ai.api_key_env` + 环境变量
- `recap_ai.base_url`: API 端点
- `recap_ai.model`: 模型名称
- `recap_ai.timeout_seconds`: HTTP 超时
- `recap_ai.cli_path`: CLI 工具路径（可选）
- `recap_ai.max_continuations`: 自动续写次数（默认 2）
- Per-channel 覆盖: `channels.recap_model`, `channels.max_continuations`

**术语表注入：** Handler 持有 `glossaryStore`，在 `buildPrompt` 中调用 `glossaryStore.ExportForPrompt(ctx, channelID)` 获取术语校正参考。

**回顾前术语校正转写：** `transcript_correction.go` 在 HandleTask 中于 Prompt 构建前执行，生成 `transcript.corrected.txt` 与 `transcript.correction.json`。

**自动续写：** 当 Provider 返回 `FinishReason="length"` 时自动续写，最多 `max_continuations` 次（per-channel 可覆盖）。

**术语建议闭环：** `extractSuggestedTerms` 从回顾 Markdown 中提取 `[应为：xxx]` 模式，写入 `suggested_terms.json`。

**模板解析流程（HandleTask 中）：**
1. 通过 `templateStore.Resolve` 获取合并后的模板
2. 构建 `TemplateVars`
3. 调用 `RenderTemplate` 渲染输出格式要求
4. 使用术语表生成 corrected transcript
5. 调用 `provider.Generate` 生成回顾
6. 生成后调用 `applyGlossaryCorrections`、`ensureFinalAddressSection`、`FormatDanmakuStats` 完成后处理

## 测试与质量

- `recap_test.go`: 72 个测试用例，覆盖：
  - CreateTask: 成功、状态错误、文件缺失、活跃冲突、场次不存在
  - Provider: LocalProvider（正常/空 prompt/system prompt）
  - 辅助函数: firstParagraph、safeName、parseChatCompletionContent、parseAnthropicResult
  - 续写: shouldContinueRecap、appendContinuation
  - HandleTask: LocalProvider 全流程、空转写、suggestedTerms 提取、续写（length 停止 + max 限制）、per-channel recap options、SessionNotFound
  - 弹幕: analyzeDanmaku（正常/空数据）
  - Prompt: buildPromptWithDanmaku、buildPromptWithoutDanmaku、模板渲染、术语表引用规则
  - 统计: FormatDanmakuStats（正常/nil/零计数）、appendDanmakuStats（存在/不存在/emoji/空章节）、omitTopicClusters
  - 元数据: readSessionMetadata
  - 术语校正: buildCorrectionRules 排序、correctTextWithRules、correctedTranscript segments/fallback+range
  - 最终 Markdown: applyGlossaryCorrections（跳过 Markdown 引用）、cleanSuggestedTermMarkers、ensureFinalAddressSection
  - 预设: BuiltinPresetsPromptLayering、defaultSystemPromptHasDetailConstraints
  - 署名识别: `hasGeneratedNotice` 兼容历史 Hazel 与新 Hikami 署名/变体（改名过渡期 AI 可能吐回旧签名，`TestHasGeneratedNotice`）
  - Handler: NewHandlerNilTemplateStore

- `template_test.go`: 26 个测试用例，覆盖：
  - TemplateStore CRUD: GetGlobal、GetByChannel、Upsert、Delete、ListGlobal
  - Resolve 合并逻辑: 7 种场景
  - RenderTemplate: 8 种场景

- `test_recap_main_test.go`: 1 个端到端集成测试（TestGenerateRecapFromRealData，使用真实 API）

- `test_recap_0329_test.go`: 1 个端到端集成测试（TestGenerateRecap_0329，使用真实 DeepSeek API 和 03.29 场次数据）

## 相关文件清单

- `handler.go` -- Handler、CreateTask、CreateTaskWithRange、Register、HandleTask 主流程
- `provider_util.go` -- Provider 接口、LocalProvider、NewConfiguredProvider
- `provider_openai.go` -- OpenAICompatibleProvider 实现
- `anthropic.go` -- AnthropicProvider 实现
- `claude_cli.go` -- ClaudeCLIProvider 实现
- `codex_cli.go` -- CodexCLIProvider 实现
- `prompt.go` -- buildPrompt（PromptSection 模块化）、术语表注入、模板变量整合
- `transcript_correction.go` -- 回顾前术语校正转写、校正报告产物
- `glossary_correction.go` -- 最终 Markdown 术语兜底、"致..."章节整理
- `filter.go` -- 局部回顾 transcript / danmaku 过滤
- `segmentation.go` -- 长文本分段与摘要上下文
- `transcript_summarizer.go` -- 转写摘要器
- `danmaku.go` -- 弹幕分析（analyzeDanmaku、峰值检测、关键词统计）
- `danmaku_stats.go` -- FormatDanmakuStats、appendDanmakuStats 程序化弹幕统计
- `template.go` -- TemplateStore CRUD、Resolve 合并逻辑、导入导出、ClearCustom 全量清除、哨兵错误
- `render.go` -- RenderTemplate 变量插值引擎、TemplateVars 结构体
- `presets.go` -- TemplatePreset 类型、BuiltinPresets 5 个内置模板预设
- `recap_test.go` -- 单元+集成测试（76 个用例）
- `template_test.go` -- 模板测试（27 个用例）
- `test_recap_main_test.go` -- 端到端集成测试（1 个用例）
- `test_recap_0329_test.go` -- 端到端集成测试（1 个用例，03.29 场次）

## 变更记录 (Changelog)

- 2026-07-16(三):**术语校正词边界感知替换 + ResolvedTemplate 补 json tag**(调查文档修复批次,codex 审核 APPROVED)。① **术语词边界**(`glossary_correction.go`/`transcript_correction.go`):原两处调用点用 `strings.ReplaceAll` 做纯子串匹配,含 ASCII 字母数字的 term(`AI`/`277`/`Nike`)嵌在更长字符串(`MAIL`/`123277456`/`Nikerussia`)里时被误替换,静默损坏转写文本(位置B)和回顾正文(位置A)。新增 `replaceTermBoundaryAware`/`hasAlphanumeric`/`isASCIIAlphanumeric` 3 函数——对含 `[A-Za-z0-9]` 的 term 强制左右边界非 ASCII 字母数字,纯 CJK term 回落 `strings.ReplaceAll` 保持零回归;`term==""` 提前返回避免 `ReplaceAll` 在每字符间插入 canonical(调查文档 §5.6 边界 bug)。手写 rune 扫描(不用正则:Go RE2 不支持 lookbehind + 150 条规则各自 Compile 性能差)。位置B 顺带修正:只在 `replaced != output` 时才记 applied,correction report 更准。**关键**:纯 CJK 零回归有专门测试验证。新增 4 测试(`TestReplaceTermBoundaryAware` 16 用例/`TestHasAlphanumeric` 9 用例/`TestApplyGlossaryCorrectionsAlphanumericBoundary`/`TestCorrectTextWithRulesBoundaryAwareAndAppliedAccuracy`),recap_test.go 72→76。② **ResolvedTemplate 补 json tag**(`template.go:57-63`):该结构体缺 `json:` tag,Go 默认 PascalCase 序列化,前端按 snake_case 访问得 undefined → 主播级模板「跟随全局」预览全空、切换自定义不回填。补 4 字段 `json:"snake_case"` tag(与同文件 `Template` 风格一致),同步 OpenAPI spec(`templates.yaml`/`openapi.yaml`/`README.md`/`api-gap-analysis.md` 从 PascalCase 改回 snake_case)、重新生成 `web/src/api/generated.ts`、清理 `types-derived.ts` 误导注释。新增 `TestResolvedTemplateJSONKeys`(断言 snake_case 键名 + 不再有 PascalCase),template_test.go 26→27。后端 27 包全过、gofmt/vet 通过、redocly 7 warnings 同基线。

