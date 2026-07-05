[根目录](../../CLAUDE.md) > **internal/secrets**

# internal/secrets -- API Key 管理

## 模块职责

在 SQLite 数据库中存储和管理 API Key（如 DashScope、AI 回顾等），支持值掩码显示、加载到环境变量、已知 Key 校验、全量清除（含事务版）。作为 Web 设置页面的后端，让用户通过 Web 界面配置 API Key，无需手动编辑环境变量。全量清除用于配置导入的 overwrite 策略——`Clear`（普通版）与 `ClearTx`（事务版，与 `runtimeconfig.WithTx` + `SaveTx` 绑同一 `*sql.Tx`，避免 Clear 成功但后续写入失败导致密钥全丢）。

## 入口与启动

- **入口文件**: `secrets.go`
- **核心类型**: `Store`

## 对外接口

| 方法/函数 | 说明 |
|-----------|------|
| `NewStore(db)` | 创建 Store 实例 |
| `Store.Get(ctx, key)` | 获取指定 Key 的值 |
| `Store.Set(ctx, key, value)` | 设置或覆盖指定 Key 的值 |
| `Store.SetTx(ctx, tx, key, value)` | Set 的事务版（共用调用方传入的 `*sql.Tx`） |
| `Store.GetTx(ctx, tx, key)` | Get 的事务版 |
| `Store.List(ctx)` | 列出所有 Key（含掩码值和来源） |
| `Store.Delete(ctx, key)` | 删除指定 Key |
| `Store.DeleteTx(ctx, tx, key)` | Delete 的事务版 |
| `Store.Clear(ctx)` | 清除所有 Key（DELETE FROM secrets） |
| `Store.ClearTx(ctx, tx)` | Clear 的事务版（与 `runtimeconfig.SaveTx` / `SetTx` 共用同一 `*sql.Tx`），用于配置导入 overwrite 的原子清理 |
| `Store.LoadIntoEnv(ctx)` | 将所有非空 Key 加载为环境变量 |
| `MaskValue(value)` | 掩码函数：短于 8 字符返回 `****`，否则保留末 4 位 |
| `KnownKeys(dashScopeEnv, recapEnv)` | 返回已知 Key 列表用于校验 |
| `BuildView(key, dbValue)` | 构建带来源和掩码的视图对象 |
| `ValidateKey(key, knownKeys)` | 校验 Key 是否在已知列表中 |

**API 端点：**
- `GET /api/secrets` -- 列出所有已知 Key 的状态（含数据库和环境变量来源）
- `PUT /api/secrets/:key` -- 更新指定 Key 的值

## 关键依赖与配置

- 无外部依赖，直接操作 SQLite `secrets` 表
- 启动时 `LoadIntoEnv` 将数据库中的 Key 加载到环境变量，使下游模块（DashScope、recap）通过 `os.Getenv` 读取
- 环境变量中的 Key 作为备选方案仍然可用（`BuildView` 比较数据库和环境变量）

## 数据模型

**Secret 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `Key` | string PK | 环境变量名（如 `DASHSCOPE_API_KEY`） |
| `Value` | string | API Key 值 |
| `UpdatedAt` | string | 最后更新时间 |

**SecretView 结构体（API 响应）：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `Key` | string | 环境变量名 |
| `MaskedValue` | string | 掩码后的值 |
| `Set` | bool | 是否已配置 |
| `Source` | string | 来源：`database` / `environment` / `none` |
| `UpdatedAt` | string | 数据库中的更新时间 |

## 测试与质量

- `secrets_test.go`: 9 个测试用例，覆盖：
  - `TestSetAndGet`: 基本存取
  - `TestSetOverwrite`: 值覆盖
  - `TestDelete`: 删除
  - `TestList`: 列表排序
  - `TestLoadIntoEnv`: 加载到环境变量
  - `TestLoadIntoEnvSkipsEmpty`: 空值不覆盖环境变量
  - `TestMaskValue`: 掩码函数（空值、短值、长值）
  - `TestValidateKey`: 已知 Key 校验
  - `TestSetWritesLocalTimezoneUpdatedAt`: `updated_at` 存本地时区 RFC3339（2026-07-04 DB 时区统一）

## 常见问题 (FAQ)

**Q: API Key 在哪里配置？**
A: 通过 Web 设置页面（`/settings`）配置，也可通过环境变量设置。数据库中的 Key 优先级更高，启动时加载到环境变量。

**Q: 重启服务后 Key 生效吗？**
A: 是的，启动时 `LoadIntoEnv` 将数据库 Key 加载到环境变量。

## 相关文件清单

- `secrets.go` -- Store 实现、掩码、校验、视图构建、Clear/ClearTx 全量清除、SetTx/GetTx/DeleteTx 事务版
- `secrets_test.go` -- 单元测试（9 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-05 | 功能 | 新增 `ClearTx(ctx, tx)` 方法（DELETE FROM secrets 的事务版），用于配置备份 import 的 overwrite 策略——与 `runtimeconfig.WithTx` + `SaveTx` 绑同一 `*sql.Tx`，避免 Clear 成功但后续写入失败导致密钥全丢（`6a2bb18`） |
| 2026-07-04 | 修复 | `updated_at` 改存本地时区 RFC3339（DB 时间字段统一的一部分），新增 `TestSetWritesLocalTimezoneUpdatedAt` |
| 2026-06-03 | 增量扫描 | 新增 `Clear(ctx)` 方法（DELETE FROM secrets），用于配置导入 overwrite 策略的全量清除 |
| 2026-05-04 | 初始化 | 首次生成模块文档 |
