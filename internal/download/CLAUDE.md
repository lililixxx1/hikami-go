[根目录](../../CLAUDE.md) > **internal/download**

# internal/download -- 回放音频下载

## 模块职责

下载 B 站回放的最佳音频、弹幕和元数据，支持 yt-dlp 后端与 Go 原生 native 后端。yt-dlp 支持单 P 和多 P（播放列表）视频，多 P 场景下自动合并音频、提取各 P 弹幕和时长信息；native 支持单 P 和多 P BV 视频音频 + 弹幕 + 元数据，弹幕策略为 seg.so 优先、XML 回退。下载完成后自动排队标准化任务。

## 入口与启动

- **入口文件**: `download.go`, `native.go`, `downloader_select.go`
- **核心类型**: `Handler`, `YTDLPDownloader`, `NativeDownloader`, `AutoDownloader`
- **任务类型**: `download`
- **测试总数**: 48（按 `grep -c "^func Test" internal/download/*_test.go` 统计；probe_test.go 的 7 个用例受 `//go:build probe` 保护，常规测试不编译但仍计入函数定义）

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewHandler(cfg, sessions, states, workers, downloader, channels)` | 创建 Handler |
| `Register(pool)` | 注册 download 任务处理器 |
| `Enqueue(ctx, sessionID)` | 对已存在场次创建并排队下载任务（重跑/重试用） |
| `CreateFromURL(ctx, channelID, url)` | 从用户粘贴的视频链接（BV 号等）创建下载场次并入队，复用 download → normalize → asr → recap 管道；同 BV 重复提交返回 `worker.ErrTaskConflict` |
| `SetCookieAccountStore(store)` | 注入账号池，使 HandleTask 经 `ResolveCookie` 解析 cookie（账号池 → 默认下载账号 → 主播 legacy `download_cookie_file`）；未注入则退化为只用 legacy 路径 |
| `NewConfiguredDownloader(cfg)` | 根据 `downloader.backend` 选择 `AutoDownloader` / `NativeDownloader` / `YTDLPDownloader` |
| `NativeDownloader.SetCookie(cookie)` | 注入内存 Cookie，供后续 native 专用路径绕过临时 cookie 文件 |

**接口：**

```go
type Downloader interface {
    Download(ctx context.Context, sourceURL string, rawDir string, cookieFile string) error
}
```

**NativeDownloader 字段：**

| 字段 | 说明 |
|------|------|
| `FFmpeg` | ffmpeg 可执行文件路径，用于 native 多 P 音频 concat |
| `FFprobe` | ffprobe 可执行文件路径，用于 native/yt-dlp 多 P 时长探测 |

## 关键依赖与配置

- 外部工具: yt-dlp, ffprobe, ffmpeg
- 依赖: config, session.Store, state.Store, worker.Pool, normalize (后续排队), biliutil (view/playurl/danmaku/seg.so/WBI/Cookie)
- 日志上下文: 下载任务传播 `channel_id`、`session_id`，下载开始/完成日志包含源地址和产物大小
- `downloader.backend`: `auto`（默认，native 优先，不支持时回退 yt-dlp）、`native`（显式原生单后端）、`ytdlp`（显式 yt-dlp 单后端）
- `NewConfiguredDownloader` 使用 `cfg.FFmpeg` / `cfg.FFprobe` 构造下载器；main 启动时已通过 `runtime.ResolveFFmpeg` 写入 cfg，因此工厂函数不再做额外解析，保持纯选择逻辑。

## 任务流程

1. 获取场次信息
2. 提交 `download_started` 事件
3. **解析下载 cookie**：注入了 `CookieAccountStore` 时经 `ResolveCookie`（账号池 → 默认下载账号 → 主播 legacy `download_cookie_file`），返回的内存 cookie 落盘成 yt-dlp 可读的明文 Netscape 文件；未注入时退化为只用 `ch.DownloadCookieFile`
4. 创建 `raw/` 目录
5. 调用配置的下载后端：`auto` 先尝试 native，遇 `ErrNativeUnsupported` 时回退 yt-dlp；显式 native/ytdlp 不回退
6. 提交 `download_succeeded` 事件
7. 排队 `normalize` 任务

**结构化日志：**
- 下载开始时记录 `channel_id`、`session_id` 和 source URL
- 下载完成时记录 `channel_id`、`session_id` 和输出文件大小，便于排查空文件、异常中断和多 P 合并结果

### 单 P 下载

- `yt-dlp --no-playlist -x --audio-format m4a --write-info-json`
- 输出: `raw/audio.m4a`, `raw/metadata.ytdlp.json`

### Native 单 P 下载

- 调 view 接口获取 `aid/bvid/title/pages`，支持单 P BV 视频；非 BV、番剧等返回 `ErrNativeUnsupported`
- 调 WBI playurl 获取 DASH 音频流，选择 bandwidth 最高的音频；baseUrl 失败时按 backupUrl 顺序回退
- 音频采用流式写入 `raw/audio.m4a.tmp`，成功后 rename 为 `raw/audio.m4a`；音频 HTTP client 使用独立 `http.Transport` 且 `ForceAttemptHTTP2=false`，避免复用全局 HTTP/2 连接池
- 拉取 `raw/danmaku.xml`，优先使用 seg.so 分段弹幕；seg.so 失败时回退 comment XML；双失败写空 `<i></i>`
- 写出 `raw/metadata.ytdlp.json`，保持 normalize 统一读取路径

### Native 多 P 下载

- 调 view 接口获取所有分 P，逐 P 下载音频到 `raw/parts/pNNN/audio.m4a`
- 每 P 弹幕优先拉取 seg.so 并写入 `raw/danmaku_parts/pNNN.xml`；seg.so 失败回退 XML；双失败写空 `<i></i>`
- 每 P 写入 `raw/metadata_parts/pNNN.info.json`，产物命名对齐 yt-dlp 多 P 路径
- 使用 ffprobe 探测各 P 时长，原子写入 `raw/part_durations.json`
- 使用 ffmpeg concat demuxer 合并为 `raw/audio.m4a`
- 清理 `raw/parts/` 和临时 `concat.list`，保留 `audio.m4a`、`danmaku_parts/`、`metadata_parts/`、`part_durations.json`

### yt-dlp 多 P 下载

1. `yt-dlp --dump-json --flat-playlist --no-download` 获取播放列表条目
2. 逐 P 下载到 `raw/parts/pNNN/` 子目录
3. 使用 ffprobe 探测各 P 时长
4. 使用 ffmpeg concat demuxer 合并为 `raw/audio.m4a`
5. 写入 `raw/part_durations.json`（供 normalize 合并弹幕使用）
6. 将各 P 的 `.info.json` 移动到 `raw/metadata_parts/pNNN.info.json`
7. 将各 P 的弹幕 XML 移动到 `raw/danmaku_parts/pNNN.xml`
8. 清理 `raw/parts/` 目录

### 共享 Helper

- `probeDuration`、`concatAudio`、`partDuration`、`partDownloadResult` 已抽为包级函数，native 和 yt-dlp 多 P 路径共用。
- `escapeConcatListPath` 将分 P 音频路径绝对化并转义后写入 ffmpeg concat listfile（`file '...'`）：避免 `OutputRoot` 为相对路径时 ffmpeg 在其当前工作目录下二次拼接导致路径翻倍（`6536b32`）。native 与 yt-dlp 多 P 合并共用。
- `part_durations.json` 通过原子写入生成，避免 normalize 读取到半写入文件。

## 数据模型

**playlistEntry（yt-dlp 播放列表条目）：**

| 字段 | 说明 |
|------|------|
| `id` | BV 号 |
| `title` | 视频标题 |
| `url` | 原始 URL |
| `webpage_url` | 网页 URL |
| `playlist_index` | 播放列表序号 |

**partDuration（多 P 时长记录）：**

| 字段 | 说明 |
|------|------|
| `index` | P 序号 |
| `dur_secs` | 时长（秒） |

**NativeDownloader 错误：**

| 错误 | 说明 |
|------|------|
| `ErrNativeCookieMissing` | native 缺少可用 Cookie |
| `ErrAudioDownloadFailed` | DASH 音频所有 URL 下载失败 |
| `ErrNativeUnsupported` | native 不支持当前来源（非 BV、多 P、番剧等）；auto 模式据此回退 yt-dlp |

## 测试与质量

- `download_test.go`: 29 个测试用例，覆盖下载辅助函数、Handler 创建/注册/入队/任务执行、临时 Cookie 文件写入、`escapeConcatListPath` 相对路径绝对化 / 绝对路径保留转义等
- `native_test.go`: 9 个测试用例，覆盖 native 成功下载、Cookie 缺失、非 BV、多 P 产物、seg.so 回退 XML、双失败空弹幕、无 pages、音频 URL 全失败、ffprobe/ffmpeg mock 与音频 HTTP client 独立 Transport/无超时/禁用 HTTP/2
- `downloader_select_test.go`: 3 个测试用例，覆盖后端工厂、auto 遇 `ErrNativeUnsupported` 回退、其他错误不回退
- `probe_test.go`: 7 个 `//go:build probe` 真实联调用例，覆盖 view/playurl/danmaku/native E2E、单 P/多 P 联调；常规测试不编译

## 相关文件清单

- `download.go` -- Handler、Downloader 接口、YTDLPDownloader、单 P/多 P yt-dlp 下载逻辑
- `native.go` -- NativeDownloader 原生 B 站单 P/多 P 下载，含音频流式 `.tmp`+rename、seg.so 优先弹幕策略、ffprobe/ffmpeg 多 P 产物生成和错误定义
- `downloader_select.go` -- NewConfiguredDownloader 与 AutoDownloader 双后端选择/fallback，复用 cfg 中已解析的 FFmpeg/FFprobe 路径
- `probe_test.go` -- `//go:build probe` 真实联调探针（view/playurl/danmaku/E2E），默认不参与常规测试
- `download_test.go` -- 既有下载 Handler 与 yt-dlp 辅助逻辑测试
- `native_test.go` -- native 下载、错误边界、音频 client/Transport 测试
- `downloader_select_test.go` -- 后端工厂与 auto fallback 测试

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-24 | 重构 | **双重降级收敛**（`5fadea4`）：移除 `download.go` HandleTask 中冗余的 `Apply(EventTaskFailed)` 调用（1 处）。任务失败降级统一由 `worker` 处理（普通任务 `EventTaskFailed` 全局特判降级；旁路任务经 `Register(..., WithBypassFailState())` 声明后仅写 `last_error`），各业务 handler 不再自行 `Apply`，避免双写。本模块无新增对外接口，测试数无变化（仍 48） |
| 2026-06-23 | 修复 | `escapeConcatListPath` 将写入 ffmpeg concat listfile 的分 P 音频路径绝对化并转义（`file '...'`），修复 `OutputRoot` 为相对路径时 ffmpeg 在 CWD 下二次拼接导致路径翻倍、concat 失败的问题（`6536b32`）；native 与 yt-dlp 多 P 合并共用。新增 2 个测试（相对路径绝对化 / 绝对路径保留转义）。download 46→48 |
| 2026-06-18 | 功能/修复 | **native 多 P 下载**：NativeDownloader 新增 FFmpeg/FFprobe 字段 + `downloadMultiP`，产物对齐 yt-dlp（`audio.m4a` / `danmaku_parts/pNNN.xml` / `metadata_parts/pNNN.info.json` / `part_durations.json`，`parts` + `concat.list` 清理）；`probeDuration`/`concatAudio`/`partDuration`/`partDownloadResult` 抽为包级共享函数（native/ytdlp 共用）；`part_durations.json` 改原子写入。**seg.so 弹幕**：native 弹幕策略改为 seg.so 优先 + XML 回退 + 双失败写空 `<i></i>`。**downloader_select**：`NewConfiguredDownloader` 用 `cfg.FFmpeg`/`cfg.FFprobe`（main 已 ResolveFFmpeg 写入 cfg），去掉冗余解析副作用（恢复纯函数）。测试计数：download 40→42 |
| 2026-06-18 | 功能 | 新增 `native.go`（NativeDownloader 单 P BV 音频+弹幕+元数据，音频流式 `.tmp`+rename，独立 HTTP/1.1 Transport）和 `downloader_select.go`（NewConfiguredDownloader/AutoDownloader：auto native 优先，遇 ErrNativeUnsupported 回退 yt-dlp）；新增 probe build tag 联调测试 |
| 2026-06-17 | 功能 | 新增 `CreateFromURL(ctx, channelID, url)` 支持单链接（BV 号等）触发下载；新增 `SetCookieAccountStore`，HandleTask cookie 解析改为经 `ResolveCookie`（账号池→默认账号→legacy 文件），并落盘临时 Netscape 文件供 yt-dlp `--cookies` 使用，修复账号池配置的主播下载无 cookie 的缺口 |
| 2026-06-05 | 日志增强 | 下载任务新增 channel_id/session_id 上下文传播；下载开始和完成日志记录 source URL、输出文件大小等关键信息 |
| 2026-05-17 | 修复 | moveInfoJSON 和 moveDanmakuXML 现在正确返回错误而非静默吞没 |
| 2026-05-01 | 重大更新 | 新增多 P 下载支持：播放列表检测、逐 P 下载、音频合并、弹幕分 P 提取 |
| 2026-04-29 | 初始化 | 首次生成模块文档（仅单 P） |
