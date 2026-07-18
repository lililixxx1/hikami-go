[根目录](../../CLAUDE.md) > **internal/live_record**

# internal/live_record -- B 站直播录制

## 模块职责

管理 B 站直播状态检查、直播音频流录制和实时弹幕采集。支持自动检查开播、手动开始/停止录制、同一主播录制互斥、录制健康检查和通知事件。录制时优先使用 Cookie Account 解析下载 Cookie，完成后自动排队标准化任务。

## 入口与启动

- **入口文件**: `manager.go` (编排), `types.go` (类型定义)
- **辅助文件**: `bilibili.go` (B 站 API), `danmaku.go` (弹幕 WebSocket), `ffmpeg.go` (ffmpeg 封装)
- **任务类型**: `live_record`

## 对外接口

### Manager

| 方法 | 说明 |
|------|------|
| `NewManager(cfg, channels, sessions, states, workers, client, audio, danmaku)` | 创建 Manager |
| `Register(pool)` | 注册 live_record 任务处理器 |
| `CheckAll(ctx)` | 检查所有主播直播状态 |
| `CheckAndStartAll(ctx)` | 检查并自动开始录制（仅 auto_record=true 的主播）。内部 `checkOne` 透传已得 `LiveInfo` 走 `startWithInfo`，省掉 `Start` 路径的二次 `getInfoByRoom`；捕获 `ErrRiskControl352` 触发频道级阶梯冷却（5/10/20m），冷却期跳过该频道的 CheckLive，成功后重置计数；CheckLive 前加 0~800ms jitter 摊开并发突发 |
| `Check(ctx, channelID)` | 检查单个主播（同样尊守 -352 频道级冷却） |
| `Start(ctx, channelID)` | 手动开始录制；`session.CreateLive` 返回 `ErrAlreadyLive` 时映射为 `ErrAlreadyRecording`（同槽已存在 session，cron `CheckAndStartAll` 走静默 no-op 兜底）。内部经 `ensureStartAllowed`（承载全部前置防护）+ `startWithInfo`（用已得的 LiveInfo 建 session+入队，不重复 CheckLive） |
| `Stop(channelID)` | 手动停止录制 |
| `StartHealthCheck(interval)` | 启动后台健康检查，默认 60s 检查录制文件是否持续增长（健康检测读 `activeRecord.CurrentOutputPath`，切换分段时重置 fileSizes/failCount 基线） |
| `SetNotifyManager(notifyMgr)` | 注入通知管理器 |
| `Adopt(ctx, channelID, sessionID, taskID, cancel)` | 服务重启时由 `worker.recoverRunning` 调用接管在跑的 ffmpeg 进程；重建 active 记录，`CurrentOutputPath` 未知时用 `globLatestAudio` 兜底取最新音频文件 |

> **-352 频道级冷却（异常 #9）**：B 站 -352 风控实为周期性脉冲（实测 8~9min/波），`CheckLive` 检测到 -352 后先 `RefreshKeys`（按 baseCookie 选 signer）+ `Invalidate(baseCookie)` 失效 buvid 缓存重试一次，仍 -352 返回 `ErrRiskControl352` 哨兵；`checkOne`/`Check` 用 `errors.Is` 识别哨兵，按 `cooldownStep[channelID]` 进阶梯冷却（5/10/20m），冷却期跳过该频道的 CheckLive，CheckLive 成功后重置 step。冷却 + jitter 把脉冲期的雪崩式硬打兜住，脉冲间即恢复（61min 实战 CheckLive 成功率 95%）。

### 接口

```go
type BiliClient interface {
    CheckLive(ctx, roomID, cookieHeader) (LiveInfo, error)   // -352 单次重试(RefreshKeys+Invalidate),仍 -352 返回 ErrRiskControl352 哨兵
    GetStream(ctx, roomID, audioOnly, cookieHeader) (StreamInfo, error)
}

type AudioRecorder interface {
    Record(ctx, stream StreamInfo, outputPath) error
}

type DanmakuRecorder interface {
    Record(ctx, roomID, outputPath) error
}

var ErrRiskControl352 = errors.New("bilibili -352 risk control")  // CheckLive 重试仍 -352 的哨兵,checkOne/Check 据此触发频道级冷却
```

> **CheckLive 的 -352 单次重试**（与 `danmaku.go:326` 范式对齐）：把 buvid 注入 + WBI 签名 + 请求封进 `query()` 闭包，首请求 -352 时按 **baseCookie**（注入前的原始 cookie，既是 `BuvidStore` 缓存 key 也是 signer 选择 key）`RefreshKeys` 刷新 WBI 密钥 + `buvids.Invalidate(baseCookie)` 失效指纹缓存，再 `query()` 一次；仍 -352 返回 `ErrRiskControl352`。WBI 签名密钥源自账号身份（nav API）不随 buvid 变，故 signer 按 baseCookie 选（codex 审核要点）。`GetStream` 同样注入共享 `BuvidStore` + WBI + Referer/Origin 加固 -352。

**API 端点：**
- `POST /api/live/check`
- `GET /api/live/status`
- `GET /api/live/:channel_id/status`
- `POST /api/live/:channel_id/record/start`
- `POST /api/live/:channel_id/record/stop`

## 关键依赖与配置

- B 站 API: `api.live.bilibili.com`
- Cookie: 通过 `CookieAccountStore.ResolveCookie` 解析下载账号；失败时回退主播/Bootstrap `download_cookie_file`
- ffmpeg: 通过 HTTP pipe 传入流数据，`-vn -c:a copy` 录制音频
- gorilla/websocket: B 站弹幕 WebSocket 连接
- 配置: `live_record.enabled`, `audio_only`, `record_danmaku`, `audio_container`, `require_audio_stream`, `fallback_extract_audio`, `stop_grace_seconds`
- 日志上下文: 录制链路传播 `channel_id`、`session_id`，直播状态确认、ffmpeg 启动和退出会写入结构化日志

## 自动录制逻辑

`CheckAndStartAll` 遍历所有主播，对满足以下条件的主播自动开始录制：
1. `live_room_id > 0` 且 `enabled = true`
2. 当前未在录制（无 active 记录且无活跃 session）
3. B 站 API 确认正在直播
4. **`auto_record = true`**（主播配置字段）

不满足 `auto_record` 的主播仅返回直播状态，不自动开始录制。

## 直播流选择策略

`selectStream` 方法默认获取混合流（audioOnly=false），由 ffmpeg 在录制时丢弃视频轨并直接拷贝音频轨：

1. 调用 `GetStream(ctx, roomID, false, cookieHeader)` 获取混合流
2. 获取失败则返回错误（不自动回退纯音频流）
3. 录制时通过 HTTP 打开直播流，pipe 到 ffmpeg stdin

## 重连循环（按错误类型分支）

`HandleTask` 的重连循环按错误来源走两条独立预算（异常 #1/#2 修复）：

- **`selectStream` 失败**（含 CheckLive 判定已下播）：走 `maxReconnect`（配置上限），首次失败前若仍在线会探测开播状态避免下播误重连
- **CDN 瞬时错误**（404 / connection reset / pipe 中断）：走独立的 `cdnRetryBudget`（固定 5 次）+ 指数退避（`cdnBackoff`），**不**消耗 `maxReconnect`，绕过对 selectStream 的依赖
- 两类重试**均不**调 `CheckLive`，避免 B 站 `live:false` 抖动误判为下播而放弃整段录制
- 健康检测读 `activeRecord.CurrentOutputPath`：重连切分段（`.part.N`）时 `updateCurrentOutputPath` 同步更新并重置 `fileSizes`/`failCount` 基线，避免分段切换被误报为"文件不增长"

## BilibiliClient 流选择细节

`GetStream` 方法解析 B 站播放信息，根据 codec 类型选择流：

- `audioOnly=true` 时只选择纯音频 codec（aac, opus, mp3 等），不可用时报错并列出可用 codec
- `audioOnly=false` 时从所有候选流中选择优先级最高的（FLV+avc > FLV > avc > 其他）
- 流 URL 拼接：`host + base_url + extra`
- 自动设置 B 站 Referer 和 Cookie 请求头

## FFmpegRecorder 录制机制

1. 通过 HTTP GET 打开直播流（携带 Cookie、Referer 等 Headers）
2. 将 HTTP Response Body pipe 到 ffmpeg 的 stdin
3. ffmpeg 参数：`-y -hide_banner -loglevel warning [-f flv] -i pipe:0 -vn -c:a copy {outputPath}`
4. FLV 格式自动检测并添加 `-f flv` 解复用器
5. 优雅停止：context 取消时发 SIGTERM（非 SIGKILL），WaitDelay 10s 让 ffmpeg 写完容器头
6. 录制停止后触发 `live_record_succeeded`

**结构化日志：**
- 直播状态检查确认开播时记录 `channel_id`、`room_id` 等上下文
- ffmpeg 录制启动时记录命令参数和已脱敏的流 URL（保留 host/path，移除敏感 query）
- ffmpeg 退出时记录进程退出码，便于区分正常停止、外部中断和异常退出

## Cookie 查找策略

`cookieHeaderForChannel` 的优先级：
1. 主播 `download_account_id` 对应的 Cookie Account
2. 全局默认下载 Cookie Account
3. 主播自身的 `download_cookie_file`
4. Bootstrap 配置中匹配的主播（按 ID/UID/LiveRoomID）
5. Bootstrap 配置中首个有 `download_cookie_file` 的主播（兜底）

Cookie Account 解析失败且不是 `ErrNoDefaultAccount` 时记录 warn，然后继续旧文件回退。

## 任务流程

1. 从任务 payload 解析 room_id
2. 设置 active 记录（互斥检查）
3. 提交 `live_record_started` 事件
4. 获取主播的 Cookie header
5. 调用 `selectStream` 获取流地址
6. 写入 `raw/live.raw.json` 元数据
7. 并发启动弹幕录制（如启用）
8. FFmpegRecorder 通过 HTTP pipe 录制音频到 `raw/audio.{container}`
9. 正常结束或手动停止时提交 `live_record_succeeded` 事件
10. 排队 `normalize` 任务

## 健康检查与通知

`StartHealthCheck` 在 `cmd/hikami/main.go` 中以默认 60s 间隔启动。后台检查当前 active 录制的 `activeRecord.CurrentOutputPath`（重连切分段后会更新为最新的 `.part.N`）文件大小：
- 文件增长时重置 `failCount`
- 文件不存在或大小不变时累加失败次数
- 连续 3 次未增长时输出 `recording unhealthy: file not growing` 警告日志
- `CurrentOutputPath` 为空（重启接管时丢失）时用 `globLatestAudio` 在 session 的 raw 目录下按 mtime 兜底取最新音频文件
- 切换分段时 `updateCurrentOutputPath` 同步重置 `fileSizes`/`failCount` 基线，避免分段边界误报

通知事件：
- `record_start`：`HandleTask` 成功进入录制状态后发送
- `record_stop`：`Stop(channelID)` 手动停止时发送

## 弹幕采集

- 通过 B 站 `getDanmuInfo` API 获取 WebSocket 地址和 Token
- **-352 风控三级回退机制**：
  1. `getDanmuInfo` 返回 `-352` 时，先刷新 WBI 密钥后重试一次
  2. 重试仍返回 `-352` 时，回退到旧版 `getDanmuConf` API（`/room/v1/Danmu/getConf`，无需 WBI 签名），解析 `host_server_list` 中 wss 端口构建地址
  3. 旧版 API 也失败时，降级到默认弹幕服务器 `wss://broadcastlv.chat.bilibili.com:2245/sub`（无 token）
- 当 Cookie header 中存在 `DedeUserID` 时作为 uid，避免从 `download_cookie_file` 推断导致 uid=0
- 通过共享的 `biliutil.BuvidStore` 获取 `buvid3`（按 cookie 缓存 24h，与 `publisher`/`channel` 共用同一套实现），并在鉴权 body 中携带 buvid 提升认证成功率
- 支持协议版本 2 (zlib) 和 3 (brotli) 的压缩消息解压
- 解析 `DANMU_MSG` 命令，提取文本、用户、颜色、发送时间戳
- 输出 JSONL 格式到 `raw/danmaku.jsonl`
- `time_ms` = `received_at - record_started_at`（有发送时间戳时使用发送时间）
- WebSocket 建连使用浏览器风格 Headers（User-Agent、Origin、Referer）
- 支持协议版本 3 (brotli) 和 2 (zlib) 压缩；连接时先尝试 protover=3，失败回退 protover=2
- host_list 每次随机打散；单个服务器失败后继续尝试剩余服务器
- 连接后等待鉴权回复 `op=8` 且 `code=0`，确认成功后才进入消息读取循环
- `RecordWithStartTime` 内置重连循环，按 2s 起步、最大 30s 的指数退避重试

## 测试与质量

- `bilibili_test.go`: 13 个测试用例，覆盖：
  - 直播状态和流信息解析
  - FLV 混合流优先选择
  - 纯音频流选择和 codec 区分
  - 风控注入头（AntiRiskHeaders）
  - AntiRisk buvid 注入
  - GetStream 风控加固
  - signer 失败时降级容错
  - **CheckLive -352 单次重试成功**（TestCheckLiveRetriesOn352ThenSucceeds）
  - **CheckLive 持续 -352 返回哨兵**（TestCheckLiveReturnsErr352OnPersistent352）
  - **CheckLive RefreshKeys 调到实际签名 signer**（TestCheckLiveRefreshKeysCalledOnActualSigningSigner）
  - **CheckLive Invalidate 用 baseCookie**（TestCheckLiveInvalidatesBaseCookieWithBuvidStore）
- `ffmpeg_test.go`: 4 个测试用例，覆盖：
  - HTTP pipe + Headers + fake-ffmpeg 参数传递
  - FFmpeg 参数构建（非 FLV 不强制 -f flv）
  - .part 临时文件不被覆盖
  - 优雅停止等待期（StopGracePeriod）
- `manager_test.go`: 51 个测试用例，覆盖：
  - 启动录制创建会话和任务
  - 重复录制拒绝
  - 离线主播拒绝
  - 任务执行写入原始产物
  - 重连录制分片拼接（.part 文件 + ffmpeg concat，`writeConcatList` 写绝对路径）
  - Cookie 文件查找（Bootstrap 回退和精确匹配）
  - selectStream 混合流/纯音频/回退/必须音频策略
  - CheckAndStartAll 跳过 replay_only 主播
  - redactURL 脱敏
  - Stop 幂等性
  - 健康检查生命周期
  - setActive/clearActive
  - **Adopt 重启接管重建 active + Stop 终止进程**（TestAdopt_RebuildsActiveAndStopTerminatesProcess）
  - **preflight CheckLive 离线/错误处理**（TestHandleTaskPreflightCheckLive*）
  - **重连按错误类型分支**：selectStream 失败走 maxReconnect、CDN 瞬时错误走独立 `cdnRetryBudget`、clean EOF 探测开播状态、reconnect 耗尽返回哨兵、`isCDNTransientError`、`cdnBackoff`
- `manager_anomaly9_test.go`: 10 个测试用例，覆盖异常 #9 的 -352 冷却机制：
  - CheckAndStartAll 对同场不重复 CheckLive（TestCheckAndStartAllDedupCheckLive）
  - 命中 -352 标记冷却（TestCheckAndStartAllMarksCooldownOn352）
  - 冷却期跳过 CheckLive（TestCheckAndStartAllSkipsCooldown352）
  - 成功后重置冷却计数（TestCheckAndStartAllResetsCooldownOnSuccess）
  - live_record 禁用时整体跳过（TestCheckAndStartAllSkipsWhenLiveRecordDisabled）
  - `ensureStartAllowed` 前置防护（TestEnsureStartAllowed）
  - `Check` 同样尊守冷却（TestCheckRespectsCooldown352 / TestCheckMarksCooldownOn352）
- `danmaku_test.go`: 11 个测试用例，覆盖：
  - 普通消息包解包
  - zlib 压缩消息包解包
  - brotli 压缩消息包解包
  - 弹幕消息内容解析（文本、用户、颜色、时间偏移）
  - getDanmuInfo Cookie 传递
  - getDanmuInfo 空 Cookie 不设置 header
  - -352 重试成功
  - -352 全部失败降级默认服务器
  - -352 旧版 getConf API 回退成功
  - buildAuthBody UID 设置
  - buildAuthBodyWithProtover 协议版本设置

## 相关文件清单

- `types.go` -- 类型定义、常量、接口、风控哨兵（`ErrRiskControl352` 业务码 -352、`ErrHTTPRiskControl` HTTP 412/403/429）、健康检测哨兵（`ErrZeroByteStalled`/`ErrRecordingNotGrowing`）
- `manager.go` -- Manager 实现、任务执行、Cookie 查找、流选择逻辑、自动录制逻辑、**风控频道级阶梯冷却**（`cooldownRiskUntil`/`applyCooldownRiskControl`/`resetCooldownRiskControl`/`cooldownStep`,原 `*352` 命名 v3 改为泛化 `RiskControl` 覆盖 -352 + HTTP 412/403/429）、`isRiskControlError` helper、`ensureStartAllowed`/`startWithInfo`、重连按错误类型分支（含 `afterRecordProbeFailReconnect` + `probeErrorBudget` 异常 #10）、`Adopt`/`globLatestAudio`/`updateCurrentOutputPath`、**健康检测**（`checkOneChannelHealth` 分发 + `healthStats` 聚合 map + `applyHealthStat`/`peekAbortReason`/`markAbort`/`handleSelectStreamRiskControl`，异常 #11）
- `bilibili.go` -- BiliClient 实现、B 站 API 交互、流优先级算法、CheckLive -352/HTTP 风控单次重试（RefreshKeys + Invalidate）、`httpStatusError`（412/403/429→`ErrHTTPRiskControl`）、`signerForCookie`/`injectAntiRisk`
- `danmaku.go` -- 弹幕 WebSocket 协议、BilibiliDanmakuRecorder、消息解包、-352 三级回退
- `ffmpeg.go` -- FFmpegRecorder 实现、HTTP pipe 机制、FLV 自动检测、优雅停止
- `bilibili_test.go` -- B 站客户端测试（含 -352 重试/哨兵/HTTP 风控测试）
- `ffmpeg_test.go` -- FFmpeg 录制器测试
- `manager_test.go` -- Manager 集成测试（含重连按错误类型分支、preflight、Adopt、异常 #10/#11 健康检测）
- `manager_anomaly9_test.go` -- 异常 #9 的 -352 频道级冷却测试（10 个用例）
- `danmaku_test.go` -- 弹幕消息解包、-352 回退和解析测试

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-18 | 修复 | **ffmpeg 子进程闪窗**（branch `fix/investigations-2026-07-18`，codex 计划+执行审核 APPROVED）。桌面模式（`-H windowsgui`）下录制启动时 ffmpeg 窗口常驻、分段合并时 ffmpeg 窗口闪现。本模块 3 处调用点加 `executil.HideWindow(cmd)`（在 `exec.CommandContext` 之后、`Start()`/`Run()` 之前）：① `ffmpeg.go:41` 录制长子进程（`Record`，跟随 `cmd.Cancel = SIGTERM` 优雅停止——两者正交，`HideWindow` 设 CreationFlags、`Cancel` 走 `Process.Signal`，互不影响；`HideWindow` 建议紧跟 CommandContext 之后便于阅读，放 Cancel 前后行为不变）；② `manager.go:1337` `runFFmpegConcat` var（分段合并，内调 `Run()`）；③ `manager.go:1362` `RecordWithProcessStart`（测试注入点，内调 `Start()`，含 `cmd.Cancel = SIGTERM`）。helper 见新增包 `internal/executil/`。无新增测试（helper 在非 Windows 是 no-op；测试桩 `RecordWithProcessStart`/`runFFmpegConcat` 是 var 替换真实 exec，加 helper 不影响桩逻辑）。live_record 测试 89 不变。 |
| 2026-07-07 | 修复 | **录播稳定性 异常 #10/#11 + P2**（`7c9ae23`/`a35cbbb`/`ce4b0ba`，计划 v1→v4 四轮 codex APPROVED）。① **异常 #10（重连死循环）**：`decideAfterRecord` default 分支不再"attempt anyway"，改返回 `afterRecordProbeFailReconnect`，重连循环用独立 `probeErrorBudget=1` 控制；耗尽校验 `hasRecordedAudio`：有→成功收尾、无→失败路径（避免空音频污染 normalize）。② **异常 #11（0 字节僵尸 + NotGrowing）**：`healthStats` 聚合 map 替换 fileSizes/failCount，`checkRecordingHealth` 拆 `checkOneChannelHealth` 分发 + `applyHealthStat` 锁内闭包（消除循环内 defer 自锁）；0 字节 2 次→`ErrZeroByteStalled`→失败路径；`failCount>=3`→`ErrRecordingNotGrowing`→成功保留音频（行为变更）；`markAbort` 只 Cancel，`peekAbortReason` 只读不 clearActive。③ **P2（HTTP 412/403/429 风控冷却）**：新哨兵 `ErrHTTPRiskControl`，`getJSON` 经 `httpStatusError` 识别风控码（5xx 不冷却），CheckLive 单次重试；`isRiskControlError` 统一识别；**全部 6 个 CheckLive 调用点 + GetStream 经 selectStream**（首次 + 重连 3 处，抽 `handleSelectStreamRiskControl`）覆盖冷却/快速失败；preflight 风控不乐观放行；decideAfterRecord 风控冷却为独立副作用（不改 #10 决策）；**改名** `*352*`→`*RiskControl*`（泛化）。新增 17 测试（manager_test +8、bilibili_test +3 含 `newStatusSequenceServer`、manager_anomaly9_test +2、#10/#11/P2 各覆盖 + 执行复审新增 2 确定性测试含前序有效音频保留）、types.go +3 哨兵。live_record 总测试 72→89 |
| 2026-07-06 | 修复 | **异常 #9:scheduler 批量 CheckLive 触发 -352 频率风控**（`f13c854`）。① **CheckLive 单次重试**：把 buvid 注入+WBI 签名+请求封进 `query()` 闭包，首请求 -352 时按 **baseCookie**（BuvidStore 缓存 key + signer 选择 key，注入前的原始 cookie）`RefreshKeys` 刷 WBI 密钥 + `buvids.Invalidate(baseCookie)` 失效指纹缓存后重试一次，仍 -352 返回新哨兵 `ErrRiskControl352`（types.go）。WBI 密钥源自账号身份不随 buvid 变，故 signer 按 baseCookie 选（codex 审核要点）。② **频道级阶梯冷却**：`cooldown352Until`/`cooldownStep`（5/10/20m），`checkOne`/`Check` 用 `errors.Is(err, ErrRiskControl352)` 识别哨兵触发冷却，冷却期跳过该频道的 CheckLive，CheckLive 成功后 `delete(cooldownStep)` 重置。③ **jitter**：CheckLive 前 0~800ms 随机抖动（放在 activeFor/冷却 早退之后、CheckLive 之前），摊开并发突发。④ 抽 `ensureStartAllowed`+`startWithInfo`：`checkOne` 透传已得 info 走 `startWithInfo`，省掉 Start 路径冗余的二次 `getInfoByRoom`。**部署验证**（61min 实战，3 路录制）：CheckLive 成功率 95%（3470/3645），B站 -352 实为周期性脉冲（8~9min/波），修复前雪崩式硬打致 100% 持续失败，修复后脉冲期冷却兜住、脉冲间即恢复，0 ERROR。新增 `manager_anomaly9_test.go`（8 用例）、bilibili_test +4（-352 重试/哨兵/RefreshKeys/Invalidate）、types.go +`ErrRiskControl352`。biliutil 新增配套 `BuvidStore.Invalidate`。live_record 总测试 64→72 |
| 2026-07-06 | 修复 | **录播稳定性 8 个异常**（`3ae2435`）：① worker.recoverRunning 恢复重启后孤儿 **pending** 任务（只入队不递增 attempt，超限 MarkFailed + syncSessionState 同步 session 状态，解除 discovered→ActiveLiveForChannel 误判 active→scheduler 死锁跳过的死锁）；② CheckAndStartAll 的 checkOne 对 CheckLive/Start 失败打 WARN 不再静默吞；③ BilibiliClient 注入共享 BuvidStore + WBI 签名 + Referer/Origin，加固 CheckLive/GetStream 的 -352（降级容错）；④ **删除 `worker.live_record_num` 死配置项**（调度器从不读它，worker.num 为唯一并发旋钮）；⑤ 重连循环重构：按 err 类型分支（selectStream 失败走 maxReconnect / CDN 瞬时错误走独立 `cdnRetryBudget=5` + 指数退避），两类重试均不调 CheckLive 避免 `live:false` 抖动误判；⑥ 健康检测 activeRecord 加 `CurrentOutputPath`，切换分段时重置 fileSizes/failCount 基线；⑦ `globLatestAudio` 兜底 Adopt 重启接管时恢复 CurrentOutputPath；⑧ `updateCurrentOutputPath` 切分分段同步更新。manager_test 17→38（+21：Adopt、preflight CheckLive、重连按错误类型分支全套、isCDNTransientError、cdnBackoff）、bilibili_test 3→11（+风控注入/GetStream 风控/signer 降级）、worker_test +2（pending 恢复）。live_record 总测试 36→64 |
| 2026-07-05 | 重构 | **buvid3 拉取下沉到共享组件**：`danmaku.go` 本地 `buvids` map/`buvidsMu`/`buvidURL`/`cachedBuvid`/`getBuvidConf` 缓存逻辑删除，改用 `biliutil.BuvidStore`（`getBuvidConf` 瘦身为调 `r.buvids.GetBuvids` 取 buvid3）。`BilibiliDanmakuRecorder` 字段 `buvids map[string]cachedBuvid` → `buvids *biliutil.BuvidStore`，构造时 `NewBuvidStoreWithHTTPClient(r.httpClient)` 复用同一 client。**行为等价**，nil-safe（测试 helper `newTestRecorder` 字面量构造未注入时 `GetBuvids` 返回空串，等价于旧的 `buvidURL==""` 短路）。测试数无变化 |
| 2026-06-27 | 修复 | **下播竞态配合**（`d7a1346`）：`Start` 在 `session.CreateLive` 返回 `ErrAlreadyLive` 时映射为 `ErrAlreadyRecording`，让 cron 的 `CheckAndStartAll` 走既有兜底分支静默 no-op（同一场不重复录）。竞态根因防护在 `session.CreateLive` 的同槽 UNIQUE 约束（移除旧 failed→discovered 复用），本模块仅消费其语义。测试数无变化（仍 36） |
| 2026-06-24 | 重构 | **双重降级收敛**（`5fadea4`）：移除 `manager.go` 中冗余的 `Apply(EventTaskFailed)` 调用（4 处，覆盖录制启动失败/健康检查异常等路径）。任务失败降级统一由 `worker` 处理（普通任务 `EventTaskFailed` 全局特判降级；旁路任务经 `Register(..., WithBypassFailState())` 声明后仅写 `last_error`），各业务 handler 不再自行 `Apply`，避免双写。本模块无新增对外接口，测试数无变化（仍 36） |
| 2026-06-23 | 修复 | `writeConcatList` 重连分片拼接的 concat listfile 写绝对路径（`6536b32`），与 download 模块 `escapeConcatListPath` 同源问题——避免相对 `OutputRoot` 时 ffmpeg 在 CWD 下二次拼接导致路径翻倍。新增 TestWriteConcatListWritesAbsolutePaths。manager_test.go 16→17，live_record 总测试 35→36 |
| 2026-06-10 | 增量扫描 | danmaku.go 新增 `getDanmuConf` 旧版 API 回退（/room/v1/Danmu/getConf，无需 WBI 签名），getDanmuInfo 遇到 -352 时执行三级回退（WBI 刷新重试 -> getConf -> 默认服务器）；新增 brotli 压缩消息解压（协议版本 3）；protover 支持先 3 后 2 回退；parseSendTime 支持弹幕原始发送时间戳。danmaku_test.go 10->11（新增 getDanmuInfo Cookie 传递/空 Cookie/-352 重试/-352 降级/-352 getConf 回退/buildAuthBody UID/buildAuthBodyWithProtover）；manager_test.go 15->16（新增重连分片拼接、selectStream 多策略、CheckAndStartAll 跳过 replay_only、redactURL、Stop 幂等、健康检查生命周期、setActive/clearActive）；ffmpeg_test.go 3->4（新增 StopGracePeriod）。live_record 总测试 31->34 |
| 2026-06-05 | 修复/日志 | 弹幕 WebSocket 修复 uid=0：从 Cookie header 提取 DedeUserID；新增 buvid3 获取与 24h 缓存，鉴权 body 携带 buvid；连接前随机 host_list，失败遍历所有服务器；增加浏览器风格 WS Headers；鉴权回复需等待 op=8/code=0；RecordWithStartTime 增加 2s-30s 指数退避重连。直播录制链路新增结构化日志：直播状态确认、ffmpeg 启动命令与脱敏 URL、ffmpeg 退出码，并传播 channel_id/session_id |
| 2026-05-17 | 安全修复 | 新增 redactURL() 流 URL 脱敏（去除 query/fragment/user）；元数据文件权限 0o600；StartHealthCheck 接受 context 参数；新增 StopHealthCheck() 优雅关闭；healthCancel context.CancelFunc 字段 |
| 2026-05-15 | 重大更新 | main.go 激活 StartHealthCheck(0)，默认 60s 监控 active 录制文件增长；新增 SetNotifyManager 并发送 record_start/record_stop；集成 CookieAccountStore.ResolveCookie，下载 Cookie 优先主播账号/默认账号，再回退旧 download_cookie_file |
| 2026-05-08 | 更新 | manager_test 增至 7 个测试用例（新增 Cookie 查找精确匹配优先级测试、selectStream 混合流测试） |
| 2026-05-07 | 更新 | 新增 danmaku_test.go（3 个测试用例），弹幕采集文档完善（zlib 压缩支持） |
| 2026-05-03 | 更新 | CheckAndStartAll 尊重 auto_record 字段、FFmpegRecorder 优雅停止（SIGTERM + WaitDelay） |
| 2026-05-02 | 重大更新 | 接口签名增加 cookieHeader、Cookie 查找策略、HTTP pipe 录制、新增 3 个测试文件、新增 biliutil 依赖 |
| 2026-05-01 | 更新 | 完善 selectStream 流选择策略，支持 fallback_extract_audio 配置 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
