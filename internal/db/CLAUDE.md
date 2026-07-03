[根目录](../../CLAUDE.md) > **internal/db**

# internal/db -- SQLite 数据库初始化与迁移

## 模块职责

打开 SQLite 数据库文件（启用外键约束），执行 schema 迁移。使用版本化迁移机制，每个迁移版本记录在 `schema_migrations` 表中。

## 入口与启动

- **入口文件**: `db.go`, `migrate.go`
- **核心函数**: `Open(path) (*sql.DB, error)`, `Migrate(*sql.DB) error`

## 对外接口

| 函数 | 说明 |
|------|------|
| `Open(path)` | 打开 SQLite 数据库，启用 `PRAGMA foreign_keys = ON` |
| `Migrate(database)` | 按顺序执行版本化迁移，跳过已应用的版本 |

## 关键依赖与配置

- 驱动: `modernc.org/sqlite`（纯 Go，无 CGO 依赖）
- 迁移存储: `schema_migrations` 表，记录 `version` 和 `applied_at`

## 数据模型

**十张核心表 + 33 个迁移版本：**

1. **channels** -- 主播配置
   - `id TEXT PK`, `name`, `uid`, `live_room_id`, `replay_source_url`, `space_url`, `title_prefix`, `cookie_file`, `enabled`, `created_at`, `updated_at`
   - 追加列: `download_cookie_file` (v9), `auto_record` (v10, 默认 1), `auto_asr` (v11, 默认 0), `record_danmaku` (v13, 默认 1), `publish_enabled`/`publish_mode`/`publish_category_id`/`publish_list_id`/`publish_private_pub`/`publish_original`/`auto_publish` (v17), `publish_aigc`/`publish_timer_pub_time`/`publish_cover_url`/`publish_topics` (v18), `source_mode` (v19, 默认 'both'), `discover_limit` (v20, 默认 0), `download_account_id` (v26, 默认 NULL), `publish_account_id` (v27, 默认 NULL), `recap_model` (v29, 默认 ''), `max_continuations` (v30, 默认 -1), `auto_recap` (v32, 默认 1)

2. **sessions** -- 场次
   - `id TEXT PK`, `slug`, `channel_id FK`, `source_type`, `source_id`, `title`, `started_at`, `ended_at`, `source_url`, `status`, `current_task_id`, `last_error`, `local_available`, `uploaded_at`, `published_at`, `publish_target`, `archived_at` (v31), `created_at`, `updated_at`
   - 唯一索引: `(channel_id, source_type, source_id)`, `(channel_id, slug)`

3. **tasks** -- 任务
   - `id TEXT PK`, `channel_id FK`, `session_id FK`, `type`, `status`, `payload`, `progress`, `message`, `error`, `attempt`, `started_at`, `finished_at`, `created_at`, `updated_at`
   - 追加列: `usage_metadata` (v24, 默认 `{}`)
   - 索引: `status`, `(channel_id, session_id)`

4. **secrets** -- API Key 存储
   - `key TEXT PK`, `value`, `updated_at`

5. **glossary_entries** -- 术语表条目
   - `id INTEGER PK AUTO`, `channel_id`, `term`, `canonical`, `category`, `enabled`, `created_at`, `updated_at`
   - 唯一索引: `(channel_id, term)`

6. **glossary_meta** -- 术语表备注
   - `channel_id TEXT PK`, `note`, `updated_at`

7. **recap_templates** -- 回顾模板（v21 新增）
   - `id INTEGER PK AUTO`, `channel_id`, `name`, `system_prompt`, `user_format`, `fan_name`, `extra_vars`, `enabled`, `is_default`, `created_at`, `updated_at`
   - 唯一索引: `(channel_id, name)`（v22）
   - 内置默认: v23 插入一条 `channel_id='', name='default', system_prompt='__builtin__', user_format='__builtin__', is_default=1` 的记录

8. **bili_cookie_accounts** -- B 站 Cookie Account（v25 新增）
   - `id INTEGER PK AUTO`, `uid INTEGER UNIQUE`, `nickname`, `cookie_file`, `is_default_download`, `is_default_publish`, `created_at`, `updated_at`
   - 用于全局账号池，`channels.download_account_id`/`channels.publish_account_id` 保存主播级账号覆盖

9. **glossary_candidates** -- AI 术语发现候选（v28 新增）
   - `id INTEGER PK AUTO`, `channel_id`, `term`, `canonical`, `category`, `status` (pending/approved/rejected, CHECK 约束), `confidence` (0-1), `score` (0-1), `occurrence_count`, `session_count`, `first_session_id`, `last_session_id`, `reason`, `normalized_key` (NOT NULL), `created_at`, `updated_at`, `reviewed_at`
   - 唯一索引: `(channel_id, normalized_key)`
   - 索引: `(channel_id, status, score DESC, updated_at DESC)`, `(last_session_id)`

10. **runtime_settings** -- 全局运行时配置覆盖（v33 新增）
   - `section TEXT PK`, `data TEXT NOT NULL DEFAULT '{}'`, `updated_at TEXT NOT NULL DEFAULT (datetime('now'))`
   - `CHECK(section IN ('publish','asr_s3','dashscope','recap_ai','webdav','archive'))` 白名单限定 6 个全局段
   - `CHECK(json_valid(data))` 保证 JSON 完整性
   - config.yaml 降级为只读基线，UI 改动按段存此表；启动时 `config.ApplyOverrides` 用本表覆盖 viper 基线（详见 `internal/runtimeconfig/CLAUDE.md`）

**迁移版本：**

| 版本 | 说明 |
|------|------|
| 1 | `schema_migrations` 表 |
| 2 | `channels` 表 |
| 3 | `sessions` 表 |
| 4 | `sessions_channel_source_uidx` 唯一索引 |
| 5 | `sessions_channel_slug_uidx` 唯一索引 |
| 6 | `tasks` 表 |
| 7 | `tasks_status_idx` 索引 |
| 8 | `tasks_channel_session_idx` 索引 |
| 9 | `channels` 追加 `download_cookie_file` |
| 10 | `channels` 追加 `auto_record`（默认 1） |
| 11 | `channels` 追加 `auto_asr`（默认 0） |
| 12 | `secrets` 表 |
| 13 | `channels` 追加 `record_danmaku`（默认 1） |
| 14 | `glossary_entries` 表 |
| 15 | `glossary_entries_channel_term_uidx` 唯一索引 |
| 16 | `glossary_meta` 表 |
| 17 | `channels` 追加 `publish_enabled`/`publish_mode`/`publish_category_id`/`publish_list_id`/`publish_private_pub`/`publish_original`/`auto_publish` |
| 18 | `channels` 追加 `publish_aigc`/`publish_timer_pub_time`/`publish_cover_url`/`publish_topics` |
| 19 | `channels` 追加 `source_mode`（默认 'both'） |
| 20 | `channels` 追加 `discover_limit`（默认 0） |
| 21 | `recap_templates` 表 |
| 22 | `recap_templates_channel_name_uidx` 唯一索引 |
| 23 | 插入内置默认回顾模板 |
| 24 | `tasks` 追加 `usage_metadata`（默认 `{}`） |
| 25 | `bili_cookie_accounts` 表 |
| 26 | `channels` 追加 `download_account_id`（默认 NULL） |
| 27 | `channels` 追加 `publish_account_id`（默认 NULL） |
| 28 | `glossary_candidates` 表（含 CHECK 约束、唯一索引、评分/状态索引） |
| 29 | `channels` 追加 `recap_model`（默认 ''） |
| 30 | `channels` 追加 `max_continuations`（默认 -1） |
| 31 | `sessions` 追加 `archived_at`（发布成功后自动归档到 WebDAV 用，不推进主状态） |
| 32 | `channels` 追加 `auto_recap`（默认 1，per-channel 自动回顾开关，ASR 成功后是否自动生成回顾） |
| 34 | `tasks.bypass_fail_state` 列（任务实例级 bypass fail state，配合重新生成回顾） |
| 33 | `runtime_settings` 表（全局运行时配置覆盖，per-section JSON；`CHECK(section)` 白名单 6 段 + `CHECK(json_valid(data))`） |

## 测试与质量

- `migrate_test.go`: 9 个测试用例，覆盖：
  - `TestMigrateIsIdempotent`: 迁移幂等性（重复执行不报错，版本数正确）
  - `TestMigrateCreatesCoreTables`: 核心表创建验证（channels, sessions, tasks）
  - `TestMigrateCreatesAllTables`: 核心应用表创建验证（枚举 10 张：channels, sessions, tasks, secrets, glossary_entries, glossary_meta, recap_templates, bili_cookie_accounts, glossary_candidates, runtime_settings）
  - `TestMigrateCreatesIndexes`: 7 个关键索引创建验证
  - `TestMigrateDefaultRecapTemplate`: 内置默认回顾模板插入验证
  - `TestOpen_EnablesForeignKeys`: 外键约束启用验证
  - `TestMigrate_VersionSequence`: 版本号连续性验证
  - `TestMigrate_ChannelsColumns`: channels 表 10 个追加列存在性验证
  - `TestOpen_SetsFilePermissions0600`: 主数据库文件权限 0600 校验（ISS-7）

## 常见问题 (FAQ)

**Q: 如何新增数据库表？**
A: 在 `migrate.go` 的 `migrations` 切片中追加新的 DDL 语句。注意每条 migration 都有自动递增的版本号。

**Q: 迁移失败怎么办？**
A: 迁移在事务中执行。失败时整个事务回滚，已应用的版本不受影响。修复后重新启动即可。

**Q: `__builtin__` 标记是什么意思？**
A: `recap_templates` 表中的 `system_prompt` 和 `user_format` 字段使用 `__builtin__` 标记表示使用 Go 代码中的内置默认常量。Resolve 合并时会将 `__builtin__` 替换为实际值。

## 相关文件清单

- `db.go` -- 数据库打开
- `migrate.go` -- 迁移定义与执行（34 个版本）
- `migrate_test.go` -- 迁移测试（9 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-23 | 重大更新 | 迁移增至 v32：v31 新增 `sessions.archived_at TEXT`（发布成功后自动归档到 WebDAV 的时间戳，不推进会话主状态，由 `internal/archive` 写入）；v32 新增 `channels.auto_recap INTEGER NOT NULL DEFAULT 1`（per-channel 自动回顾开关，三态经 `resolveAutoRecap` 兜底为 true）。总版本数 30→32 |
| 2026-06-01 | 测试扩充 | migrate_test.go 从 2 扩充至 8 个用例：新增 TestMigrateCreatesAllTables（9 张表）、TestMigrateCreatesIndexes（7 个索引）、TestMigrateDefaultRecapTemplate（内置模板）、TestOpen_EnablesForeignKeys（外键）、TestMigrate_VersionSequence（版本连续性）、TestMigrate_ChannelsColumns（10 个追加列） |
| 2026-05-23 | 重大更新 | 迁移增至 v30：v28 新增 `glossary_candidates` 表（含 status CHECK 约束、confidence/score REAL、normalized_key 唯一索引、score/status 复合索引、last_session_id 索引）；v29 新增 `channels.recap_model`（默认 ''）；v30 新增 `channels.max_continuations`（默认 -1） |
| 2026-05-17 | 修正 | 修正迁移版本计数：v19 仅含 source_mode，v20 仅含 discover_limit；v21-v23 分别为 recap_templates 表、唯一索引、内置模板插入；v24 usage_metadata；v25 bili_cookie_accounts；v26 download_account_id；v27 publish_account_id。总版本数从 25 修正为 27 |
| 2026-05-15 | 重大更新 | 迁移文档基线增至 v25 |
| 2026-05-14 | 重大更新 | 迁移增至 v21 |
| 2026-05-08 | 更新 | 迁移增至 v18 |
| 2026-05-07 | 更新 | 新增迁移 v11-v16 |
| 2026-05-04 | 更新 | 新增迁移 v10、migrate_test.go |
| 2026-05-03 | 更新 | 新增迁移 v7-v9 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
