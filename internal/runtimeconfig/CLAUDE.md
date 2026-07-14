[根目录](../../CLAUDE.md) > **internal/runtimeconfig**

# internal/runtimeconfig -- 全局运行时配置覆盖持久化

## 模块职责

把 UI 通过设置面板改动的**全局运行时配置**按「配置段」(per-section JSON) 持久化到 SQLite `runtime_settings` 表。`config.yaml` 由此降级为**只读基线**(启动由 viper 加载一次);启动时 handler 链路在 `db.Migrate` 之后调用 `Load`,再由 `internal/config.ApplyOverrides` 用本表覆盖基线,得到最终生效配置。

**覆盖优先级(高→低)**:`runtime_settings` > `config.yaml` > viper `SetDefault`。

本包**只做 raw JSON 存取**(含事务版 `SaveTx` + `WithTx` helper),**不**感知任何 `config` 类型——DTO 与 `ApplyOverrides` 逻辑放在 `internal/config`,以避免 `config` 反向依赖 `db`。

## 入口与启动

- **入口文件**: `store.go`
- **核心类型**: `Store`
- **启动集成** (`cmd/hikami/main.go:102-113`):
  - `runtimeconfig.NewStore(database)` 构造 Store。
  - `Load(ctx)` 读出 `map[section]json.RawMessage`;**DB 级错误(非空表)视为启动 fatal**,section JSON 损坏由 `ApplyOverrides` 跳过+告警(不致命)。
  - `config.ApplyOverrides(cfg, overrides)` 把覆盖应用到内存 cfg。

## 对外接口

| 方法/函数 | 说明 |
|-----------|------|
| `NewStore(db *sql.DB) *Store` | 基于已迁移的 `*sql.DB` 构造 Store |
| `Store.Load(ctx) (map[string]json.RawMessage, error)` | 读取所有 section;空表返回空 map(调用方按零值处理 = 不覆盖) |
| `Store.Save(ctx, section, data) error` | 写入/覆盖单个 section 的 data(普通路径) |
| `Store.SaveTx(ctx, tx, section, data) error` | Save 的事务版,与 `secrets` 的 `*Tx` 方法共用同一 `*sql.Tx`(原子提交) |
| `Store.DB() *sql.DB` | 暴露底层 DB,供 handler 用 `WithTx(ctx, db, fn)` 绑定事务 |
| `WithTx(ctx, db, fn) error` | 在一个事务内执行 `fn(*sql.Tx)`;`fn` 返回 nil 提交、返回错误回滚 |
| `SavePayload(data) error` | 防御性校验(data 非空);真正的 JSON 有效性由 DB `CHECK(json_valid(data))` 兜底 |

**无独立 HTTP 端点**——本包是纯存储层,被 `internal/handler` 的 6 个全局设置 handler 内部使用(`runtimeconfig.WithTx` + `SaveTx`),配合 `secrets` 表做原子写入。

## 关键依赖与配置

- 仅依赖 `database/sql` + `encoding/json`,无外部库。
- **强依赖 `internal/db` 的迁移**:`runtime_settings` 表(v33)的 `CHECK(section IN (...))` 与 `CHECK(json_valid(data))` 约束是本包正确性的兜底——未知 section / 非法 JSON 由 DB 拒绝(见测试 `TestSaveRejectsUnknownSection` / `TestSaveRejectsInvalidJSON`)。
- **与 `internal/secrets` 共享同一 `*sql.DB` 实例**(通过 `Store.DB()`),使密钥类 handler 能把「secrets 写入 + runtime_settings 写入」绑成同一事务。
- **不依赖 `internal/config`**(刻意反向解耦:DTO 与 Apply 逻辑在 config 包,本包保持为纯 raw JSON 存储)。

## 数据模型

**`runtime_settings` 表** (DB schema v33,定义见 `internal/db/migrate.go:188-196`):

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `section` | TEXT | `NOT NULL`, `CHECK(section IN ('publish','asr_s3','dashscope','recap_ai','webdav','archive'))`, `PRIMARY KEY` | 配置段名(白名单 6 个全局段) |
| `data` | TEXT | `NOT NULL DEFAULT '{}'`, `CHECK(json_valid(data))` | 该段 DTO 的 JSON(`presence-aware`,指针字段,不含隐藏字段/密钥) |
| `updated_at` | TEXT | `NOT NULL DEFAULT (datetime('now'))` | 最后更新时间 |

**Section 白名单(6 个全局段)**,与 `internal/config` 的 `*SectionDTO` 一一对应:

| section | 对应 DTO | 管理 handler | 走 secrets 的密钥字段 |
|---------|----------|--------------|----------------------|
| `publish` | `PublishSectionDTO` | `updatePublishConfig` | — |
| `asr_s3` | `ASRS3SectionDTO` | `updateASRS3Config` | `access_key_secret` |
| `dashscope` | `DashScopeSectionDTO` | `updateDashScopeConfig` | `api_key` |
| `recap_ai` | `RecapAISectionDTO` | `updateRecapConfig` | `api_key` |
| `webdav` | `WebDAVSectionDTO` | `updateWebDAVConfig` | `password` |
| `archive` | `ArchiveSectionDTO` | `updateArchiveConfig` | — |

> 每个 `SectionDTO` 只含对应 handler 实际管理的字段(指针、`presence-aware`),**不含**完整 config struct 的隐藏字段(如 `RecapAIConfig.CLIPath`/`GlossaryFile`),避免冻结手工改 yaml 的字段。密钥字段不进 DTO(走 secrets 表),WebDAV/ASRS3 通过 `*_managed` tombstone 标记接管状态。

## 测试与质量

- `store_test.go`: **9 个测试用例**(基于 `:memory:` SQLite + `db.Migrate`):
  - `TestLoadEmpty`: 空表返回空 map
  - `TestSaveAndLoad`: 基本存取往返
  - `TestSaveReplace`: 同 section 二次 Save 覆盖(`ON CONFLICT DO UPDATE`)
  - `TestSaveRejectsInvalidJSON`: DB `CHECK(json_valid)` 拒绝非 JSON
  - `TestSaveRejectsUnknownSection`: DB `CHECK(section IN (...))` 拒绝未知段
  - `TestWithTxCommits`: `fn` 成功 → 提交、可见
  - `TestWithTxRollsBackOnFnError`: `fn` 失败 → 整段回滚
  - `TestWithTxAtomicTwoSections`: 两次 `SaveTx` 在同一事务,第二写触发约束 → 第一写也被回滚(原子性)

## 常见问题 (FAQ)

**Q: 为什么本包不直接 import `internal/config` 来做类型化存取?**
A: 反向依赖规避。`config` 已经被 `db` 之外的众多包依赖;若 `runtimeconfig` 持有 `config` 类型,会让 `config` 与 `db` 之间形成不必要的耦合。本包保持为纯 raw JSON 存储,DTO 与 `ApplyOverrides` 留在 `config` 包(那里可以自由 import `runtimeconfig` 类型,反之不行)。

**Q: 密钥(WebDAV 密码、ASR S3 secret、AI API key)为什么不存进本表?**
A: 密钥统一走 `internal/secrets` 表(带掩码、可加载到环境变量)。本表只存「非密钥」配置段。密钥类 handler 把两段写入绑进同一 `*sql.Tx`(`WithTx` + `SaveTx`),保证「密钥写入 + 配置段写入」原子提交——commit 成功后调用方才更新进程 env / 内存 cfg。

**Q: 启动时某个 section 的 JSON 损坏怎么办?**
A: `ApplyOverrides` 逐段 unmarshal,损坏段 `slog.Error` + 跳过(不致命),其余段仍正常应用;DB 级错误(非空表读取失败)才是启动 fatal。

## 相关文件清单

- `store.go` — `Store` 实现、`SaveTx`、`WithTx`、`SavePayload`(103 行)
- `store_test.go` — 单元测试(9 个用例)

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-01 | 初始化 | 首次生成模块文档(配合 `feat(config): 全局运行时配置持久化到 SQLite`,DB v33)。 |
