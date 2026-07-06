# 计划:auto_recap 默认值反转 + -352 剩余端点加固

> 状态:**codex 审核通过(v3)** — 执行中
> 创建:2026-07-06
> 修订:2026-07-06(v3,落实 codex 审核 v1 的 6 条 + v2 复审的 2 条必改项,均 APPROVED)
> 合并来源:
> - `docs/plan-auto-recap-default-false.md`(已审批未执行,本次落地)
> - `docs/archive/investigations/风控-352-剩余端点加固计划.md`(P0 已落地,P1/P2 本次落地)
>
> 本计划是对上述两份文档的**重新审核 + 统一执行版**。审核结论:
> - auto_recap 文档描述与代码完全吻合,未执行,按原方案落地。
> - -352 文档中 **P0(`live_record/bilibili.go`)已落地**(超出原计划,含 -352 重试 + `ErrRiskControl352` 哨兵 + scheduler 冷却,见 AGENTS.md 2026-07-06 异常 #9),**P1/P2 共 3 端点未落地**,本次按原方案落地。
> - 审核中**新发现**:`VideoClient` 是值类型且 `Fetch` 接收者为值,加字段后必须改指针接收者,原 -352 文档未覆盖此改造点,本计划补齐。

---

## 第一部分:auto_recap 默认值 true → false

### 背景

`auto_recap`(per-channel 自动回顾开关)用三态指针 `*bool`,`nil` 时走 fallback。当前 Create/Bootstrap fallback 写死 `true`,导致"新建主播默认自动回顾"。其余布尔字段(`auto_record`/`auto_asr`/`auto_publish`)走零值机制(不传即 false),唯独 `auto_recap` 被特殊处理为默认 true(历史 v32 schema 承诺)。用户要求反转为默认 false。

### 改动清单

#### 1.1 应用层 fallback(核心)

**`internal/channel/channel.go`** — 2 处:
- `:178` `boolToInt(resolveAutoRecap(input.AutoRecap, true))` → `..., false)`(Create 路径)
- `:257` `boolToInt(resolveAutoRecap(input.AutoRecap, true))` → `..., false)`(Bootstrap 路径)

`resolveAutoRecap` 函数体(`:546`)**不动**(通用三态解析器,fallback 由调用方传)。

#### 1.2 resolveAutoRecap 注释同步

**`internal/channel/channel.go:540-545`** 注释:
- `:541`「Create/Bootstrap 默认 true(保持历史「ASR 后自动回顾」行为,对齐 v32 迁移 DEFAULT 1)」→「Create/Bootstrap 默认 false(2026-07-06 反转,新建主播默认不自动回顾)」
- `:545`「唯独 auto_recap 因承诺「默认 true」需三态」→「唯独 auto_recap 需三态(其余 bool 字段零值即 false,无需三态)」

#### 1.3 DB schema DEFAULT 一致性

**`internal/db/migrate.go:186-187`**:
- 注释 `:186` 更新为说明「DEFAULT 1 保留:保护旧库升级路径(ADD COLUMN 用 DEFAULT 回填已有行,改 0 会静默关闭已有主播);新建主播默认值由应用层 `resolveAutoRecap(nil, false)` 决定」

> **v3 执行时订正(原计划错误,经 codex 执行审核 P1 发现)**:SQL `DEFAULT 1` **不改**。原计划以为"DEFAULT 只影响全新建表",但 SQLite `ALTER TABLE ADD COLUMN ... DEFAULT x` 会用 DEFAULT **回填所有已有行**——若改为 0,从 v31 旧库升级的用户其所有已有主播会被静默关闭自动回顾,违背"已有主播不受影响"目标。正确做法:**迁移 DEFAULT 保持 1**(保护升级路径),**只改应用层 fallback 为 false**(只影响新建)。全新部署的新建主播仍由应用层 Create 显式插 0 → 默认关 ✓;旧库升级的已有行回填 1 → 保持历史开 ✓。

#### 1.4 测试断言同步

**`internal/channel/channel_test.go`**:

**TestAutoRecapRoundTrip(`:1164`):**
- 函数注释 `:1160`「Create 不提供 → 默认 true(...)」→「默认 false(2026-07-06 反转)」
- `:1168` 行内注释「// 1. Create 不提供 auto_recap → 默认 true」→「→ 默认 false」
- `:1177-1179` 断言反转:
  ```go
  // 旧
  if !ch.AutoRecap {
      t.Fatalf("AutoRecap = false on create (omitted), want true (default)")
  }
  // 新
  if ch.AutoRecap {
      t.Fatalf("AutoRecap = true on create (omitted), want false (default)")
  }
  ```
- 其余 4 步(显式 false / nil 保留 false / 显式 true / nil 保留 true)**不动**(验证显式值或持久值,与 fallback 无关)。

**TestBootstrapAutoRecapDefault(`:1240`):**
- 函数注释 `:1237-1239`「频道默认开启自动回顾(对齐 v32 迁移 DEFAULT 1 与历史...)」→「频道默认关闭自动回顾(2026-07-06 反转默认,对齐 DEFAULT 0)」
- `:1246` 行内注释「// AutoRecap=nil → 默认 true」→「→ 默认 false」
- `:1257-1259` 断言反转:
  ```go
  // 旧
  if !omit.AutoRecap {
      t.Fatalf("ch_omit AutoRecap = false, want true (default when omitted)")
  }
  // 新
  if omit.AutoRecap {
      t.Fatalf("ch_omit AutoRecap = true, want false (default when omitted)")
  }
  ```
- `ch_off`(显式 false,`:1265-1267`)**不动**。

#### 1.5 文档同步(措辞)

- **`internal/channel/CLAUDE.md`** — 提到 auto_recap「默认 true」处改为「默认 false」+ 测试描述同步
- **`internal/db/CLAUDE.md`** — "v32 默认 1"措辞更新为"默认 0(2026-07-06 反转)"
- **`internal/config/CLAUDE.md`** — `:154` `resolveAutoRecap(nil, true)` 兜底为「默认开」+ DB 默认 1 → 改为 `resolveAutoRecap(nil, false)` 兜底为「默认关」+ DB 默认 0;`:205` changelog 同步(codex 发现 #6)
- **`channel.go` `resolveAutoRecap` 注释** — 见 1.2

#### 1.6 不改的部分(明确边界)

- `resolveAutoRecap` 函数体 — 通用解析器
- Update 路径(`:341`,`fallback=existingAutoRecap`)— 保留现有值,正确
- `SaveIdentified`(`:434-435`)— 识别保存复用 existing 值,正确
- 前端 `StreamersView.vue` / `api/types.ts` — `auto_recap: boolean`,无硬编码默认,Go 侧 nil 走新 fallback false,UI 开关正确显示"关"。**无需改前端**。
- 运行时消费方 `cmd/hikami/main.go:250`(`if err != nil || !ch.AutoRecap { return }`)— 只读字段,行为自然跟随,无需改。

---

## 第二部分:-352 剩余端点加固(P1 + P2)

### 共享风控层(已存在,本次复用)

| 组件 | 位置 | API |
|------|------|-----|
| `BuvidStore` | `internal/biliutil/buvid.go` | `NewBuvidStore()` / `NewBuvidStoreWithHTTPClient(c)` / `GetBuvids(ctx, cookie) (b3,b4,err)` / `Invalidate(cookie)` |
| `InjectBuvids` | `internal/biliutil/buvid.go` | `InjectBuvids(cookie, b3, b4) string`(replace 语义) |
| `WBISigner` | `internal/biliutil/wbi.go` | `NewWBISigner(cookie)` / `SignURL(url) (string,error)` / `RefreshKeys() error`;实现 `URLSigner` 接口 |
| `BiliUserAgent` | `internal/biliutil` | 浏览器 UA 常量 |

**通用三件套模板**(identify/bilibili.go 已验证):
1. 注入 buvid:`InjectBuvids`(失败降级,仅 warn)
2. WBI 签名:`SignURL`(按 cookie 懒缓存 signer,失败降级为不签名)
3. 请求头:UA + Referer + Origin

### P1:`internal/biliutil/video.go` — `/x/web-interface/view`

| 项 | 现状 | 改造 |
|----|------|------|
| 当前对抗 | UA + Referer + Cookie(`setBiliHeaders:111`);**无 Origin、无 buvid、无 WBI** | 加三件套 |
| `VideoClient` 类型 | **值类型**,`Fetch` 接收者 `(c VideoClient)`,4 处 `biliutil.VideoClient{}` 直接构造 | **改指针接收者** `(c *VideoClient)`,构造点配套 |
| httpClient 注入口 | `httpClientOrDefault(c.HTTPClient)`(`:104`)已有 | 保留 |

**改动细节**:

1. **`VideoClient` 加字段**(struct 定义 `:29`):
   ```go
   type VideoClient struct {
       HTTPClient HTTPDoer
       BaseURL    string
       buvids     *BuvidStore
       signers    map[string]URLSigner  // 按 cookie 懒缓存,与 identify/bilibili.go 同模式
       signersMu  sync.Mutex
       newSigner  func(cookie string) URLSigner  // 默认 NewWBISigner,测试可注入桩
   }
   ```

2. **`Fetch` 接收者改指针**:`func (c VideoClient) Fetch` → `func (c *VideoClient) Fetch`(保证字段一致性)。

3. **懒初始化字段**(值类型零值时 `buvids==nil` / `signers==nil`,Fetch 内首次调用初始化)。**关键:`newSigner` 只在为 nil 时设默认值,不得覆盖测试经 `SetSignerFactory` 注入的桩**(codex 发现 #3);**且整个 `ensure()` 必须持有 `signersMu` 锁,避免并发首次 `Fetch` 的数据竞争**(codex 复审发现):
   ```go
   func (c *VideoClient) ensure() {
       c.signersMu.Lock()
       defer c.signersMu.Unlock()
       if c.buvids == nil {
           c.buvids = NewBuvidStoreWithHTTPClient(httpClientOrDefault(c.HTTPClient))
       }
       if c.signers == nil {
           c.signers = make(map[string]URLSigner)
       }
       if c.newSigner == nil {
           c.newSigner = NewWBISigner  // 只在未注入时设默认,不覆盖 setter
       }
   }
   ```
   `buvids` / `signers` / `newSigner` 三个字段的初始化**全部在 `signersMu` 保护下**(与 `signerForCookie` 的读写共用同一把锁,确保并发首次 `Fetch` 安全)。`Fetch` 开头调 `c.ensure()`。
   > 注:`signersMu` 这把锁的字段名暗示了它保护 `signers`,但实际它保护 `VideoClient` 所有可变状态的初始化与读写。可在字段注释里写明这一点。

4. **注入 buvid + WBI**(`Fetch` 内,`endpoint` 构造后)。**signer 缓存 key 用原始 cookie(`baseCookie`),buvid 注入只改请求 Cookie 头,不改 signer key**(对齐 `live_record/bilibili.go:103-115` 的 v3 修正,codex 发现 #1):
   ```go
   baseCookie := cookie          // 原始 cookie:signer key + buvid 缓存 key
   cookieHeader := baseCookie    // 请求用的 Cookie 头,会被注入 buvid
   if b3, b4, err := c.buvids.GetBuvids(ctx, baseCookie); err != nil {
       slog.Warn(...)            // 降级:不改 cookieHeader(不剔除已有 buvid3)
   } else if b3 != "" || b4 != "" {
       cookieHeader = InjectBuvids(cookieHeader, b3, b4)
   }
   // signer 按原始 cookie 取(WBI 密钥随账号身份不随 buvid 变),signerForCookie 内部用 signersMu 保护
   if signed, err := c.signerForCookie(baseCookie).SignURL(endpoint); err == nil {
       endpoint = signed
   } // 失败降级为不签名
   // 后续请求用 cookieHeader 作为 Cookie 头,endpoint 用签名后的 URL
   ```

5. **`setBiliHeaders` 补 Origin**(`:111`):加 `req.Header.Set("Origin", biliReferer)`。

6. **`signerForCookie` helper**(新增,仿 identify/bilibili.go):按 cookie 加 `signersMu` 锁查/建 signer。

7. **包级 `FetchVideoInfo`(`:51`)配套**:内部 `VideoClient{}.Fetch(...)` 是**复合字面量(不可寻址)**,改指针接收者后会编译失败,必须改:
   ```go
   vc := &VideoClient{}
   return vc.Fetch(ctx, bvid, cookie)
   ```

8. **调用点配套**(值→指针)。Go 方法集规则:**可寻址变量**(如 `viewClient := VideoClient{...}`)调用指针接收者方法时编译器自动取址,无需改调用点;**不可寻址的复合字面量**(如 `(VideoClient{...}).Fetch` / `VideoClient{}.Fetch`)必须改为先赋值或显式 `&`(codex 发现 #2):
   - `download/download.go:404` `viewClient := biliutil.VideoClient{}` → 变量可寻址,**调用点 `viewClient.Fetch(...)` 不变**(自动取址)
   - `download/download.go:504` `biliutil.FetchVideoInfo(...)` → 调用包级函数,函数内部已配套(见第 7 点),调用方不变
   - `download/native.go:83` `viewClient := biliutil.VideoClient{...}` → 变量可寻址,**调用点 `viewClient.Fetch(...)` 不变**
   - `native.go:108/159` 的 `playClient.Fetch(...)` 是 `PlayURLClient` 的方法,**不属本次改造**(codex 发现 #2 澄清)
   - **测试文件需配套**(复合字面量不可寻址,改指针接收者后编译失败):
     - `internal/biliutil/video_test.go:39/55/66` `(VideoClient{...}).Fetch(...)` → `(&VideoClient{...}).Fetch(...)`(`&VideoClient{}` 是 `*VideoClient`,可直接调指针方法)或先赋值给变量再调用
     - `internal/download/probe_test.go:48/65/89/127/189` `biliutil.VideoClient{}.Fetch(...)` → `(&biliutil.VideoClient{}).Fetch(...)` 或先赋值

**测试副请求配套**(codex 发现 #5):`VideoClient` 加 buvid/WBI 后,默认 `BuvidStore` 会先请求 `finger/spi`、默认 `WBISigner` 会请求 `x/web-interface/nav`。现有 `video_test.go:10` 的 mock handler 只接受 `/x/web-interface/view`,直接跑会因副请求失败或泄漏真实网络。**必须为 `video_test.go` / `probe_test.go` 注入桩**:
- `SetBuvidStore(httptestBuvidStore)` —— 指向 httptest 的 spi URL(或返回固定 buvid 的 no-op store)
- `SetSignerFactory(stubSigner)` —— 返回不经网络的桩 signer(直接返回原 URL 或预签名 URL)
- 调整后的 mock handler 应断言:请求带 `Origin` 头、注入后的 `Cookie` 含 buvid3、签名参数(`w_rid`/`wts`)存在于 URL
- 这样第三部分"strace 确认测试无对外网络"才成立

**测试注入点**:加 `SetBuvidStore` / `SetSignerFactory`(仿 identify/bilibili.go:65/71),供测试注入桩指向 httptest 的 spi/nav URL。

### P2:`internal/handler/server.go` — `searchBiliTopics` + `listBiliSeries`(合并改造)

两处都是函数内内联 `&http.Client{Timeout: 15s}` 裸调,无任何对抗基建。改造方案:

1. **抽公共 helper**(同文件新增,或复用现有 client):
   ```go
   // biliCreativeGet 发起带风控对抗头的 B站创作类 GET 请求。
   // cookie 为空时跳过 Cookie 头(适用于无账号也能调的端点)。
   func (s *Server) biliCreativeGet(ctx context.Context, endpoint, cookieHeader string, target any) error {
       req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
       if err != nil { return err }
       req.Header.Set("User-Agent", biliutil.BiliUserAgent)
       req.Header.Set("Referer", biliCreativeReferer)   // https://www.bilibili.com(handler 包本地常量,见下)
       req.Header.Set("Origin", biliCreativeReferer)
       req.Header.Set("Accept", "application/json")
       if cookieHeader != "" { req.Header.Set("Cookie", cookieHeader) }
       resp, err := s.biliCreativeClient.Do(req)  // 注入的共享 client,见下
       ...
   }
   ```
   > **`biliReferer` 是 `biliutil/video.go:17` 的包内私有常量,handler 包不可见**(codex 发现 #4)。需在 `handler/server.go` 本地新增 `const biliCreativeReferer = "https://www.bilibili.com"`(或导出 `biliutil.BiliReferer` 复用,本计划采用本地常量,改动最小且不扩大 biliutil 公共面)。

2. **`Server` 加共享 client 字段**:`biliCreativeClient *http.Client`(构造期初始化一次,替代两处内联 client)。或直接复用 `s` 已有的某 client 字段(若存在合适的);若无则新增。

3. **`searchBiliTopics`(`:3978`)改造**:
   - 删内联 `client := &http.Client{...}`(`:4006`)
   - 补 Referer/Origin(原只有 UA + Accept)
   - cookie:topic 端点无账号也能用,但补 cookie 更稳——用 `s.cookieAccounts.GetDefaultPublish` 解析(与 `listBiliSeries:4058` 同源),失败则不带 cookie 继续
   - 改用 `s.biliCreativeGet(...)`

4. **`listBiliSeries`(`:4040`)改造**:
   - 删内联 `client := &http.Client{...}`(`:4076`)
   - 补 Referer/Origin(原只有 UA + Cookie)
   - 改用 `s.biliCreativeGet(...)`,cookie 走现有 `LoadCookie`(`:4058`)

5. **是否加 buvid/WBI**:`topic/pub/search` 和 `article/creative/list/all` 是创作类端点,**带 cookie + Referer/Origin 通常足够**(计划原文判断,P2 非核心路径,先只补防御性头,观察)。若后续报告 -352 再升级加 buvid。**本次不加 buvid/WBI,保持最小改动**。

### 不改的端点(备查)

`getDanmuInfo`/`getDanmuConf`(danmaku.go,已完整对抗)、`finger/spi`(buvid.go,本身是对抗层)、`web-interface/nav`(wbi.go,WBI 取密钥,UA+Referer+Cookie 暂够)、publisher 全套(已完整)、`passport qrcode`(未登录态)、`getInfoByRoom`(identify.go,2026-07-05 已修)、`getInfoByRoom`/`getRoomPlayInfo`(live_record/bilibili.go,P0 已修)。

---

## 第三部分:验证步骤

### 编译与测试
1. `gofmt -w internal/channel/channel.go internal/channel/channel_test.go internal/db/migrate.go internal/biliutil/video.go internal/handler/server.go`
2. `go build ./...` 确认编译通过(重点:`VideoClient` 指针接收者改造后,4 处调用点 + 包级函数)
3. `go test ./internal/channel/... ./internal/db/...` — auto_recap 两测试通过
4. `go test ./internal/biliutil/... ./internal/handler/... ./internal/download/...` — video.go 改造后,`FetchVideoInfo`/`VideoClient` 相关测试通过(download 包依赖 view)
5. `go test ./...` — 全量回归

### 行为验证(auto_recap)
- 新建主播(不传 auto_recap)→ DB `auto_recap=0`,前端开关显示"关"
- 已有主播设置不受影响(Update 走 existing 值)

### 行为验证(-352,手动,可选)
- video.go:用真实账号 cookie curl `/x/web-interface/view?bvid=xxx`,确认 200 code=0(改造前后对比,确认未回归)
- handler 两端点:前端发布页打开话题搜索 / 合集列表,确认功能正常

### strace 确认测试无对外网络(可选,严格项)
- `strace -f -e trace=connect -o /tmp/sc.txt go test ./...` 后检查无 `*.bilibili.com` 连接(测试应全用 httptest 桩)

---

## 第四部分:行为影响说明

### auto_recap
- **所有新建主播**(手动添加 / 引导识别保存 / bootstrap 配置)**默认不会自动生成回顾**,需用户在主播详情页手动打开"回顾"开关。
- **已有主播设置完全不受影响**(Update 走 existing 值)。
- 老数据库无需迁移(应用层 Create 总显式插值)。

### -352 P1/P2
- `view` 端点:风控对抗能力提升,回放下载更稳。buvid/WBI 注入失败时降级(仅 warn),不阻断。
- handler 两端点:补防御性头,功能不变,风控收紧时更稳。本次不加 buvid/WBI。

---

## 第五部分:实施纪律

- **不扩大范围**:严格按本清单改,不顺手重构无关代码。
- **行为等价优先**:风控对抗组件(buvid/WBI)失败时降级,不 panic,不阻断主流程。
- **复用共享层**:所有对抗走 `biliutil.BuvidStore` + `biliutil.WBISigner`,禁止再造本地实现。
- **codex 审核两次**:计划审核(本步)+ 执行后审核。
- **文档同步**:执行后同步 `channel/CLAUDE.md`、`db/CLAUDE.md`、`biliutil/CLAUDE.md`、`handler/CLAUDE.md`、根 `CLAUDE.md`/`AGENTS.md` changelog;原两份计划文档标注落地状态。

---

## 执行入口

codex 审核本计划通过后,按"第一部分 1.1→1.6 → 第二部分 P1 → P2 → 第三部分验证"顺序执行。
