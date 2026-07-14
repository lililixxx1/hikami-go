[根目录](../../CLAUDE.md) > **internal/runtime**

# internal/runtime -- 外部工具与能力探测、FFmpeg 自动解析、磁盘/Cookie 健康检查

## 模块职责

在服务启动时探测外部工具（ffmpeg, ffprobe, yt-dlp, rclone, claude, codex）的可用性，综合判断系统能力（回放下载、ASR 提交、回顾生成、WebDAV 上传、专栏发布），暴露健康检查接口。硬依赖缺失时阻止启动。同时暴露配置状态（API Key 是否设置、各子系统是否配置完整）。提供平台感知的外部工具安装提示、跨平台磁盘使用检查和 Cookie 过期检查。

**FFmpeg 自动解析/下载/嵌入系统**：当系统 PATH 中未找到 ffmpeg/ffprobe 时，按以下优先级自动解析：
1. 系统路径查找（`exec.LookPath`）
2. 嵌入式 FFmpeg（通过 `embed_ffmpeg` build tag 编译时嵌入 `assets/ffmpeg.zip`）
3. 在线下载并安装到 `.runtime/ffmpeg/{platform}/{version}/` 目录

支持 Linux (amd64/arm64) 和 Windows (amd64) 三平台，自动解压 zip/tar.xz 归档，SHA256 校验。

## 入口与启动

- **入口文件**: `probe.go`, `health.go`, `ffmpeg_resolver.go`, `ffmpeg_manifest.go`, `ffmpeg_embed.go`, `ffmpeg_embed_none.go`, `disk_unix.go`, `disk_windows.go`
- **核心函数**: `Probe(cfg) *Status`, `Status.StartupError() error`, `ResolveFFmpeg(ctx, cfg) (*Resolution, error)`, `CheckDiskUsage(paths) []DiskInfo`, `CheckCookieExpiry(ctx, channelStore) []CookieWarning`

## 对外接口

| 函数/方法 | 说明 |
|-----------|------|
| `Probe(cfg)` | 探测所有工具，返回 `*Status` |
| `Status.StartupError()` | 硬依赖缺失时返回 error |
| `ResolveFFmpeg(ctx, cfg)` | FFmpeg 自动解析：系统路径 -> 嵌入资源 -> 在线下载 |
| `CurrentManifest()` | 返回各平台的 FFmpeg 资源清单（URL、路径、格式） |
| `PlatformKey()` | 返回当前平台的 `{os}-{arch}` 标识 |
| `CheckDiskUsage(paths)` | 跨平台磁盘使用检查（Linux/darwin: syscall.Statfs; Windows: GetDiskFreeSpaceEx） |
| `CheckCookieExpiry(ctx, channelStore)` | 检查所有启用主播的 Cookie 过期情况（7 天内过期或已过期） |
| `Status` 结构体 | JSON 序列化后通过 `/api/health/runtime` 返回 |

**Status 结构体：**

- `Tools`: map，每个工具的名称、路径、是否必需、是否可用、错误信息、安装提示
- `Capabilities`: `ReplayDownload`, `ASRSubmit`, `ASRModel`, `ASRRequestMode`, `RecapGenerate`, `WebDAVUpload`, `PublishOpus`, `Reason`
- `ConfigStatus`: 配置完整性快照（见下方数据模型）
- `CookieWarnings`: Cookie 过期警告列表（通过 `CheckCookieExpiry` 填充）
- `DiskUsage`: 磁盘使用信息列表（通过 `CheckDiskUsage` 填充）

## FFmpeg 自动解析系统

### ffmpeg_resolver.go -- 核心解析逻辑

`ResolveFFmpeg(ctx, cfg)` 三级解析链：

1. **系统路径**：`exec.LookPath(cfg.FFmpeg)` / `exec.LookPath(cfg.FFprobe)`
2. **缓存检查**：检查 `.runtime/ffmpeg/{platform}/{version}/` 目录是否存在有效二进制
3. **嵌入式资源**：`embedAssets()` 读取编译时嵌入的 zip（仅 `embed_ffmpeg` build tag）
4. **在线下载**：`downloadAndInstallFFmpeg(ctx, versionDir, asset)` 下载归档到临时目录并解压

**关键函数：**

| 函数 | 说明 |
|------|------|
| `ResolveFFmpeg(ctx, cfg)` | 主入口，按优先级尝试解析 |
| `resolveSystemFFmpeg(cfg)` | 系统 PATH 查找 |
| `cachedResolution(versionDir, asset, source)` | 检查本地缓存 |
| `installEmbeddedFFmpeg(data, versionDir, asset)` | 解压嵌入资源 |
| `downloadAndInstallFFmpeg(ctx, versionDir, asset)` | 在线下载并安装 |
| `downloadFile(ctx, url, destPath, expectedSHA256)` | HTTP 下载 + SHA256 校验 + 进度日志 |
| `extractArchive(reader, size, format, destDir)` | 解压归档（zip/tgz） |
| `safeJoin(root, name)` | 防目录穿越安全检查 |
| `finalizeInstall(tmpDir, versionDir, asset)` | 原子安装（临时目录 rename） |

### ffmpeg_manifest.go -- 平台资源清单

`CurrentManifest()` 返回 `map[string]FFmpegAsset`，key 为 `{os}-{arch}`：

| 平台 | 归档格式 | FFmpeg 源 |
|------|----------|-----------|
| linux-amd64 | tar.xz | BtbN/FFmpeg-Builds（在线下载，系统 ffmpeg 优先） |
| linux-arm64 | tar.xz | BtbN/FFmpeg-Builds（在线下载，系统 ffmpeg 优先） |
| windows-amd64 | zip | **裁剪版嵌入**（`assets/ffmpeg.zip`，`FFmpegPath=bin/ffmpeg.exe`、`ArchiveURL` 留空防误下完整版） |

**FFmpegAsset 结构体：**

| 字段 | 说明 |
|------|------|
| `Version` | 版本标识（`embedded-minimal-7.x`，标识嵌入的是裁剪版 ffmpeg）；改 Version 会让旧用户升级后重新解包，避免用到旧缓存 |
| `ArchiveURL` | 归档下载 URL |
| `ArchiveFormat` | 归档格式（zip/txz） |
| `FFmpegPath` | 归档内 ffmpeg 二进制相对路径 |
| `FFprobePath` | 归档内 ffprobe 二进制相对路径 |
| `ArchiveSHA256` | SHA256 校验值（可选） |

### ffmpeg_embed.go / ffmpeg_embed_none.go -- 构建标签条件编译

- `ffmpeg_embed.go`：`//go:build embed_ffmpeg`，嵌入 `assets/ffmpeg.zip`
- `ffmpeg_embed_none.go`：`//go:build !embed_ffmpeg`，返回 `nil, false`（默认构建）

**裁剪版 ffmpeg**：`assets/ffmpeg.zip` 是裁剪版（约 8-12MB），仅含本项目用到的音频
demuxer/muxer（flv/concat/mov/mp3）+ mp3/aac encoder，由 `scripts/build-ffmpeg-minimal.sh`
交叉编译产出（Docker + MinGW-w64）。录制/合并路径全走 `-c:a copy`（零编码器），仅 normalize
需 mp3 encoder、importer 需 aac encoder，故裁剪空间极大（完整 BtbN gpl 版约 80MB）。
`scripts/verify-ffmpeg-minimal.sh` 逐条复刻代码里的 ffmpeg 调用参数验证产物合格。详见
`scripts/README-ffmpeg-build.md`。裁剪版进版本库（`.gitignore` 白名单放行 `ffmpeg.zip`），
让 `make build-windows-amd64` 开箱即用。

**Resolution 结构体：**

| 字段 | 说明 |
|------|------|
| `FFmpegPath` | 解析后的 ffmpeg 可执行路径 |
| `FFprobePath` | 解析后的 ffprobe 可执行路径 |
| `Source` | 解析来源标识：system/cached/embedded/downloaded |

## 关键依赖与配置

- 依赖 `os/exec.LookPath` 探测工具路径
- 依赖 `os.Getenv` 检查 API Key 是否设置
- 硬依赖: ffmpeg, ffprobe（可通过自动解析安装）
- 按需依赖: yt-dlp, rclone
- AI Provider: 支持 `openai_compatible`、`anthropic`（HTTP API）、`claude_cli`、`codex_cli`（本地 CLI）
- 平台感知安装提示: Linux（apt/yum/pip/curl）和 Windows（winget/choco）
- Cookie 过期检查依赖 `internal/biliutil.CheckCookieExpiry`
- 磁盘检查: Linux/darwin 使用 `syscall.Statfs`，Windows 使用 `kernel32.GetDiskFreeSpaceEx`
- FFmpeg 自动解析依赖 `config.OutputRoot`（作为 `.runtime/ffmpeg/` 存储目录）

## 数据模型

**ToolStatus 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 工具名称 |
| `path` | string | 工具可执行路径 |
| `required` | bool | 是否为硬依赖 |
| `available` | bool | 是否是否可用 |
| `error` | string | 不可用原因 |
| `install_hint` | string | 平台感知的安装提示 |

**CookieWarning 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `channel_id` | string | 主播 ID |
| `channel_name` | string | 主播名称 |
| `cookie_type` | string | "publish" 或 "download" |
| `expires_at` | string | 过期时间 |
| `days_left` | int | 剩余天数 |
| `is_expired` | bool | 是否已过期 |

**DiskInfo 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `path` | string | 磁盘路径（绝对路径） |
| `total_gb` | float64 | 总容量 GB |
| `used_gb` | float64 | 已使用 GB |
| `free_gb` | float64 | 可用 GB |
| `used_percent` | float64 | 使用百分比 |

**能力依赖：**

| 能力 | 依赖条件 |
|------|----------|
| `ReplayDownload` | yt-dlp 可用 |
| `ASRSubmit` | asr_temp 配置完整（rclone 或本地 HTTP 服务） + DashScope API Key 已设置 |
| `RecapGenerate` | recap_ai provider 配置有效 |
| `WebDAVUpload` | WebDAV 配置完整（rclone 远端或原生 WebDAV URL） |
| `PublishOpus` | publish.enabled 为 true |

**ConfigStatus 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `dashscope_key_set` | bool | DashScope API Key 环境变量是否已设置 |
| `dashscope_key_env` | string | DashScope API Key 环境变量名 |
| `asr_temp_configured` | bool | ASR 临时公开配置是否完整 |
| `recap_provider` | string | 回顾生成 Provider 类型 |
| `recap_key_set` | bool | 回顾生成 API Key 是否已设置 |
| `recap_key_env` | string | 回顾生成 API Key 环境变量名 |
| `recap_model` | string | 回顾生成模型名称 |
| `webdav_configured` | bool | WebDAV 远端是否配置 |
| `publish_enabled` | bool | 专栏发布是否启用 |

**安装提示映射：**

| 工具 | Linux | Windows |
|------|-------|---------|
| ffmpeg | `apt install ffmpeg` | `winget install ffmpeg` |
| ffprobe | `apt install ffmpeg` | `winget install ffmpeg` |
| yt-dlp | `pip install yt-dlp` | `winget install yt-dlp` |
| rclone | `curl https://rclone.org/install.sh \| sudo bash` | `winget install rclone` |

## 测试与质量

- `probe_test.go`: 1 个测试用例，覆盖 ASR 模型和请求模式探测。
- `health_test.go`: 8 个测试用例，覆盖：
  - Cookie 过期检查: NoCookieFile（无文件无警告）、Expired（已过期警告）、ExpiringSoon（3 天内过期）、Valid（有效不警告）、MultipleChannels（多主播混合）、DisabledChannel（禁用主播跳过）
  - 磁盘检查: LowUsage（基本信息验证）、DeduplicatesPaths（路径去重）
- `ffmpeg_resolver_test.go`: 15 个测试用例，覆盖：
  - safeJoin: 正常路径、目录穿越防御、绝对路径防御、多层穿越防御
  - executableFile: 普通文件、目录、不存在
  - extractArchive: zip 格式解压、不支持格式报错
  - ffmpegVersionDir: 路径格式验证
  - extractZip/extractTgz: 路径穿越条目拦截
  - cachedResolution: 缓存缺失、缓存命中成功
  - 并发安全: lastFFmpegResolution 并发读写

## 相关文件清单

- `probe.go` -- 工具探测核心（Probe、probeTool、probeRecapProvider、getInstallHint、CookieWarning、DiskInfo 类型定义）
- `health.go` -- Cookie 过期检查（CheckCookieExpiry、checkCookieFile）
- `ffmpeg_resolver.go` -- FFmpeg 自动解析核心（ResolveFFmpeg 三级回退、下载、解压、安装、安全检查）
- `ffmpeg_manifest.go` -- FFmpeg 多平台资源清单（CurrentManifest、PlatformKey、FFmpegAsset）
- `ffmpeg_embed.go` -- 嵌入式 FFmpeg 实现（build tag: embed_ffmpeg）
- `ffmpeg_embed_none.go` -- 嵌入式 FFmpeg 空实现（build tag: 默认）
- `disk_unix.go` -- Linux/darwin 磁盘使用检查（CheckDiskUsage，syscall.Statfs）
- `disk_windows.go` -- Windows 磁盘使用检查（CheckDiskUsage，GetDiskFreeSpaceEx）
- `probe_test.go` -- 单元测试（1 个用例）
- `health_test.go` -- 健康检查测试（8 个用例）
- `ffmpeg_resolver_test.go` -- FFmpeg 解析器测试（15 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-13 | 裁剪版 ffmpeg + manifest 路径修复 | `build-windows-amd64` 嵌入的 ffmpeg 从 BtbN 完整 gpl 版(~80MB)改为裁剪版(~8-12MB)。新增 `scripts/build-ffmpeg-minimal.sh`(Docker+MinGW-w64 交叉编译,`--disable-everything` 后白名单启用 flv/concat/mov/mp3 demuxer/muxer + mp3/aac encoder,依据:录制全 `-c:a copy` 零编码器)+ `scripts/verify-ffmpeg-minimal.sh`(逐条复刻真实参数)+ `scripts/README-ffmpeg-build.md`。`ffmpeg_manifest.go` Version 改 `embedded-minimal-7.x`(新缓存目录隔离旧完整版)。`.gitignore` 白名单放行 `assets/ffmpeg.zip`(入库让 Windows 构建开箱即用)。Makefile 新增 `build-ffmpeg-minimal`/`verify-ffmpeg-minimal` target。未改任何解析逻辑(ResolveFFmpeg/installEmbeddedFFmpeg/probe.go 原样复用)。**manifest 路径同步修复**(`4a79b44`)：裁剪版 zip 顶层是 `bin/ffmpeg.exe`，但 manifest 的 `windows-amd64` 段仍写死 BtbN 完整版目录结构 `ffmpeg-master-latest-win64-gpl-shared/bin/ffmpeg.exe` → 解包后按 manifest 找不到二进制 → 启动 health check fatal（Windows 双击 exe 看似闪退）。修复：`FFmpegPath`→`bin/ffmpeg.exe`、`FFprobePath`→`bin/ffprobe.exe`、`ArchiveURL` 删除（留空防误下 80MB 完整版，`downloadAndInstallFFmpeg` 对空 URL 有显式保护兜底）。`linux-*` 不动（走系统 ffmpeg）。 |
| 2026-06-04 | 测试补充 | 新增 ffmpeg_resolver_test.go（15 用例）：safeJoin 安全检查 4 个、executableFile 3 个、extractArchive 2 个、ffmpegVersionDir 1 个、extractZip/extractTgz 穿越拦截 2 个、cachedResolution 2 个、并发安全 1 个。总用例从 9 增至 24 |
| 2026-06-03 | 重大更新 | 新增 FFmpeg 自动解析/下载/嵌入系统：ffmpeg_resolver.go（ResolveFFmpeg 三级回退：系统 -> 嵌入 -> 在线下载）、ffmpeg_manifest.go（三平台资源清单）、ffmpeg_embed.go（embed_ffmpeg build tag 嵌入）、ffmpeg_embed_none.go（默认空实现）；支持 zip/tar.xz 解压、SHA256 校验、路径穿越防护、原子安装、下载进度日志 |
| 2026-06-01 | 测试补充 | 新增 `health_test.go`（8 用例）：Cookie 过期检查 6 个场景（无文件/已过期/即将过期/有效/多主播混合/禁用跳过）+ 磁盘检查 2 个场景（基本验证/路径去重） |
| 2026-05-15 | 增量更新 | 发现并记录遗漏文件：health.go（CheckCookieExpiry Cookie 过期检查）、disk_unix.go（Linux/darwin 磁盘使用检查）、disk_windows.go（Windows 磁盘使用检查）；CookieWarning/DiskInfo 类型定义位于 probe.go |
| 2026-05-14 | 更新 | ToolStatus 新增 InstallHint 字段；新增 getInstallHint 函数（基于 runtime.GOOS 返回平台感知安装提示）；新增 linuxInstallHints 和 windowsInstallHints 映射表；probeTool 在工具不可用时填充 InstallHint |
| 2026-05-04 | 更新 | ConfigStatus 新增 glossary_configured、glossary_path 字段、新增 probe_test.go |
| 2026-05-03 | 更新 | 新增 PublishOpus 能力、ConfigStatus 结构体 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
