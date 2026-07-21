[根目录](../../CLAUDE.md) > **internal/asr**

# internal/asr -- ASR 转写

## 模块职责

提交 ASR 转写任务，将 ASR 标准音频转写为文本。支持两种实现：DashScope API（线上）和 LocalTranscriber（本地占位）。转写完成后生成 transcript.txt、transcript.srt、segments.json，并根据 ASR segments 校正标准弹幕时间。

**ASR 临时音频发布**：支持三级后端将 ASR 音频发布为 DashScope 可访问的公开 URL（按优先级）：
1. **本地 HTTP 服务**（`TempAudioServer`）：当 `ASRTempConfig.NativeConfigured()` 为 true 时使用，通过本地 HTTP 文件服务器提供音频访问
2. **S3 兼容对象存储**（`S3Publisher`）：当 `ASRS3Config.Configured()` 为 true 时使用，通过 minio-go SDK 上传到 S3 兼容存储
3. **rclone 远端**：传统方式，使用 rclone copyto 上传到远端存储

## 入口与启动

- **入口文件**: `asr.go`, `dashscope.go`, `temp_server.go`, `s3_publisher.go`, `public_ip.go`, `danmaku_correction.go`
- **任务类型**: `asr`

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewHandler(cfg, sessions, states, transcriber)` | 创建 Handler |
| `CreateTask(ctx, pool, sessionID)` | 校验前置条件并创建任务 |
| `Register(pool)` | 注册 asr 任务处理器 |

**接口：**

```go
type Transcriber interface {
    Transcribe(ctx context.Context, audioPath string, sessionInfo session.Session) (Result, error)
}

type resumableTranscriber interface {
    TranscribeWithTaskID(ctx context.Context, audioPath string, sessionInfo session.Session, taskID string) (Result, error)
}
```

**API 端点：** `POST /api/sessions/:sid/asr/submit`

**前置条件：**
- 场次状态必须为 `media_ready`
- `asr/audio.asr.mp3` 文件必须存在
- 不能有同场次的活跃 ASR 任务
- ASR 能力必须可用（runtime 探测）

## TempAudioServer -- 本地 ASR 临时音频服务

`temp_server.go` 提供本地 HTTP 文件服务，替代 rclone 发布 ASR 音频到 DashScope：

| 方法 | 说明 |
|------|------|
| `NewTempAudioServer(cfg)` | 创建实例 |
| `MountHandler()` | 返回 `/asr-temp/` 前缀的 http.Handler |
| `Publish(ctx, localAudio, sessionInfo)` | 复制音频到本地目录，返回公开 URL 和对象路径 |
| `Delete(ctx, objectPath)` | 删除已发布的音频文件，自动清理空父目录 |

**路径安全**：`localPath()` 方法验证对象路径不逃逸 `ASRTemp.LocalDir` 基目录。

**配置依赖**（`ASRTempConfig`）：

| 字段 | 说明 |
|------|------|
| `enabled` | 启用本地 HTTP 文件服务 |
| `listen` | 本地 HTTP 服务监听地址 |
| `local_dir` | 本地音频存储目录 |
| `public_base_url` | 公开访问基础 URL |

当 `NativeConfigured()`（enabled + local_dir + public_base_url 均非空）为 true 时，DashScopeTranscriber 使用 TempAudioServer。

## S3Publisher -- S3 兼容对象存储后端

`s3_publisher.go` 提供通过 S3 兼容协议上传 ASR 音频的实现，使用 minio-go SDK：

| 方法 | 说明 |
|------|------|
| `NewS3Publisher(cfg)` | 创建 S3Publisher 实例（解析 endpoint、初始化 minio.Client） |
| `Publish(ctx, localAudio, sessionInfo)` | 上传本地音频到 S3，返回公开 URL 和对象键 |
| `Delete(ctx, objectKey)` | 删除 S3 对象 |

**对象键格式**：`{channel_id}/{session_id}/audio.asr.mp3`

**公开 URL 格式**：`{public_url_prefix}/{channel_id}/{session_id}/audio.asr.mp3`

**配置依赖**（`ASRS3Config`）：

| 字段 | 说明 |
|------|------|
| `endpoint` | S3 端点 URL（如 `https://oss.example.com`） |
| `bucket` | 存储桶名称 |
| `access_key_id` | Access Key ID |
| `access_key_secret` | Access Key Secret（直接值） |
| `access_key_env` | Access Key Secret 环境变量名（优先于直接值） |
| `region` | 区域（可选） |
| `public_url_prefix` | 公开访问 URL 前缀 |
| `use_path_style` | 使用路径风格访问（默认虚拟主机风格） |

当 `Configured()`（endpoint + bucket + access_key_id + secret + public_url_prefix 均非空）为 true 时启用。

**后端选择优先级**：TempAudioServer > S3Publisher > rclone。DashScopeTranscriber 根据 `NewConfiguredTranscriber` 中三级优先级选择音频发布后端。

## DetectPublicIP -- 公网 IP 自动检测

`public_ip.go` 提供公网 IP 自动检测功能，用于自动填充 ASR 临时音频服务的公开访问 URL：

| 函数 | 说明 |
|------|------|
| `DetectPublicIP(ctx)` | 尝试多个外部服务获取本机公网 IP（10s 超时） |

**特性**：
- 遍历 4 个外部 IP 检测服务（ipify、ifconfig.me、icanhazip、checkip.amazonaws.com）
- 单次请求超时 3s，总超时 10s
- 过滤私有 IP、回环地址、链路本地地址
- 全部失败返回空字符串

## 关键依赖与配置

**DashScopeTranscriber 流程：**
1. 使用 TempAudioServer 或 S3Publisher 或 rclone 将 ASR 音频发布为公开 URL
2. 调用 DashScope ASR 提交接口（异步）
3. 轮询任务状态（最长 120 次，间隔 5s；连续失败超过 10 次才放弃）
4. 获取结果 URL 并解析转写文本
5. 按需清理临时文件

**退避重试与恢复：**
- `doJSONWithRetry` 包装提交、轮询和状态检查请求，对网络错误、HTTP 429、HTTP 5xx 执行最多 3 次退避重试（1s/2s/4s）。
- `poll` 对单次状态查询失败做容错，`consecutiveFailures > 10` 才返回错误。
- `DashScopeTranscriber` 实现 `TranscribeWithTaskID`：任务 payload 中存在 `dashscope_task_id` 时，先 `checkTask` 检查远端状态；成功则直接拉取结果，仍在运行则继续 poll，失败/取消/检查失败时重新提交。

**结构化日志：**
- ASR Handler 在任务开始和完成时记录 `channel_id`、`session_id`、`model`
- DashScope 提交流程记录 submit、poll、状态变化、重试和完成事件
- 日志上下文从 Handler 传播到 Transcriber，便于按主播和场次追踪一次转写生命周期

**配置依赖：**
- `dashscope.api_key_env` + 环境变量 `DASHSCOPE_API_KEY`
- `dashscope.model` (默认 `qwen3-asr-flash-filetrans`)
- `dashscope.language` (默认 `zh`)
- `asr_temp.enabled` + `asr_temp.local_dir` + `asr_temp.public_base_url`（本地 HTTP 模式，优先级 1）
- `asr_s3.endpoint` + `asr_s3.bucket` + `asr_s3.access_key_id` + `asr_s3.access_key_secret` + `asr_s3.public_url_prefix`（S3 模式，优先级 2）
- `asr_temp.rclone_remote` + `asr_temp.public_base_url`（rclone 模式，优先级 3）

## 数据模型

**Result 结构体：**

| 字段 | 说明 |
|------|------|
| `Transcript` | 转写文本 |
| `SRT` | SRT 字幕格式 |
| `Segments` | 分段信息（start_ms, end_ms, text） |
| `Raw` | 原始 API 响应 |

**输出文件：**
- `package/transcript.txt` -- 纯文本转写
- `package/transcript.srt` -- SRT 字幕
- `package/segments.json` -- 时间分段
- `asr/result.raw.json` -- 原始 ASR 结果

**弹幕校正输出：**
- `danmaku_correction.go` 读取 `package/danmaku.json`。
- `correctDanmakuTiming` 将每条弹幕的 `original_time_ms` 初始化为原时间，并将 `time_ms` clamp 到 ASR segments 覆盖区间内，同时写入 `corrected_time_ms`。
- 无弹幕文件、无弹幕或无 segments 时直接跳过，不阻断 ASR 主流程。

## 测试与质量

- `asr_test.go`: 37 个测试用例，覆盖：
  - CreateTask: 成功、错误状态、音频缺失、活跃任务冲突、session 不存在
  - LocalTranscriber: 占位转写结果
  - DashScope 模型: NormalizeDashScopeASRModel、IsQwenFileTransModel、DashScopeRequestMode
  - 请求体构建: buildDashScopeSubmitBody（qwen file_url 模式、file_urls 模式、语言参数、FunASR 词汇表、说话人分离）
  - 结果提取: extractTranscript（多种嵌套结构）、extractSegments（正常/空/无效时间范围/缺失 sentences）
  - SRT 生成: buildSRT（正常/空/缺失时间）、formatSRTTime
  - 辅助函数: lookupString、numberToInt、findResultURL、lookupLooseString、joinSentenceText、normalizeSRTText

- `danmaku_correction_test.go`: 1 个测试用例，覆盖弹幕时间校正

- `temp_server_test.go`: 10 个测试用例，覆盖：
  - localPath: 正常路径、目录穿越防御、绝对路径防御
  - Publish: 成功（文件存在+URL格式验证）、上下文取消、源文件不存在
  - Delete: 成功删除、文件已不存在、空父目录自动清理
  - MountHandler: HTTP 文件服务正常返回内容

- `s3_publisher_test.go`: 7 个顶级测试（含 8 个 t.Run 子测试 = 共 15 用例），覆盖：
  - s3ObjectKey: 对象键格式生成
  - s3PublicURL: 公开 URL 拼接（尾斜杠处理）
  - NewS3Publisher_MissingConfig: 缺少必要配置项时 Configured() 返回 false（5 种缺失场景）
  - ASRS3Config_SecretResolved: 环境变量优先、直接值回退、空值（3 个子测试）
  - ASRS3Config_Configured: 完整配置时 Configured() 返回 true
  - NewConfiguredTranscriber_S3Fallback: S3 配置时 s3Publisher 被设置、tempServer 为 nil
  - NewConfiguredTranscriber_ThreeTierPriority: 三级后端优先级验证（tempServer 优先 > S3 > LocalTranscriber，3 个子测试）

- `public_ip_test.go`: 8 个顶级测试（含 2 个 t.Run 子测试 = 共 10 用例），覆盖：
  - DetectPublicIP: 成功检测
  - AllFail: 所有端点失败返回空
  - InvalidResponse: 无效 IP 响应返回空
  - ServerError: 服务端错误返回空
  - FallbackToSecond: 第一个失败后回退到第二个
  - IPv6: IPv6 地址解析
  - RejectsPrivateIP: 拒绝私有 IP 地址（5 个子测试：192.168/10/172.16/127.0.0.1/169.254）
  - isPublicIP: 公网 IP 判定（8 种输入）

- **`dashscope_test.go`（2026-07-21 新增）**: 4 个测试，覆盖 Effective URL 兜底 + 调用点 URL 正确性：
  - `TestDashScopeEffectiveURLs`：`DefaultDashScopeASRURL`/`DefaultDashScopeTasksURL` 常量 + `EffectiveASRURL()`/`EffectiveTasksURL()` 空串兜底（与 `EffectiveBaseURL`/`EffectiveAPIKeyEnv` 同模式）
  - `TestSubmitUsesEffectiveASRURL`：用 `urlCapturingTransport` 截获 `submit` 实际请求 URL，验证调用点真的用了 Effective（而非 c.ASRURL 空串）
  - `TestCheckTaskUsesEffectiveTasksURL`：同上验证 `checkTask` 调用点
  - `TestCheckTaskUsesEffectiveTasksURL_TrimRight`：验证 TasksURL 保留 `TrimRight('/')`（B 站 tasks 端点尾斜杠敏感）

## 常见问题 (FAQ)

**Q: DashScope ASR 模型如何选择？**
A: `qwen3-asr-flash-filetrans` 使用 `file_url` 模式；其他模型使用 `file_urls` 模式。`NormalizeDashScopeASRModel()` 负责统一模型名称。

**Q: ASR 临时音频如何发布到 DashScope？**
A: 三级优先级后端选择：
1. 优先使用本地 HTTP 服务（`ASRTemp.NativeConfigured()`），通过 `TempAudioServer.Publish` 复制音频到本地目录并提供公开 URL
2. 未配置本地服务时使用 S3 兼容存储（`ASRS3Config.Configured()`），通过 `S3Publisher.Publish` 上传到 S3
3. 均未配置时回退到 rclone 上传到远端存储

**Q: S3 配置如何设置？**
A: 在 YAML 配置文件中添加 `asr_s3` 配置块：endpoint（S3 端点 URL）、bucket（存储桶）、access_key_id、access_key_secret（或 access_key_env 指定环境变量）、public_url_prefix（公开访问 URL 前缀）。可选 region 和 use_path_style。

**Q: ASR 提交返回 409？**
A: 检查场次状态（需要 `media_ready`）、ASR 音频文件是否存在、是否已有活跃任务、ASR 能力是否可用。

## 相关文件清单

- `asr.go` -- Handler 实现、LocalTranscriber
- `dashscope.go` -- DashScopeTranscriber、退避重试、远端任务恢复、SRT 生成、结果解析、**2026-07-21 3 个调用点（submit/checkTask/`tasks_url`）改用 `EffectiveASRURL()`/`EffectiveTasksURL()` 空串兜底，修复 ASR 配置丢失 BUG**
- `temp_server.go` -- 本地 ASR 临时音频 HTTP 服务（Publish/Delete/MountHandler）
- `s3_publisher.go` -- S3 兼容对象存储后端（Publish/Delete，minio-go SDK）
- `public_ip.go` -- 公网 IP 自动检测（多端点回退、私有 IP 过滤）
- `danmaku_correction.go` -- ASR segments 驱动的弹幕时间校正
- `asr_test.go` -- 单元测试（37 个用例）
- `dashscope_test.go` -- **DashScope Effective URL 测试（2026-07-21 新增 4 个用例）**
- `danmaku_correction_test.go` -- 弹幕校正测试（1 个用例）
- `temp_server_test.go` -- 临时音频服务测试（10 个用例）
- `s3_publisher_test.go` -- S3 发布后端测试（7 个顶级 + 8 个子测试）
- `public_ip_test.go` -- 公网 IP 检测测试（8 个顶级 + 2 个子测试）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-21 | BUG 修复 | **DashScope `asr_url`/`tasks_url` Effective 兜底**(branch `fix/bug-fix-2026-07-20`,commit `61f3989` v6)。**触发**:实测发现 DashScope `asr_url`/`tasks_url` 在 `runtime_settings` 表被持久化为空字符串,覆盖 viper SetDefault 默认值,导致 ASR POST 到空 URL 失败(`Post "": unsupported protocol scheme ""`)。**根因**:`dashscopeConfigToDTO` 用 `&c.ASRURL` 总是取地址,ApplyOverrides 的 nil 检查无法区分「空串指针」与「非空串指针」。**修复**:`dashscope.go` 3 个调用点（`submit` line 227 / `checkTask` line 311 / `tasks_url` line 356）改用 `c.EffectiveASRURL()`/`c.EffectiveTasksURL()`（TasksURL 保留 `TrimRight('/')`）。新增 `dashscope_test.go` 4 个测试（`TestDashScopeEffectiveURLs` + `TestSubmitUsesEffectiveASRURL`/`TestCheckTaskUsesEffectiveTasksURL` + TrimRight 变体，用 `urlCapturingTransport` 捕获实际请求 URL 验证调用点真的用了 Effective 而非空串）。asr 包总测试 63→67。 |
| 2026-06-24 | 重构 | **双重降级收敛**（`5fadea4`）：移除 `asr.go` HandleTask 中冗余的 `Apply(EventTaskFailed)` 调用（2 处）。任务失败降级现已统一由 `worker` 处理（普通任务由 `EventTaskFailed` 全局特判降级；旁路任务经 `Register(..., WithBypassFailState())` 声明后仅写 `last_error`），各业务 handler 不再自行 `Apply`，避免双写。本模块无新增对外接口，测试数无变化（仍 63） |
| 2026-06-05 | 日志增强 | ASR 任务开始/完成日志新增 channel_id、session_id、model；DashScope 转写生命周期新增 submit、poll、状态变化、retry、completion 结构化日志；Handler 向 Transcriber 传播 channel_id/session_id 上下文 |
| 2026-06-05 | 重大更新 | 新增 s3_publisher.go（S3Publisher：minio-go SDK 实现 S3 兼容对象存储上传/删除）、public_ip.go（DetectPublicIP：多端点回退公网 IP 检测）；config.go 新增 ASRS3Config 结构体（Endpoint/Bucket/AccessKeyID/AccessKeySecret/AccessKeyEnv/Region/PublicURLPrefix/UsePathStyle + SecretResolved/Configured）；DashScopeTranscriber 新增三级后端优先级（TempAudioServer > S3Publisher > rclone）；新增 s3_publisher_test.go（7 顶级 + 8 子测试）、public_ip_test.go（8 顶级 + 2 子测试）。总测试用例从 48 增至 63 |
| 2026-06-04 | 测试补充 | 新增 temp_server_test.go（10 用例）：localPath 路径安全 3 个、Publish 成功/取消/不存在 3 个、Delete 成功/已删除/空父目录清理 3 个、MountHandler HTTP 文件服务 1 个。总用例从 38 增至 48 |
| 2026-06-03 | 重大更新 | 新增 temp_server.go（TempAudioServer 本地 ASR 临时音频 HTTP 服务）：支持 MountHandler/Publish/Delete，路径安全校验，自动清理空目录。DashScopeTranscriber 根据 ASRTempConfig.NativeConfigured() 自动选择本地 HTTP 服务或 rclone 发布音频 |
| 2026-05-17 | 修复 | dashscope.go fetchResult 正确处理错误而非忽略 |
| 2026-05-15 | 重大更新 | DashScope 请求新增 doJSONWithRetry 退避重试（1s/2s/4s，最多 3 次），poll 连续失败超过 10 次才放弃；新增 resumableTranscriber 和 TranscribeWithTaskID，支持通过 dashscope_task_id 恢复远端任务；新增 danmaku_correction.go，ASR 完成后根据 segments 校正 package/danmaku.json 的 time_ms 并保留 original_time_ms/corrected_time_ms |
| 2026-05-04 | 重大更新 | 新增 asr_test.go（31 个测试用例），覆盖 CreateTask 前置条件、DashScope 模型/请求体、转写结果提取、SRT 生成 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
