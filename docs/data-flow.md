# Hikami-Go 数据流：从启动到发布

本文描述一条直播内容从进入系统到最终发布为 B 站专栏的完整路径。

---

## 1. 程序启动

入口：`cmd/hikami/main.go`

启动顺序：

```
配置加载 (config.yaml)
  → 日志初始化 (slog, JSON/text)
  → 目录创建 (output_root, raw/, package/, recap/, asr/)
  → 数据库打开+迁移 (SQLite, 38 个物理迁移元素,业务语义到 v35)
  → 密钥加载 (secrets → 环境变量)
  → 术语表初始化 (数据库 + 旧 glossary_file 自动导入)
  → 回顾模板初始化 (TemplateStore)
  → Cookie Account 初始化 (CookieAccountStore)
  → 通知管理器初始化 (Webhook/Bark/ServerChan)
  → 运行时探测 (ffmpeg, yt-dlp, rclone 等工具检查)
  → 主播 Bootstrap (配置文件中的初始主播)
  → 组件初始化 (Store/Handler/Pool 见下方)
  → 任务池启动 (goroutine worker pool)
  → 定时调度启动 (cron: 回放发现、直播检查、磁盘/Cookie 告警)
  → HTTP 服务启动 (Gin, REST + WebSocket)
  → 信号等待 (SIGINT/SIGTERM → 优雅关闭)
```

关键组件初始化依赖链：

```
channelStore ─→ sessionStore ─→ stateStore
                                   │
                              workerPool (任务池)
                                   │
                ┌──────────────────┼──────────────────┐
          downloadHandler   normalizeHandler    liveManager
          importHandler     asrHandler          discoverManager
                           recapHandler
                           uploadHandler
                           publisherHandler
```

---

## 2. 三条来源路径

所有来源最终产出统一的"场次"（Session），进入相同管道。

### 2.1 直播录制

```
用户点击"录制" 或 自动录制触发 (auto_record)
  → liveManager.HandleTask()
    → BilibiliClient 获取直播流 URL
    → FFmpegRecorder HTTP pipe 录制音频
    → BilibiliDanmakuRecorder 录制弹幕 (JSONL)
    → 录制过程中持续写入 raw/ 目录
  → 录制完成
    → normalizeHandler.HandleTask()
      → 音频标准化 (→ asr/audio.asr.mp3)
      → 弹幕解析合并 (raw/*.jsonl → package/danmaku.json)
      → 生成元数据 (package/metadata.json)
    → 状态: recording → media_ready
```

**自动录制触发方式：**
- Cron 定时检查：`scheduler` 每 60 秒调用 `liveManager.CheckAndStartAll()`
- API 手动触发：`POST /api/live/:channel_id/record/start`
- 健康检查：`liveManager.StartHealthCheck()` 检测直播断流后自动重启

### 2.2 回放下载

```
回放发现 (Cron 或 API 手动触发)
  → discoverManager.DiscoverAll()
    → 遍历所有启用的主播
    → 检查 source_mode (live_only 跳过, replay_only/live_first/replay_first/both)
    → YTDLPLister 列出 B 站回放列表
    → 对比已有场次去重
    → 限制 discover_limit 新建数量
    → 为每个新回放创建 Session (status: discovered)
    → 为每个 Session 创建 download 任务

  download 任务执行:
  → downloadHandler.HandleTask()
    → YTDLPDownloader 下载音频 (支持多 P)
    → 下载完成后自动创建 normalize 任务

  normalize 任务执行:
  → normalizeHandler.HandleTask()
    → 音频标准化
    → 弹幕解析
    → 元数据生成
  → 状态: discovered → downloading → media_ready
```

### 2.3 手动导入

```
用户通过 Web 上传文件
  → POST /api/sessions/import (multipart/form-data)
    → importHandler.HandleTask()
      → 接收 channel_id, title, media_file
      → 创建 Session (status: importing)
      → 保存原始文件到 raw/
      → 创建 normalize 任务
  → 状态: importing → media_ready
```

---

## 3. 后处理管道

音频标准化完成后，进入自动管道：

```
media_ready
  │
  ├─[自动] ASR 提交 (如果 auto_asr=true 且 ASR 能力可用)
  │   │
  │   ▼
  │ asr_submitted
  │   │
  │   ├─ DashScope API 调用 (异步)
  │   │   → 轮询直到完成
  │   │   → 写入 package/transcript.txt
  │   │   → 如果有 segments.json，执行弹幕时间校正
  │   │     → 写入 package/danmaku.json
  │   │
  │   ▼
  │ asr_done
  │   │
  │   ├─[自动] 回顾生成
  │   │   │
  │   │   ▼
  │   │  回顾生成流程 (详见第 4 节)
  │   │   │
  │   │   ▼
  │   │ recap_done
  │   │   │
  │   │   ├─[手动] WebDAV 上传
  │   │   │   → uploadHandler → rclone 复制到远程存储
  │   │   │   → uploaded
  │   │   │
  │   │   ├─[自动/手动] B 站专栏发布 (如果 auto_publish=true)
  │   │   │   → publisherHandler → BiliOpusClient
  │   │   │   → Markdown → Opus 格式转换
  │   │   │   → 草稿保存 / 直接发布
  │   │   │   → published
  │   │   │
  │   │   └─ 结束
  │
  └─[手动] 也可跳过 ASR，直接上传/发布
```

**状态机完整转换表：**

| 当前状态 | 事件 | 下一状态 |
|----------|------|----------|
| discovered | download_started | downloading |
| discovered | live_record_started | recording |
| discovered | import_started | importing |
| downloading | normalize_succeeded | media_ready |
| recording | normalize_succeeded | media_ready |
| importing | normalize_succeeded | media_ready |
| media_ready | asr_submitted | asr_submitted |
| asr_submitted | asr_succeeded | asr_done |
| asr_done | recap_succeeded | recap_done |
| asr_done | upload_succeeded | uploaded |
| recap_done | upload_succeeded | uploaded |
| recap_done | publish_succeeded | published |
| uploaded | publish_succeeded | published |
| failed | 任意阶段恢复事件 | 对应恢复状态 |

任何状态都可通过 `task_failed` 事件进入 `failed`，`failed` 可恢复到任意后续状态。

---

## 4. 回顾生成详解

这是系统最复杂的环节，由 `recapHandler.HandleTask()` 执行。

### 4.1 前置检查

```
检查 Session 状态 ≥ asr_done
检查 package/transcript.txt 存在
检查无同场次活跃 recap 任务
检查回顾生成能力可用
```

### 4.2 模板解析

```
templateStore.Resolve(channelID, "default")
  │
  ├─ 获取全局模板 (无则使用内置默认)
  ├─ 获取主播级模板
  ├─ 主播模板 enabled?
  │   ├─ 是: 非空字段覆盖全局，extra_vars 合并
  │   └─ 否: 使用全局模板
  └─ __builtin__ 标记替换为内置常量

构建 TemplateVars:
  channel_name, channel_id, date, title, duration,
  danmaku_count, unique_users, avg_per_min, fan_name, slug

RenderTemplate(userFormat, vars, extraVars)
  → 渲染输出格式模板 ({{key}} → 值)
```

### 4.3 术语校正转写

```
glossaryStore.ListByChannel(channelID)
  → 仅使用 enabled 条目
  → 按术语长度倒序排列
  → 全量回顾: 优先读取 segments.json 生成带时间戳校正版
  → 局部回顾: 先截取时间范围，再校正截取结果
  → 产物:
    recap/transcript.corrected.txt  (校正后转写)
    recap/transcript.correction.json (校正报告)
```

### 4.4 弹幕分析

```
读取 package/danmaku.json (ASR 校正后) 或 raw/danmaku.jsonl
  → analyzeDanmaku()
    → 统计: 总数, 独立用户, 平均密度, 时长
    → 峰值检测: 30 秒窗口内弹幕密度
    → 代表性弹幕: 多因子评分 (权重/唯一性/长度/上下文)
    → 关键词统计
    → 话题聚类 (2 分钟窗口滑动)
  → 结果注入 prompt
```

### 4.5 Prompt 构建

```
PromptBuilder 管道 (按顺序组装):

  [basicInfoSection]
    标题、日期、时长、弹幕数、主播名

  [formatSection]
    渲染后的输出格式模板

  [longStreamSection]  (仅时长 > 3 小时)
    分段建议

  [segmentationSection]  (仅时长 > 30 分钟)
    基于 SRT 静默间隔 + 弹幕密度变化的分段建议

  [glossarySection]
    术语校正参考表

  [danmakuSection]
    弹幕统计数据、峰值时段、代表性弹幕、关键词

  [transcriptSection]
    转写原文 (使用校正版)
    → 如果超过 30000 字且 enable_summarization=true:
       调用摘要器压缩为: 摘要 + 关键引用 + 话题列表
```

### 4.6 AI 调用

```
provider.Generate(ctx, systemPrompt, prompt, sessionInfo)
  │
  ├─ OpenAI-compatible: HTTP POST /chat/completions
  ├─ Anthropic: HTTP POST /messages
  ├─ Claude CLI: 本地命令行 stdin/stdout
  ├─ Codex CLI: 本地命令行参数传 prompt
  └─ Local: 生成模板占位文本
```

### 4.7 后处理

```
AI 返回原始 Markdown
  │
  ├─ applyGlossaryCorrections()     — 最终术语兜底替换
  ├─ ensureFinalAddressSection()    — "致..."章节移到文末
  ├─ FormatDanmakuStats()           — 程序化弹幕统计段落
  └─ appendDanmakuStats()           — 插入"弹幕互动精选"章节

写入产物:
  recap/live-recap.prompt.md        — 完整 prompt (用于审计)
  recap/live-recap.raw.json         — 原始 API 响应
  recap/直播回顾_{slug}.md          — 最终 Markdown
  recap/suggested_terms.json        — 从正文中提取的术语建议
```

---

## 5. 发布流程

### 5.1 WebDAV 上传

```
uploadHandler.HandleTask()
  → rclone copyto 将 recap/ 目录上传到远程存储
  → 支持清理策略 (上传后删除本地文件)
  → 状态: recap_done → uploaded
```

### 5.2 B 站专栏发布

```
publisherHandler.HandleTask()
  │
  ├─ 解析发布配置 (全局 + 主播级覆盖, resolvePublishConfig)
  │   → 分区, 文集, 可见性, 原创, Aigc声明, 定时发布, 封面, 话题
  │
  ├─ Markdown → Opus 格式转换 (md2opus.go)
  │   → 标题/段落/加粗/斜体/列表/引用 正常转换
  │   → 代码块 → 纯文本保留
  │   → 表格 → 纯文本保留
  │   → 图片 → 保留引用
  │
  ├─ Cookie 查找 (ResolveCookie)
  │   → 主播级账号 → 全局默认账号 → 旧 cookie 文件
  │
  ├─ 封面上传
  │   → 优先使用 recap 目录的 cover 图片
  │   → 回退到主播配置的 publish_cover_url
  │
  ├─ WBI URL 签名
  │   → 从 B 站 nav API 获取密钥
  │   → 缓存 1 小时
  │
  ├─ 草稿保存或直接发布
  │
  └─ 状态: recap_done/uploaded → published
```

---

## 6. 实时通信

### WebSocket (`/ws`)

```
客户端连接 → checkWebSocketOrigin 校验 (同源或 localhost)
  │
  ├─ 连接建立 → 发送 ws.hello (服务器时间)
  │
  ├─ 任务事件:
  │   task.created   — 新任务创建
  │   task.updated   — 任务进度/状态更新
  │   task.log       — 任务日志
  │
  ├─ 场次事件:
  │   session.created — 新场次创建
  │   session.updated — 场次状态变更
  │
  ├─ 回顾事件:
  │   recap.updated   — 回顾内容更新
  │
  ├─ 直播事件:
  │   live.changed    — 直播状态变更
  │
  ├─ 运行时事件:
  │   runtime.changed — 能力/磁盘状态变更
  │
  └─ 通知事件:
      notify.message  — 通知消息

广播机制: worker.Hub 维护订阅列表，任务状态变更时推送给所有连接
```

### 通知推送

```
业务事件 → notifyMgr.Send(ctx, event, payload)
  │
  ├─ Webhook: HTTP POST JSON
  ├─ Bark: HTTP GET URL
  └─ ServerChan: HTTP POST JSON
```

---

## 7. Web API 层

Gin 路由 (`internal/handler/server.go`)：

```
/api/channels          — 主播 CRUD
/api/sessions          — 场次 CRUD + ASR + 回顾 + 上传 + 发布
/api/tasks             — 任务查询/重试/取消/删除
/api/live              — 直播状态/录制控制
/api/bili/login        — QR 码登录
/api/cookie-accounts   — Cookie Account 管理
/api/secrets           — API Key 管理
/api/glossary          — 术语表管理 (全局 + 主播)
/api/recap/templates   — 回顾模板管理 (全局 + 主播)
/api/config/publish    — 全局发布配置
/api/stats/*           — 统计与仪表板
/api/runtime/*         — 运行时能力与健康
/api/notify/test       — 通知测试
/api/onboarding/*      — 新手引导
/api/diagnostic/report — 诊断报告
/ws                    — WebSocket
/                      — SPA 前端 (嵌入或静态文件)
```

错误处理：`writeError()` 通过 `errors.Is` 将业务错误映射到 HTTP 状态码 (400/403/404/409/410/422/429/502/500)。

---

## 8. 数据存储

### SQLite 表结构 (业务语义 v35)

| 表 | 用途 |
|----|------|
| sessions | 场次 (id, channel_id, title, status, source, timestamps) |
| tasks | 任务 (id, type, status, session_id, channel_id, progress, error) |
| channels | 主播 (id, platform, room_id, uid, name, auto_record/asr/publish, source_mode) |
| glossary_entries | 术语 (id, channel_id, term, aliases, description, weight, enabled) |
| glossary_meta | 术语表备注 (channel_id, note) |
| recap_templates | 回顾模板 (channel_id, name, system_prompt, user_format, fan_name, extra_vars) |
| secrets | API Key (provider, name, encrypted_value) |
| usage_metadata | 用量元数据 |
| bili_cookie_accounts | Cookie Account (uid, nickname, cookie_file, default flags) |

### 文件目录结构

```
{output_root}/
├── {channel_id}/
│   └── {session_id}/
│       ├── raw/                    — 原始输入 (只读, 不可覆盖)
│       │   ├── live.raw.json       — 录制元数据 (流 URL 脱敏)
│       │   ├── audio.webm          — 原始音频
│       │   └── danmaku.jsonl       — 原始弹幕
│       ├── asr/                    — 标准化 + ASR 产物
│       │   ├── audio.asr.mp3       — 标准音频(供 ASR 提交)
│       │   ├── transcript.txt      — 转写文本
│       │   ├── transcript.srt/vtt  — 带时间戳转写
│       │   ├── segments.json       — ASR 分段数据
│       │   ├── metadata.json       — 元数据
│       │   ├── danmaku.json        — 校正后弹幕 (ASR 时间对齐)
│       │   └── cover.*             — 封面图
│       ├── recap/                  — 回顾产物
│       │   ├── 直播回顾_{slug}.md  — 最终 Markdown
│       │   ├── live-recap.prompt.md — 完整 prompt
│       │   ├── live-recap.raw.json — AI 原始响应
│       │   ├── suggested_terms.json — 术语建议
│       │   ├── transcript.corrected.txt — 校正版转写
│       │   └── transcript.correction.json — 校正报告
│       └── asr/                    — ASR 临时文件
└── .cookies/bilibili/             — Cookie Account 文件 (权限 0600)
```

---

## 9. 完整流程图

```
                          ┌─────────────┐
                          │   启 动     │
                          │  main.go    │
                          └──────┬──────┘
                                 │
                    ┌────────────┼────────────┐
                    │            │            │
              ┌─────▼─────┐ ┌───▼───┐ ┌─────▼─────┐
              │ 直播录制   │ │回放下载│ │ 手动导入   │
              │ live_record│ │discover│ │ importer  │
              │ + ffmpeg   │ │+yt-dlp│ │ + ffmpeg  │
              └─────┬──────┘ └───┬───┘ └─────┬─────┘
                    │            │            │
                    └────────────┼────────────┘
                                 │
                          ┌──────▼──────┐
                          │   标准化    │
                          │  normalize  │
                          │  (ffmpeg)   │
                          └──────┬──────┘
                                 │ media_ready
                          ┌──────▼──────┐
                          │    ASR      │
                          │ (DashScope) │
                          └──────┬──────┘
                                 │ asr_done
                    ┌────────────┼────────────┐
                    │                         │
          ┌─────────▼─────────┐       ┌──────▼──────┐
          │   回顾生成         │       │  弹幕时间    │
          │   recap            │       │  校正        │
          │ ┌───────────────┐ │       └─────────────┘
          │ │ 模板解析      │ │
          │ │ 术语校正转写  │ │
          │ │ 弹幕分析      │ │
          │ │ Prompt 构建   │ │
          │ │ AI 调用       │ │
          │ │ 后处理        │ │
          │ └───────────────┘ │
          └─────────┬─────────┘
                    │ recap_done
          ┌─────────┼──────────┐
          │                    │
   ┌──────▼──────┐     ┌──────▼──────┐
   │  WebDAV     │     │  B 站专栏    │
   │  上传       │     │  发布       │
   │ (rclone)    │     │ (Opus API)  │
   └──────┬──────┘     └──────┬──────┘
          │ uploaded          │ published
          └─────────┬─────────┘
                    │
              ┌─────▼─────┐
              │   完 成    │
              └───────────┘
```
