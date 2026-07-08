# Hikami-Go 设计文档

本文档基于项目内 `CLAUDE.md`、模块源码、数据库迁移、前端 API 封装和测试文件整理。所有事实性描述均标注到对应源码依据；若历史文档与代码不一致，以当前源码为准。

## 系统概述

### 项目定位

Hikami-Go 是面向 B 站主播和录播维护者的单机自动化直播音频处理服务。系统围绕“来源适配 + 标准化 + 后处理”管道工作，将直播录制、回放发现与下载、手动导入统一汇入同一套 ASR、AI 回顾、WebDAV 归档和 B 站专栏发布流程。

源码依据：`CLAUDE.md`、`README.md`、`cmd/hikami/main.go`

### 目标用户

- B 站主播：自动录制直播音频、归档、生成直播回顾和发布专栏。
- 录播维护者：批量发现回放、下载、标准化、转写、归档。
- 运营或剪辑人员：通过 Web 管理界面查看场次、任务、回顾内容和系统能力状态。

源码依据：`README.md`、`web/CLAUDE.md`、`web/src/views/*.vue`

### 核心价值

- 单机部署：Go 服务、SQLite 单文件数据库、内嵌 Vue SPA。
- 多来源统一：直播录制、回放下载、手动导入都进入 `normalize` 后续管道。
- 状态可控：`internal/state` 统一维护场次生命周期，业务模块只提交事件。
- 异步可观测：`internal/worker` 持久化任务，`Hub` 通过 WebSocket 广播任务进度。
- 可扩展外部集成：ffmpeg、yt-dlp、rclone、DashScope、AI 回顾 Provider、B 站专栏客户端均通过接口封装。

源码依据：`internal/state/state.go`、`internal/worker/*.go`、`internal/*/CLAUDE.md`

## 架构设计

### 总体架构

```mermaid
flowchart TB
    User["用户 / Web 前端"] --> HTTP["Gin REST API / WebSocket\ninternal/handler"]
    HTTP --> Stores["SQLite Store 层\nchannel/session/task/secrets/glossary"]
    HTTP --> Worker["worker.Pool\n任务创建、排队、执行"]
    Worker --> Hub["worker.Hub\nWebSocket 任务广播"]
    Hub --> User

    Scheduler["robfig/cron/v3\nscheduler"] --> Discover["discover\n回放发现"]
    Scheduler --> LiveCheck["live_record\n直播检查/自动录制"]

    Discover --> Worker
    LiveCheck --> Worker

    Worker --> Download["download\nyt-dlp 回放下载"]
    Worker --> LiveRecord["live_record\nB 站直播音频+弹幕录制"]
    Worker --> Importer["importer\n手动导入"]
    Worker --> Normalize["normalize\n音频/弹幕/元数据标准化"]
    Worker --> ASR["asr\nDashScope/本地占位"]
    Worker --> Recap["recap\nAI 回顾"]
    Worker --> Upload["upload\nWebDAV/rclone"]
    Worker --> Publisher["publisher\nB 站 Opus 专栏"]

    Download --> Normalize
    LiveRecord --> Normalize
    Importer --> Normalize
    Normalize --> ASR
    ASR --> Recap
    Recap --> Upload
    Recap --> Publisher
    Upload --> Publisher

    Download --> State["state.Store\n场次状态机"]
    LiveRecord --> State
    Importer --> State
    Normalize --> State
    ASR --> State
    Recap --> State
    Upload --> State
    Publisher --> State
    State --> Stores

    Runtime["runtime.Probe\n工具与能力探测"] --> HTTP
    Config["YAML config\ninternal/config"] --> Runtime
    Config --> Scheduler
    Config --> Worker
    Config --> Modules["业务模块"]
```

源码依据：`cmd/hikami/main.go`、`internal/handler/server.go`、`internal/scheduler/scheduler.go`、`internal/worker/worker.go`

### 分层设计

- 入口层：`cmd/hikami/main.go` 负责加载配置、迁移数据库、加载 secrets、导入旧术语表、探测运行时依赖、初始化 Store 与 Handler、注册任务处理器、启动 worker、scheduler 和 HTTP 服务。
- 接入层：`internal/handler/server.go` 暴露 REST API、WebSocket、嵌入式 SPA 静态资源。
- 编排层：`internal/worker` 执行异步任务；`internal/scheduler` 定时触发回放发现和直播检查；`cmd/hikami/main.go` 注册自动 ASR、自动回顾、自动发布回调。
- 领域层：`channel`、`session`、`state`、`discover`、`download`、`live_record`、`importer`、`normalize`、`asr`、`recap`、`upload`、`publisher`、`glossary`、`secrets`。
- 基础设施层：`db`、`config`、`runtime`、`biliutil`，以及外部工具 `ffmpeg`、`ffprobe`（必需）；`yt-dlp`、`rclone`（可选，缺失仅降级对应能力）。
- 前端层：`web` 使用 Vue 3、Pinia、Vue Router、Element Plus、Axios 和 WebSocket。

源码依据：`cmd/hikami/main.go`、`internal/*/CLAUDE.md`、`web/CLAUDE.md`

### 模块依赖关系

```mermaid
graph LR
    config --> runtime
    config --> scheduler
    config --> handler
    config --> modules["业务处理模块"]
    db --> stores["Store"]
    stores --> handler
    stores --> modules
    state --> modules
    worker --> handler
    worker --> modules
    channel --> discover
    channel --> live_record
    channel --> publisher
    session --> discover
    session --> download
    session --> live_record
    session --> importer
    session --> normalize
    session --> asr
    session --> recap
    session --> upload
    session --> publisher
    biliutil --> channel
    biliutil --> live_record
    biliutil --> publisher
    glossary --> recap
```

源码依据：各模块 import，尤其是 `cmd/hikami/main.go`、`internal/download/download.go`、`internal/live_record/manager.go`、`internal/recap/recap.go`、`internal/publisher/publisher.go`

## 数据模型

### 迁移版本

当前 `internal/db/migrate.go` 的 `migrations` 切片包含 18 个版本。

| 版本 | 内容 | 影响对象 |
|---:|---|---|
| 1 | 创建 `schema_migrations` | 迁移记录 |
| 2 | 创建 `channels` 基础表 | 主播 |
| 3 | 创建 `sessions` 基础表，关联 `channels(id)` | 场次 |
| 4 | 创建 `sessions_channel_source_uidx` 唯一索引 | 场次去重 |
| 5 | 创建 `sessions_channel_slug_uidx` 唯一索引 | 路径 slug 去重 |
| 6 | 创建 `tasks` 表，关联 `channels(id)` 与 `sessions(id)` | 后台任务 |
| 7 | 创建 `tasks_status_idx` | 任务状态查询 |
| 8 | 创建 `tasks_channel_session_idx` | 主播/场次任务查询 |
| 9 | `channels` 增加 `download_cookie_file` | 下载/识别/录制 Cookie |
| 10 | `channels` 增加 `auto_record` | 自动录制 |
| 11 | `channels` 增加 `auto_asr` | 自动 ASR |
| 12 | 创建 `secrets` | API Key 数据库存储 |
| 13 | `channels` 增加 `record_danmaku` | 弹幕录制开关 |
| 14 | 创建 `glossary_entries` | 术语词条 |
| 15 | 创建 `glossary_entries_channel_term_uidx` | 术语词条去重 |
| 16 | 创建 `glossary_meta` | 全局/主播术语备注 |
| 17 | `channels` 增加发布配置字段：`publish_enabled`、`publish_mode`、`publish_category_id`、`publish_list_id`、`publish_private_pub`、`publish_original`、`auto_publish` | 主播级发布配置 |
| 18 | `channels` 增加发布扩展字段：`publish_aigc`、`publish_timer_pub_time`、`publish_cover_url`、`publish_topics` | AI 声明、定时发布、封面、话题 |

源码依据：`internal/db/migrate.go`

### SQLite Schema

#### `schema_migrations`

| 字段 | 类型 | 约束 |
|---|---|---|
| `version` | INTEGER | PRIMARY KEY |
| `applied_at` | TEXT | NOT NULL DEFAULT datetime('now') |

#### `channels`

| 字段 | 类型 | 约束/默认值 |
|---|---|---|
| `id` | TEXT | PRIMARY KEY |
| `name` | TEXT | NOT NULL |
| `uid` | INTEGER | NOT NULL |
| `live_room_id` | INTEGER | NOT NULL DEFAULT 0 |
| `replay_source_url` | TEXT | NOT NULL DEFAULT '' |
| `space_url` | TEXT | NOT NULL DEFAULT '' |
| `title_prefix` | TEXT | NOT NULL DEFAULT '' |
| `cookie_file` | TEXT | NOT NULL DEFAULT '' |
| `enabled` | INTEGER | NOT NULL DEFAULT 1 |
| `created_at` | TEXT | NOT NULL DEFAULT datetime('now') |
| `updated_at` | TEXT | NOT NULL DEFAULT datetime('now') |
| `download_cookie_file` | TEXT | NOT NULL DEFAULT '' |
| `auto_record` | INTEGER | NOT NULL DEFAULT 1 |
| `auto_asr` | INTEGER | NOT NULL DEFAULT 0 |
| `record_danmaku` | INTEGER | NOT NULL DEFAULT 1 |
| `publish_enabled` | INTEGER | NOT NULL DEFAULT 0 |
| `publish_mode` | TEXT | NOT NULL DEFAULT '' |
| `publish_category_id` | INTEGER | NOT NULL DEFAULT 0 |
| `publish_list_id` | INTEGER | NOT NULL DEFAULT -1 |
| `publish_private_pub` | INTEGER | NOT NULL DEFAULT 0 |
| `publish_original` | INTEGER | NOT NULL DEFAULT -1 |
| `auto_publish` | INTEGER | NOT NULL DEFAULT 0 |
| `publish_aigc` | INTEGER | NOT NULL DEFAULT -1 |
| `publish_timer_pub_time` | INTEGER | NOT NULL DEFAULT 0 |
| `publish_cover_url` | TEXT | NOT NULL DEFAULT '' |
| `publish_topics` | TEXT | NOT NULL DEFAULT '' |

#### `sessions`

| 字段 | 类型 | 约束/默认值 |
|---|---|---|
| `id` | TEXT | PRIMARY KEY |
| `slug` | TEXT | NOT NULL |
| `channel_id` | TEXT | NOT NULL, FK `channels(id)` |
| `source_type` | TEXT | NOT NULL |
| `source_id` | TEXT | NOT NULL |
| `title` | TEXT | NOT NULL |
| `started_at` | TEXT | 可空 |
| `ended_at` | TEXT | 可空 |
| `source_url` | TEXT | NOT NULL DEFAULT '' |
| `status` | TEXT | NOT NULL |
| `current_task_id` | TEXT | 可空 |
| `last_error` | TEXT | 可空 |
| `local_available` | INTEGER | NOT NULL DEFAULT 1 |
| `uploaded_at` | TEXT | 可空 |
| `published_at` | TEXT | 可空 |
| `publish_target` | TEXT | 可空 |
| `created_at` | TEXT | NOT NULL DEFAULT datetime('now') |
| `updated_at` | TEXT | NOT NULL DEFAULT datetime('now') |

索引：

- `sessions_channel_source_uidx`：`UNIQUE(channel_id, source_type, source_id)`
- `sessions_channel_slug_uidx`：`UNIQUE(channel_id, slug)`

#### `tasks`

| 字段 | 类型 | 约束/默认值 |
|---|---|---|
| `id` | TEXT | PRIMARY KEY |
| `channel_id` | TEXT | NOT NULL, FK `channels(id)` |
| `session_id` | TEXT | 可空，FK `sessions(id)` |
| `type` | TEXT | NOT NULL |
| `status` | TEXT | NOT NULL |
| `payload` | TEXT | NOT NULL DEFAULT '{}' |
| `progress` | INTEGER | NOT NULL DEFAULT 0 |
| `message` | TEXT | NOT NULL DEFAULT '' |
| `error` | TEXT | 可空 |
| `attempt` | INTEGER | NOT NULL DEFAULT 1 |
| `started_at` | TEXT | 可空 |
| `finished_at` | TEXT | 可空 |
| `created_at` | TEXT | NOT NULL DEFAULT datetime('now') |
| `updated_at` | TEXT | NOT NULL DEFAULT datetime('now') |

索引：

- `tasks_status_idx`：`tasks(status)`
- `tasks_channel_session_idx`：`tasks(channel_id, session_id)`

#### `secrets`

| 字段 | 类型 | 约束/默认值 |
|---|---|---|
| `key` | TEXT | PRIMARY KEY |
| `value` | TEXT | NOT NULL DEFAULT '' |
| `updated_at` | TEXT | NOT NULL DEFAULT datetime('now') |

#### `glossary_entries`

| 字段 | 类型 | 约束/默认值 |
|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT |
| `channel_id` | TEXT | NOT NULL DEFAULT '' |
| `term` | TEXT | NOT NULL |
| `canonical` | TEXT | NOT NULL |
| `category` | TEXT | NOT NULL DEFAULT '' |
| `enabled` | INTEGER | NOT NULL DEFAULT 1 |
| `created_at` | TEXT | NOT NULL DEFAULT datetime('now') |
| `updated_at` | TEXT | NOT NULL DEFAULT datetime('now') |

索引：

- `glossary_entries_channel_term_uidx`：`UNIQUE(channel_id, term)`

#### `glossary_meta`

| 字段 | 类型 | 约束/默认值 |
|---|---|---|
| `channel_id` | TEXT | NOT NULL DEFAULT '', PRIMARY KEY |
| `note` | TEXT | NOT NULL DEFAULT '' |
| `updated_at` | TEXT | NOT NULL DEFAULT datetime('now') |

源码依据：`internal/db/migrate.go`、`internal/channel/channel.go`、`internal/session/session.go`、`internal/worker/task.go`、`internal/glossary/glossary.go`、`internal/secrets/secrets.go`

### 实体关系图

```mermaid
erDiagram
    CHANNELS ||--o{ SESSIONS : "channel_id"
    CHANNELS ||--o{ TASKS : "channel_id"
    SESSIONS ||--o{ TASKS : "session_id"
    CHANNELS ||--o{ GLOSSARY_ENTRIES : "channel_id 可为空字符串表示全局"
    CHANNELS ||--o{ GLOSSARY_META : "channel_id 可为空字符串表示全局"

    CHANNELS {
        text id PK
        text name
        integer uid
        integer live_room_id
        text replay_source_url
        text cookie_file
        text download_cookie_file
        integer enabled
        integer auto_record
        integer auto_asr
        integer record_danmaku
        integer publish_enabled
        text publish_mode
        integer auto_publish
    }

    SESSIONS {
        text id PK
        text slug
        text channel_id FK
        text source_type
        text source_id
        text title
        text status
        text current_task_id
        text last_error
        integer local_available
        text publish_target
    }

    TASKS {
        text id PK
        text channel_id FK
        text session_id FK
        text type
        text status
        text payload
        integer progress
        integer attempt
    }

    SECRETS {
        text key PK
        text value
        text updated_at
    }

    GLOSSARY_ENTRIES {
        integer id PK
        text channel_id
        text term
        text canonical
        text category
        integer enabled
    }

    GLOSSARY_META {
        text channel_id PK
        text note
    }
```

## 核心数据流

### 完整处理管道

```mermaid
flowchart LR
    A1["直播检查\nlive_record.CheckAndStartAll"] --> A2["创建 live_record session"]
    B1["回放发现\ndiscover.DiscoverAll"] --> B2["创建 download session"]
    C1["手动导入\nPOST /api/sessions/import"] --> C2["创建 import session"]

    A2 --> T1["任务 live_record"]
    B2 --> T2["任务 download"]
    C2 --> T3["任务 import"]

    T1 --> R1["raw/audio.<container>\nraw/danmaku.jsonl\nraw/live.raw.json"]
    T2 --> R2["raw/audio.m4a\nraw/metadata.ytdlp.json\nraw/danmaku.xml 或 danmaku_parts"]
    T3 --> R3["raw/import.source.*\nraw/audio.m4a\nraw/import.raw.json"]

    R1 --> N["任务 normalize"]
    R2 --> N
    R3 --> N

    N --> P1["asr/audio.asr.mp3"]
    N --> P2["package/danmaku.json"]
    N --> P3["package/metadata.json\nmetadata.json"]
    P1 --> ASR["任务 asr"]
    ASR --> P4["package/transcript.txt\npackage/transcript.srt\npackage/segments.json\nasr/result.raw.json"]
    P4 --> Recap["任务 recap"]
    P2 --> Recap
    Recap --> P5["recap/*.md\nrecap/*_bilibili.txt\nrecap/live-recap.prompt.md\nrecap/live-recap.raw.json"]
    P5 --> Upload["任务 upload\nWebDAV 归档"]
    P5 --> Publish["任务 publish\nB 站草稿/发布"]
    Upload --> Publish
```

源码依据：`internal/discover/discover.go`、`internal/download/download.go`、`internal/live_record/manager.go`、`internal/importer/importer.go`、`internal/normalize/normalize.go`、`internal/asr/asr.go`、`internal/recap/recap.go`、`internal/upload/upload.go`、`internal/publisher/publisher.go`

### 序列图

```mermaid
sequenceDiagram
    participant Web as Web/定时器
    participant Handler as handler.Server
    participant Pool as worker.Pool
    participant Task as 任务处理器
    participant State as state.Store
    participant DB as SQLite
    participant FS as 输出目录
    participant Ext as 外部服务/工具

    Web->>Handler: 触发发现/录制/导入/API 操作
    Handler->>DB: 创建或读取 channel/session
    Handler->>Pool: Enqueue(task)
    Pool->>DB: INSERT tasks(status=pending)
    Pool-->>Web: WebSocket task_progress
    Pool->>Task: MarkRunning 后执行 handler
    Task->>State: Apply(开始事件)
    State->>DB: UPDATE sessions.status/current_task_id
    Task->>Ext: ffmpeg/yt-dlp/rclone/DashScope/AI/Bilibili
    Task->>FS: 写入 raw/asr/package/recap 产物
    Task->>State: Apply(成功事件或 task_failed)
    State->>DB: UPDATE sessions
    Task->>Pool: 返回成功/失败
    Pool->>DB: MarkSucceeded/MarkFailed
    Pool-->>Web: WebSocket task_progress
```

源码依据：`internal/worker/worker.go`、`internal/state/state.go`、各任务处理器 `HandleTask`

### 自动化回调

- `normalize` 成功后，`cmd/hikami/main.go` 根据主播 `auto_asr` 和运行时 `ASRSubmit` 能力自动创建 ASR 任务。
- `asr` 成功后，`cmd/hikami/main.go` 自动创建 recap 任务。
- `recap` 成功后，`cmd/hikami/main.go` 根据主播 `auto_publish` 和运行时 `PublishOpus` 能力自动创建 publish 任务。
- `scheduler` 根据 `cron.discovery` 定时执行 `discover.DiscoverAll`，根据 `cron.live_check` 定时执行 `live_record.CheckAndStartAll`。

源码依据：`cmd/hikami/main.go`、`internal/scheduler/scheduler.go`

## 状态机设计

### 状态与事件

状态常量：

| 状态 | 含义 |
|---|---|
| `discovered` | 场次已创建，等待来源处理 |
| `downloading` | 回放下载中 |
| `recording` | 直播录制中 |
| `importing` | 手动导入中 |
| `media_ready` | 标准化媒体已就绪 |
| `asr_submitted` | ASR 已提交或正在执行 |
| `asr_done` | ASR 产物已生成 |
| `recap_done` | AI 回顾已生成 |
| `uploaded` | 场次目录已上传归档 |
| `published` | B 站专栏草稿保存或发布已完成 |
| `failed` | 场次失败，可从后续管道事件恢复 |

事件常量：

| 事件 | 触发模块 |
|---|---|
| `download_started` / `download_succeeded` | `download` |
| `live_record_started` / `live_record_succeeded` | `live_record` |
| `import_started` / `import_succeeded` | `importer` |
| `normalize_succeeded` | `normalize` |
| `asr_submitted` / `asr_succeeded` | `asr` |
| `recap_succeeded` | `recap` |
| `upload_succeeded` | `upload` |
| `publish_succeeded` | `publisher` |
| `task_failed` | 任何任务失败路径或 worker 恢复失败路径 |

源码依据：`internal/state/state.go`

### 状态图

```mermaid
stateDiagram-v2
    [*] --> discovered
    discovered --> downloading: download_started
    discovered --> recording: live_record_started
    discovered --> importing: import_started

    downloading --> downloading: download_succeeded
    recording --> recording: live_record_succeeded
    importing --> importing: import_succeeded

    downloading --> media_ready: normalize_succeeded
    recording --> media_ready: normalize_succeeded
    importing --> media_ready: normalize_succeeded

    media_ready --> asr_submitted: asr_submitted
    asr_submitted --> asr_done: asr_succeeded
    asr_done --> recap_done: recap_succeeded
    asr_done --> uploaded: upload_succeeded
    recap_done --> uploaded: upload_succeeded
    recap_done --> published: publish_succeeded
    uploaded --> published: publish_succeeded

    discovered --> failed: task_failed
    downloading --> failed: task_failed
    recording --> failed: task_failed
    importing --> failed: task_failed
    media_ready --> failed: task_failed
    asr_submitted --> failed: task_failed
    asr_done --> failed: task_failed
    recap_done --> failed: task_failed
    uploaded --> failed: task_failed
    published --> failed: task_failed

    failed --> media_ready: normalize_succeeded
    failed --> asr_submitted: asr_submitted
    failed --> asr_done: asr_succeeded
    failed --> recap_done: recap_succeeded
    failed --> uploaded: upload_succeeded
    failed --> published: publish_succeeded
```

源码依据：`internal/state/state.go`

### 转换持久化与失败恢复

- `Store.Apply` 在事务中读取当前状态、调用 `Next` 校验转换、再更新 `sessions`。
- `task_failed` 从任意状态转入 `failed`，写入 `current_task_id` 和 `last_error`。
- `upload_succeeded` 写入 `uploaded_at`，`publish_succeeded` 写入 `published_at`。
- 非失败事件清空 `last_error`。
- `failed` 允许通过后续管道成功事件恢复到 `media_ready`、`asr_submitted`、`asr_done`、`recap_done`、`uploaded`、`published`。

源码依据：`internal/state/state.go`、`internal/state/state_test.go`

## 任务系统

### Task Pool 设计

```mermaid
flowchart TB
    API["REST API / Scheduler / 自动回调"] --> Enqueue["Pool.Enqueue / Retry"]
    Enqueue --> StoreCreate["tasks INSERT pending"]
    StoreCreate --> Queue["内存队列 chan string\n容量 workerCount*4"]
    Queue --> W1["worker goroutine"]
    Queue --> W2["worker goroutine"]
    Queue --> WN["worker goroutine"]
    W1 --> MarkRunning["MarkRunning pending->running"]
    MarkRunning --> Handler["按 task.type 查找 Handler"]
    Handler --> Reporter["Reporter.Progress"]
    Reporter --> Update["UpdateProgress"]
    Update --> Hub["Hub.Broadcast"]
    Handler --> Success["MarkSucceeded running->succeeded"]
    Handler --> Failure["MarkFailed pending/running->failed"]
    Failure --> SyncState["syncSessionState: task_failed"]
    Success --> Hub
    Failure --> Hub
```

源码依据：`internal/worker/worker.go`、`internal/worker/task.go`

### 任务类型

| 任务类型 | 注册模块 | 主要职责 |
|---|---|---|
| `download` | `internal/download` | 下载回放音频和元数据（native 单 P 优先，多 P/回放发现回退 yt-dlp），随后入队 `normalize` |
| `live_record` | `internal/live_record` | 录制 B 站直播流和弹幕，停止或结束后入队 `normalize` |
| `import` | `internal/importer` | 转换上传媒体为 `raw/audio.m4a`，随后入队 `normalize` |
| `normalize` | `internal/normalize` | 生成 ASR 音频、弹幕 JSON、元数据 |
| `asr` | `internal/asr` | 生成 transcript、SRT、segments 和原始结果 |
| `recap` | `internal/recap` | 生成直播回顾、B 站专栏文本、prompt 和 raw response |
| `upload` | `internal/upload` | 上传场次目录到 WebDAV（native HTTP 优先，未配置时回退 rclone） |
| `publish` | `internal/publisher` | 保存 B 站专栏草稿或直接发布 |

源码依据：各模块 `const TaskType` 与 `Register` 方法

### Hub 广播机制

- `Hub.Broadcast(task)` 将任务转换为 `Event{type:"task_progress", task_id, channel_id, session_id, status, progress, message, error}`。
- `Hub.Run` 持有订阅者集合，广播 channel 缓冲为 64，每个订阅者 channel 缓冲为 16。
- 向订阅者发送时使用非阻塞写；慢订阅者不会阻塞整个广播。
- `/ws` 端点订阅 Hub 并通过 gorilla/websocket 写出 JSON。
- 前端 `useWebSocket` 将 `/ws` 的 `task_progress` 事件转发到 mitt 事件总线，`TasksView` 用它更新 Pinia task store。

源码依据：`internal/worker/hub.go`、`internal/handler/server.go`、`web/src/composables/useWebSocket.ts`、`web/src/views/TasksView.vue`

### 恢复策略与并发控制

- 服务启动时 `Pool.Start` 先执行 `recoverRunning`。
- `asr_poll` 和 `upload` 类型 running 任务会 `ResetToPending` 并重新入队；当前源码没有注册 `asr_poll` 处理器，但恢复分支已存在。
- `live_record` running 任务会从 message 中解析 ffmpeg PID，若进程仍存活则保留 running，否则标记 failed 并同步 session 状态。
- 其他 running 任务标记 failed，用户可通过重试 API 再次提交。
- 每个任务的状态流转由 `MarkRunning`、`MarkSucceeded`、`MarkFailed`、`Retry`、`Cancel` 控制；`MarkSucceeded` 只允许 running 任务成功。
- 各业务 `CreateTask` 通常通过 `ActiveBySessionAndType` 避免同场次同类型 pending/running 任务重复提交。
- `live_record.Manager` 额外维护 `active map[channelID]activeRecord`，防止同主播并发录制；`Start` 还检查数据库中的活跃直播场次。

源码依据：`internal/worker/worker.go`、`internal/worker/task.go`、`internal/live_record/manager.go`、`internal/asr/asr.go`、`internal/recap/recap.go`、`internal/upload/upload.go`、`internal/publisher/publisher.go`

## API 设计

### 通用约定

- JSON 错误一般为 `{"error":"..."}`；术语表未找到/重复场景为 `{"error":"not found|duplicate","reason":"..."}`。
- 常见状态码：`200 OK`、`201 Created`、`202 Accepted`、`204 No Content`、`400 Bad Request`、`404 Not Found`、`409 Conflict`、`403 Forbidden`、`422 Unprocessable Entity`、`429 Too Many Requests`、`502 Bad Gateway`、`500 Internal Server Error`。
- 能力不可用时，ASR、回顾、上传、发布接口返回 `409`，响应包含 `error` 和 `reason`。
- 路由列表以 `Server.routes()` 为准。

源码依据：`internal/handler/server.go`

### WebSocket

| 方法 | 路径 | 请求 | 响应 |
|---|---|---|---|
| GET | `/ws` | WebSocket Upgrade | 推送 `task_progress` JSON 事件 |

事件结构：

```json
{
  "type": "task_progress",
  "task_id": "task_xxx",
  "channel_id": "bili_123",
  "session_id": "bili_123_download_BV...",
  "status": "running",
  "progress": 40,
  "message": "generating transcript package",
  "error": ""
}
```

源码依据：`internal/handler/server.go`、`internal/worker/hub.go`

### 健康与运行时

| 方法 | 路径 | 请求 | 成功响应 |
|---|---|---|---|
| GET | `/api/healthz` | 无 | `{"status":"ok"}` |
| GET | `/api/health/runtime` | 无 | `runtime.Status`，包含 `tools`、`capabilities`、`config_status` |

源码依据：`internal/handler/server.go`、`internal/runtime/probe.go`

### 主播 API

| 方法 | 路径 | 请求 | 成功响应 |
|---|---|---|---|
| GET | `/api/channels` | 无 | `{"items": Channel[]}` |
| POST | `/api/channels/identify` | `IdentifyInput` | `IdentifyResult` |
| POST | `/api/channels/identify/save` | `IdentifyInput` | `IdentifySaveResult`，新建时 `201`，更新时 `200` |
| POST | `/api/channels` | `UpsertInput` | `Channel`，`201` |
| PUT | `/api/channels/:id` | `UpsertInput` | `Channel` |
| DELETE | `/api/channels/:id` | 无 | `204` |

`IdentifyInput`：

```json
{
  "input": "https://live.bilibili.com/123 或 https://space.bilibili.com/456 或纯数字",
  "uid": 456,
  "live_room_id": 123
}
```

`Channel/UpsertInput` 字段包括：`id`、`name`、`uid`、`live_room_id`、`replay_source_url`、`space_url`、`title_prefix`、`cookie_file`、`download_cookie_file`、`enabled`、`auto_record`、`auto_asr`、`record_danmaku`、`publish_enabled`、`publish_mode`、`publish_category_id`、`publish_list_id`、`publish_private_pub`、`publish_original`、`auto_publish`、`publish_aigc`、`publish_timer_pub_time`、`publish_cover_url`、`publish_topics`。

错误映射：无效参数 `400`；不存在 `404`；重复或被 session 外键引用导致无法删除 `409`。

源码依据：`internal/handler/server.go`、`internal/channel/channel.go`、`internal/channel/identify.go`

### 直播 API

| 方法 | 路径 | 请求 | 成功响应 |
|---|---|---|---|
| POST | `/api/live/check` | 无 | `202 {"items": LiveStatus[]}`，会对开启 `auto_record` 的在线主播自动开始录制 |
| GET | `/api/live/status` | 无 | `{"items": LiveStatus[]}` |
| GET | `/api/live/:channel_id/status` | path 参数 | `LiveStatus` |
| POST | `/api/live/:channel_id/record/start` | path 参数 | `202 LiveStatus` |
| POST | `/api/live/:channel_id/record/stop` | path 参数 | `202` |

`LiveStatus` 字段：`channel_id`、`room_id`、`live`、`title`、`started_at`、`recording`、`session_id`、`task_id`、`error`。

错误映射：直播能力关闭、已在录制、未在录制、未开播返回 `409`；主播不可录制返回 `400`。

源码依据：`internal/handler/server.go`、`internal/live_record/types.go`、`internal/live_record/manager.go`

### 场次 API

| 方法 | 路径 | 请求 | 成功响应 |
|---|---|---|---|
| POST | `/api/sessions/discover` | 无 | `202 {"items": DiscoverResult[]}` |
| GET | `/api/sessions` | 无 | `{"items": Session[]}` |
| GET | `/api/sessions/:sid` | path 参数 | `{"session": Session, "files": [{"path":"...", "size":123}]}` |
| DELETE | `/api/sessions/failed` | 无 | `{"deleted": number}`，先删失败场次关联任务 |
| DELETE | `/api/sessions/:sid` | path 参数 | `204`，先删该场次关联任务 |
| POST | `/api/sessions/download` | `{"session_id":"..."}` | `202 Task` |
| POST | `/api/sessions/import` | multipart form | `202 Task` |
| POST | `/api/sessions/:sid/asr/submit` | path 参数 | `202 Task` |
| POST | `/api/sessions/:sid/recap/generate` | path 参数 | `202 Task` |
| GET | `/api/sessions/:sid/recap` | path 参数 | 回顾内容 |
| POST | `/api/sessions/:sid/upload` | path 参数 | `202 Task` |
| POST | `/api/sessions/:sid/fetch` | path 参数 | `202 {"session": Session}` |
| POST | `/api/sessions/:sid/publish` | path 参数 | `202 Task` |

导入 multipart 字段：

| 字段 | 必填 | 说明 |
|---|---|---|
| `media_file` | 是 | 本地媒体文件 |
| `channel_id` | 是 | 主播 ID |
| `title` | 是 | 场次标题 |
| `started_at` | 否 | RFC3339 |
| `ended_at` | 否 | RFC3339 |
| `source_url` | 否 | 原始来源 |
| `danmaku_file` | 否 | JSONL 弹幕文件 |

`GET /api/sessions/:sid/recap` 响应：

```json
{
  "available": true,
  "markdown": "...",
  "bilibili": "...",
  "prompt": "...",
  "raw_response": "..."
}
```

错误映射：场次不存在 `404`；参数无效 `400`；前置状态不满足、文件缺失、能力不可用或任务冲突 `409`。

源码依据：`internal/handler/server.go`、`internal/session/session.go`、`internal/importer/importer.go`、`internal/asr/asr.go`、`internal/recap/recap.go`、`internal/upload/upload.go`、`internal/publisher/publisher.go`

### 任务 API

| 方法 | 路径 | 请求 | 成功响应 |
|---|---|---|---|
| GET | `/api/tasks` | 无 | `{"items": Task[]}` |
| GET | `/api/tasks/:id` | path 参数 | `Task` |
| POST | `/api/tasks/:id/retry` | path 参数 | `202 Task` |
| POST | `/api/tasks/:id/cancel` | path 参数 | `Task` |
| DELETE | `/api/tasks/failed` | 无 | `{"deleted": number}` |
| DELETE | `/api/tasks/:id` | path 参数 | `204` |

`Task` 字段：`id`、`channel_id`、`session_id`、`type`、`status`、`payload`、`progress`、`message`、`error`、`attempt`、`started_at`、`finished_at`、`created_at`、`updated_at`。

错误映射：任务不存在 `404`；无效任务 `400`；状态冲突 `409`。

源码依据：`internal/handler/server.go`、`internal/worker/task.go`

### Secrets 与发布配置 API

| 方法 | 路径 | 请求 | 成功响应 |
|---|---|---|---|
| GET | `/api/secrets` | 无 | `{"items": SecretView[]}` |
| PUT | `/api/secrets/:key` | `{"value":"..."}`；空字符串表示删除 | `SecretView` |
| GET | `/api/config/publish` | 无 | `PublishConfig` |
| PUT | `/api/config/publish` | 部分字段指针式更新 | `PublishConfig` |

`SecretView` 字段：`key`、`masked_value`、`set`、`source`、`updated_at`。

`PublishConfig` 字段：`enabled`、`mode`、`category_id`、`list_id`、`private_pub`、`summary_len`、`aigc`、`timer_pub_time`。

`PUT /api/secrets/:key` 仅允许配置中声明的 `dashscope.api_key_env` 与 `recap_ai.api_key_env`。

源码依据：`internal/handler/server.go`、`internal/secrets/secrets.go`、`internal/config/config.go`

### 术语表 API

| 方法 | 路径 | 请求 | 成功响应 |
|---|---|---|---|
| GET | `/api/glossary/entries` | 无 | `{"items": Entry[]}` |
| POST | `/api/glossary/entries` | `{"term":"...","canonical":"...","category":"..."}` | `{"ok": true}` |
| DELETE | `/api/glossary/entries/:eid` | path 参数 | `204` |
| GET | `/api/glossary/note` | 无 | `{"note":"..."}` |
| PUT | `/api/glossary/note` | `{"note":"..."}` | `{"ok": true}` |
| GET | `/api/channels/:id/glossary/entries` | path 参数 | `{"items": MergedEntry[]}` |
| POST | `/api/channels/:id/glossary/entries` | `{"term":"...","canonical":"...","category":"..."}` | `{"ok": true}` |
| DELETE | `/api/channels/:id/glossary/entries/:eid` | path 参数 | `204` |
| GET | `/api/channels/:id/glossary/note` | path 参数 | `{"note":"..."}` |
| PUT | `/api/channels/:id/glossary/note` | `{"note":"..."}` | `{"ok": true}` |
| POST | `/api/glossary/import/markdown` | `{"content":"..."}` | `{"imported": number}` |
| POST | `/api/glossary/import/json` | JSON body | `{"imported": number}` |
| GET | `/api/glossary/export/json` | 无 | JSON 文件内容 |
| POST | `/api/channels/:id/glossary/import/markdown` | `{"content":"..."}` | `{"imported": number}` |
| POST | `/api/channels/:id/glossary/import/json` | JSON body | `{"imported": number}` |
| GET | `/api/channels/:id/glossary/export/json` | 无 | JSON 文件内容 |
| POST | `/api/glossary/entries/batch-delete` | `{"ids":[1,2]}` | `{"deleted": number}` |
| POST | `/api/glossary/entries/batch-toggle` | `{"ids":[1,2],"enabled":true}` | `{"updated": number}` |
| POST | `/api/glossary/entries/:eid/toggle` | `{"enabled":true}` | `{"ok": true}` |
| POST | `/api/channels/:id/glossary/entries/batch-delete` | `{"ids":[1,2]}` | `{"deleted": number}` |
| POST | `/api/channels/:id/glossary/entries/batch-toggle` | `{"ids":[1,2],"enabled":true}` | `{"updated": number}` |
| POST | `/api/channels/:id/glossary/entries/:eid/toggle` | `{"enabled":true}` | `{"ok": true}` |

错误映射：词条不存在 `404`；重复 `409`；无效 entry id 或 JSON `400`。

源码依据：`internal/handler/server.go`、`internal/glossary/glossary.go`

### 静态前端路由

如果 `cmd/hikami/embed.go` 内嵌的 `webdist` 可用，`handler` 的 `NoRoute` 会服务静态文件并对非 `/api/`、非 `/ws` 路径回退到 `index.html`。若没有内嵌前端，则只提供根路径 `/` 的简单 HTML。

源码依据：`cmd/hikami/embed.go`、`internal/handler/server.go`

## 配置体系

### YAML 配置结构

核心配置结构位于 `internal/config/config.go`。

| 配置块 | 主要字段 |
|---|---|
| 根配置 | `output_root`、`db_path`、`ffmpeg`、`ffprobe`、`yt_dlp`、`rclone` |
| `web` | `enabled`、`listen` |
| `worker` | `num` |
| `cron` | `discovery`、`live_check` |
| `live_record` | `enabled`、`audio_only`、`record_danmaku`、`audio_container`、`require_audio_stream`、`fallback_extract_audio`、`generate_asr_audio`、`segment_minutes`、`stop_grace_seconds` |
| `logs` | `dir`、`level` |
| `dashscope` | `api_key_env`、`asr_url`、`tasks_url`、`model`、`language` |
| `asr_temp` | `rclone_remote`、`base_path`、`public_base_url`、`cleanup_after_success` |
| `recap_ai` | `provider`、`api_key_env`、`base_url`、`model`、`timeout_seconds`、`cli_path`、`glossary_file` |
| `webdav` | `remote`、`base_path` |
| `upload` | `cleanup_policy` |
| `publish` | `enabled`、`mode`、`category_id`、`list_id`、`private_pub`、`summary_len`、`aigc`、`timer_pub_time` |
| `bootstrap_channels` | 主播初始数据与 per-channel 发布字段 |

源码依据：`internal/config/config.go`、`config.example.yaml`

### 默认值与校验

- 默认输出目录为 `hikami-go`，默认数据库为 `hikami.db`。
- 默认命令：`ffmpeg`、`ffprobe`、`yt-dlp`、`rclone`。
- 默认监听 `:8080`，worker 数为 3。
- 默认定时：回放发现 `@every 20m`，直播检查 `@every 30s`。
- `output_root`、`db_path` 必须非空；启用 Web 时 `web.listen` 必须非空。
- `worker.num` 必须大于 0。
- `live_record.audio_container` 必须非空，`segment_minutes` 和 `stop_grace_seconds` 不能为负。
- `publish.mode` 只能为空、`draft` 或 `publish`；`publish.summary_len` 不能为负。
- `EnsureDirs` 创建 `output_root`、`logs.dir` 和数据库父目录。

源码依据：`internal/config/config.go`

### YAML + SQLite 分层

- YAML 提供全局运行配置、外部工具命令、首次主播引导、全局发布默认值。
- SQLite 持久化运行期业务数据：主播、场次、任务、API Key、术语表。
- `cmd/hikami/main.go` 启动时先加载 YAML，再打开 SQLite 并迁移。
- `channel.Store.Bootstrap` 只在 `channels` 表为空时导入 `bootstrap_channels`；如果数据库已有主播，不再覆盖。
- `secrets.Store.LoadIntoEnv` 启动时将数据库中非空 secrets 写入进程环境变量；Web 修改 secret 后也立即 `os.Setenv` 或 `os.Unsetenv`。
- `recap_ai.glossary_file` 是旧配置；启动时如果全局术语表为空，会把该 Markdown 文件导入数据库。

源码依据：`cmd/hikami/main.go`、`internal/channel/channel.go`、`internal/secrets/secrets.go`、`internal/glossary/glossary.go`

### Per-channel 发布配置合并

`publisher.resolvePublishConfig` 将主播配置与全局 `publish` 合并：

- `PublishMode == ""` 时使用全局 `Mode`。
- `PublishCategoryID == 0` 时使用全局 `CategoryID`。
- `PublishListID == -1` 时使用全局 `ListID`。
- `PublishPrivatePub == 0` 时使用全局 `PrivatePub`。
- `PublishOriginal == -1` 时解析为 `0`；当前实现没有回退到全局原创字段，因为全局 `PublishConfig` 没有 `Original` 字段。
- `PublishAigc == -1` 时使用全局 `Aigc`。
- `PublishTimerPubTime == 0` 时使用全局 `TimerPubTime`。
- `PublishCoverURL` 和 `PublishTopics` 直接使用主播字段；当前 `DraftRequest` 使用 `CoverURL`，`Topics` 已进入配置结构但当前发布请求未消费。
- 发布启用条件：主播 `PublishEnabled` 或全局 `publish.enabled` 至少一个为 true；否则 `CreateTask` 返回 `ErrPublishNotEnabled`。

源码依据：`internal/publisher/publisher.go`、`internal/config/config.go`、`internal/channel/channel.go`

## 外部集成

### Bilibili API 与 Cookie

- Cookie 文件使用 Netscape cookie 格式解析，要求存在未过期的 `SESSDATA`、`bili_jct`、`DedeUserID`。
- `cookie_file` 用于 B 站专栏发布；`download_cookie_file` 用于识别、回放发现、回放下载、直播状态检查、直播流获取和弹幕连接。
- 主播识别支持 UID、直播间 ID、B 站直播间 URL、B 站空间 URL和数字输入。
- 识别按已存主播 Cookie、Bootstrap Cookie 的匹配或兜底策略加载下载 Cookie。
- 直播状态接口调用 `https://api.live.bilibili.com/xlive/web-room/v1/index/getInfoByRoom`。
- 直播流接口调用 `https://api.live.bilibili.com/xlive/web-room/v2/index/getRoomPlayInfo`，优先选择 FLV/AVC 混合流；`selectStream` 当前固定取混合流，由 ffmpeg 丢弃视频轨。
- 发布接口调用：
  - `https://api.bilibili.com/x/dynamic/feed/article/draft/add`
  - `https://api.bilibili.com/x/dynamic/feed/create/opus`
  - `https://api.bilibili.com/x/dynamic/feed/article/draft/del`
  - `https://api.bilibili.com/x/article/creative/article/upcover`
- B 站 API 错误映射：`-101` 为 Cookie 过期，`-403` 为内容拒绝，`-509` 为限流，其他非零 code 归类为 B 站 API 错误。

源码依据：`internal/biliutil/cookie.go`、`internal/channel/identify.go`、`internal/live_record/bilibili.go`、`internal/publisher/bilibili_opus.go`

### WBI 签名

- `biliutil.WBISigner` 实现 `URLSigner`，对 URL 增加 `wts` 和 `w_rid`。
- 密钥从 `https://api.bilibili.com/x/web-interface/nav` 获取，读取 `wbi_img.img_url` 与 `wbi_img.sub_url`，通过 64 元素置换表生成 `mixinKey`，缓存 1 小时。
- 签名时按 query key 排序，移除值中的 `!'()*`，对拼接字符串加 `mixinKey` 后计算 MD5。
- 弹幕 `getDanmuInfo` 先尝试 WBI 签名，签名失败时降级为未签名 URL；遇到 B 站 `-352` 风控时刷新密钥重试一次，仍失败时降级到默认弹幕服务器。

源码依据：`internal/biliutil/wbi.go`、`internal/live_record/danmaku.go`

### 弹幕协议

- 弹幕录制先调用 `getDanmuInfo` 获取 token 和 WebSocket host，然后连接 `wss://host:wss_port/sub`。
- 鉴权包 operation 为 7，心跳包 operation 为 2，每 30 秒发送一次。
- 消息包 operation 为 5；协议版本 0/1 为明文 JSON，版本 2 使用 zlib 解压后递归解析。
- 当前只写入 `DANMU_MSG`，格式为 JSONL，字段包括 `time_ms`、`type`、`user_id`、`user_name`、`text`、`color`、`raw_time`、`source`。

源码依据：`internal/live_record/danmaku.go`

### DashScope ASR

- 启用 DashScope 需要 `dashscope.api_key_env` 对应环境变量存在，且 `asr_temp.rclone_remote` 与 `asr_temp.public_base_url` 非空；否则使用本地占位转写器。
- ASR 先用 `rclone copyto` 将 `asr/audio.asr.mp3` 发布到临时公开地址。
- 提交接口默认 `https://dashscope.aliyuncs.com/api/v1/services/audio/asr/transcription`，Header 包含 `Authorization: Bearer ...`、`Content-Type: application/json`、`X-DashScope-Async: enable`。
- `qwen3-asr-flash-filetrans` 使用 `input.file_url`；其他模型使用 `input.file_urls`。
- 轮询接口为 `dashscope.tasks_url + "/" + taskID`，最多 120 次，每 5 秒一次；成功后读取结果 URL。
- 输出 `package/transcript.txt`、`package/transcript.srt`、`package/segments.json`、`asr/result.raw.json`。

源码依据：`internal/asr/dashscope.go`、`internal/asr/asr.go`、`internal/runtime/probe.go`

### AI 回顾后端

- Provider 类型包括 `openai_compatible`、`anthropic`、`claude_cli`、`codex_cli` 和本地占位。
- OpenAI-compatible 后端请求 `recap_ai.base_url + "/chat/completions"`，body 包含 `model` 和 system/user messages。
- Anthropic 与 CLI Provider 在独立文件中实现，均满足 `Provider.Generate(ctx, prompt, sessionInfo)` 接口。
- Prompt 包含基本信息、输出格式要求、术语表校正参考、弹幕分析数据和转写原文。
- 弹幕分析基于 `package/danmaku.json`，计算总量、独立用户、平均每分钟、30 秒桶峰值、代表性弹幕和关键词统计。

源码依据：`internal/recap/recap.go`、`internal/recap/anthropic.go`、`internal/recap/claude_cli.go`、`internal/recap/codex_cli.go`、`internal/recap/danmaku.go`

### WebDAV 上传

- `upload` 使用 `RcloneCopier` 执行 `rclone copy source target`。
- 远端路径为 `webdav.remote + filepath.ToSlash(filepath.Join(webdav.base_path, channel_id, slug))`。
- `Fetch` 使用相同 Copier 从远端复制回本地。
- 上传后清理策略：
  - `none` 或空：不清理。
  - `temp`：删除本地 `asr/audio.public.json`，并尝试删除 ASR 临时远端对象。
  - `generated`：删除本地 `asr/` 目录。
  - `all`：确认 session 状态为 `uploaded` 后删除整个本地场次目录。

源码依据：`internal/upload/upload.go`

## Web 前端架构

### 技术栈与构建体系

- 核心框架：Vue 3.5、TypeScript、Vite、Vue Router 4、Pinia。
- UI 与交互：Element Plus 2.9、`@element-plus/icons-vue`、Axios、mitt、marked。
- 构建命令：`web/package.json` 定义 `dev`、`build`、`preview`、`type-check`；生产构建先执行 `vue-tsc -b` 类型检查，再执行 `vite build`。
- 开发代理：`web/vite.config.ts` 将 `/api` 代理到 `http://localhost:8080`，将 `/ws` 代理到 `ws://localhost:8080`。
- 路径别名：Vite 将 `@` 指向 `web/src`，业务模块通过 `@/api`、`@/stores`、`@/components` 等路径导入。
- 嵌入式部署：前端构建产物写入 `cmd/hikami/webdist`，由 `cmd/hikami/embed.go` 通过 Go `embed.FS` 嵌入服务端二进制。

源码依据：`web/package.json`、`web/vite.config.ts`、`cmd/hikami/embed.go`

### 目录结构与职责分层

| 目录/文件 | 职责 |
|---|---|
| `web/src/api` | HTTP API 封装。`client.ts` 管理 Axios 实例和通用方法，各业务文件按后端资源拆分请求函数。 |
| `web/src/stores` | Pinia 状态管理。维护频道、场次、任务、直播状态、运行时能力等跨页面共享状态。 |
| `web/src/components` | 可复用 UI 组件。按 `channel`、`session`、`task`、`layout` 分组，承载列表、表单、状态、时间线、布局等可组合视图。 |
| `web/src/composables` | 组合式函数。封装轮询、WebSocket、主播健康检测等跨组件逻辑。 |
| `web/src/views` | 路由页面。负责页面级数据装配、筛选、批量操作、弹窗抽屉控制和业务动作编排。 |
| `web/src/utils` | 纯工具层。集中维护常量、格式化、状态颜色、生命周期映射和动作能力判断。 |
| `web/src/router` | Vue Router 配置。定义路由表、兼容重定向和页面标题元数据。 |

该分层保持 API 访问、全局状态、页面编排、可复用组件和纯业务规则分离：页面从 store/API 取数，组件通过 props/emit 表达 UI 契约，生命周期与状态映射留在 `utils`，避免把业务规则散落到模板中。

源码依据：`web/CLAUDE.md`、`web/src/api/*.ts`、`web/src/stores/*.ts`、`web/src/components/**/*.vue`、`web/src/views/*.vue`、`web/src/utils/*.ts`

### 状态管理架构

Pinia store 采用组合式写法，每个 store 暴露 `ref` 状态、`loading` 标记和显式 action：

| Store | 状态 | Action / 数据流 |
|---|---|---|
| `channels` | `items`、`loading` | `fetchChannels` 拉取列表；`create`、`update`、`remove` 调用 API 后同步本地列表并显示结果消息。 |
| `sessions` | `items`、`currentDetail`、`loading` | `fetchSessions` 拉取场次列表；`fetchDetail` 维护详情页当前场次。 |
| `tasks` | `items`、`loading` | `fetchTasks` 全量拉取任务；`handleTaskProgress` 根据 WebSocket 事件增量更新任务状态、进度、消息和结束时间，未知任务触发全量刷新。 |
| `liveStatus` | `statusMap`、`loading` | `fetchAll` 拉取直播状态列表并转换为 `channel_id -> LiveStatus` 映射；`getStatus` 提供按主播读取。 |
| `runtime` | `status`、`loading` | `fetchRuntime` 拉取工具、能力和配置状态，供页面动作禁用和设置中心展示。 |

数据流以单向更新为主：视图在 `onMounted` 或用户操作时调用 store action；store 调用 `api` 模块；API 返回后更新响应式状态；组件通过 computed 派生筛选结果、能力提示和操作按钮。实时任务进度由 `AppLayout` 建立 WebSocket 连接，`useWebSocket` 通过 mitt 发布 `task_progress`，`tasks` store 负责落地更新。

源码依据：`web/src/stores/channels.ts`、`web/src/stores/sessions.ts`、`web/src/stores/tasks.ts`、`web/src/stores/liveStatus.ts`、`web/src/stores/runtime.ts`、`web/src/components/layout/AppLayout.vue`

### 路由设计

路由使用 `createWebHistory()`，每个路由项配置 `name`、懒加载组件和 `meta.title`：

| 路径 | 视图 | 说明 |
|---|---|---|
| `/` | `DashboardView` | 工作台首页。 |
| `/live` | `LiveView` | 直播控制台。 |
| `/sessions` | `SessionsView` | 场次列表。 |
| `/sessions/:sid` | `SessionDetailView` | 场次详情，使用生命周期视图，路径保持兼容。 |
| `/tasks` | `TasksView` | 任务中心。 |
| `/channels` | `ChannelsView` | 主播列表。 |
| `/channels/:id` | `ChannelDetailView` | 主播详情或新建配置页。 |
| `/settings` | `SettingsView` | 设置中心。 |

兼容重定向规则：

- `/import` 重定向到 `/sessions?import=1`，由场次页打开手动导入抽屉。
- `/health` 重定向到 `/settings?section=runtime`，系统能力被合并到设置中心。

当前前端没有父子路由配置，嵌套结构由 `AppLayout` 的全局布局和 `router-view` 承载；详情页通过动态段 `/sessions/:sid`、`/channels/:id` 表达资源嵌套关系。`AppLayout` 根据当前路径前缀把 `/sessions/:sid` 归并到场次导航，把 `/channels/:id` 归并到主播导航。

源码依据：`web/src/router/index.ts`、`web/src/components/layout/AppLayout.vue`

### 核心组合函数

- `useChannelHealth`：提供 `getChannelRisks(channel, capabilities)` 纯函数和 `useChannelHealth(channelRef, capabilitiesRef)` 组合函数。风险覆盖自动录制缺少直播间 ID、回放来源未配置、自动 ASR 能力不可用、自动发布缺少发布 Cookie、发布能力不可用；返回 `risks`、`riskCount`、`healthy`。
- `usePolling`：通用页面轮询。默认 5 秒间隔、默认立即执行；返回 `active`、`start`、`stop`。内部在卸载时停止定时器，回调异常交由调用方或 API 层处理。
- `useWebSocket`：同源构造 `/ws` 地址，支持传入自定义 URL；维护 `connected` 状态、指数退避重连、30 秒消息心跳检测；解析 `task_progress` 后通过 mitt 分发。`useEventBus` 暴露同一个事件总线给布局和 store 协作。

源码依据：`web/src/composables/useChannelHealth.ts`、`web/src/composables/usePolling.ts`、`web/src/composables/useWebSocket.ts`

### 生命周期引擎

`web/src/utils/lifecycle.ts` 是前端场次生命周期显示和动作推导的核心纯工具模块，统一把后端状态映射到 6 步流程：

1. `source` 来源处理：`discovered`、`downloading`、`recording`、`importing`
2. `media` 媒体就绪：`media_ready`
3. `asr` ASR：`asr_submitted`、`asr_done`
4. `recap` 回顾：`recap_done`
5. `upload` 上传：`uploaded`
6. `publish` 发布：`published`

关键设计：

- 步骤定义：`LIFECYCLE_STEPS` 保存 key、label、description、statuses，供迷你指示器和详情时间线复用。
- 状态映射：`STATUS_STEP_MAP` 将后端 session status 映射到生命周期步骤；未知状态回退到 `source`。
- 展示格式化：`formatLifecycleForDisplay(session)` 生成当前步骤、当前索引、状态标签、错误标记和 6 个节点的 `completed/current/future/error` 状态。
- 动作元数据：`ACTION_META` 定义 `stop_record`、`submit_asr`、`generate_recap`、`upload`、`fetch`、`publish` 的标签、端点、能力依赖、确认文案和破坏性标记。
- 下一步推导：`getNextAction(status, capabilities)` 先由状态推导动作，再调用能力检测生成 `disabled` 和 `disabledReason`。
- 能力检测：`getDisabledReason` 检查动作依赖的 `Capabilities` 字段；能力未加载时返回等待提示，能力不可用时优先使用后端 reason，再回退到 `ACTION_DISABLED_REASON`。
- 时间推导：来源、媒体、上传、发布节点分别优先使用 `started_at/created_at`、`ended_at`、`uploaded_at`、`published_at`，当前步骤使用 `updated_at`。

源码依据：`web/src/utils/lifecycle.ts`、`web/src/components/session/SessionLifecycleMini.vue`、`web/src/components/session/SessionLifecycleTimeline.vue`、`web/src/components/session/SessionActionPanel.vue`

### 组件架构

#### session 组件族

- `SessionLifecycleMini`：场次列表和工作台中的 6 步圆点指示器。
- `SessionLifecycleTimeline`：详情页生命周期网格，展示节点状态、时间、错误和下一步动作。
- `SessionActionPanel`：根据生命周期引擎输出主操作和次要操作，向页面 emit 具体动作。
- `SessionArtifactSummary`：按媒体、ASR、回顾、归档等产物维度汇总关键文件。
- `SessionFilterBar`：集中处理关键词、主播、来源、生命周期状态过滤。
- `DiscoverResultDrawer`：展示发现回放结果，按新建、跳过、错误分组。
- `ImportSessionDrawer`：手动导入表单，包含媒体文件、弹幕文件和后续处理选项。
- `SessionFileTree`、`SessionStatusBadge`、`SessionActions`：分别负责文件树、状态徽章和旧版操作按钮。

#### channel 组件族

- `ChannelIdentifyDialog`：根据输入识别主播并保存。
- `GlossaryEditor`：可复用术语表编辑器，支持 global/channel 作用域、词条 CRUD、批量操作、Markdown/JSON 导入和 JSON 导出。
- `ChannelListTable`、`ChannelFormDialog`、`ChannelGlossaryDialog`：保留的旧组件，部分能力已由新版详情页和 `GlossaryEditor` 替代。

#### layout 组件族

- `AppLayout`：全局应用布局，包含侧边导航、工作/配置分组、WebSocket 连接状态、运行中任务数、失败任务数和能力告警入口；挂载后初始化任务与运行时状态，并订阅 WebSocket 任务事件。

源码依据：`web/src/components/session/*.vue`、`web/src/components/channel/*.vue`、`web/src/components/layout/AppLayout.vue`

### 页面视图详解

- `DashboardView`：工作台首页。整合任务、场次、主播、运行时能力和直播状态，提供检查直播、发现回放、手动导入入口；展示能力风险条、6 个指标卡片、运行中任务、待处理场次和最近场次。
- `SessionsView`：场次列表。组合 `SessionFilterBar`、生命周期指示器、下一步快捷操作、发现结果抽屉和导入抽屉；支持按关键词、主播、来源、生命周期状态过滤，并提供清空失败场次、清空失败任务等批处理入口。
- `SessionDetailView`：场次详情。顶部 hero 展示标题、状态和主操作；信息条展示主播、来源、时间、文件状态；主体由生命周期时间线、操作面板、产物摘要、任务列表、文件树、回顾预览和元数据标签组成。
- `ChannelsView`：主播列表。展示主播基础信息、UID/Room、自动化状态、发布状态、术语数量和健康风险；使用 `getChannelRisks` 计算风险标签，支持搜索、风险过滤、启停和进入配置。
- `LiveView`：直播控制台。按录制中、直播未录制、异常、未开播分组展示启用主播；支持关键词和状态过滤、立即检查全部、开始录制、停止录制，并用 `usePolling` 定期刷新直播和任务状态。
- `SettingsView`：设置中心。左侧导航分区包括系统能力、API 密钥、全局发布、全局术语；系统能力展示 ASR、回顾、上传、发布可用性、配置摘要和外部工具；API 密钥支持编辑/清除；全局发布编辑默认发布配置；全局术语内嵌 `GlossaryEditor`。
- `ChannelDetailView`：主播详情和新建页。以标签组织概览、来源与录制、自动化、发布覆盖、术语表；结合直播状态、最近场次、能力状态和 `useChannelHealth` 展示风险；发布覆盖页展示全局默认、主播覆盖、最终生效值。
- `TasksView`：任务队列。按状态和类型过滤任务，展示主播、场次、类型、状态、进度、消息和操作；支持失败任务重试、运行任务取消、任务删除、清空失败任务。

源码依据：`web/src/views/DashboardView.vue`、`web/src/views/SessionsView.vue`、`web/src/views/SessionDetailView.vue`、`web/src/views/ChannelsView.vue`、`web/src/views/LiveView.vue`、`web/src/views/SettingsView.vue`、`web/src/views/ChannelDetailView.vue`、`web/src/views/TasksView.vue`

### API 层设计

- `api/client.ts` 创建共享 Axios 实例，`baseURL` 为空以使用同源请求，超时 30 秒。
- 响应拦截器统一处理错误：后端错误优先读取 `data.error`，其次读取 `data.reason`，否则使用 HTTP status；无响应时提示网络错误；错误继续 `Promise.reject` 交由调用方保持控制流。
- 通用方法 `get<T>`、`post<T>`、`put<T>` 返回 `response.data`；`del` 返回 `Promise<void>`（不解析响应体）；`delJson<T>` 返回解析后的 JSON（用于批量删除等需读取响应体的 DELETE 请求）。业务 API 文件不直接暴露 Axios 响应对象。
- 模块化封装按资源拆分：`channels.ts`、`sessions.ts`、`tasks.ts`、`live.ts`、`health.ts`、`settings.ts`、`glossary.ts`；`types.ts` 集中维护 Channel、Session、Task、LiveStatus、RuntimeStatus、Capabilities、PublishConfig、Glossary、WebSocket 事件等类型。
- 批量删除接口统一使用 `delJson<T>` 调用 `DELETE /api/sessions/failed` 和 `DELETE /api/tasks/failed`，走 Axios 拦截器统一错误处理。

源码依据：`web/src/api/client.ts`、`web/src/api/channels.ts`、`web/src/api/sessions.ts`、`web/src/api/tasks.ts`、`web/src/api/live.ts`、`web/src/api/health.ts`、`web/src/api/settings.ts`、`web/src/api/glossary.ts`、`web/src/api/types.ts`

### 与后端交互模式

- REST API 是主要交互通道：页面通过 API 模块调用 `/api/channels`、`/api/sessions`、`/api/tasks`、`/api/live`、`/api/health/runtime`、`/api/secrets`、`/api/config/publish`、`/api/glossary` 等后端接口。
- WebSocket 用于任务实时推送：前端连接同源 `/ws`，后端广播 `task_progress`，前端由 `useWebSocket` 解析并分发，`tasks` store 增量更新任务队列。
- 轮询用于低频状态刷新：`LiveView` 通过 `usePolling` 定期刷新直播状态和任务列表，避免为直播状态单独引入实时协议。
- 能力状态贯穿动作控制：`runtime` store 获取 `Capabilities`，生命周期引擎和页面根据 `asr_submit`、`recap_generate`、`webdav_upload`、`publish_opus` 决定按钮可用性和禁用原因。
- 嵌入式 SPA 部署：生产环境中 Go 服务同时提供 REST、WebSocket 和静态前端资源；前端使用同源相对路径调用后端，刷新或直接访问前端路由时由服务端静态路由回退到 SPA。

源码依据：`internal/handler/server.go`、`web/src/api/*.ts`、`web/src/composables/useWebSocket.ts`、`web/src/composables/usePolling.ts`、`web/src/stores/runtime.ts`、`cmd/hikami/embed.go`

## 测试策略

### 测试分布

当前后端测试全部位于 `internal` 下，按 `func Test` 数量统计：

| 文件 | 测试数量 | 覆盖重点 |
|---|---:|---|
| `internal/asr/asr_test.go` | 31 | ASR 任务前置条件、DashScope 请求体、结果解析、SRT |
| `internal/biliutil/cookie_test.go` | 1 | Cookie 解析 |
| `internal/biliutil/wbi_test.go` | 13 | WBI 签名、密钥缓存、nav 错误 |
| `internal/channel/channel_test.go` | 48 | 主播 CRUD、Bootstrap、识别输入、合并策略 |
| `internal/channel/identify_test.go` | 5 | B 站识别和 Cookie 策略 |
| `internal/db/migrate_test.go` | 2 | 迁移幂等性、核心表创建 |
| `internal/discover/discover_test.go` | 2 | 回放发现和任务创建 |
| `internal/download/download_test.go` | 20 | 下载辅助函数、Handler 入队与失败路径 |
| `internal/glossary/glossary_test.go` | 31 | 术语 CRUD、合并、导入导出、批量操作 |
| `internal/handler/server_test.go` | 13 | API 路由、能力拒绝、任务路由 |
| `internal/importer/importer_test.go` | 8 | 导入源查找、JSON 写入 |
| `internal/live_record/bilibili_test.go` | 3 | 直播状态和流选择 |
| `internal/live_record/danmaku_test.go` | 8 | 弹幕包解析、getDanmuInfo、风控降级 |
| `internal/live_record/ffmpeg_test.go` | 1 | ffmpeg HTTP pipe 和 Headers |
| `internal/live_record/manager_test.go` | 7 | 录制启动、活跃锁、Cookie fallback、流选择 |
| `internal/normalize/normalize_test.go` | 66 | 弹幕解析、多 P 合并、元数据、Handler |
| `internal/publisher/md2opus_test.go` | 17 | Markdown 到 Opus 格式转换 |
| `internal/recap/recap_test.go` | 24 | 回顾任务、Provider、Prompt、弹幕分析 |
| `internal/runtime/probe_test.go` | 1 | ASR 模型和请求模式探测 |
| `internal/secrets/secrets_test.go` | 8 | secrets 存取、环境加载、掩码、校验 |
| `internal/session/session_test.go` | 27 | 场次创建、去重、失败重试、查询 |
| `internal/state/state_test.go` | 10 | 合法/非法状态转换、Apply 持久化 |
| `internal/upload/upload_test.go` | 25 | 上传前置条件、Fetch、清理策略、Handler |
| `internal/worker/task_test.go` | 5 | Task Store 生命周期 |
| `internal/worker/worker_test.go` | 30 | Store、Pool、Hub、恢复策略 |

源码依据：`rg -n "^func Test" internal -g "*_test.go"`、各测试文件

### 测试层次

- 单元测试：状态机、配置辅助逻辑、Cookie/WBI、Markdown 转换、弹幕解析、ASR 结果解析。
- Store 集成测试：使用 SQLite 验证 channel、session、task、glossary、secrets、migration。
- Handler 测试：使用内存数据库和 fake 依赖验证 API 行为。
- 任务处理器测试：用 fake converter/copier/provider/downloader 验证任务前置条件、产物写入和失败路径。
- 外部服务测试：通过 httptest 或 fake 客户端覆盖 B 站、DashScope 相关解析逻辑。

源码依据：各 `*_test.go`

### 覆盖缺口

- 前端当前未配置 Vitest 或端到端测试。
- `cmd/hikami/main.go` 启动编排没有专门测试。
- 真实外部工具和真实第三方 API 调用主要通过接口抽象和 fake 覆盖，没有端到端联网测试。

源码依据：`web/package.json`、`cmd/hikami/main.go`、测试文件分布

## 部署与运维

### 构建与运行

```bash
make build
make build-go
make web-build
make web-dev
make run
make test
make fmt
make tidy
```

运行流程：

1. 复制并编辑 `config.example.yaml`。
2. 运行 `./hikami -config config.yaml` 或 `make run`。
3. 默认监听 `:8080`。

源码依据：`README.md`、`CLAUDE.md`、`cmd/hikami/main.go`

### 依赖管理

- Go module：`hikami-go`，Go 版本 1.25.0。
- 主要 Go 依赖：Gin、gorilla/websocket、Viper、modernc.org/sqlite、robfig/cron。
- 前端依赖：Vue、Vue Router、Pinia、Element Plus、Axios、marked、mitt、Vite、TypeScript。
- 硬依赖外部工具：`ffmpeg`、`ffprobe`，启动时不可用会导致 `StartupError`。
- 可选外部工具：`yt-dlp`、`rclone`、`claude`、`codex`；不可用会降低对应 capability。

源码依据：`go.mod`、`web/package.json`、`internal/runtime/probe.go`

### 监控与健康检查

- `GET /api/healthz` 用于进程存活探测。
- `GET /api/health/runtime` 返回工具可用性、能力开关、ASR 模型与请求模式、配置状态。
- 任务进度通过 `/ws` 实时推送。
- 日志使用 `slog` JSON handler，级别由 `logs.level` 控制。

源码依据：`internal/handler/server.go`、`internal/runtime/probe.go`、`cmd/hikami/main.go`

### 运行时能力判定

- `ReplayDownload`：`yt-dlp` 可用。
- `ASRSubmit`：`rclone` 可用，且 `asr_temp` 和 DashScope API Key 配置完整。
- `RecapGenerate`：OpenAI-compatible/Anthropic 需要 API Key、BaseURL、Model；CLI Provider 需要对应命令可执行。
- `WebDAVUpload`：`rclone` 可用且 `webdav.remote` 非空。
- `PublishOpus`：全局 `publish.enabled` 为 true；运行期可通过 `PUT /api/config/publish` 更新。

源码依据：`internal/runtime/probe.go`、`internal/handler/server.go`

## 扩展性设计

### 接口抽象

| 接口 | 位置 | 扩展点 |
|---|---|---|
| `download.Downloader` | `internal/download/download.go` | 替换 yt-dlp 下载实现 |
| `discover.Lister` | `internal/discover/discover.go` | 替换回放列表发现来源 |
| `live_record.BiliClient` | `internal/live_record/types.go` | 替换直播状态/流获取客户端 |
| `live_record.AudioRecorder` | `internal/live_record/types.go` | 替换 ffmpeg 录制实现 |
| `live_record.DanmakuRecorder` | `internal/live_record/types.go` | 替换弹幕录制协议 |
| `normalize.AudioConverter` | `internal/normalize/normalize.go` | 替换标准化转码实现 |
| `importer.MediaConverter` | `internal/importer/importer.go` | 替换导入转码实现 |
| `asr.Transcriber` | `internal/asr/asr.go` | 增加新的 ASR 后端 |
| `recap.Provider` | `internal/recap/recap.go` | 增加新的 AI 回顾后端 |
| `upload.Copier` / `upload.Deleter` | `internal/upload/upload.go` | 替换 WebDAV/rclone 上传与删除 |
| `publisher.OpusClient` | `internal/publisher/bilibili_opus.go` | 替换 B 站发布客户端 |
| `publisher.OpusCoverUploader` | `internal/publisher/bilibili_opus.go` | 替换封面上传实现 |
| `biliutil.URLSigner` | `internal/biliutil/wbi.go` | 替换 WBI 签名实现 |

源码依据：各接口定义文件

### 插件点与未来扩展方向

- 新来源适配：新增来源模块后创建 `session`，写入 `raw/`，提交 `normalize` 即可接入后续管道。
- 新 ASR 后端：实现 `asr.Transcriber`，在 `NewConfiguredTranscriber` 中按配置选择。
- 新回顾后端：实现 `recap.Provider`，在 `NewConfiguredProvider` 中按 `recap_ai.provider` 选择。
- 新发布目标：当前 `publisher` 面向 B 站 Opus；可增加新的发布模块和任务类型，复用 `recap` 产物与 `state` 事件。
- 新归档后端：实现 `upload.Copier`/`Deleter`，替换 rclone 或扩展到其他对象存储。
- 前端扩展：通过 `web/src/api` 增加 API 封装、Pinia store 和对应 view/component。

源码依据：`cmd/hikami/main.go` 任务注册方式、各模块接口定义、`web/src/api/*.ts`

### 设计约束

- 场次状态更新应通过 `internal/state` 的 `Apply` 完成，避免业务模块直接散写 `sessions.status`。
- 场次路径以 `output_root/channel_id/slug` 为根，原始输入保存在 `raw/`，可再生文件保存在 `asr/`、`package/`、`recap/`。
- `normalize` 对 JSON 产物使用临时文件加 rename 的原子写入。
- 主播隔离依赖 `channel_id`，任务、场次、输出目录和配置合并均携带主播维度。
- 发布与下载 Cookie 分离，避免发布凭证和下载/录制凭证混用。

源码依据：`internal/state/state.go`、`internal/normalize/normalize.go`、`internal/session/session.go`、`internal/channel/channel.go`、`internal/biliutil/cookie.go`

## 源码依据索引

- 项目总览：`CLAUDE.md`、`README.md`
- 入口与编排：`cmd/hikami/main.go`、`cmd/hikami/embed.go`
- 配置：`internal/config/config.go`、`config.example.yaml`
- 数据库迁移：`internal/db/migrate.go`
- 运行时探测：`internal/runtime/probe.go`
- API 与错误映射：`internal/handler/server.go`
- 主播与识别：`internal/channel/channel.go`、`internal/channel/identify.go`
- 场次：`internal/session/session.go`
- 状态机：`internal/state/state.go`
- 任务系统：`internal/worker/task.go`、`internal/worker/worker.go`、`internal/worker/hub.go`
- 回放发现与下载：`internal/discover/discover.go`、`internal/download/download.go`
- 直播录制与弹幕：`internal/live_record/*.go`
- 手动导入：`internal/importer/importer.go`
- 标准化：`internal/normalize/normalize.go`
- ASR：`internal/asr/asr.go`、`internal/asr/dashscope.go`
- 回顾：`internal/recap/*.go`
- 上传：`internal/upload/upload.go`
- 发布：`internal/publisher/*.go`
- B 站工具：`internal/biliutil/*.go`
- Secrets：`internal/secrets/secrets.go`
- 术语表：`internal/glossary/glossary.go`
- 调度：`internal/scheduler/scheduler.go`
- 前端：`web/CLAUDE.md`、`web/src/api/*.ts`、`web/src/stores/*.ts`、`web/src/composables/*.ts`、`web/src/views/*.vue`、`web/src/components/**/*.vue`
