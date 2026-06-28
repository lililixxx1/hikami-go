# 核心管道与模块详解

> 本文件由根 CLAUDE.md 拆分而来，作为 AI 上下文补充文档。

## 核心模块详解

### 回顾生成管道 (recap 模块化架构)

原 928 行的 `recap.go` 已拆分为职责清晰的多个文件：

| 文件 | 职责 |
|------|------|
| `handler.go` | Handler 公共 API：CreateTask、CreateTaskWithRange、HandleTask 主流程、默认常量、自动续写（continuation）、per-channel recap model/continuations、回顾后 AI 术语发现 |
| `prompt.go` | PromptSection 管道：PromptBuilder 组装多个 section（basicInfo/format/longStream/segmentation/glossary/danmaku/transcript） |
| `filter.go` | 时间范围过滤：SRT/VTT/danmaku JSON/JSONL 过滤，字幕时间归零 |
| `provider_openai.go` | OpenAI-compatible Provider 实现 |
| `provider_util.go` | Provider 接口定义（返回 aiprovider.GenerateResult）、LocalProvider、NewConfiguredProvider、AI 前导语去除 |
| `danmaku_analysis.go` | 弹幕分析子函数：多因子评分、突发时刻检测、话题聚类、高权重弹幕 |
| `segmentation.go` | 话题驱动分段：检测静音间隔和弹幕密度变化，生成分段建议 |
| `transcript_summarizer.go` | 转写摘要器：长文本压缩为精简摘要+关键引用+话题列表 |
| `glossary_correction.go` | 最终 Markdown 术语兜底、ensureFinalAddressSection（"致..."章节文末保证） |
| `transcript_correction.go` | 回顾前术语校正转写：buildCorrectionRules、correctedTranscriptForPrompt、校正产物写入 |
| `danmaku.go` | 弹幕分析入口：analyzeDanmaku、rawDanmakuItem 类型、工具函数 |
| `template.go` | TemplateStore CRUD + Resolve 合并 |
| `render.go` | 模板变量插值引擎 |
| `danmaku_stats.go` | 弹幕统计程序化生成 |
| `presets.go` | 5 个内置模板预设（粉丝向精修/正式详实/粉丝向/简洁摘要/弹幕聚焦） |

### AI 术语发现系统 (glossary/candidate_store.go + discovery.go)

回顾生成完成后自动触发 AI 术语发现，从转写文本中提取可能的术语候选，进入人工审核队列。

**核心类型：**

- `Candidate` -- 术语候选模型（id, channel_id, term, canonical, category, status, confidence, score, occurrence_count, session_count, first/last_session_id, reason, normalized_key）
- `DiscoveryItem` -- AI 返回的单个候选（term, canonical, category, confidence, occurrence_count, reason）
- `Discoverer` -- AI 术语发现器（分块转写文本、调用 AI 提取候选、合并到 candidate store）
- `DiscoveryProvider` -- 接口定义（与 recap.Provider 方法签名一致，避免 glossary 反向导入 recap）

**候选生命周期：**

```
AI 发现 --> pending（等待审核）--> approved（自动写入 glossary_entries）/ rejected
```

**候选评分算法（calculateCandidateScore）：**

- 置信度权重 0.65 + 会话因子 0.20 + 出现次数因子 0.15
- 会话因子：`min(1, sessionCount/3.0)`
- 出现次数因子：`min(1, log1p(count)/log1p(8))`

**API 端点：**

| 端点 | 说明 |
|------|------|
| `GET /api/glossary/candidates` | 列出全局候选（支持 ?status=） |
| `POST /api/glossary/candidates/:cid/approve` | 审批通过（写入 glossary_entries） |
| `POST /api/glossary/candidates/:cid/reject` | 拒绝候选 |
| `GET /api/channels/:id/glossary/candidates` | 列出主播候选 |
| `POST /api/channels/:id/glossary/candidates/batch-approve` | 批量审批 |
| `POST /api/channels/:id/glossary/candidates/batch-reject` | 批量拒绝 |
| `POST /api/sessions/:sid/glossary/discover` | 手动触发场次术语发现 |

**自动发现触发：** 回顾完成后，`recap.Handler.HandleTask` 在后台 goroutine 中调用 `glossaryDiscoverer.Discover`，使用校正后的转写文本和 segments 分块。

### 回顾自动续写 (recap/handler.go continuation)

当 AI 返回 `finish_reason != "stop"` 时，自动发送续写请求继续生成内容，直到达到 `max_continuations` 次数或自然结束。

- `max_continuations` 来源：`recap_ai.max_continuations` 配置（默认 0），可被 `channels.max_continuations` 覆盖（>=0 时覆盖）
- 续写使用 `buildContinuationPrompt` 生成后续 prompt
- `appendContinuation` 拼接续写内容，`dropDuplicateLeadingHeading` 去除重复标题
- `shouldContinueRecap` 判断 `finish_reason` 是否为 `length`/`max_tokens`

### Per-Channel 回顾配置 (recap/handler.go recapOptions)

- `channels.recap_model`：主播级回顾模型覆盖（非空时覆盖全局 `recap_ai.model`）
- `channels.max_continuations`：主播级续写次数覆盖（>=0 时覆盖全局 `recap_ai.max_continuations`）
- `recapRuntimeOptions` 结构体封装运行时选项
- `withRecapModel`/`recapModelFromContext` 通过 context 传递 per-channel model

### 回顾模板系统 (recap/template.go + render.go + danmaku_stats.go + presets.go)

自定义回顾模板允许用户通过 Web 界面配置回顾生成的 system prompt、输出格式和自定义变量，支持全局默认和主播级别覆盖。内置 5 个模板预设供快速选择。

**核心类型：**

- `Template` -- 数据库行模型（id, channel_id, name, system_prompt, user_format, fan_name, extra_vars, enabled, is_default）
- `ResolvedTemplate` -- 合并后的最终模板（system_prompt, user_format, fan_name, extra_vars map）
- `TemplateVars` -- 模板变量（channel_name, channel_id, date, date_time, title, duration, duration_min, fan_name, danmaku_count, unique_users, avg_per_min, slug）
- `TemplateStore` -- CRUD + Resolve 合并逻辑
- `TemplatePreset` -- 模板预设（name, description, system_prompt, user_format）
- `BuiltinPresets` -- 5 个内置预设：粉丝向精修、正式详实、粉丝向、简洁摘要、弹幕聚焦

**模板合并逻辑（Resolve）：**
1. 获取全局模板，不存在则使用内置默认
2. 获取主播级模板
3. 若主播模板存在且 enabled：非空非 `__builtin__` 的字段覆盖全局，extra_vars 合并
4. 否则直接使用全局模板
5. 剩余 `__builtin__` 标记替换为实际内置常量

**模板变量插值（RenderTemplate）：**
- `{{channel_name}}`、`{{channel_id}}`、`{{date}}`、`{{date_time}}`、`{{title}}`、`{{duration}}`、`{{duration_min}}`、`{{fan_name}}`、`{{danmaku_count}}`、`{{unique_users}}`、`{{avg_per_min}}`、`{{slug}}`
- 支持自定义 extra_vars（JSON 格式）
- 未知变量保留原样

**弹幕统计程序化生成（danmaku_stats.go）：**
- `FormatDanmakuStats` 生成弹幕统计 Markdown 段落（总数/独立用户/密度/时长/峰值）
- `appendDanmakuStats` 将统计段插入回顾 Markdown 的"弹幕互动精选"章节

### 友好错误映射 (worker/errors.go)

`GetFriendlyError` 将原始错误信息映射为中文友好消息和操作建议：
- ffmpeg 相关：音频处理失败/工具未安装
- 网络相关：连接失败/超时/DNS 错误
- Cookie/认证：过期/无效/未登录
- API 限流：频率超限
- ASR/DashScope：配额不足/Key 无效
- 发布：审核未通过/稿件重复
- yt-dlp/rclone：工具未安装/视频不可用
- 按任务类型提供默认友好消息（download/normalize/asr/recap/upload/publish/live_record）
- handler 层在任务详情 API 中返回 `friendly_error` 字段

### 运行时健康检查 (runtime/health.go + disk_unix.go + disk_windows.go)

- `CheckCookieExpiry(ctx, channelStore)` 检查所有启用主播的 Cookie 过期情况，返回 7 天内过期或已过期的警告
- `CheckDiskUsage(paths)` 跨平台磁盘使用检查（Linux/darwin 使用 syscall.Statfs，Windows 使用 GetDiskFreeSpaceEx）
- 诊断报告 API 包含磁盘使用和 Cookie 警告信息

### 来源模式 (source_mode)

Channel 的 `source_mode` 字段控制回放发现行为：
- `both`（默认）-- 同时支持直播录制和回放发现
- `live_only` -- 仅直播录制，跳过回放发现
- `replay_only` -- 仅回放发现，不录制直播
- `live_first` -- 优先直播，回放兜底
- `replay_first` -- 优先回放，直播兜底

`discover_limit` 字段限制每次发现最多创建的新场次数（0 = 不限制）。

### 新手引导 (handler onboarding + OnboardingWizard.vue)

`GET /api/onboarding/status` 检查是否需要显示引导：
- 未跳过（secrets 中无 `_onboarding_dismissed`）
- 且满足以下之一：缺少硬依赖工具、未设置 API Key、无主播配置
- 前端 `OnboardingWizard` 三步引导：工具检查 -> API Key 设置 -> 添加首个主播
