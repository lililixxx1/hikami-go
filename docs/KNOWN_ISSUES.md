# 已知问题（Known Issues）

> 本文件收集已发现但尚未修复的问题。每条记录发现日期、严重程度、根因、影响、建议修复方案。
> 修复完成后将对应条目移至「已修复」小节并标注修复日期。
> 最后更新：2026-07-17

---

## 待修复

> （暂无）

---

## 已修复

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
