[根目录](../../CLAUDE.md) > **internal/normalize**

# internal/normalize -- 媒体与弹幕标准化

## 模块职责

将所有来源（直播录制、回放下载、手动导入）的原始产物统一为后续管道需要的标准产物：ASR 音频、标准弹幕包和元数据。支持 JSONL、XML 和多 P 弹幕格式的解析与合并，并保留原始/校正弹幕时间字段。

## 入口与启动

- **入口文件**: `normalize.go`
- **任务类型**: `normalize`
- **测试总数**: 69（按 `grep -c "^func Test" internal/normalize/*_test.go` 顶级函数口径统计）

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewHandler(cfg, sessions, states, converter)` | 创建 Handler |
| `Register(pool)` | 注册 normalize 任务处理器 |

**接口：**

```go
type AudioConverter interface {
    Convert(ctx context.Context, inputPath string, outputPath string) error
}
```

## 关键依赖与配置

- ffmpeg: `-vn -ac 1 -ar 16000 -b:a 64k` 生成 ASR 标准 MP3
- 输入: `raw/audio.m4a`, `raw/danmaku.jsonl` 或 `raw/danmaku.xml` 或 `raw/danmaku_parts/`
- 输出:
  - `asr/audio.asr.mp3` -- ASR 标准音频
  - `package/danmaku.json` -- 标准弹幕包
  - `package/metadata.json` -- 标准元数据
  - `metadata.json` -- 根目录场次元数据

## 任务流程

1. 获取场次信息
2. 定位 `raw/audio.{ext}`
3. ffmpeg 转码为 `asr/audio.asr.mp3`（原子写入 + **空文件 post-condition 2026-07-25**:Convert 返回后、Rename 前 `os.Stat` 校验 size>0,堵住 ffmpeg exit 0 但产出 0 字节文件导致 session 误进 media_ready 的根因)
4. 解析弹幕（按优先级：JSONL > XML > 多 P XML）
5. 写入 `package/danmaku.json`（原子写入）
6. 构建并写入 `package/metadata.json` 和根 `metadata.json`
7. 提交 `normalize_succeeded` 事件（推进到 `media_ready`）

## 弹幕解析优先级

1. **`raw/danmaku.jsonl`** -- 直播录制或手动导入的 JSONL 格式
2. **`raw/danmaku.xml`** -- 单 P 回放下载的 B 站 XML 格式
3. **`raw/danmaku_parts/pNNN.xml`** -- 多 P 回放下载的分 P XML 格式

### XML 弹幕解析

解析 B 站 XML 格式 `<d p="appear_time,mode,font_size,color,send_timestamp,pool,user_hash,danmaku_id">text</d>`。

`bilibiliDanmaku.Text` 使用 `xml:",innerxml"` 读出原始 XML 片段，`parseXMLDanmaku` 再通过 `html.UnescapeString` 反解实体，避免 innerxml 不反解实体导致弹幕文本被污染。例如原始弹幕 `a&b` 写成 XML 后是 `a&amp;b`，解析结果会还原为 `a&b`；现有 `comment.bilibili.com` XML 弹幕和 seg.so 转 XML 弹幕均受益。

### 多 P 弹幕合并

读取 `raw/part_durations.json` 获取各 P 时长，按序号排序后累加时间偏移，合并为统一的弹幕数组。

## 数据模型

**DanmakuItem（标准弹幕）：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `time_ms` | int64 | 相对场次起点的毫秒时间 |
| `original_time_ms` | int64 | 原始时间，JSONL 解析缺省时由 `time_ms` 初始化 |
| `corrected_time_ms` | int64 | 已校正时间；JSONL 中存在时覆盖 `time_ms` |
| `type` | string | "danmaku" |
| `user_id` | string | 用户 ID |
| `user_name` | string | 用户名 |
| `text` | string | 弹幕文本 |
| `color` | string | 颜色 hex |
| `raw_time` | string | 原始接收时间 |
| `received_at` | string | 原始接收时间戳（直播 JSONL 可用） |
| `source` | string | 来源类型 |

## 弹幕时间偏移修复

多 P 回放合并时，`mergeMultiPDanmaku` 不再只按存在 XML 的分 P 累加偏移：
- `durationBeforePart` 计算首个弹幕 XML 前所有分 P时长，确保从非第 1 P 开始也能得到正确全局时间。
- `durationBetweenParts` 在相邻 XML 文件之间累计所有分 P 时长，缺失弹幕 XML 的 P 也计入偏移。
- XML 弹幕的 `OriginalTimeMS` 保留分 P 内原始时间，`TimeMS` 为加偏移后的全局时间。

直播/导入 JSONL：
- `parseJSONLDanmaku` 会保留 `original_time_ms`、`corrected_time_ms`、`received_at` 等字段。
- 当 `corrected_time_ms > 0` 时用它覆盖 `time_ms`，便于 ASR 校正后的弹幕继续参与回顾分析。

**Metadata（标准元数据）：**

| 字段 | 说明 |
|------|------|
| `session_id`, `channel_id`, `slug` | 场次标识 |
| `source_type`, `source_id` | 来源信息 |
| `title`, `started_at`, `ended_at` | 时间信息 |
| `raw_audio_path`, `asr_audio_path` | 音频路径 |
| `danmaku_count` | 弹幕数量 |
| `generated_at` | 生成时间 |

## 测试与质量

- `normalize_test.go`: 69 个测试用例，覆盖：
  - JSONL 弹幕解析（基础、空行、默认 type、默认 source、无效 JSON、保留字段）
  - XML 弹幕解析（基础、实体反解、时间偏移、跳过无效、无效时间、空文件、无效 XML、文件不存在、负偏移、user_id/raw_time 提取、最少字段）
  - 弹幕优先级（JSONL 优先于 XML、XML 回退）
  - 多 P 弹幕合并（基础、缺失分 P、缺失 durations、无 XML 文件、乱序分 P、无效文件名、不存在目录、损坏 XML）
  - 文件操作（findRawAudio 优先级/回退/不存在、writeJSONAtomic 原子性/覆盖/切片）
  - 元数据构建（buildMetadata 完整/空会话/子目录）
  - 集成测试（HandleTask 成功/会话不存在/转换失败/音频缺失、Register、convertAtomic 成功/失败/**空输出检测 2026-07-25**）

## 相关文件清单

- `normalize.go` -- 唯一源文件
- `normalize_test.go` -- 单元测试和集成测试（69 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-25 | BUG 修复 | **convertAtomic 空文件 post-condition**(branch `fix/media-ready-consistency-2026-07-25`,qoder 计划审核 Ready with fixes + 代码审核 Ready)。**触发**:用户反馈界面上 64 个「音频已就绪」实际只有 1 个本地真有音频文件,经 DB + 文件交叉核查确认是 2026-07-16 批量脏数据(qoder 独立审核定位根因)。**根因**:`FFmpegConverter.Convert` 只检查退出码,ffmpeg 在输入截断/损坏时可 exit 0 但产出 0 字节 mp3 → `convertAtomic` Rename 成功 → `EventNormalizeSucceeded` → session 误进 media_ready 但音频为空。**修复**:`convertAtomic` 在 Convert 返回后、Rename 前 `os.Stat(tempPath)`,size==0 则删 tmp 返回 error(任务进 failed,不进 media_ready)。新增 `TestConvertAtomicEmptyOutput`。测试 68→69。**为什么 Size==0 够**:tmp→rename 原子性已防住中途崩溃;mp3 是帧格式「exit 0 + 部分帧」几乎不可能;加 ffprobe 时长校验属过度工程。见 `plans/plan-media-ready-consistency-2026-07-25.md` Phase 1。 |
| 2026-06-24 | 重构 | **双重降级收敛**（`5fadea4`）：移除 `normalize.go` HandleTask 中冗余的 `Apply(EventTaskFailed)` 调用（3 处）。任务失败降级统一由 `worker` 处理（普通任务 `EventTaskFailed` 全局特判降级；旁路任务经 `Register(..., WithBypassFailState())` 声明后仅写 `last_error`），各业务 handler 不再自行 `Apply`，避免双写。本模块无新增对外接口，测试数无变化（仍 68） |
| 2026-06-21 | 增量同步 | 测试计数校正：82→68（历史口径以 `go test -v \| grep "=== RUN"` 统计含子测试，现统一改用 `grep -c "^func Test"` 顶级函数口径，与各模块一致；实际测试函数未减少）。模块功能自 06-18 以来无变化 |
| 2026-05-15 | 更新 | 修复多 P 弹幕合并偏移：首个 XML 前和相邻 XML 间缺失分 P 也计入 part_durations；DanmakuItem 新增 original_time_ms/corrected_time_ms/received_at，JSONL 解析保留校正时间并在 corrected_time_ms 存在时覆盖 time_ms |
| 2026-05-02 | 更新 | 新增 normalize_test.go（65+ 个测试用例），覆盖全部弹幕解析、文件操作和集成测试 |
| 2026-05-01 | 重大更新 | 新增 XML 弹幕解析、多 P 弹幕合并（带时间偏移）、弹幕来源优先级机制 |
| 2026-04-29 | 初始化 | 首次生成模块文档（仅 JSONL） |
