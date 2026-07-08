[根目录](../../CLAUDE.md) > **internal/glossary**

# internal/glossary -- ASR 术语校正表管理与 AI 术语发现

## 模块职责

管理 ASR 术语校正表（glossary），支持全局和主播级别的术语映射。提供术语 CRUD、合并视图、导入/导出（Markdown 和 JSON）、批量操作、全量清除，以及为 AI prompt 生成格式化的术语校正参考。术语表用于纠正 ASR 转写中的误识别词汇，在生成直播回顾时注入到 AI prompt 中。

新增 AI 术语发现系统：回顾生成完成后，自动从转写文本中通过 AI 提取可能的术语候选，进入人工审核队列（glossary_candidates）。支持候选 CRUD、审批/拒绝、批量操作、多因子评分和去重合并。

## 入口与启动

- **入口文件**: `glossary.go`, `candidate_store.go`, `discovery.go`
- **核心类型**: `Store`, `Entry`, `MergedEntry`, `GlossaryExport`, `Candidate`, `DiscoveryItem`, `Discoverer`

## 对外接口

### Store（术语表 CRUD）

| 方法 | 说明 |
|------|------|
| `NewStore(db)` | 创建 Store 实例 |
| `ListGlobal(ctx)` | 列出所有全局术语表条目（channel_id=''），按 term 排序 |
| `ListByChannel(ctx, channelID)` | 返回全局+主播级别的合并视图（`[]MergedEntry`） |
| `Upsert(ctx, channelID, term, canonical, category)` | 插入或替换术语条目（基于 channel_id+term 唯一索引） |
| `Delete(ctx, id)` | 按 ID 删除条目，返回 `ErrNotFound` |
| `Toggle(ctx, id, enabled)` | 切换条目启用/禁用状态 |
| `GetNote(ctx, channelID)` | 获取自由文本备注（Markdown 格式） |
| `SetNote(ctx, channelID, note)` | 设置自由文本备注 |
| `ExportForPrompt(ctx, channelID)` | 生成 Markdown 表格格式的术语校正参考（注入 AI prompt） |
| `CountGlobal(ctx)` | 返回全局术语条目总数 |
| `ImportMarkdown(ctx, channelID, content)` | 解析并导入 Markdown 格式术语表 |
| `ExportJSON(ctx, channelID)` | 导出 JSON 格式术语表 |
| `ImportJSON(ctx, channelID, data)` | 导入 JSON 格式术语表 |
| `DeleteByIDs(ctx, channelID, ids)` | 批量删除术语条目（事务+分批 900 条） |
| `ToggleByIDs(ctx, channelID, ids, enabled)` | 批量切换启用/禁用状态（事务+分批 900 条） |
| `ClearAll(ctx)` | 清除所有术语条目和元数据（DELETE FROM glossary_entries + glossary_meta），用于配置导入 overwrite 策略 |
| `CopyFromChannel(ctx, srcChannelID, dstChannelID)` | 复制源主播术语条目到目标主播（仅复制 source=channel 的条目） |

### Store（术语候选 CRUD）

| 方法 | 说明 |
|------|------|
| `ListCandidates(ctx, channelID, status)` | 列出术语候选（支持 status 过滤：pending/approved/rejected/all） |
| `GetCandidate(ctx, id)` | 获取单个候选 |
| `ApproveCandidate(ctx, id, term, canonical, category)` | 审批通过候选（事务：写入 glossary_entries + 更新候选状态） |
| `RejectCandidate(ctx, id)` | 拒绝候选 |
| `BatchApproveCandidates(ctx, channelID, ids)` | 批量审批通过 |
| `BatchRejectCandidates(ctx, channelID, ids)` | 批量拒绝 |
| `UpsertCandidate(ctx, channelID, item, sessionID)` | 插入或更新候选（基于 normalized_key 去重，累加出现次数/会话数，重新计算评分） |

### Discoverer（AI 术语发现）

| 方法 | 说明 |
|------|------|
| `NewDiscoverer(store, provider, sessions, opts...)` | 创建 Discoverer 实例（可配置分块大小/最大块数/超时） |
| `Discover(ctx, channelID, sessionID, transcript, segments, existingGlossary)` | 对转写文本执行 AI 术语发现，合并候选到 store |

**Discoverer 选项函数：**

| 函数 | 说明 |
|------|------|
| `WithDiscoveryChunkChars(n)` | 设置每块字符数（默认 12000，最小 1000） |
| `WithDiscoveryMaxChunks(n)` | 设置最大块数（默认 8） |
| `WithDiscoveryTimeout(d)` | 设置发现超时（默认 2 分钟） |

### 错误定义

| 错误 | 说明 |
|------|------|
| `ErrNotFound` | 条目不存在 |
| `ErrDuplicate` | 条目已存在 |
| `ErrCandidateNotFound` | 候选不存在 |
| `ErrInvalidCandidate` | 无效候选（缺少必填字段/无效状态） |

## 术语候选评分算法

`calculateCandidateScore(confidence, occurrenceCount, sessionCount)` 计算候选评分（0-1）：

- 置信度权重 0.65（直接使用 confidence）
- 会话因子 0.20：`min(1, sessionCount/3.0)`
- 出现次数因子 0.15：`min(1, log1p(count)/log1p(8))`
- 结果四舍五入到 4 位小数

## AI 术语发现流程

1. `Discoverer.Discover` 接收转写文本和 segments
2. 优先使用 segments 分块（`buildChunksFromSegments`），否则按段落分块（`buildChunksFromText`）
3. 每块最多 `chunkChars` 字符，最多 `maxChunks` 块
4. 对每块调用 AI Provider 的 `Generate` 方法，使用 `discoverySystemPrompt`
5. `parseDiscoveryResult` 解析 JSON 响应，过滤空 term/canonical
6. `mergeCandidates` 逐条调用 `UpsertCandidate` 合并到 store
7. AI prompt 包含"已有术语表"以避免重复提取

## 合并规则

`ListByChannel` 合并全局和主播级别的术语条目：

1. 全局条目默认包含在结果中，标注 `source: "global"`
2. 主播级别 `enabled=true` 的条目覆盖同 term 的全局条目，标注 `source: "channel"`
3. 主播级别 `enabled=false` 的条目阻止同 term 的全局条目出现（屏蔽）
4. 主播级别 `enabled=false` 且不匹配任何全局条目的条目不包含在结果中

## 导入格式

### Markdown 导入

支持管道符表格格式和标题分类：

```markdown
# 技术术语

| 误识别 | 正确 | 分类 |
|---|---|---|
| AI | 人工智能 | 技术 |
| AGI/ASI | 通用人工智能 | 技术 |
```

- 第一列包含 "ASR" 或 "误识别" 的行被视为表头跳过
- term 和 canonical 相同时跳过
- term 中 `/` 分隔多个变体，分别创建条目
- canonical 中 `/` 取第一个值
- 标题行（`#` 开头）作为后续表格行的默认分类

### JSON 导入/导出

```json
{
  "channel_id": "",
  "entries": [
    {"term": "AI", "canonical": "人工智能", "category": "技术", "enabled": true, "source": "global"}
  ],
  "note": "备注文本",
  "exported_at": "2026-05-07T12:00:00Z"
}
```

> **ImportJSON 双格式**(2026-07-08):除上述 `GlossaryExport` 对象外,也接受**裸数组** `[{...},{...}]`(前端 `importGlobalJSON` 的 `JSON.parse` 典型形态)。两种格式都解析失败时返回 `ErrInvalidJSON` 哨兵(handler 层 `writeError` 映射为 400,而非通用 500)。范式与 `internal/recap/template.go` 的 `ImportJSON` 对称。

## 关键依赖与配置

- 依赖 `database/sql`（SQLite）
- 依赖 `internal/aiprovider`（GenerateResult 类型）
- 依赖 `internal/session`（Session 类型）
- 数据库表: `glossary_entries`（v11 迁移）、`glossary_meta`（v12 迁移）、`glossary_candidates`（v28 迁移）
- 唯一索引: `glossary_entries(channel_id, term)`、`glossary_candidates(channel_id, normalized_key)`
- 批量操作使用 900 条/批避免 SQLite 参数限制

## 数据模型

**glossary_entries 表：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | INTEGER PK AUTO | 自增主键 |
| `channel_id` | TEXT | 主播 ID，空字符串表示全局 |
| `term` | TEXT | 错误写法（ASR 误识别词） |
| `canonical` | TEXT | 正确写法 |
| `category` | TEXT | 分类标签 |
| `enabled` | INTEGER | 是否启用（1/0） |
| `created_at` | TEXT | 创建时间 |
| `updated_at` | TEXT | 更新时间 |

**glossary_meta 表：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `channel_id` | TEXT PK | 主播 ID，空字符串表示全局 |
| `note` | TEXT | 自由文本备注 |
| `updated_at` | TEXT | 更新时间 |

**glossary_candidates 表（v28 新增）：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | INTEGER PK AUTO | 自增主键 |
| `channel_id` | TEXT | 主播 ID |
| `term` | TEXT | 转写中出现的写法（NOT NULL, 非空） |
| `canonical` | TEXT | 建议正式写法 |
| `category` | TEXT | 分类 |
| `status` | TEXT | pending/approved/rejected（CHECK 约束） |
| `confidence` | REAL | AI 置信度（0-1） |
| `score` | REAL | 综合评分（0-1） |
| `occurrence_count` | INTEGER | 出现次数 |
| `session_count` | INTEGER | 涉及会话数 |
| `first_session_id` | TEXT | 首次出现的会话 |
| `last_session_id` | TEXT | 最近出现的会话 |
| `reason` | TEXT | AI 给出的一句依据 |
| `normalized_key` | TEXT | 去重键（NOT NULL, 非空） |
| `created_at` | TEXT | 创建时间 |
| `updated_at` | TEXT | 更新时间 |
| `reviewed_at` | TEXT | 审核时间 |

唯一索引：`(channel_id, normalized_key)`
索引：`(channel_id, status, score DESC, updated_at DESC)`、`(last_session_id)`

## 测试与质量

- `glossary_test.go`: 31 个测试用例，覆盖：
  - CRUD: Upsert、UpsertIdempotent、Delete、DeleteNotFound、Toggle
  - 合并逻辑: ListByChannelMerge、ListByChannelOverride、ListByChannelBlock
  - Prompt 导出: ExportForPromptEmpty、ExportForPromptWithEntries、ExportForPromptSkipsDisabled、ExportForPromptWithNote
  - 备注: GetNoteEmpty、SetAndGetNote
  - 计数: CountGlobal
  - Markdown 导入: ImportMarkdownBasic、ImportMarkdownMultiVariant、ImportMarkdownCategory、ImportMarkdownSkipHeaders、ImportMarkdownSkipIdentical、ImportMarkdownEmpty、ImportMarkdownOnlyHeadings
  - JSON 导入/导出: ExportImportJSON（往返测试）、ImportJSONInvalid、ImportJSONMissingFields、ImportJSONArrayInput（裸数组格式,2026-07-08）、ImportJSONSingleObjectNoEntries、ImportJSONInvalidReturnsSentinel（400 哨兵）
  - 批量删除: DeleteByIDs、DeleteByIDsEmpty、DeleteByIDsChannelScope
  - 批量切换: ToggleByIDs、ToggleByIDsEmpty、ToggleByIDsChannelScope

- `candidate_store_test.go`: 12 个测试用例，覆盖：
  - Upsert: 新建候选、合并候选（累加计数/更新置信度/分类/原因）、normalized_key 去重
  - 评分: calculateCandidateScore 多档验证（高/低/中/超范围/负值）
  - 列表过滤: status 过滤（pending/approved/all）、channel 隔离
  - 审批: ApproveCandidate（写入 glossary_entries + 状态更新）、幂等审批
  - 拒绝: RejectCandidate（状态变更）
  - 批量: BatchApprove（3 条同时审批）、BatchReject
  - 查询: GetCandidateByID（存在/不存在）

- `discovery_test.go`: 12 个测试用例，覆盖：
  - 分块: ChunkText_Basic、ChunkText_Segments、ChunkText_MaxChunks
  - 发现流程: Discover_EmptyTranscript、Discover_ProviderError、Discover_InvalidJSON、Discover_Success（2 候选）、Discover_MergeCandidates（跨块合并）
  - Prompt 构建: BuildDiscoveryPrompt（含/空术语表）
  - 解析: ParseDiscoveryResult（6 种 JSON 输入场景）
  - 时间戳: formatDiscoveryTimestamp（5 种时间格式）

## 常见问题 (FAQ)

**Q: 全局术语和主播术语的优先级是什么？**
A: 主播级别 `enabled=true` 的条目覆盖同 term 的全局条目；主播级别 `enabled=false` 的条目屏蔽同 term 的全局条目。

**Q: glossary_file 配置还有效吗？**
A: 已标记为 deprecated。启动时如果数据库中无全局词条且文件存在，会自动导入。后续操作通过 Web API 管理。

**Q: 术语发现什么时候触发？**
A: 回顾生成完成后自动触发（后台 goroutine），也可通过 `POST /api/sessions/:sid/glossary/discover` 手动触发。

**Q: 候选如何变成正式术语？**
A: 通过审批（ApproveCandidate）自动写入 glossary_entries 表。支持批量审批。

## 相关文件清单

- `glossary.go` -- Store 实现、CRUD、合并、导入/导出、Prompt 生成、ClearAll 全量清除、CopyFromChannel 跨主播复制
- `candidate_store.go` -- 候选 CRUD、审批/拒绝、批量操作、评分算法、去重合并
- `discovery.go` -- Discoverer AI 术语发现、分块、prompt 构建、结果解析
- `glossary_test.go` -- 单元+集成测试（31 个用例）
- `candidate_store_test.go` -- 候选 CRUD 和评分测试（12 个用例）
- `discovery_test.go` -- AI 术语发现测试（12 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-03 | 增量扫描 | 新增 `ClearAll(ctx)` 方法（DELETE FROM glossary_entries + glossary_meta），用于配置导入 overwrite 策略的全量清除 |
| 2026-06-01 | 测试补充 | 新增 `candidate_store_test.go`（12 用例：Upsert 新建/合并/normalized_key 去重、评分算法验证、状态过滤/channel 隔离/审批/拒绝/批量操作/幂等/GetCandidateByID）和 `discovery_test.go`（12 用例：分块策略、发现流程完整路径、Prompt 构建、JSON 解析、时间戳格式化），填补了上次扫描识别的测试缺口 |
| 2026-05-23 | 重大更新 | 新增 AI 术语发现系统：`candidate_store.go`（Candidate CRUD/审批/拒绝/批量操作/评分算法 calculateCandidateScore/去重合并 UpsertCandidate）、`discovery.go`（Discoverer/分块策略/DiscoveryProvider 接口/discoverySystemPrompt/AI 候选提取/parseDiscoveryResult）；新增 glossary_candidates 表（v28 迁移）；新增 7 个 API 端点（candidates 列表/审批/拒绝/批量审批/批量拒绝/会话发现） |
| 2026-05-17 | 优化 | ListByChannel 从 O(GxL) 优化为 O(G+L)，使用 globalTerms map[string]struct{} 预构建全局索引 |
| 2026-05-07 | 初始化 | 首次生成模块文档 |
