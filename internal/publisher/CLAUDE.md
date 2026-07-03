[根目录](../../CLAUDE.md) > **internal/publisher**

# internal/publisher -- B 站专栏发布

## 模块职责

将直播回顾 Markdown 转换为 B 站 Opus 格式，通过 B 站 API 创建草稿或直接发布专栏文章。支持代码块/表格文本保留、封面图上传、Cookie Account 认证、草稿/发布两种模式、per-channel 发布配置合并（Aigc/TimerPubTime/CoverURL/Topics/TopicID）和发布完成通知。内置 B 站 -352 风控自动处理（buvid 指纹注入 + gaia 验证 + WBI 密钥刷新重试）。

## 入口与启动

- **入口文件**: `publisher.go` (Handler), `bilibili_opus.go` (B 站 API 客户端), `cookie.go` (Cookie 重导出), `md2opus.go` (Markdown 转 Opus)
- **任务类型**: `publish`

## 对外接口

### Handler

| 方法 | 说明 |
|------|------|
| `NewHandler(cfg, sessions, states, channels, client...)` | 创建 Handler |
| `CreateTask(ctx, pool, sessionID)` | 校验前置条件并创建任务 |
| `Register(pool)` | 注册 publish 任务处理器 |
| `SetCookieAccountStore(store)` | 注入 CookieAccountStore，发布 Cookie 可走账号池解析 |
| `SetNotifyManager(m)` | 注入通知管理器，完成后发送 `publish_done` |
| `SetOnSuccess(fn)` | 注册发布成功后的回调（范本：asr/recap 的 SetOnSuccess）。`cmd/hikami` 用它在 published 后按 `archive.auto_after_publish` 决定是否自动归档 |

### OpusClient 接口

```go
type OpusClient interface {
    SaveDraft(ctx, cookie, req) (draftID, error)
    PublishOpus(ctx, cookie, req) (dynID, error)
    DeleteDraft(ctx, cookie, draftID) error
}

type OpusCoverUploader interface {
    UploadCover(ctx, cookie, imagePath) (coverURL, error)
}
```

### BiliOpusClient 实现

| 方法 | 说明 |
|------|------|
| `SaveDraft` | 调用 `x/dynamic/feed/article/draft/add` 创建草稿 |
| `PublishOpus` | 调用 `x/dynamic/feed/create/opus` 发布专栏 |
| `DeleteDraft` | 调用 `x/dynamic/feed/article/draft/del` 删除草稿 |
| `UploadCover` | 调用 `x/dynamic/feed/draw/upload_bfs` 上传封面图（form 字段 `file_up`，响应 `data.image_url`） |

### Cookie 管理

| 函数 | 说明 |
|------|------|
| `LoadCookie(path)` | 从 Netscape 格式 cookie 文件加载（重导出自 `internal/biliutil`） |
| `BiliCookie.CookieHeader()` | 生成 Cookie 请求头（重导出自 `internal/biliutil`） |

### Markdown 转 Opus

| 函数 | 说明 |
|------|------|
| `ConvertMarkdownToOpus(md)` | 将 Markdown 转换为 `[]OpusParagraph` |

**API 端点：** `POST /api/sessions/:sid/publish`

**前置条件：**
- 场次状态为 `recap_done` 或 `uploaded`
- `local_available=true`（上传 `all` 策略清理本地目录后置 `false`，需先 `Fetch` 取回）
- `recap/` 目录存在且包含 `.md` 文件
- 主播配置了可用发布 Cookie：Cookie Account 默认/覆盖账号，或旧版 `cookie_file`
- 主播 `publish_enabled` 或全局 `publish.enabled` 为 true
- Cookie 未过期
- 不能有同场次的活跃发布任务

## 关键依赖与配置

- `publish.enabled`: 是否启用发布能力
- `publish.mode`: `draft`（仅保存草稿）或 `publish`（自动发布）
- `publish.category_id`: B 站分区 ID（默认 15 = 生活->其他）
- `publish.list_id`: 文集 ID（0 = 不加入文集）
- `publish.private_pub`: 2=公开, 1=仅自己可见
- `publish.summary_len`: 摘要截取字数（默认 100）
- `publish.aigc`: AI 辅助创作声明（0=未声明，默认 0）
- `publish.timer_pub_time`: 定时发布 Unix 时间戳（0=不定时）
- `publish.topic_id`: 话题 ID
- `publish.topic_name`: 话题名称
- 主播 `cookie_file`: Netscape 格式 cookie 文件路径
- Cookie Account: `ResolveCookie(..., "publish", ch.CookieFile)` 优先默认发布账号，再回退旧版 `cookie_file`

### ResolvedPublishConfig（发布配置合并）

`resolvePublishConfig` 将主播级配置与全局 `PublishConfig` 合并：

| 字段 | 合并策略 |
|------|----------|
| `Mode` | 空字符串时使用全局 |
| `CategoryID` | 0 时使用全局 |
| `ListID` | -1 时使用全局 |
| `PrivatePub` | 0 时使用全局 |
| `Original` | -1 时默认 0（原创时 reproduced=0） |
| `Aigc` | -1 时使用全局 |
| `TimerPubTime` | 0 时使用全局 |
| `CoverURL` | 直接使用主播配置 |
| `Topics` | 直接使用主播配置 |
| `TopicID` | 全局透传 |
| `TopicName` | 全局透传 |
| `CloseComment` | 全局透传 |
| `UpChooseComment` | 全局透传 |

## 数据模型

**DraftRequest 结构体：**

| 字段 | 说明 |
|------|------|
| `Title` | 文章标题 |
| `Paragraphs` | Opus 段落数组 |
| `Summary` | 摘要 |
| `CategoryID` | 分区 ID |
| `ListID` | 文集 ID |
| `PrivatePub` | 可见性 |
| `Original` | 原创声明 |
| `CoverURL` | 封面图 URL |
| `Aigc` | AI 辅助创作声明 |
| `TimerPubTime` | 定时发布时间戳 |

**PublishRequest 结构体：**

| 字段 | 说明 |
|------|------|
| `Title` | 文章标题 |
| `Paragraphs` | Opus 段落数组 |
| `CategoryID` | 分区 ID |
| `ListID` | 文集 ID |
| `PrivatePub` | 可见性 |
| `Originality` | 原创声明 |
| `Reproduced` | 转载标记（原创时=0） |
| `DraftID` | 草稿 ID |
| `Mid` | 用户 ID |
| `CoverURL` | 封面图 URL（写入 `opus_req.opus.article.cover`） |
| `Aigc` | AI 辅助创作声明 |
| `TopicID` | 话题 ID（写入 `opus_req.topic.id`） |
| `TopicName` | 话题名称（写入 `opus_req.topic.name`） |
| `Tags` | 标签（**保留字段，不再写入请求**——Opus 专栏无标签输入） |
| `TimerPubTime` | 定时发布时间（Unix 秒，写入 `opus.pub_info`+`option` 两处） |

**OpusParagraph（B 站专栏段落，扁平结构，对齐当前 opus 编辑器真实格式）：**

所有文字内容统一放在 `text.nodes`，用 `para_type` + `format` 内嵌字段区分类型，连续同类型段落（列表/引用）用 `format.combine_hash` 关联——**不使用嵌套 children 容器**。每个内容段落前插入一个空段落（para_type=1,nodes:[]）作分隔符。

| 字段 | 说明 |
|------|------|
| `para_type` | 1=文本, 3=分割线, 4=引用, 6=列表, 9=标题 |
| `format` | `{indent, heading_type?, list_format?, combine_hash?}` 按类型按需填充 |
| `format.heading_type` | 标题(para_type=9)级别：2=H2, 3=H3 |
| `format.list_format` | 列表(para_type=6)：`{level, order, theme}`，theme 为 dot/arabic_num |
| `format.combine_hash` | 连续列表/引用段落的关联键（同组共享同一值） |
| `text` | 文字节点数组（所有类型的内容都放这里） |
| `line` | para_type=3 分割线（line_type=1，不带 format/align） |

**OpusNode：** `node_type` 必须为整数 `1`（字段名 `node_type`，非字符串 `type`）。经抓包 B站官方编辑器 draft/add 真实请求确认：字符串 `type:"TEXT_NODE_TYPE_WORD"` 会被服务端 `code:0` 接受但 content 字段存储为空（正文空白），改用整数 `node_type:1` 后正文正常存储。

> 历史 bug（2026-06-19 彻底修复）：曾误用 para_type 8(标题)/5(列表) + 嵌套 heading.nodes/list.children/blockquote.children 容器结构，导致草稿正文能存但渲染时块级元素（标题/列表/引用/分割线）边界被误判成含"1"的代码块。经抓包官方编辑器 draft/add 真实请求确认当前正确格式为扁平结构：para_type 9(标题)/6(列表)，内容统一放 text.nodes，用 format.heading_type/list_format/combine_hash 区分。每个内容段前加空段落（para_type=1,nodes:[]）对齐编辑器分隔行为。

**任务流程：**

1. 加载主播 Cookie
2. 查找 recap 目录下最新的 `.md` 文件（排除 `.prompt.md`）
3. `ConvertMarkdownToOpus` 转换为 Opus 段落
4. 提取标题和摘要
5. `resolvePublishConfig` 合并主播级和全局发布配置
6. 解析封面来源（`resolveCoverUpload`，`f5594a6`）：优先级 **recap 目录 `cover.*` > 配置 `cover_url`**。recap cover 存在时仅上传它（不对配置本地路径做无用上传）；否则 fallback 到配置 `cover_url`——已是 `http(s)://`（或协议相对 `//host`）URL 原样/规范化后用，否则视为本地文件路径走 `UploadCover` 上传换真实 URL，失败/不支持上传时记 warn 并置空（避免本地路径残留进请求）
7. 构建 `DraftRequest`（含 Aigc/TimerPubTime/CoverURL）
8. 调用 `SaveDraft` 创建草稿
9. 若 `mode=publish`：构建 `PublishRequest`（含 Aigc/TimerPubTime/CoverURL/Topic），调用 `PublishOpus` 发布
10. 提交 `publish_succeeded` 事件
11. 记录 `publish_target`（`draft:{id}` 或 `dyn_id`）
12. 发送 `notify.EventPublishDone`

**Markdown 到 Opus 格式（段落类型映射：H2/H3→para_type=8 heading，引用→para_type=4 blockquote，列表→para_type=5 list 容器，分割线→para_type=3，文本/表格→para_type=1）：**
- 连续列表项合并为单个 para_type=5 容器段落（list.children），而非每项独立段落（对齐官方结构）。
- 引用内容封装在 blockquote.children 子段落中（非直接放 text.nodes）。
- 代码块边界（```）不输出，代码内容按普通文本段落保留。
- Markdown 表格行通过 `formatTableRow` 用 ` | ` 连接所有单元格输出；分隔行（`|---|---|`）跳过；表头行（下一行是分隔行）整行加粗输出。
- `---` 作为普通分割线（`makeHR`）输出，**不**作为装饰块边界吞掉后续内容。
- H1 跳过；H2/H3、引用、有序/无序列表、行内加粗、行内代码去除、链接去除、图片去除按既有规则转换。

**B 站 API 请求体字段：**

- SaveDraft: `reprint` 字段通过 `map[int]int{0: 1, 1: 0}[req.Original]` 映射（原创时 reprint=0）
- SaveDraft: `opus.attachments.is_aigc` 传递 Aigc 声明
- SaveDraft: `timer_pub_time` 传递定时发布时间
- SaveDraft: `image_urls`（字符串数组 `["http://..."]`）传递封面图，仅 CoverURL 非空时写入（2026-06-22 抓包确认草稿端字段结构）
- PublishOpus: `option.aigc` 传递 Aigc 声明
- PublishOpus: `opus_req.topic = {id, name}` 对象传递话题（opus_req 顶层，非 option.topic_id），仅 TopicID≠0 时写入（2026-06-22 抓包确认发布端字段结构）
- PublishOpus: `opus_req.opus.article.cover = [{url: "..."}]` 对象数组传递封面图，仅 CoverURL 非空时写入
- PublishOpus: `timer_pub_time` 定时发布时间(Unix 秒)需在 `opus.pub_info` 和 `option` 两处冗余写入，仅 TimerPubTime>0(定时)时写，立即发布(==0)不写(2026-06-22 抓包确认)

> **重要（2026-06-22 抓包确认）：** 话题(topic)在 B 站是「发布时绑定」模型——草稿端 `draft/add` 根本没有 topic 字段。故纯草稿模式(mode=draft)下话题永不生效，这是 B 站限制不是 bug；只有发布模式(mode=publish)经 `opus_req.topic` 才能让话题生效。此外 Opus 专栏编辑器无「标签(tag)」输入框，`DraftRequest.Tags`/`PublishRequest.Tags` 字段保留以维持调用方兼容但不再写入请求（历史 arg.tags/option.tags 为死字段）。

**B 站 API 错误映射：**

| 错误码 | 错误类型 |
|--------|----------|
| -101 | `ErrCookieExpired` |
| -403 | `ErrContentRejected` |
| -509 | `ErrRateLimited` |
| 其他 | `ErrBilibiliAPI` |

### -352 风控自动处理

B 站专栏发布 API（`SaveDraft`/`PublishOpus`）可能在请求触发风控时返回 `code=-352`。`BiliOpusClient` 内置两级风控应对策略，确保发布链路在风控触发时自动恢复：

**1. buvid 指纹注入（所有请求前置）**

- `getBuvids(ctx, cookieHeader)`：调用 B 站指纹接口获取 `buvid3` 和 `buvid4`，按 `cookieHeader` 为 key 缓存 24 小时（`buvidCache` + `buvidCacheMu` 互斥锁保护）。
- `injectBuvids(cookieHeader, buvid3, buvid4)`：将 buvid 追加到 Cookie 请求头尾部。
- 指纹获取失败时仅打印 warn 日志，不阻断主流程（继续使用无 buvid 的 Cookie）。

**2. `doRequestWithGaia` 双重重试（Opus 发布专用）**

`SaveDraft` 和 `PublishOpus` 走 `doRequestWithGaia`，`DeleteDraft` 走 `doRequest`（仅 WBI 刷新重试，无 gaia）。

| 策略 | 触发条件 | 动作 |
|------|----------|------|
| gaia 验证重试 | `code=-352` 且响应携带 `gaia_voucher` | `performGaiaVerification` 执行两步验证（register + validate），成功后用 token 重试一次 |
| WBI 密钥刷新重试 | `code=-352` 且 `urlSigner` 实现了 `RefreshKeys() error` | 强制刷新 WBI 密钥后重试一次 |

**`performGaiaVerification` 两步验证流程：**

1. **register**：`POST x/gaia-vgate/v2/register`，body 含 `v_voucher`、`dm_track`（固定 WebGL/ANGLE 指纹 JSON）、`csrf`（BiliJct），获取验证会话。
2. **validate**：`POST x/gaia-vgate/v2/validate`，body 仅含 `csrf`，模拟点击完成验证。
3. 验证成功后返回 voucher 复用作为 token（简化处理）。

任一步 `code != 0` 返回 `mapBiliError(-352)`。gaia 验证失败时不阻断，回退到上层错误映射。

**请求头伪装：** 所有 B 站 API 请求携带完整浏览器风格 Header（Referer/Origin/Sec-Fetch-*/Accept-Language），`User-Agent` 走 `biliutil.BiliUserAgent`。

**业务错误：**

| 错误 | 说明 |
|------|------|
| `ErrSessionNotReady` | 场次状态不在 recap_done/uploaded |
| `ErrRecapMissing` | recap 目录或 .md 文件不存在 |
| `ErrChannelNoCookieFile` | 主播未配置 cookie_file |
| `ErrPublishNotEnabled` | 主播和全局均未启用发布 |

## 测试与质量

- `md2opus_test.go`: 21 个测试用例，覆盖纯文本、加粗、H2/H3、引用、分割线、有序/无序列表、空行跳过、H1 跳过、代码块内容保留、代码块带语言标记、表格文本保留（` | ` 连接）、复杂表格、HR 不吞内容（`TestHRDoesNotSwallowContent`）、行内代码去除、链接去除、图片去除、混合内容、JSON 序列化、段落格式（对齐当前编辑器 para_type 9 标题/6 列表/扁平 text.nodes）。

- `bilibili_opus_test.go`: 11 个测试函数，覆盖 mapBiliError 错误映射（保留原始 message）、SaveDraft/PublishOpus/DeleteDraft/UploadCover HTTP 交互及字段结构子用例（RemoveOpus 接口已移除，本系统不删 B站专栏）。

- `publisher_test.go`: 29 个顶级测试函数（含 t.Run 子测试），覆盖：
  - resolvePublishConfig: 主播覆盖全局、回退全局默认、Original -1 处理、Aigc -1 处理、ListID -1 处理、TopicID 全局透传、零 TopicID 有效、ChannelOverride 完整验证、Fallback 完整验证、OriginalMinusOne
  - extractTitle: H1 提取、带空格、H2 跳过、无标题、空字符串、非首行标题
  - extractSummary: 正常文本、跳过标题/特殊行、长文本截断、空内容、跳过代码围栏、跳过表格行
  - findRecapMarkdown: 查找最新 md、跳过 prompt.md、无 md 返回错误、目录不存在
  - findCoverImage: cover.png/jpg/jpeg、png 优先、无封面、空目录
  - publishRecap: 共用发布逻辑（HandleTask 调用）
  - HandleTask 集成测试（含完整 testHelper 框架、fake 依赖注入、SQLite 内存数据库）：
    - HandleTask_Success: 成功保存草稿、验证 Title/CategoryID/PublishTarget/Status
    - HandleTask_NoRecapMarkdown: 回顾文件缺失返回错误
    - HandleTask_NoCookie: 无 Cookie 返回 ErrChannelNoCookieFile
    - HandleTask_SaveDraftMode: 草稿模式不调用 PublishOpus
    - HandleTask_PublishMode: 发布模式调用 PublishOpus
    - HandleTask_PublishFail: 发布失败返回 ErrContentRejected
    - HandleTask_SaveDraftFail: 草稿保存失败返回 ErrCookieExpired
    - HandleTask_ContextCancelled: 上下文取消返回错误
    - HandleTask_CoverImage: 封面图上传流程
    - HandleTask_NoTitleFallback: 无 H1 标题回退到 session title
    - HandleTask_CookieAccountStore: 通过 CookieAccountStore 解析 Cookie
    - HandleTask_NotifyManager: 发布完成后发送通知
    - HandleTask_InvalidStatus: 非有效前置状态返回错误
    - HandleTask_UploadedStatus: 从 uploaded 状态成功发布

- `publish_target_test.go`: 2 个测试用例，覆盖 `PublishTarget.Marshal`（JSON 结构序列化）与 `ParsePublishTarget`（兼容旧裸 dyn_id/`draft:id` 格式）。

## 相关文件清单

- `publisher.go` -- Handler 实现、任务流程编排、ResolvedPublishConfig、resolvePublishConfig、封面来源解析（`resolveCoverUpload`/`webCoverURL`）、`findCoverImage`
- `bilibili_opus.go` -- BiliOpusClient 实现、B 站 API 交互、错误映射、OpusCoverUploader + UploadCover、-352 风控处理（doRequestWithGaia/doRequest/getBuvids/injectBuvids/performGaiaVerification）、buvid 24h 缓存
- `cookie.go` -- 重导出 `internal/biliutil` 的 Cookie 类型（保持对外接口兼容）
- `md2opus.go` -- Markdown 到 Opus 格式转换器（含 `formatTableRow`、`isTableSeparator`、`isHR`、`parseTableCells`、`makeHR`、`parseInlineBold` 等 helpers）
- `md2opus_test.go` -- 转换器单元测试（21 个用例）
- `bilibili_opus_test.go` -- B 站 API 交互测试（11 个用例）
- `publisher_test.go` -- HandleTask 集成测试与配置合并测试（34 个用例）
- `publish_target_test.go` -- PublishTarget 序列化/反序列化测试（2 个用例）

## 变更记录 (Changelog)

