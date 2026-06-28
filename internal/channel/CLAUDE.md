[根目录](../../CLAUDE.md) > **internal/channel**

# internal/channel -- 主播 CRUD 与 B 站识别

## 模块职责

管理主播配置的增删改查，提供 B 站主播识别功能（通过直播间 URL、空间 URL、UID 等输入），以及首次引导导入。主播配置以 SQLite 为运行时主来源。识别过程支持使用主播的下载 Cookie 访问 B 站 API。支持 per-channel 发布配置（分区、文集、可见性、原创、Aigc、定时发布、封面、话题）。支持来源模式（source_mode）和发现限制（discover_limit）。支持 per-channel 回顾配置（recap_model、max_continuations、`auto_recap` 三态开关）。

## 入口与启动

- **入口文件**: `channel.go`, `identify.go`
- **核心类型**: `Store`, `Identifier`

## 对外接口

### Store（数据库操作）

| 方法 | 说明 |
|------|------|
| `NewStore(db)` | 创建 Store 实例 |
| `Bootstrap(ctx, channels)` | 首次引导：数据库为空时导入 YAML 配置 |
| `List(ctx)` | 列出所有主播 |
| `Get(ctx, id)` | 按 ID 获取主播 |
| `Create(ctx, input)` | 创建主播（重复 ID 返回 ErrDuplicate） |
| `Update(ctx, id, input)` | 更新主播 |
| `Delete(ctx, id)` | 删除主播（有关联场次时返回 ErrInUse） |
| `SaveIdentified(ctx, input)` | 识别后幂等保存：不存在则创建，已存在则更新 |
| `UpdateCookieFile(ctx, id, usage, cookiePath)` | 更新主播 Cookie 文件路径（download 或 publish） |

### Identifier（B 站识别）

| 方法 | 说明 |
|------|------|
| `Identify(ctx, input)` | 根据 UID/直播间 URL/空间 URL/数字识别主播 |
| 支持输入格式 | 直播间 URL、空间 URL、纯 UID 数字 |

**API 端点：**
- `POST /api/channels/identify` -- 识别
- `POST /api/channels/identify/save` -- 识别并保存
- `CRUD`: `GET/POST/PUT/DELETE /api/channels[/:id]`

## 关键依赖与配置

- 数据库: `*sql.DB`
- B 站 API: `api.live.bilibili.com/xlive/web-room/v1/index/getInfoByRoom`
- Cookie: 通过 `internal/biliutil` 加载主播的 `download_cookie_file`
- ID 规则: `id` 不允许包含路径分隔符 `/` 和 `\`

## 数据模型

**Channel 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string PK | 主播标识，用于路径和外键 |
| `name` | string | 显示名称 |
| `uid` | int64 | B 站 UID |
| `live_room_id` | int64 | 直播间 ID（0 表示禁用录制） |
| `replay_source_url` | string | 回放合集地址 |
| `space_url` | string | 空间视频页地址 |
| `title_prefix` | string | 回放标题过滤前缀 |
| `cookie_file` | string | 主播专用发布 Cookie 路径 |
| `download_cookie_file` | string | 主播专用下载 Cookie 路径 |
| `enabled` | bool | 是否启用 |
| `auto_record` | bool | 检测到开播后是否自动开始录制（默认 true） |
| `auto_asr` | bool | 录播完成后是否自动提交 ASR 转写（默认 false） |
| `auto_recap` | bool | 标准化/ASR 完成后是否自动提交回顾（默认 true）。`UpsertInput.AutoRecap` 为 `*bool` 三态：显式 true/false 直接采用，`nil` 时经 `resolveAutoRecap(nil, true)` 兜底为 true |
| `record_danmaku` | bool | 录制直播时是否同时采集弹幕（默认 true） |
| `source_mode` | string | 来源模式：`both`（默认）/`live_only`/`replay_only`/`live_first`/`replay_first` |
| `discover_limit` | int | 每次回放发现最大新建场次数（0 = 不限制，默认 0） |
| `publish_enabled` | bool | 是否启用 per-channel 发布（默认 false） |
| `publish_mode` | string | 发布模式：`draft`（仅保存草稿）或 `publish`（直接发布），空=跟随全局 |
| `publish_category_id` | int | B 站分区 ID（0=跟随全局） |
| `publish_list_id` | int | 文集 ID（-1=跟随全局，0=无文集） |
| `publish_private_pub` | int | 可见性：2=公开, 1=仅自己可见（0=跟随全局） |
| `publish_original` | int | 原创声明：0=非原创, 1=原创（-1=跟随全局） |
| `auto_publish` | bool | 回顾完成后是否自动发布（默认 false） |
| `publish_aigc` | int | AI 辅助创作声明：0=未声明, 1=声明（-1=跟随全局） |
| `publish_timer_pub_time` | int64 | 定时发布 Unix 时间戳（0=不定时） |
| `publish_cover_url` | string | 自定义封面图片路径或 URL |
| `publish_topics` | string | 话题标签（逗号分隔） |
| `recap_model` | string | Per-channel 回顾模型覆盖（空=跟随全局） |
| `max_continuations` | int | Per-channel 回顾续写次数覆盖（-1=跟随全局） |

**发布配置合并策略（mergeIdentified）：**
- 识别并保存时，发布相关字段保留已有主播配置，不覆盖。

**CookieUsage 类型：**
- `CookieUsageDownload` = "download"
- `CookieUsagePublish` = "publish"

## 识别 Cookie 查找策略

识别时查找下载 Cookie 的优先级：
1. 数据库中主播的 `download_cookie_file`（匹配 UID 或直播间 ID）
2. 数据库中任意主播的 `download_cookie_file`（兜底）
3. Bootstrap 配置中匹配的主播 `download_cookie_file`
4. Bootstrap 配置中首个有 `download_cookie_file` 的主播（兜底）

## 测试与质量

- `channel_test.go`: 54 个测试用例，覆盖：
  - Store CRUD: Create（成功、重复、校验-无 ID/无 Name/无效 UID/路径分隔符/负 RoomID）、Get（成功/未找到）、List（空/排序）、Update（成功/未找到/校验）、Delete（成功/未找到/关联场次）、SaveIdentified（新建/已存在/保留 TitlePrefix/CookieFile/Enabled/PublishFields）、**auto_recap 三态解析（resolveAutoRecap：nil→true、显式值、UpsertInput 持久化）**
  - Bootstrap: 空表导入、非空表跳过、空列表、校验
  - identify.go: normalizeIdentifyInput（10 种输入格式）、parseLiveURL、parseSpaceURL、Identify（直播间/UID/缺失）、mergeIdentified（合并策略）、boolToInt
- `identify_test.go`: 5 个测试用例，覆盖：
  - `normalizeIdentifyInput`: 直播间 URL、空间 URL、UID 数字解析
  - `IdentifyByLiveRoom`: 通过直播间 ID 识别
  - `IdentifyByUIDLooksUpLiveRoom`: UID 反查直播间
  - `IdentifyUsesConfiguredDownloadCookie`: 使用已配置主播的下载 Cookie
  - `IdentifyFallsBackToBootstrapDownloadCookie`: 回退到 Bootstrap Cookie

## 常见问题 (FAQ)

**Q: Bootstrap 什么时候执行？**
A: 只在 `channels` 表为空时执行。已有数据不会覆盖。

**Q: source_mode 如何影响行为？**
A: `discover.go` 的 `DiscoverAll` 检查 `source_mode == "live_only"` 跳过回放发现。`live_record` 模块可按 `replay_only` 跳过直播录制。`both`（默认）两者都执行。

**Q: discover_limit 如何工作？**
A: 每次发现新回放时，只创建前 `discover_limit` 个新场次（0 = 不限制）。已存在的场次不受影响。

**Q: recap_model 和 max_continuations 如何使用？**
A: `recap_model` 非空时覆盖全局 `recap_ai.model` 配置。`max_continuations >= 0` 时覆盖全局 `recap_ai.max_continuations`。`-1` 表示跟随全局。

## 相关文件清单

- `channel.go` -- Store 实现、SQL 常量（含 28 列 selectColumns/createSQL/updateSQL）、校验、UpdateCookieFile、`resolveAutoRecap(*bool, fallback)` 三态解析
- `identify.go` -- Identifier 实现、B 站 API 交互、URL 解析、Cookie 查找
- `channel_test.go` -- Store 单元测试（54 个用例）
- `identify_test.go` -- 识别单元测试（5 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-23 | 功能 | 自动触发链加固：Channel/UpssertInput 新增 `auto_recap` 字段（`Channel` 为 `bool` 默认 true，`UpsertInput` 为 `*bool` 三态）；新增 `resolveAutoRecap(*bool, fallback)` 助手（nil→fallback=true）；Create/Update/Bootstrap 持久化 `auto_recap`（`boolToInt`）。channel_test.go 49→54（+5 覆盖三态解析/持久化） |
| 2026-05-23 | 更新 | Channel/UpsertInput 新增 recap_model（默认 ''）和 max_continuations（默认 -1）字段；selectColumns/createSQL/updateSQL 扩展至 28 列 |
| 2026-05-14 | 更新 | Channel/UpsertInput 新增 source_mode（默认 'both'）和 discover_limit（默认 0）字段；selectColumns/createSQL/updateSQL 扩展至 26 列；mergeIdentified 保留 SourceMode/DiscoverLimit；Create/Update 默认 source_mode='both'；Bootstrap 传递 SourceMode/DiscoverLimit；新增 UpdateCookieFile 方法；新增 CookieUsage 类型和常量 |
| 2026-05-12 | 更新 | channel_test.go 测试用例计数更新为 49 |
| 2026-05-08 | 重大更新 | Channel 新增 per-channel 发布配置字段（11 个） |
| 2026-05-04 | 重大更新 | 新增 channel_test.go（48 个测试用例） |
| 2026-05-03 | 更新 | 新增 `auto_record` 和 `auto_asr` 字段 |
| 2026-05-02 | 更新 | 新增 `download_cookie_file` 字段、识别 Cookie 查找策略 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
