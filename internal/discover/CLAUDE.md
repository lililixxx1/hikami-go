[根目录](../../CLAUDE.md) > **internal/discover**

# internal/discover -- B 站回放发现

## 模块职责

遍历所有启用的主播，使用 yt-dlp 发现回放合集中的新视频，按标题前缀过滤，去重后创建场次并排队下载任务。支持来源模式过滤（source_mode）、每次发现数量限制（discover_limit）、发现预览和详细跳过/接受日志。

**两套发现流程并存：**
- **一步式（旧）**：`DiscoverAll` 直接列出 + 建场次 + 入队（一键全部下载）。保留为抽屉「全部下载」快捷按钮的后端调用。
- **两步式（新，2026-07-02）**：`PreviewAll`（第一步预览，不建场次不入队，按主播分组并标注已处理项）→ 前端勾选 → `Execute`（第二步执行，复用预览的 entry 信息建场次+入队，不重跑 yt-dlp）。`Execute` 复用 `session.CreateDownload` 的幂等性去重。

## 入口与启动

- **入口文件**: `discover.go`
- **核心类型**: `Manager`, `YTDLPLister`

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewManager(channels, sessions, workers, lister)` | 创建 Manager |
| `DiscoverAll(ctx)` | 一步式：发现所有主播的回放（跳过 source_mode=live_only 的主播），建场次+入队 |
| `PreviewAll(ctx)` | 两步式·第一步：遍历所有启用且配了 ReplaySourceURL 的主播，预览可发现回放**不建场次、不入队**；为每条标注 `Exists`（是否已建过 download 场次），并按 `discover_limit` 截断新项 |
| `Execute(ctx, items)` | 两步式·第二步：按前端勾选的 `ExecuteItem` 列表批量建 download 场次 + 入队下载；不重跑 yt-dlp，复用 `CreateDownload` 幂等去重 |
| `DiscoverChannel(ctx, channel)` | 发现单个主播的回放（受 discover_limit 限制） |
| `PreviewChannel(ctx, channel)` | 只预览可发现回放，不创建场次和任务 |

**接口：**

```go
type Lister interface {
    List(ctx context.Context, sourceURL string, cookieFile string) ([]Entry, error)
}
```

**API 端点：**
- `POST /api/sessions/discover` — 一步式：发现全部并下载（`DiscoverAll`）
- `POST /api/sessions/discover/preview` — 两步式·第一步：预览（`PreviewAll`），返回 `{items: Result[]}`，每条带 `exists` 标记
- `POST /api/sessions/discover/execute` — 两步式·第二步：执行勾选项（`Execute`），body `{items: ExecuteItem[]}`
- `POST /api/channels/:id/discover/preview` — 单主播预览（`PreviewChannel`）

## 关键依赖与配置

- 外部工具: yt-dlp (`--dump-json --flat-playlist`)
- 依赖: channel.Store (主播列表), session.Store (场次去重), worker.Pool (排队任务)
- 过滤: `channel.TitlePrefix` 非空时按逗号分隔前缀匹配；为空时跳过过滤。**匹配在原始标题上做**（`DiscoverChannel`/`PreviewChannel` 在 `resolveTitle` 之前用 `entry.Title` 匹配），因为 `resolveTitle` 内部调 `CleanReplayTitle` 会去掉 `【直播回放】` 等前缀，清洗后的标题不再匹配前缀（`96b5115`）
- 去重: 通过 `session.CreateDownload` 的唯一约束 `(channel_id, source_type, source_id)` 实现
- 来源模式: `DiscoverAll`/`PreviewAll` 均跳过 `source_mode == "live_only"` 的主播
- 发现限制: `DiscoverChannel` 在 `createdCount >= discover_limit` 时停止创建新场次（0 = 不限制）；`PreviewAll` 完全镜像该语义（按新项计数截断，达限后该频道后续项全丢弃，避免两步流程绕过 limit 一次性下载超配额——codex 审核 P1）
- Cookie: 传递 `channel.DownloadCookieFile` 给 yt-dlp
- **`annotateExists` 辅助**（`PreviewAll` 内部）：按 `channel_id` 分组做一次 `IN` 查询批量标注 `Exists`，避免 N 条结果 N 次查询；标注失败不致命（降级返回不带标记的结果）。频道级失败项也纳入 span 作为不可计数项，避免被静默丢弃（codex 审核 P2）
- 日志: 对 title_prefix 不匹配、discover_limit 达到、创建失败、已存在、新建成功和预览结果输出结构化日志

## 数据模型

**Entry（yt-dlp 条目）：**

| 字段 | 说明 |
|------|------|
| `id` | BV 号 |
| `title` | 视频标题 |
| `url` | 原始 URL |
| `webpage_url` | 网页 URL |

**Result（发现结果）：**

| 字段 | 说明 |
|------|------|
| `channel_id`, `session_id`, `source_id`, `title`, `source_url` | 标识信息 |
| `created` | 是否为新发现 |
| `exists` | 该 source 是否已建过 download 场次（仅 `PreviewAll` 填充）；前端预览阶段据此标记「已处理」项，默认不勾选 |
| `task_id` | 排队的下载任务 ID |
| `error` | 错误信息 |

**ExecuteItem（前端勾选后回传给 `Execute` 的单项）：**

| 字段 | 说明 |
|------|------|
| `channel_id`, `source_id`, `title`, `source_url` | 前端从预览结果里勾选的 entry 信息；不含 created/session_id/task_id（那些由后端返回） |

## 测试与质量

- `discover_test.go`: 16 个测试函数，覆盖 DiscoverAll（建任务/跳过已存在/跳过 live_only）、discover_limit（达限/0=不限）、**PreviewAll**（标注 Exists、尊重 discover_limit、达限时在已存在项后 break）、**Execute**（不重跑 yt-dlp、幂等去重）、title_prefix 匹配（原始标题 before CleanReplayTitle / 空 resolver 结果 guard / limit 前置检查）。

## 相关文件清单

- `discover.go` -- 唯一源文件（DiscoverAll / PreviewAll / Execute / DiscoverChannel / PreviewChannel + `annotateExists` 辅助）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-19 | 功能 | **主播管理 ↔ 回顾管理·回放 解耦**(branch `fix/decouple-recap-replay-2026-07-18`,codex 计划 v1/v2 审核 + 代码审核)。**新增**:`PreviewInput{SourceURL,CookieFile,TitlePrefix,ChannelID}` struct + `Preview(ctx, in)` 方法(不绑定 channel 表的预览,ChannelID 空串时填 `channel.UnassignedID`)+ `previewCore` 私有 helper(不标注 exists,供 Preview 和 PreviewChannel 共享,避免 PreviewAll「逐频道+批量」双重标注 codex r13b MEDIUM #3);`DiscoverAll`/`PreviewAll` 改用 `channels.ListVisible`(过滤占位,三重保险)。**新端点**:`POST /api/sessions/discover/preview-by-url`(body `{url, cookie_file?, title_prefix?}`,供回顾管理·回放页「发现回放」独立 URL 入口使用);旧端点 `/api/sessions/discover/preview`(遍历主播表)保留向后兼容。**测试**:discover 16→20(+4:PreviewUnassigned/PreviewWithExplicitChannelID/PreviewAnnotatesExists/PreviewChannelForwardsToPreview)。 |
| 2026-07-13 | Bug 修复 | **title_prefix 匹配改在原始标题上做**（`96b5115`）：`DiscoverChannel`/`PreviewChannel` 的 title_prefix 匹配从 `resolveTitle` **之后**移到**之前**——在 `resolveTitle`（内部调 `CleanReplayTitle` 去掉 `【直播回放】` 等前缀）之前用 `entry.Title`（原始标题）做前缀匹配，否则清洗后的标题不再匹配前缀导致回放被错误跳过。配套：limit 检查移到 title resolution 之前（避免对将跳过的项做无谓的 resolve）、guard 空 resolver 结果。新增 6 测试（title_prefix 原始标题匹配 / 空 resolver 结果 / limit 前置），测试 10→16。 |
| 2026-07-11 | Bug 修复 | **ISSUE-003 回放标题解析**（`4e96177` + `589aab5`）：回放发现时用 `resolveTitle` 解析真实视频标题替代 yt-dlp flat-playlist 的粗糙标题。v2 修复：limit 检查移到 title resolution 之前（避免对将跳过的项做无谓解析），guard 空 resolver 结果。 |
| 2026-07-02 | 功能 | **两步式预览勾选下载**（`83ef024`）：新增 `PreviewAll`（第一步预览，不建场次不入队，按主播分组+标注已处理项+按 discover_limit 截断）、`Execute`（第二步执行，复用预览 entry 信息建 download 场+入队，不重跑 yt-dlp，复用 CreateDownload 幂等）、`ExecuteItem` 类型、`Result.Exists` 字段、`annotateExists` 批量 IN 查询辅助（codex 审核 P1/P2 修复：limit 语义镜像 + 频道失败项纳入 span）。新增 handler 路由 `POST /api/sessions/discover/preview` + `/execute`。`discover_test.go` 5→10（+PreviewAll/Execute 覆盖）。前端抽屉保留一步式「全部下载」作回退，发现按钮补 yt-dlp 能力守卫 |
| 2026-05-15 | 更新 | 空 title_prefix 时不再过滤标题；DiscoverChannel/PreviewChannel 增加结构化日志，记录 title_prefix_mismatch、discover_limit_reached、create_session_failed、already_exists、accepted 等原因；新增 PreviewChannel 文档 |
| 2026-05-14 | 更新 | DiscoverAll 新增 source_mode 检查（跳过 live_only 主播）；DiscoverChannel 新增 discover_limit 限制（每次最多创建 N 个新场次）；Lister.List 签名新增 cookieFile 参数 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
