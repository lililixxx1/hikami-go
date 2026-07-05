[根目录](../../CLAUDE.md) > **internal/biliutil**

# internal/biliutil -- B 站 Cookie、账号、扫码登录与 URL 签名工具

## 模块职责

提供 B 站 Netscape 格式 Cookie 文件的加载、解析、写入、AES-256-GCM 静态加密、账号池管理、QR Login 扫码登录、WBI URL 签名机制、设备指纹（buvid）拉取与注入，以及视频 view/playurl/弹幕 XML/seg.so API 客户端。作为共享工具包被 `publisher`、`channel`、`live_record`、`handler`、`download` 等多个模块引用，统一 Cookie 处理、账号解析、API 签名、B 站 -352 风控对抗（buvid + WBI）和 B 站 HTTP 请求基础能力。

## 入口与启动

- **入口文件**: `cookie.go`, `cookie_crypto.go`, `cookie_account.go`, `cookie_writer.go`, `login.go`, `wbi.go`, `ua.go`, `video.go`, `playurl.go`, `danmaku.go`, `danmaku_seg.go`, `buvid.go`
- **核心类型**: `BiliCookie`, `CookieAccountStore`, `QRLoginSessionStore`, `WBISigner`, `BuvidStore`, `VideoClient`, `PlayURLClient`, `DanmakuClient`, `SegDanmaku`, `SegDanmakuClient`
- **测试总数**: 80（按 `grep -c "^func Test" internal/biliutil/*_test.go` 统计；含 `buvid.go` 的 6 个测试函数）

## 对外接口

### Cookie 加载

| 函数/方法 | 说明 |
|-----------|------|
| `LoadCookie(cookiePath string) (*BiliCookie, error)` | 从 Netscape 格式文件加载 Cookie |
| `BiliCookie.CookieHeader() string` | 生成完整 Cookie 请求头字符串 |

### Cookie 加密

| 函数/方法 | 说明 |
|-----------|------|
| `SetCookieEncryptionKey(hexKey string) error` | 设置 Cookie 文件 AES-256-GCM 加密密钥；64 位 hex（32 字节）启用，空字符串禁用 |
| `CookieEncryptionEnabled() bool` | 返回当前进程是否启用 Cookie 文件加密 |
| `encryptCookieFile(plaintext []byte) ([]byte, error)` | 启用密钥时加密 Netscape Cookie 内容；未启用时原样返回 |
| `decryptCookieFile(data []byte) ([]byte, error)` | 自动识别 `HIKAMI_V1` 加密文件并解密；无 magic 时按明文兼容读取 |

### Cookie Account

| 函数/方法 | 说明 |
|-----------|------|
| `NewCookieAccountStore(db)` | 创建账号 Store |
| `List(ctx)` / `GetByID(ctx, id)` / `GetByUID(ctx, uid)` | 查询账号 |
| `Create(ctx, account)` / `Update(ctx, account)` / `Delete(ctx, id)` | 账号 CRUD |
| `CreateImported(ctx, account)` | 创建导入账号（跳过 Cookie 路径校验，cookie_file 设为空字符串），用于配置导入 |
| `ClearAll(ctx)` | 清除所有账号（DELETE FROM bili_cookie_accounts），用于配置导入 overwrite 策略 |
| `GetDefaultDownload(ctx)` / `GetDefaultPublish(ctx)` | 获取默认下载/发布账号 |
| `SetDefaultDownload(ctx, id)` / `SetDefaultPublish(ctx, id)` | 设置默认下载/发布账号 |
| `ResolveCookie(ctx, downloadAccountID, publishAccountID, usage, fallbackCookiePath)` | 解析最终 Cookie：主播账号覆盖 -> 全局默认账号 -> 旧文件回退 |

### Cookie Writer

| 函数/方法 | 说明 |
|-----------|------|
| `WriteNetscapeCookieFile(cookies, opts)` | 将扫码 Cookie 原子写入 Netscape Cookie 文件 |
| `FormatNetscapeCookies(cookies, now)` | 格式化 Cookie 内容并校验必需字段 |
| `NormalizeBiliCookie(cookie, now)` | 规范化 domain/path/expires/HttpOnly |

### QR Login

| 函数/方法 | 说明 |
|-----------|------|
| `NewQRLoginClient(httpClient)` | 创建 B 站扫码登录客户端 |
| `NewQRLoginSessionStore(client, ttl)` / `NewQRCodeManager(client, ttl)` | 创建扫码会话管理器 |
| `Create(ctx)` | 创建二维码会话 |
| `Poll(ctx, sessionID)` | 轮询二维码状态 |
| `GetSucceeded(ctx, sessionID)` | 获取已成功扫码会话和 Cookie |
| `Delete(sessionID)` / `CleanupExpired(now)` | 删除/清理会话 |

### WBI URL 签名

| 函数/方法 | 说明 |
|-----------|------|
| `NewWBISigner(cookie string) *WBISigner` | 创建 WBI 签名器 |
| `WBISigner.SignURL(rawURL string) (string, error)` | 对 URL 进行 WBI 签名，附加 w_rid 和 wts 参数 |
| `WBISigner.RefreshKeys() error` | 强制刷新 WBI 签名密钥 |
| `URLSigner` | 可测试的 URL 签名接口 |
| `HTTPDoer` | 可测试的 HTTP 客户端接口（`Do(*http.Request)`） |

### 设备指纹（buvid）—— -352 风控对抗

| 函数/方法 | 说明 |
|-----------|------|
| `NewBuvidStore() *BuvidStore` | 创建带默认 HTTP 客户端 + 默认 spi URL 的指纹存储 |
| `NewBuvidStoreWithHTTPClient(client HTTPDoer) *BuvidStore` | 指定 HTTP 客户端（复用业务 client 避免连接池分裂） |
| `NewBuvidStoreWithOptions(client HTTPDoer, spiURL string) *BuvidStore` | 指定 client + spi URL（测试注入 httptest 桩用） |
| `(*BuvidStore).GetBuvids(ctx, cookieHeader) (buvid3, buvid4 string, err error)` | 按 cookie 拉取并 24h 缓存 buvid3/buvid4；**nil 接收者返回空串 + nil（不打网络）**，覆盖测试 helper 字面量构造的禁用场景 |
| `InjectBuvids(cookieHeader, buvid3, buvid4 string) string` | 把 buvid3/buvid4 注入 cookie 头，采用 **replace 语义**（剔除旧 buvid3=/buvid4= 段再追加新值，避免 B 站按首个同名 cookie 解析导致新指纹失效） |

> **设计要点**：buvid 注入是 -352 风控对抗的**必要但不充分**条件——`getInfoByRoom`/`getRoomInfoOld` 端点还需 **WBI 签名**（`WBISigner.SignURL`）才能完全通过（探针实测：buvid only 仍 -352，buvid + WBI → 200 code=0）。`getDanmuInfo` 端点 buvid + WBI 同样必要。三处调用方（`channel`/`publisher`/`live_record`）统一复用本组件，消除此前两份重复的 buvid 拉取实现。

### 视频与弹幕 API

| 函数/方法 | 说明 |
|-----------|------|
| `FetchVideoInfo(ctx, bvid, cookie)` / `VideoClient.Fetch` | 调用 `/x/web-interface/view` 获取 `VideoInfo`（aid/bvid/title/pages） |
| `FetchPlayURL(ctx, aid, cid, bvid, cookie, signer)` / `PlayURLClient.Fetch` | 调用 WBI playurl 获取 DASH 音频流列表 |
| `SelectBestAudioStream(streams)` | 从 `AudioStream` 列表选择 bandwidth 最高且有 URL 的音频流 |
| `AudioStream.URLs()` | 返回 baseUrl + backupUrl 顺序列表 |
| `FetchDanmakuXML(ctx, cid, cookie)` / `DanmakuClient.FetchXML` | 拉取 `comment.bilibili.com/{cid}.xml`，支持 `deflate`（zlib/raw flate）和 `gzip` 解压 |
| `DecodeDanmakuSeg(data []byte) ([]SegDanmaku, error)` | 手写 protobuf wire 解码 DmSegMobileReply |
| `SegDanmakuClient.FetchSegments(ctx, cid, cookie) ([]byte, error)` | 分页拉取 seg.so（`segment_index` 1..200，空页/404/-404/-796 停止）并返回合并 XML |

| 类型 | 说明 |
|------|------|
| `VideoInfo` | view 接口核心视频信息：AID/BVID/Title/Pages |
| `VideoPage` | 分 P 信息：CID/Part/Page |
| `AudioStream` | DASH 音频流：id/baseUrl/backupUrl/bandwidth/mimeType/codecs |
| `SegDanmaku` | seg.so 单条弹幕：progress/mode/fontSize/color/ctime/pool/midHash/id/content |

**SegDanmaku 字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `Progress` | int64 | 弹幕出现时间，毫秒 |
| `Mode` | int32 | 弹幕模式 |
| `FontSize` | int32 | 字号 |
| `Color` | uint32 | RGB 颜色 |
| `CTime` | int64 | 发送时间戳 |
| `Pool` | int32 | 弹幕池 |
| `MidHash` | string | 用户哈希，写 XML 属性时会转义 |
| `ID` | int64 | 弹幕 ID |
| `Content` | string | 弹幕文本，写 XML 文本节点时会转义 |

### User-Agent 常量

| 常量 | 说明 |
|------|------|
| `BiliUserAgent` | 用于所有 B 站 API 请求的 User-Agent 字符串 |
| `BrowserUA` | `BiliUserAgent` 的兼容别名，供原生下载链路复用 |

### 错误定义

| 错误 | 说明 |
|------|------|
| `ErrCookieMissing` | 缺少必需字段（SESSDATA, bili_jct, DedeUserID） |
| `ErrCookieExpired` | SESSDATA 已过期 |
| `ErrInvalidKey` | Cookie 加密密钥格式无效（必须为空或 64 位 hex） |
| `ErrAccountNotFound` | Cookie Account 不存在 |
| `ErrAccountUIDDuplicate` | Cookie Account UID 重复 |
| `ErrNoDefaultAccount` | 未配置默认 Cookie Account 且无旧文件回退 |
| `ErrQRLoginSessionNotFound` | QR Login 会话不存在 |
| `ErrQRLoginSessionExpired` | QR Login 会话过期 |
| `ErrQRLoginNotSucceeded` | QR Login 会话尚未成功 |
| `ErrBiliLoginUpstream` | B 站扫码登录上游错误 |
| `ErrWBIKeyUnavailable` | 无法获取 WBI 签名密钥 |
| `ErrRiskControl` | 触发了 B 站风控（-352） |
| `ErrInvalidCookiePath` | Cookie 文件路径校验失败（路径穿越） |
| `ErrNoAudioStream` | playurl 响应无可用 DASH 音频流 |
| `ErrPlayURLFailed` | playurl 请求、签名、HTTP 状态或响应解析失败 |
| `ErrSegDanmakuDecodeFailed` | seg.so protobuf wire 解码失败 |
| `ErrSegDanmakuFetchFailed` | seg.so 分段拉取、HTTP 状态或响应转换失败 |

## 关键依赖与配置

- 无外部依赖
- Cookie 文件格式：Netscape HTTP Cookie File（支持 `#HttpOnly_` 前缀行）
- Cookie 加密格式：`HIKAMI_V1` magic（8 字节）+ GCM nonce（12 字节）+ ciphertext/tag；由 `cookie_crypto.go` 自动封装
- 必需字段：`SESSDATA`、`bili_jct`、`DedeUserID`

## 数据模型

**BiliCookie 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `SESSDATA` | string | B 站会话凭证 |
| `BiliJct` | string | CSRF Token |
| `DedeUserID` | string | 用户 ID |

**CookieAccount 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `ID` | int64 | 自增主键 |
| `UID` | int64 | B 站 UID，数据库唯一 |
| `Nickname` | string | 账号昵称 |
| `CookieFile` | string | Netscape Cookie 文件路径 |
| `IsDefaultDownload` | bool | 是否默认下载账号 |
| `IsDefaultPublish` | bool | 是否默认发布账号 |
| `CreatedAt` / `UpdatedAt` | string | 时间戳 |

**Cookie 解析规则：**
- `LoadCookie` 和 `CheckCookieExpiry` 解析前先调用 `decryptCookieFile`
- 未包含 `HIKAMI_V1` magic 的旧明文 Cookie 文件保持兼容
- 跳过空行和 `#` 开头的注释行（`#HttpOnly_` 前缀除外）
- 解析过期时间，SESSDATA 过期直接返回 `ErrCookieExpired`
- 其他过期 Cookie 跳过不报错
- `CookieHeader()` 输出格式：`name1=value1; name2=value2; ...`（保留所有非过期字段）

**Cookie 写入规则：**
- `WriteNetscapeCookieFile` 格式化并校验 Netscape Cookie 内容后调用 `encryptCookieFile`
- 启用 `cookie_encryption_key` 时落盘文件为 AES-256-GCM 密文
- 未启用密钥时仍按明文写入，保持旧部署兼容

**WBISigner 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `httpClient` | *http.Client | HTTP 客户端（15s 超时） |
| `cookie` | string | Cookie 字符串 |
| `mixinKey` | string | 混合签名密钥（缓存在内存中） |
| `updatedAt` | time.Time | 密钥最后更新时间 |
| `mu` | sync.Mutex | 并发保护 |

**WBI 签名流程：**
1. `ensureKeys` 检查密钥缓存，空或超过 1 小时则自动刷新
2. `fetchKeys` 从 `api.bilibili.com/x/web-interface/nav` 获取 `img_url` 和 `sub_url`
3. 从 URL 提取文件名（去 `.png` 后缀）作为 imgKey 和 subKey
4. `getMixinKey` 使用 64 元素置换表将 imgKey+subKey 混合为 32 字符 mixinKey
5. `SignURL` 对 URL 参数排序、清理特殊字符 `!'()*`、拼接 mixinKey 后计算 MD5 得到 `w_rid`
6. 附加 `w_rid` 和 `wts`（Unix 时间戳）参数到 URL

**测试辅助方法：**
- `SetMixinKeyForTest(key string)` -- 直接设置 mixinKey，避免真实网络请求

**QR Login 状态：**
- `pending`：未扫码
- `scanned`：已扫码未确认
- `expired`：二维码或会话过期
- `succeeded`：扫码成功，Session 内保留 Cookie、UID、refresh_token
- `failed`：上游返回失败状态

## 测试与质量

- `cookie_test.go`: 1 个测试用例，验证 Cookie 加载、完整请求头生成和 HttpOnly 行处理
- `cookie_crypto_test.go`: 10 个测试用例，覆盖密钥校验、加解密往返、明文 passthrough、错误密钥和截断数据
- `cookie_account_test.go`: 14 个测试用例，覆盖：
  - CRUD: Create、Update（昵称修改）、Delete（删除后 ErrAccountNotFound）、List（多账号列表）
  - 默认账号: SetDefaultDownload（切换默认下载）、SetDefaultPublish（切换默认发布、旧默认清除）、DeleteDefaultAccount（删除唯一默认 -> ErrNoDefaultAccount）
  - 路径校验: ValidateCookiePath（允许/路径穿越/空路径/无限制）
  - Cookie 解析: ResolveCookie（channel override 成功、fallback 文件不存在、unknown usage）
- `videoid_test.go`: 7 个测试用例，覆盖 ExtractVideoID/NormalizeSourceURL（BV 优先、非 B 站 sha1 兜底、各类 URL 归一化）
- `wbi_test.go`: 13 个测试用例，覆盖：
  - `TestGetMixinKey`: 置换表正确性验证
  - `TestGetMixinKeyShortInput`: 短输入越界跳过
  - `TestExtractKeyFromURL`: URL 到 key 提取（3 种 URL 格式）
  - `TestSanitizeValue`: 特殊字符清理
  - `TestSignURLAddsWRidAndWts`: 签名后包含 w_rid 和 wts
  - `TestSignURLParamSorting`: 参数按 key 排序
  - `TestSignURLCalculatesCorrectWRid`: 手动验证 w_rid 计算
  - `TestKeyCaching`: 缓存命中
  - `TestKeyCachingWithNavMock`: 缓存过期检测
  - `TestNavErrorReturnsErrWBIKeyUnavailable`: nav 返回非零 code
  - `TestNavResponseParseError`: 辅助函数组合测试
  - `TestEmptyCookieStillRequestsNav`: 空 Cookie 仍可签名
  - `TestRefreshKeysForceRefresh`: 强制刷新密钥
- `login_test.go`: QR Login 客户端和会话管理测试（3 个用例）
- `cookie_writer_test.go`: Netscape Cookie 文件写入测试（3 个用例）
- `video_test.go`: 3 个测试用例，覆盖 view 成功、HTTP 非 2xx、API code 非 0
- `playurl_test.go`: 4 个测试用例，覆盖 playurl 成功+选流、无音频流、HTTP 非 2xx、API code 非 0
- `danmaku_test.go`: 2 个测试用例，覆盖明文 XML 和 `Content-Encoding: deflate`（zlib）解压
- `danmaku_seg_test.go`: 9 个测试用例，覆盖多段解码、未知字段、截断、varint overflow、fixed64/fixed32 跳过、XML 转义、midHash 转义、空页停止、段数上限

## 常见问题 (FAQ)

**Q: Cookie 文件从哪里获取？**
A: 浏览器安装 Cookie 导出扩展，以 Netscape 格式导出 `.bilibili.com` 域名的 Cookie。

**Q: 如何启用 Cookie 文件静态加密？**
A: 在配置中设置 `cookie_encryption_key` 为 64 位 hex（32 字节）密钥。启动时 `main.go` 调用 `SetCookieEncryptionKey` 初始化，之后扫码登录写入的 Cookie 文件会以 AES-256-GCM 加密落盘。

**Q: 已有明文 Cookie 文件还能读取吗？**
A: 可以。`decryptCookieFile` 只在检测到 `HIKAMI_V1` magic 时解密；没有 magic 的文件按旧明文格式透传解析。

**Q: 更换或丢失 cookie_encryption_key 会怎样？**
A: 使用旧密钥加密的 Cookie 文件无法用新密钥解密，会导致 Cookie 加载失败。生产环境应妥善备份该密钥。

**Q: 为什么 SESSDATA 过期直接报错而其他字段过期只跳过？**
A: SESSDATA 是核心会话凭证，过期后所有需要登录的 API 都会失败，应尽早通知用户。

**Q: WBI 签名什么时候使用？**
A: B 站部分 API（如搜索、用户信息）需要 WBI 签名参数。`WBISigner` 提供了标准的签名流程，密钥自动缓存 1 小时。

**Q: WBI 密钥从哪里获取？**
A: 从 B 站 `nav` API 的 `wbi_img.img_url` 和 `wbi_img.sub_url` 字段提取，通过置换表混合生成 mixinKey。

## 相关文件清单

- `cookie.go` -- Cookie 加载、解析、请求头生成
- `cookie_crypto.go` -- Cookie 文件 AES-256-GCM 静态加密、解密和密钥配置
- `cookie_account.go` -- Cookie Account Store、默认账号管理、ResolveCookie、CreateImported、ClearAll
- `cookie_writer.go` -- Netscape Cookie 文件格式化和原子写入
- `videoid.go` -- 视频链接解析：`ExtractVideoID`（BV 号优先，非 B 站用归一化 URL 的 sha1 兜底）、`NormalizeSourceURL`（去 fragment/跟踪参数，幂等）
- `login.go` -- QR Login 客户端、扫码会话 Store、状态映射
- `wbi.go` -- WBI URL 签名器（WBISigner、密钥获取与缓存、MD5 签名计算）
- `ua.go` -- B 站 User-Agent 常量
- `video.go` -- B 站 view API 客户端（VideoClient/FetchVideoInfo/VideoInfo/VideoPage）
- `playurl.go` -- B 站 WBI playurl API 客户端、DASH 音频流解析与选流
- `danmaku.go` -- B 站弹幕 XML 拉取，支持 deflate/gzip 解压
- `danmaku_seg.go` -- seg.so 弹幕 protobuf wire 解码、分页拉取与 XML 合并
- `buvid.go` -- 设备指纹存储（BuvidStore，按 cookie 24h 缓存 buvid3/buvid4，nil-safe）+ InjectBuvids replace 注入
- `cookie_test.go` -- Cookie 单元测试（1 个用例）
- `cookie_crypto_test.go` -- Cookie 加密单元测试（10 个用例）
- `cookie_account_test.go` -- Cookie Account 单元测试（14 个用例）
- `wbi_test.go` -- WBI 签名单元测试（13 个用例）
- `login_test.go` -- QR Login 测试（3 个用例）
- `cookie_writer_test.go` -- Cookie Writer 测试（3 个用例）
- `videoid_test.go` -- 视频链接解析测试（BV 提取多 URL 形态、非 B 站兜底稳定性、归一化幂等、NetscapeBytes）
- `video_test.go` -- view API 测试（3 个用例）
- `playurl_test.go` -- playurl API 和选流测试（4 个用例）
- `danmaku_test.go` -- 弹幕 XML 拉取与解压测试（2 个用例）
- `danmaku_seg_test.go` -- seg.so 解码、分页停止、转义和上限测试（9 个用例）
- `buvid_test.go` -- BuvidStore 测试（6 个用例：拉取+缓存、按 cookie 缓存、空 b_3 报错、HTTP 失败、nil-safe、InjectBuvids replace 语义 5 子用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-05 | 功能 | 新增 `buvid.go`：`BuvidStore`（按 cookie 24h 缓存 buvid3/buvid4，nil-safe）+ `InjectBuvids`（replace 语义注入，剔除旧同名再追加）。**目的**：统一 B 站 -352 风控对抗的设备指纹层，消除 `publisher`/`live_record` 此前两份重复的 buvid 拉取实现，并供 `channel/identify` 修复 -352 使用。关键洞察：buvid 注入是 -352 对抗的必要但不充分条件，`getInfoByRoom`/`getRoomInfoOld`/`getDanmuInfo` 端点还需 WBI 签名（探针实测：buvid only 仍 -352，buvid + WBI → 200）。测试计数：biliutil 69→80 |
| 2026-06-18 | 功能/修复 | **seg.so 弹幕**：新增 `danmaku_seg.go`，手写 protobuf wire 解码 DmSegMobileReply，分页拉取 `segment_index` 1..200 并转 XML；**protobuf 解码健壮性**：varint overflow 保护（第 10 字节 `shift==63 && b>1` 报错）、skip 支持 fixed64/fixed32 wire type、midHash 属性级 XML 转义。测试计数：biliutil 60→64 |
| 2026-06-18 | 功能 | 新增 `video.go`/`playurl.go`/`danmaku.go`：view、WBI playurl、弹幕 XML API 客户端；`BrowserUA` 兼容别名、`HTTPDoer` 测试接口、`ErrNoAudioStream`/`ErrPlayURLFailed`；弹幕响应支持 deflate（zlib/raw flate）和 gzip 解压 |
| 2026-06-17 | 功能 | 新增 `videoid.go`：`ExtractVideoID`（BV 号优先，非 B 站用归一化 URL sha1 兜底）、`NormalizeSourceURL`（去 fragment/跟踪参数、幂等）；`BiliCookie.NetscapeBytes()` 把内存 cookie 序列化为 yt-dlp 可读的明文 Netscape 字节（账号池落盘是加密的，供下载场景落盘临时文件） |
| 2026-06-03 | 增量扫描 | 新增 `CreateImported(ctx, account)` 方法（跳过 Cookie 路径校验，cookie_file 设为空字符串，用于配置导入）；新增 `ClearAll(ctx)` 方法（DELETE FROM bili_cookie_accounts，用于配置导入 overwrite 策略） |
| 2026-06-01 | 测试补充 | 新增 `cookie_account_test.go`（14 用例：CRUD 全覆盖、默认账号切换/清除/删除、ValidateCookiePath 路径穿越防护 4 场景、ResolveCookie 3 场景） |
| 2026-05-23 | 安全更新 | 新增 cookie_crypto.go：Cookie 文件 AES-256-GCM 静态加密；LoadCookie/CheckCookieExpiry 自动解密，WriteNetscapeCookieFile 自动加密；新增 ErrInvalidKey 和 cookie_crypto_test.go（10 个用例） |
| 2026-05-17 | 安全修复 | 新增 ValidateCookiePath 防止路径遍历攻击；新增 ErrInvalidCookiePath 哨兵错误 |
| 2026-05-15 | 重大更新 | 新增 Cookie Account 系统（cookie_account.go：CookieAccountStore、默认下载/发布账号、ResolveCookie 解析优先级）；新增 Cookie Writer 原子写入 Netscape Cookie 文件；QR Login 通过 login.go/QRCodeManager 提供 Create/Poll/GetSucceeded/Delete/CleanupExpired，并支持保存为全局账号 |
| 2026-05-12 | 重大更新 | 新增 WBI URL 签名功能（wbi.go：WBISigner、URLSigner 接口、密钥获取/缓存/签名计算、ErrWBIKeyUnavailable/ErrRiskControl 错误）；新增统一 User-Agent 常量（ua.go：BiliUserAgent）；新增 wbi_test.go（12 个测试用例） |
| 2026-05-02 | 初始化 | 首次生成模块文档（从 publisher/cookie.go 提取为独立模块） |
