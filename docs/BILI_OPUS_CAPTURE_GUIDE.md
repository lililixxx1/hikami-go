# B 站专栏草稿/发布 请求体抓包指引

> **用途**:为 hikami 的专栏发布功能采集 B 站官方编辑器的**真实请求体**,用于修复两个已知 bug:
> 1. 自定义封面 `CoverURL` 在 `SaveDraft` 请求体里完全没被使用(死字段,封面失效)
> 2. 话题 `TopicID` 草稿模式漏传(已在 hikami 侧修好,但需确认官方字段名是否一致)
>
> **执行者**:你(用户)在本地的 AI 编码助手,配合 `chrome-devtools-mcp` 打开本地真实 Chrome(已登录 B 站)抓包。
> **产出**:本指南末尾「交付物」章节列出的 JSON 片段,回贴给我即可。
> **预计耗时**:10–15 分钟。

---

## 一、为什么需要这次抓包

hikami 的 `internal/publisher/bilibili_opus.go` 构造 B 站专栏请求体时:

| 字段 | 现状 | 问题 |
|------|------|------|
| 封面图 URL | `DraftRequest.CoverURL` 字段存在,但 `SaveDraft` 函数体里**从没被写进请求 JSON** | 用户设的封面、recap 目录自动上传的封面,全部被丢弃 |
| 话题 | 草稿端用了 `arg.topic_id`,发布端用了 `option.topic_id` | 需确认官方真实字段名/层级是否一致 |

历史上的 `banner_url` 字段(2026-05-08 commit `f5c5ff8`)经实测对 Opus 类型专栏**无效**后被删除,但删完没补替代字段。我搜遍了项目文档和 git 全历史,**没有任何地方记录过封面字段的正确名字**——所以必须抓一次官方真实请求来对齐。

`bilibili-API-collect` 社区文档对 `draft/add` 的 `arg` 内层结构没有权威定义,只能靠抓包。

---

## 二、环境要求

1. **本地真实 Chrome**(非 headless),且**已登录 B 站**(能看到创作中心)。
2. 本地 AI 助手挂载了 `chrome-devtools-mcp`(或等价的浏览器控制 MCP / Playwright)。
3. 抓包过程**必须用你本人的账号操作**,我不接触任何 cookie。

> ⚠️ 不要用早期的 headless Chrome 方案——那个卡在 cookie 注入。这次用你本地真实登录的 Chrome,绕开所有鉴权问题。

---

## 三、抓包任务清单(共 3 个请求)

需要抓 **3 个不同操作的请求体**。每个操作都按「操作步骤 → 抓什么 → 怎么标记」执行。

### 任务 A:草稿端 —— 带封面、带话题、带标签

**目标接口**: `POST https://api.bilibili.com/x/dynamic/feed/article/draft/add?csrf=...`

这是**最重要的一个**,封面字段的主要来源。

**操作步骤**:

1. 在已登录的 Chrome 里打开专栏编辑器:
   `https://member.bilibili.com/platform/upload/text/edit`
2. 填入以下内容(**每项都要填,且用特征值,方便后续在 JSON 里定位**):
   - **标题**: `hikami抓包测试草稿A` ← 固定这个,方便搜索
   - **正文**: 随便打一行字,如 `测试正文A`
   - **封面**: 点「更换封面/上传封面」,**上传一张任意图片**(或填一个图片 URL,取决于编辑器入口)。关键是要让封面字段非空。
   - **话题**: 在话题选择器里**搜并选中一个真实话题**(任意,比如「游戏」)。**不要手打标签当话题**,要用官方的话题搜索选择。
   - **标签**: 如果编辑器有单独的「标签」输入框,填 `hikami_test_tag_A`(逗号分隔多个也行)。
   - **AI 声明 / 原创声明 / 可见性**: 随便开一两个,让它们的字段也出现在请求里,顺便核对字段名。
3. **打开 F12 / DevTools 的 Network 面板**(或让 MCP 直接挂 Network 监听),清空记录。
4. **点「保存草稿」按钮**(不是发布)。
5. Network 里会捕获到一个发往 `draft/add` 的 POST 请求。

**要抓的内容**:
- **请求 URL**(含 query string,确认 `csrf` 之外的参数)
- **请求头**(Request Headers)—— 全部,特别是 `Cookie` 之外的非敏感头(如 `Origin`、`Referer`、`User-Agent`、`Content-Type`)。**Cookie 头可以删掉**,我不需要。
- **请求体**(Request Payload / post data)—— **完整 JSON,原样贴出**。这是最关键的。

**怎么标记交付**:把这次的内容放到一个文件里,文件名 `capture_A_draft.json`,结构见「交付物」。

---

### 任务 B:发布端 —— 完整发布一篇

**目标接口**: `POST https://api.bilibili.com/x/dynamic/feed/create/opus?csrf=...&gaia_source=...`

发布端和草稿端**用的是完全不同的端点和请求体结构**(hikami 代码里也是两套)。两个都要抓,才能各自对齐。

**操作步骤**:

1. 同样在专栏编辑器里,**新建一篇**(或用任务 A 保存的那个草稿继续编辑)。
2. 填入特征值:
   - **标题**: `hikami抓包测试发布B`
   - **正文**: `测试正文B`
   - **封面**: 上传/设置一张图片
   - **话题**: 选一个真实话题
3. 清空 Network 面板。
4. **点「发布」按钮**(立即发布,不要定时)。如果担心公开发布,发布后可以立刻去 B 站删掉这篇动态——**请求体已经抓到了,删除不影响**。
5. Network 里捕获发往 `create/opus` 的 POST 请求。

**要抓的内容**:同任务 A(URL、请求头非敏感部分、完整请求体 JSON)。

**怎么标记交付**:文件名 `capture_B_publish.json`。

---

### 任务 C(可选,辅助):封面上传接口

**目标接口**: `POST https://api.bilibili.com/x/article/creative/article/upcover?csrf=...`

这个接口 hikami 已经实现了(`UploadCover` 方法),目的是把本地图片上传得到一个 URL,再把这个 URL 放进 `draft/add` 的封面字段。抓它是为了**确认上传后返回的 URL 格式**,以及确认上传接口有没有变。

**操作步骤**:

1. 编辑器里点「上传封面」,选一张本地图片。
2. 在图片上传的瞬间,Network 里会捕获一个 `upcover`(或类似 `cover`/`upload`)的 multipart 请求。
3. 抓这个请求的**响应体**(Response),里面会有 `image_url` 或 `url` 字段,就是封面 URL。

**要抓的内容**:
- 请求 URL(确认端点没变)
- **响应体 JSON**(含返回的封面 URL 字段名)

**怎么标记交付**:文件名 `capture_C_cover_upload.json`。

> 任务 C 是可选的——如果上传瞬间抓不到、或接口名对不上,跳过即可,主要靠任务 A/B 的请求体里**封面字段填的是什么值**来反推。

---

## 四、操作要点与坑

1. **特征值很重要**:标题固定用 `hikami抓包测试草稿A` / `hikami抓包测试发布B`,这样你在 Network 的巨量请求里能一眼定位,我也好对应。封面/话题/标签**一定要填非空**,空值字段经常不出现在请求体里,等于白抓。

2. **话题 ≠ 标签**:
   - **话题(topic)**:编辑器里通常带 `#` 号、有下拉搜索联想、选中后是个「胶囊」样式的 chip。对应字段一般是 `topic_id` + `topic_name`。
   - **标签(tag)**:通常是纯文本输入框,逗号分隔。对应字段一般是 `tags`。
   - 两个都要填,我才能区分官方到底怎么命名。

3. **请求体里的封面值**:
   - 如果是上传图片,封面字段里会是类似 `//i0.hdslb.com/...` 或 `https://i0.hdslb.com/...` 的 URL。
   - 如果编辑器支持填 URL,就是那个 URL 本身。
   - **不管哪种,把字段名和完整值都贴给我**。我要的是字段名(如 `image_url`?`cover_url`?`banner_url`?它在 `arg` 下还是 `opus` 下?)。

4. **Cookie 不用给我**:请求头里的 `Cookie` / `Set-Cookie`、以及 URL 里的 `csrf=` 后面的值,**全部删掉或打码**再给我。这些都是敏感凭据,我只需要字段结构。

5. **如果 Network 里有多个 `draft/add` 请求**:编辑器可能有自动保存。以你**手动点「保存草稿」那一刻**触发的、且请求体最完整(含封面+话题)的那个为准,通常是体积最大的那个。

6. **格式化**:如果 AI 助手能把请求体 JSON 美化(缩进)再贴,更好读;原样贴也行。

---

## 五、交付物

把以下 3 个文件(任务 C 可选)整理好回贴给我。**每个文件用这个固定结构**:

```json
{
  "task": "A | B | C",
  "api_endpoint": "完整的请求 URL,csrf 值可打码",
  "method": "POST",
  "request_headers_non_sensitive": {
    "Content-Type": "...",
    "Origin": "...",
    "Referer": "...",
    "User-Agent": "..."
  },
  "request_body": {
  },
  "response_body": {
  }
}
```

字段说明:
- `task`:A / B / C,对应上面的任务。
- `api_endpoint`:请求首行 URL。
- `request_headers_non_sensitive`:**剔除 Cookie / Authorization / csrf 后**的请求头。我要看 `Content-Type`(是 `application/json` 还是 `multipart`?)、`Origin`、`Referer`。
- `request_body`:**完整请求体 JSON,原样**。这是核心。multipart 请求(如封面上传)可以只给 form 字段名和类型,不必给二进制内容。
- `response_body`:响应 JSON。任务 C 的封面上传响应特别要看这里面的 URL 字段名。

**任务 A、B 必给,任务 C 可选。**

---

## 六、我拿到后会做什么

1. 逐字段对比任务 A 的请求体和 hikami `SaveDraft`(`bilibili_opus.go:186`)的构造逻辑,定位封面字段的**真实名字和层级**。
2. 逐字段对比任务 B 和 hikami `PublishOpus`(`bilibili_opus.go:251`),确认发布端的封面/话题字段。
3. 修复 `SaveDraft`/`PublishOpus`,让 `CoverURL` 真正进入请求体,并校准话题字段层级。
4. 补单元测试,验证请求体含正确的封面字段。
5. 回复你确认修复点,你重发一篇专栏验证封面和话题是否生效。

---

## 七、附:给执行 AI 的浓缩指令

如果你把这份文档丢给本地 AI 助手执行,可以直接附上这段:

> 请用 chrome-devtools-mcp 连接我本地已登录 B 站的 Chrome。
> 按文档「任务 A」操作:打开 `https://member.bilibili.com/platform/upload/text/edit`,
> 标题填 `hikami抓包测试草稿A`,正文填 `测试正文A`,上传一张封面图,选一个真实话题,
> 标签填 `hikami_test_tag_A`,开原创声明。点「保存草稿」。
> 抓 `draft/add` 请求,把 URL(csrf 打码)、非敏感请求头、完整请求体、响应体,
> 按「交付物」的 JSON 结构整理成 `capture_A_draft.json`。
> 再按「任务 B」点发布,抓 `create/opus`,整理成 `capture_B_publish.json`。
> Cookie 和 csrf 值不要包含在产出里。
