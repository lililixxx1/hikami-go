# 已知问题（Known Issues）

> 本文件收集已发现但尚未修复的问题。每条记录发现日期、严重程度、根因、影响、建议修复方案。
> 修复完成后将对应条目移至「已修复」小节并标注修复日期。
> 最后更新：2026-08-16

---

## 待修复

（当前无待修复项。历史遗留项见下方「已修复」小节的残余窗口说明。）

---

## 已修复

### ISSUE-006：崩溃恢复可能重复执行远端付费任务（ASR 重新提交 DashScope）

- **发现日期**：2026-08-01
- **修复日期**：2026-08-16
- **严重程度**：中（需极窄崩溃时机 + 重复付费，无数据损坏）
- **发现途径**： codex 四阶段代码审核（r7/r8/r9/r10，session `019fbacf-5a77-7ae0-a653-e64c87bb9ab2`）
- **修复方案**：`dashscope_task_id` 持久化（发现时记录的正解方向①）——`SubmitASRTask`/`AwaitASRTask` 两阶段接口 + submit 成功后立即写入 task payload（`worker.Store.UpdatePayload`，M11 基础设施）+ 恢复重入 await 轮询零付费 + 瞬态错误 fail-closed + `ErrDashScopeTaskDead` 哨兵。正解方向②（下游幂等创建）由 PR#1（recap `EnqueueIfNoActive`）+ M11（publisher）+ 本次 G-1（asr 自身）共同落地。
- **修复计划**：[`plans/plan-issue006-dashscope-taskid-persist-2026-08-16.md`](../plans/plan-issue006-dashscope-taskid-persist-2026-08-16.md)（自查 + 三轮 plan-code-reviewer 审核闭环记录）
- **测试**：asr 107→123（resubmit_test.go 8 + dashscope_await_test.go 8），核心契约「恢复重入 Submit 零调用」「fail-closed 零 POST」「防无限重提交」均有专项测试钉死。

#### 残余窗口（已知且接受，均无数据损坏）

| 窗口 | 说明 | 缓解 |
|------|------|------|
| submit→persist 毫秒级窗口 | DashScope submit HTTP 成功后、payload 持久化完成前崩溃，重入仍会重新提交 | DashScope 无客户端幂等键，根本性限制；窗口从「整个 poll 阶段（最长 10min）」缩到毫秒级 |
| persist 持续失败 | DB 故障导致 `UpdatePayload` 持续失败时，每次 retry 都重新提交（WARN 日志可见） | 概率极低；WARN 带 task_id/dashscope_task_id 便于发现 |
| G-2：人工 reset 丢 taskID | `ResetFailedSession` 不建新任务，reset 后重新 CreateTask 得空 payload → 若旧远端任务仍活着会重新付费 | **asr 失败优先用任务 retry（保留 taskID，走 await 免费恢复），不要 reset** |
| fetch 结果 URL 过期 | 远端已 SUCCEEDED 但结果 URL 404 → fail-closed 停 failed，人工出口只有 reset（会触发上一条） | 极罕见；遇见时接受一次重付费 |
| 恢复期间改 VAD 配置 | 恢复重入时 VAD 用新配置重裁，与首次提交的远端结果时间线错位 | 恢复期间勿改 VAD 阈值/padding/engine 配置 |

### ISSUE-005：MCP 配置不参与导入导出

- **发现日期**：2026-07-23
- **修复日期**：2026-07-23
- **严重程度**：中（功能遗漏，数据无损坏）
- **报告人**：用户（实测配置备份功能时发现）
- **修复方案**：投影 DTO + 密钥走 Secrets（仿 WebDAV/ASRS3 范式）
- **详细分析**：见 [`docs/MCP配置导入导出缺失问题分析.md`](./MCP配置导入导出缺失问题分析.md) 第六节「修复实施」
- **修复计划**：[`plans/plan-mcp-config-export-import-2026-07-23.md`](../plans/plan-mcp-config-export-import-2026-07-23.md)

#### 问题描述（修复前）

Web 设置页的「配置备份」（导出/导入）不包含 MCP 配置段。导出的 JSON bundle 顶层字段只有 6 个全局段 + secrets/channels/glossary/templates/bili_accounts，**没有 `mcp` 字段**。换机器后 MCP 配置（Servers 列表、Brave/Tavily key、enabled、max_tool_rounds）需全部手动重建。

#### 根因

`internal/handler/config_export.go` 的 `ConfigExportBundle` 结构没有 MCP 字段；导出填充逻辑（`handleExportConfig`）也未读取 `s.cfg.MCP`。深层原因：导入导出功能完成于 2026-07-05（6 段），MCP 段是 2026-07-22 新增（commit `5b84b63`），引入时漏更新 `config_export.go`。导入侧有保护性副作用——只处理 bundle 携带的段，所以 merge/overwrite 都不碰 MCP，现有 MCP 配置不会损坏，但也恢复不了。

#### 修复实施（2026-07-23）

仅改 `internal/handler/config_export.go` 单文件（后端，前端/OpenAPI 无需动 —— config-export bundle 不在 OpenAPI spec 范围）：

1. 新增 `MCPExportSection`/`mcpServerExport`/`mcpBuiltinExport` 投影 DTO，剔明明文密钥（`Builtin.BraveAPIKey`/`TavilyAPIKey`、`Servers[].Headers["Authorization"]`）。
2. 3 helper：`mcpToExport`（密钥写 secrets map）、`mcpServerSecretKey`（「下标+名」双键 `MCP_SERVER_{idx}_{NAME}_AUTHORIZATION` 防归一化碰撞）、`mcpFromExport`（密钥回填）。
3. `ConfigExportBundle` 加 `MCP *MCPExportSection`（指针+omitempty，旧备份缺段为 nil）。
4. 导出填充 + 导入恢复（走 `MCPSectionDTO` 与 PUT handler 同构）+ `mcpManager.Reload` 热重载。
5. 密钥约定：Brave/Tavily → `MCP_BRAVE_API_KEY`/`MCP_TAVILY_API_KEY`（固定键名）。

#### 验证

- `config_export_test.go` +6 测试（密钥不泄漏、omitempty、round-trip 完全可逆、同名碰撞双键、merge 持久化+密钥回填、旧 bundle 零回归），共 17 用例。
- handler/config/mcp 包测试全过、`go vet`/`gofmt` 通过。
- 零回归：旧 bundle 无 mcp 段 → MCP 配置不被破坏（`TestImportConfigOldBundleLeavesMCPUntouched` 钉死）。
- 经 qoder 计划审核（Qwen3.8-Max-Preview，Ready with fixes，3 Important + 3 Minor 全采纳）+ 执行后复审。

### ISSUE-001：ASR 成本估算单价严重偏高（约 40 倍）

- **发现日期**：2026-07-11
- **修复日期**：2026-07-11
- **严重程度**：低（仅影响仪表盘费用估算显示，不影响实际计费）
- **报告人**：用户

#### 问题描述

费用趋势表（`GET /api/stats/dashboard` → 前端 `DashboardSection.vue`）中的 ASR 成本使用的单价 **¥36/小时（¥0.01/秒）** 与阿里云百炼实际计价严重不符。

#### 根因

代码中硬编码的 ASR 单价来源于一个粗略的错误估算：

`internal/handler/server.go:3939`：
```go
// Cost estimate: DashScope ASR ~¥0.01/sec = ¥36/hour
const asrCostPerHour = 36.0
```

同样地，`internal/session/session.go:871` 的 SQL 中直接写死了 `36.0`：
```sql
asr_hours * 36.0 AS asr_cost,
asr_hours * 36.0 + recap_count * 0.1 AS total_cost
```

#### 实际计费（阿里云官方文档）

项目默认 ASR 模型为 `fun-asr`（`internal/config/config.go:778`，`v.SetDefault("dashscope.model", "fun-asr"`）。

| 模型 | 实际单价 | 折合每小时 | 来源 |
|------|---------|-----------|------|
| `fun-asr`（=fun-asr-2025-11-07） | $0.000035/秒 ≈ ¥0.00025/秒 | **≈ ¥0.90/小时** | [百炼 fun-asr SDK 文档](https://www.alibabacloud.com/help/zh/model-studio/fun-asr-recorded-speech-recognition-java-sdk) |
| `fun-asr` 快照版 | $0.000032/秒 ≈ ¥0.00023/秒 | ≈ ¥0.83/小时 | 同上 |
| `paraformer-1`（ISI 平台） | ¥0.00008/秒 | ¥0.288/小时 | [ISI 计费文档](https://help.aliyun.com/zh/isi/developer-reference/metering-and-billing) |

此外 `fun-asr` 每月有 **36,000 秒（10 小时）免费额度**，且仅对音轨中被判定为语音内容的时长计费（非语音不计费，实际计费时长通常短于音频时长）。

| | 代码里的值 | 实际 fun-asr |
|--|----------|-------------|
| 每秒 | ¥0.01 | ≈¥0.00025 |
| 每小时 | ¥36 | ≈¥0.90 |
| **偏差** | — | **高估约 40 倍** |

#### 影响范围

以下 3 处使用了错误的 `36.0` 单价：

1. `internal/handler/server.go:3940` — `handleStatsOverview`，`asrCostPerHour = 36.0`
2. `internal/handler/server.go:4078` — `handleStatsCost`，`asrCostPerHour = 36.0`
3. `internal/session/session.go:871` — `GetDashboardStats` SQL，`asr_hours * 36.0`

#### 附带问题：时长计算方式不准确

即使单价修正，当前 ASR 小时数的计算也有偏差：

- **用的是场次录制时长**（`ended_at - started_at`），不是音频实际送检时长。
- `ended_at` 为 NULL 时兜底按 **2.0 小时** 算，短视频（如 1~2 分钟回放片段）会被估成 2 小时，偏差可达 110 倍。
- SQL 只统计 `status IN ('asr_done','recap_done','uploaded','published')` 的场次，未跑 ASR 的不计。

#### 建议修复方案

**方案 A（最小改动）**：把 3 处 `36.0` 改成 `0.9`，注释更新为实际单价来源。

**方案 B（更准确）**：
1. ASR 任务完成时，从 DashScope 返回结果的 `content_duration` 字段读取真实计费时长（毫秒），写入 `tasks.usage_metadata`（DB v34 已加此列但目前全为 `{}`，代码中无任何写入点）。
2. 费用统计从 `usage_metadata` 读取实际计费秒数 × 实际单价。
3. 兜底场景（无 usage_metadata 的历史数据）用 ffprobe 读 `audio.asr.mp3` 实际时长。

**推荐**：先做方案 A（改单价），方案 B 作为后续改进。

### ISSUE-003：发现回放建场次时标题为空，显示为 BV 号

- **发现日期**：2026-07-11
- **修复日期**：2026-07-11
- **严重程度**：中（影响可读性，功能正常）
- **报告人**：用户

#### 问题描述

通过发现回放（discover）自动或手动创建的 download 场次，`title` 字段被设为 BV 号（如 `BV1QQLr6kEFw`），而非视频真实标题。用户在"最近场次"/"最近回顾"列表中看到的是 BV 号而非有意义的标题。

#### 根因

有两条创建 download 场次的代码路径，对标题的处理完全不同：

**路径 A（手动粘贴 URL）——标题正确**：`internal/download/download.go:466-517`

`Handler.CreateFromURL` → `resolveDownloadTitle` 调用 `biliutil.FetchVideoInfo` 取 B 站视频真实标题 + `biliutil.CleanReplayTitle` 清洗（去掉 `【直播回放】`/日期后缀）。失败时退回 sourceID。此路径仅用于 `POST /api/sessions/download`（用户在 UI 粘贴 BV 链接）。

**路径 B（发现回放）——标题为空 → 兜底 BV 号（Bug）**：`internal/discover/discover.go`

`YTDLPLister.List`（`:33-72`）用 `yt-dlp --dump-json --flat-playlist` 列出频道回放列表，`--flat-playlist` 模式下 B 站合集/系列 URL 的 `title` 字段经常为空。`entry.Title`（空串）被原样传入 `CreateDownload`：

- `DiscoverChannel`（`:340-345`）：`Title: entry.Title`
- `Execute`（`:235-240`）：`Title: item.Title`（来自前端，前端从 `PreviewChannel` 拿到，也是 `entry.Title` 原样透传）
- `PreviewChannel`（`:388-413`）：同样透传 `entry.Title`

然后 `session.CreateDownload`（`internal/session/session.go:147-149`）的空标题兜底逻辑生效：

```go
if strings.TrimSpace(input.Title) == "" {
    input.Title = input.SourceID   // ← 标题变成 BV 号
}
```

**discover 包不导入 `biliutil`，从不调用 `FetchVideoInfo`**——已有的标题解析基础设施完全未被这条路径使用。

受影响入口：
- 调度器自动发现（`scheduler.go:85-97` → `DiscoverAll` → `DiscoverChannel`）
- 一步式发现按钮（`POST /api/sessions/discover` → `DiscoverAll`）
- 两步式发现执行（`POST /api/sessions/discover/execute` → `Execute`）

**标题一旦设置就不更新**：全代码树无任何 `UPDATE sessions SET title` 或 `UpdateTitle` 调用。下载 worker `HandleTask` 不碰标题，`biliutil.VideoClient.Fetch` 在下载包内仅用于取 CID（弹幕），不回写标题。

#### 影响范围

| 文件 | 位置 | 问题 |
|------|------|------|
| `internal/discover/discover.go` | `:340-345` `DiscoverChannel` | `entry.Title`（空）直接传入 `CreateDownload` |
| `internal/discover/discover.go` | `:235-240` `Execute` | `item.Title`（空，来自前端透传）直接传入 `CreateDownload` |
| `internal/discover/discover.go` | `:388-413` `PreviewChannel` | 预览结果也透传空 `entry.Title` |
| `internal/discover/discover.go` | `:33-72` `YTDLPLister.List` | `--flat-playlist` 下 B 站 title 字段经常为空 |
| `internal/session/session.go` | `:147-149` `CreateDownload` | 空标题兜底为 `sourceID`（BV 号） |

#### 建议修复方案

**方案 A（推荐，最小改动）**：在 discover 包的 `DiscoverChannel` 和 `Execute` 创建场次前，对空标题做延迟解析——复用已有的 `biliutil.FetchVideoInfo` + `CleanReplayTitle` + cookie 解析逻辑。

需要把 `download.Handler.resolveDownloadTitle` 和 `downloadCookieHeader` 的能力下沉或暴露给 discover 包（discover 的 `Manager` 目前不持有 cookie 账号存储等依赖）。

```go
// 伪代码：DiscoverChannel 内 entry.Title 为空时
if strings.TrimSpace(entry.Title) == "" {
    entry.Title = m.resolveTitle(ctx, item.ID, entry.ID)
}
```

**方案 B（备选，下载时补全）**：在 `download.HandleTask` 执行下载时，如果 session 标题仍是 BV 号（等于 source_id），用 `FetchVideoInfo` 取真实标题并 `UPDATE sessions SET title`。好处是不改 discover 的依赖链，坏处是标题在下载完成后才更新（发现预览阶段仍空）。

**方案 C（Preview 阶段并行解析）**：`PreviewChannel` 阶段对空标题 entry 批量调 `FetchVideoInfo` 填充，前端预览即可看到真实标题，`Execute` 直接带上。代价是预览阶段多 N 次 B 站 API 调用（需限速 + 风控处理）。

### ISSUE-002：清空失败场次后返回页面仍显示失败状态

- **发现日期**：2026-07-11
- **修复日期**：2026-07-11
- **严重程度**：中（影响用户体验，数据无损坏）
- **报告人**：用户

#### 问题描述

回顾管理页面（RecapsView）点击"清空失败"操作失败后，再次打开/导航回该页面仍然显示失败状态（错误提示残留 + 失败场次列表不刷新）。

#### 根因（三层叠加）

**根因 1（主因）：`handleClearFailed` 缺少 try/catch**

`web/src/views/RecapsView.vue:452-461` 是全文件唯一没有 `try/catch/finally` 的动作处理器：

```js
async function handleClearFailed() {
  ...
  const result = await deleteFailedSessions()  // ← 失败时直接抛出
  HMessage.success(`已删除 ${result.deleted} 个`)  // ← 被跳过
  await sessionsStore.fetchSessions()             // ← 被跳过，列表不刷新
}
```

`deleteFailedSessions()` 失败时：
- 错误作为 unhandled rejection 冒泡，`client.ts` 拦截器弹一条 error toast
- `fetchSessions()` **被跳过**，`sessionsStore.items` 保持旧数据（仍含已试图删除的失败场次）

对比同文件其他 handler（`handleRowAction`/`handleDrawerAction`/`handleRetry` 等）均有 `try/finally`，`handleRetry` 甚至有 `catch`。

**根因 2：store 缓存导致返回页面不刷新**

`web/src/stores/sessions.ts` 的 `ensureLoaded()` 是缓存模式——`loaded` 标志一旦为 `true` 就永远 no-op（无 TTL、无失效机制）。`RecapsView.onMounted` 用 `ensureLoaded()` 而非 `fetchSessions()`，所以导航离开再回来不会重新请求，旧数据一直显示。

**根因 3：toast 队列是全局的，不随导航清除**

`web/src/components/ui/message.ts` 的 `toasts` 数组是模块级全局状态，由 `HToast.vue` 挂载在 `<body>` 上，独立于组件生命周期。toast 仅靠 3 秒 `setTimeout` 自动消失，导航/重新挂载组件不会清除残留 toast。

#### 影响范围

| 文件 | 位置 | 问题 |
|------|------|------|
| `web/src/views/RecapsView.vue` | `:452-461` `handleClearFailed` | 无 try/catch，失败时列表不刷新 |
| `web/src/components/ui/message.ts` | `:15` `toasts` 全局队列 | 不随导航/组件卸载清除 |
| `web/src/stores/sessions.ts` | `:33-40` `ensureLoaded` | 缓存无失效，返回页面不重新请求 |

#### 建议修复方案

1. **`handleClearFailed` 加 try/catch/finally**（对齐其他 handler 模式）：`finally` 里调 `fetchSessions()` 确保无论成功失败都刷新列表；`catch` 为空（错误 toast 由 `client.ts` 拦截器统一处理，与 `openDiscover` 同模式）。

2. **`message.ts` 导出 `clearToasts()`**：`RecapsView.onMounted` 开头调用，清掉从其他页面或上一次操作残留的 toast。

3. **（可选）`ensureLoaded` 返回页面时强制刷新**：当前不改，属预期缓存行为；如需改可让 `onMounted` 用 `fetchSessions()` 替代 `ensureLoaded()`，但会丧失 inflight 去重。

#### 附带发现

同文件其他 handler（`handleRowAction`/`handleDrawerAction`/`handlePartialRecap`/`handleFetch`/`handleRegenerate`/`handleDiscoverExecute`/`handleDiscoverAll`）虽有 `try/finally` 但都无 `catch`，失败时 `fetchSessions()` 同样被跳过，列表同样变 stale。`handleRetry` 是唯一在 `catch` 里补偿刷新的。这些不在本 issue 范围内但值得后续统一处理。

### ISSUE-004：回顾内容编辑保存后页面不刷新，看似编辑无效

- **发现日期**：2026-07-11
- **修复日期**：2026-07-11
- **严重程度**：中（UX 误导，功能实际可用）
- **报告人**：用户
- **状态**：已实测验证

#### 问题描述

回顾管理页面打开回顾抽屉，点击"编辑"修改 markdown 内容后点"保存"，页面显示"回顾内容已保存"成功提示，但**预览区域仍显示旧内容**，让用户以为编辑没有生效。实际文件已正确写入磁盘，重新打开抽屉才能看到更新后的内容。

#### 实测验证

在浏览器中打开回顾管理 → 回放 tab → 点击 `media_ready` 状态场次，抽屉显示**"回顾内容尚未生成"**，**"编辑"按钮不显示**。原因：这些场次只下载+转码了，没跑过 ASR/recap，`GET /api/sessions/:sid/recap` 返回 `available: false`，模板 `v-if="content?.available"`（`RecapDrawerV10.vue:207`）为 false，整个动作栏（含编辑按钮，`:233`）不渲染。

**但编辑功能本身完整可用**：后端 `PUT /api/sessions/:sid/recap/content`（`server.go:320,3675-3712`）正常工作，前端 `saveEdit()`（`RecapDrawerV10.vue:108-120`）正确调用。当回顾已生成（`available: true`）时编辑按钮会显示，保存后文件确实写入磁盘。问题仅在保存后 UI 不刷新。

#### 根因

`web/src/features/recaps/components/RecapDrawerV10.vue:108-120` 的 `saveEdit()`：

```js
async function saveEdit(): Promise<void> {
  if (!props.session) return
  saving.value = true
  try {
    const { updateRecapContent } = await import('@/api/sessions')
    await updateRecapContent(props.session.id, draft.value)
    HMessage.success('回顾内容已保存')
    editing.value = false       // ← 退出编辑态，切回预览
  } finally {
    saving.value = false
  }
}
```

保存成功后 `editing = false`，模板切回 `v-if="!editing"` 的预览区（`:267`），预览内容由 `renderedMarkdown` computed 驱动（`:50-53`）：

```js
const renderedMarkdown = computed(() => {
  if (!props.content?.markdown) return ''
  return DOMPurify.sanitize(marked.parse(props.content.markdown) as string)
})
```

`renderedMarkdown` 依赖 `props.content.markdown`，但 **`props` 是只读的，保存后从未更新**。父组件 `RecapsView.vue` 的 `openRecap`（`:221-234`）是唯一加载回顾内容的地方，`saveEdit` 没有向父组件 emit 任何事件，父组件也没有 re-fetch。

结果：文件写入了磁盘（后端 `os.WriteFile` 正常执行），但 UI 预览仍渲染旧的 `props.content.markdown`，用户看不到改动，以为编辑无效。

#### 附带问题 1：`saveEdit` 无 catch

`saveEdit` 只有 `try/finally` 没有 `catch`。后端 `handleUpdateRecapContent`（`server.go:3691-3694`）有 `LocalAvailable` 守卫——已发布且本地文件已归档的场次 PUT 会返回 4xx 错误。此时异常从 `await updateRecapContent` 抛出，`editing.value = false` 不执行（编辑器保持打开），`client.ts` 拦截器弹 error toast。行为可接受但缺少内联错误提示。

#### 影响范围

| 文件 | 位置 | 问题 |
|------|------|------|
| `web/src/features/recaps/components/RecapDrawerV10.vue` | `:108-120` `saveEdit` | 保存成功后不更新 `props.content`，不 emit 事件让父组件 re-fetch |
| `web/src/features/recaps/components/RecapDrawerV10.vue` | `:50-53` `renderedMarkdown` | 依赖只读 `props.content.markdown`，保存后仍渲染旧内容 |
| `web/src/views/RecapsView.vue` | `:221-234` `openRecap` | 唯一的内容加载入口，无 post-save 刷新机制 |

#### 建议修复方案

**方案 A（推荐）**：`saveEdit` 成功后 emit `saved` 事件，父组件 `RecapsView` 监听后 re-fetch 回顾内容。

`RecapDrawerV10.vue`：
```js
const emit = defineEmits<{
  // ...existing...
  saved: []
}>()

async function saveEdit(): Promise<void> {
  if (!props.session) return
  saving.value = true
  try {
    const { updateRecapContent } = await import('@/api/sessions')
    await updateRecapContent(props.session.id, draft.value)
    HMessage.success('回顾内容已保存')
    editing.value = false
    emit('saved')   // ← 新增
  } catch {
    // 错误 toast 由 client.ts 拦截器处理；保持编辑态让用户可重试
  } finally {
    saving.value = false
  }
}
```

`RecapsView.vue`：
```vue
<RecapDrawerV10
  ...
  @saved="onRecapSaved"
/>
```
```js
async function onRecapSaved() {
  if (selectedSession.value) {
    try {
      recapContent.value = (await getRecapContent(selectedSession.value.id)) as unknown as DerivedRecapContent
    } catch { /* ignore */ }
  }
}
```

**方案 B（备选，不依赖父组件）**：在 `RecapDrawerV10` 内部维护一个 `committedDraft` ref，`renderedMarkdown` 优先用 `committedDraft`，保存成功后写入 `committedDraft`。但这样父组件的 `recapContent` 仍是旧值，复制/导出等功能可能取到旧内容。

#### 附带问题 2：GET 与 PUT 路径的 slug 清洗不一致（latent，当前不触发）

GET（`server.go:1271`）用 `safeRecapName("直播回顾_" + slug)` 清洗路径，PUT（`:3696-3697`）直接用 raw slug。由于 `session.sanitizeSlug`（`session.go:518-535`）在创建时已把 slug 限制为 `[a-z0-9_-]`，`safeRecapName` 的替换（`/ \ space` → `_`）是 no-op，两条路径产出相同文件名。但如果未来有其他入口创建未清洗的 slug，会读不到刚写的文件。建议 PUT 也统一用 `safeRecapName`。

> **已修复（2026-07-11）**：PUT 路径已改用 `safeRecapName`，与 GET 一致。新增 `TestRecapContentRoundTrip` 测试覆盖含空格 slug 的读写一致性。

### ISSUE-007：recap provider 返回空 content（2026-08-13 定位真根因 flash+max_tokens 并修复，原判断"间歇性"不准确）

- **发现日期**：2026-08-03
- **严重程度**：中（2026-08-13 改判：根因实为 model 被覆盖成 flash + max_tokens 对 reasoning 模型不足的**确定性**失败，并非间歇；残余真·间歇由 provider 层重试兜底）
- **发现途径**：救回 8-2 场（`bili_1298779265_live_20260802_204346`）时实测

#### 触发条件

DeepSeek（`openai_compatible` provider）偶发返回 HTTP 200 但 `choices[0].message.content` 为空字符串的响应。`provider_openai.go:46-48` 判定 `result.Content == "" && len(result.ToolCalls) == 0` 后返回 `fmt.Errorf("recap provider response missing content")`。

#### 现象

- recap 任务失败，`task.error = "recap provider response missing content"`，session 回到 `failed`。
- **关键诊断缺陷**：错误返回时 `GenerateResult.Raw`（DeepSeek 原始 JSON 响应）已在 `provider_openai.go:47` 设置，但 `handler.go:690-692` 的 `if err != nil { return err }` **丢弃了 result（含 Raw）**，既不写日志也不存 DB。导致无法判断 DeepSeek 为何返回空 content（模型 `deepseek-v4-pro` 不存在？token 超限 `finish_reason=length`？content 以 null 返回？API 限流？）。
- 本次实测：同配置重跑（attempt 2）成功生成完整回顾，说明是**间歇性**而非配置错误。

#### 当前缓解

- recap 任务有 `attempt` 重试机制（worker 池），间歇失败时重试通常能成功。
- 本次救场：把 session 从 `failed` 重置回 `asr_done`（ASR 数据真实有效）+ recap task 重置 pending + 重启 → attempt 2 成功。
- **注意 `canHandleRecap`（`handler.go:445`）状态守卫**：recap HandleTask 只接受 `asr_done`/`uploaded`/`recap_done`/`published`，**拒绝 `failed`**。所以 recap 失败后不能直接重置 recap task 重跑，必须先把 session 推回 `asr_done`（`failed → EventASRSucceeded → asr_done` 状态机合法）。

#### 为什么没修（2026-08-03）

- **间歇性 + 重试通常成功**：本次仅一次失败，重试即成功，优先级低于本次的 live_record 重连 bug 修复。
- **根因不明**：在拿到 DeepSeek 失败响应的原始 JSON 之前无法定位是 provider 端问题还是代码解析问题。曾尝试加临时诊断日志（`handler.go:691` 记录 `result.Raw`）重跑，但重跑成功未触发，未捕获到失败响应样本。
- **建议治本方向**（待后续）：
  1. 永久保留「recap 失败时记录原始响应」的可观测性改进（`handler.go` 错误分支补 `slog.ErrorContext(..., "raw_response", result.Raw)`），下次失败即捕获样本。
  2. `parseChatCompletionResult`（`provider_openai.go:176`）记录 `finish_reason`，空 content 时结合 finish_reason 判断（`length` = token 截断，`content_filter` = 内容过滤，`stop` = 模型异常）。
  3. 确认 `deepseek-v4-pro` 是否为 DeepSeek 官方有效模型名（官方文档标准名为 `deepseek-chat`/`deepseek-reasoner`）；若非官方名，考虑 `EffectiveModel` 兜底或配置校正。

---

#### 2026-08-13 更新：定位真正根因（非间歇）+ 已修复

8-12、8-13 两场回顾再次失败，深入调查（直接 curl 复现 + 加诊断日志看生产真实响应）**推翻了"间歇性"判断**，真正根因是两个叠加的确定性配置问题：

- **根因① model 被运行时配置覆盖成 flash**：`config.yaml` 原为 `deepseek-v4-pro`，但 `runtime_settings.recap_ai` 覆盖成了 `deepseek-v4-flash`。flash 是 reasoning 模型，面对完整 `defaultSystemPrompt`（90 行约束）+ 长转写时，reasoning 爆炸（16382 token 占满 max_tokens 16384），正文来不及输出 → content 空。
- **根因② max_tokens=16384 对 reasoning 模型不够**：DeepSeek 的 `max_tokens` **限制 reasoning + completion 的总和**（非仅 completion）。pro 对长内容 reasoning 消耗不稳定（实测 1900~6698+ token），当 reasoning 多 + 正文 > 16384 时被截成空 content（DeepSeek 报 `finish_reason=stop` 而非 `length`，是其行为特性）。

**完整实验矩阵**（直接调 DeepSeek API 复现，铁证）：

| model | system prompt | reasoning token | content | 结果 |
|-------|--------------|-----------------|---------|------|
| flash | 完整 default（90 行） | 16382（占满 16384） | 0 字 | ❌ |
| pro | 完整 default（8-12 超长 64K 字符） | 6698 | 10738（总和 17436 > 16384） | ❌ |
| pro | 完整 default（8-13 中等 25K 字符） | 6323 | 9944（总和 16267 < 16384） | ✅ |
| pro/flash | 简化 system | <6000 | 4500-5700 | ✅ |

**为什么 8-3 误判为间歇**：8-2 那场内容较短（pro + 16384 勉强够），重跑成功；当时未看到 finish_reason/reasoning_content（诊断缺陷），无法区分 `length`（确定性 reasoning 耗尽）与 `stop+空`（真·间歇），笼统归为"间歇"。8-3 建议的治本方向 ①②（记录 Raw + finish_reason）在本次实施，正是这次能定位根因的关键。

**已修复（2026-08-13）**：

1. **诊断日志**（`internal/recap/provider_openai.go`）：content 空时记录 `finish_reason` + `reasoning_content` 片段 + 响应长度（原诊断缺陷 + 8-3 建议①②）。下次失败 journald 直接可见根因。
2. **空 content 兜底重试**（`provider_openai.go` 抽公共 `doOpenAIRequestWithRetry`，`Generate` 与 `GenerateWithTools` 共用）：空 content（含纯空白）按 `finish_reason` 区分处理——`finish_reason=length`（token 预算耗尽）或 `content_filter`（内容安全过滤）属确定性失败，**不重试**直接报错并提示对应治本方向；其余（`stop`+空等）做有界重试（共 3 次尝试）兜底真·间歇；HTTP/网络错误不重试。`provider_openai_test.go` 8 个单测覆盖（重试后成功 / 重试耗尽 / HTTP 不重试 / length 不重试 / content_filter 不重试 / 首调成功不重试 / GenerateWithTools 重试 / truncateForLog 边界）。MCP 工具开启时 recap 走 `GenerateWithTools`，同样享有重试（不再硬失败）。
3. **配置**（运维）：`runtime_settings.recap_ai` 的 model 改回 `deepseek-v4-pro`、`max_tokens` 16384→32768（给 reasoning 留空间，已验证 8-12 能出 6101 字）。
4. **救回两场**：8-12（`bili_1298779265_live_20260812_200635`）、8-13（`bili_1298779265_live_20260813_144237`）均已走完 recap→publish→archive 到 `published`。

**finish_reason 诊断口径**（今后排查依据；关键：DeepSeek 的 finish_reason **不能**单独区分确定性与间歇）：
- `finish_reason=length` + 空 content = 确定性 reasoning+completion 超过 max_tokens。provider **不重试**（重试无效），直接报错提示加大 `max_tokens` / 换 model。**注**：DeepSeek 在 max_tokens 耗尽时常报 `stop` 而非 `length`（见上实验矩阵），所以 `length` 出现即确定性铁证，但 `stop` **不代表**"非预算问题"。
- `finish_reason=stop` + 空 content = **歧义**：可能为真·间歇（重试可救），也可能是 max_tokens 不足的确定性失败（DeepSeek 此情形同样报 `stop`）。provider 做有界重试（共 3 次）兜底真·间歇；若重试耗尽仍空，按确定性配置问题排查（检查 model 是否被覆盖、`max_tokens` 是否足够）。
- `finish_reason=content_filter` + 空 content = 内容触发安全过滤，确定性（同输入同结果），provider **不重试**直接报错 → 检查转写内容（换 model 或调整 prompt）。

**运维教训**：直接用 sqlite `json_set()` 改 `runtime_settings.data` 会把列存储类型从 BLOB 改成 TEXT，导致 Go `*json.RawMessage` Scan 失败、服务崩溃循环（`unsupported Scan, storing driver.Value type string into type *json.RawMessage`）。改 runtime_settings 应走 handler API（`PUT /api/config/recap-ai`），或 `json_set` 后必须 `CAST(... AS BLOB)`。

#### 2026-08-16 更新：MCP 工具路径空回顾绕行收口（H4）

provider 层重试只覆盖「模型返回空 content」；**MCP agent loop 耗尽路径**（`mcp/loop.go` maxRounds 用尽后 `return result, nil`，Content 为空且无 error）此前绕过所有守卫直达落盘/发布。2026-08-15 修复（commit `82f1584`，全项目审核 H4）：`recap/handler.go` 生成后补空 content 终判，MCP 路径空回顾不再穿透。ISSUE-007 至此两个入口（provider 空响应、agent loop 耗尽）均已收口。
