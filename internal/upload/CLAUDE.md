[根目录](../../CLAUDE.md) > **internal/upload**

# internal/upload -- WebDAV 归档上传

## 模块职责

使用 WebDAV 将场次目录上传到远端，并支持从远端取回。支持两种后端实现：
- **rclone**：传统方式，通过 rclone 命令行工具操作
- **原生 WebDAV**（`WebDAVCopier`）：使用 gowebdav 库直接操作，无需 rclone 依赖

上传成功后根据清理策略清理本地和远端资源。

## 入口与启动

- **入口文件**: `upload.go`, `webdav_copier.go`
- **任务类型**: `upload`

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewHandler(cfg, sessions, states, copier)` | 创建 Handler |
| `CreateTask(ctx, pool, sessionID)` | 校验前置条件并创建任务 |
| `Fetch(ctx, sessionID)` | 从 WebDAV 取回到本地 |
| `Register(pool)` | 注册 upload 任务处理器 |
| `NewConfiguredCopier(cfg)` | 工厂方法：根据配置自动选择 WebDAV 原生或 rclone Copier |
| `NewConfiguredDeleter(cfg)` | 工厂方法：根据配置自动选择 WebDAV 原生或 rclone Deleter |
| `CleanupSession(ctx, sessions, deleter, cfg, sessionDir, sessionInfo, policy, guardStatus)` | 抽出的共享清理函数（archive 复用）；按 `policy` 派发到 `cleanupTempShared`/`cleanupGeneratedShared`/`cleanupAllShared`，`guardStatus` 决定守卫态（upload 传 `uploaded`，archive 传 `published`） |

**Copier/Deleter 接口：**

```go
type Copier interface {
    Copy(ctx context.Context, source string, target string) error
}

type Deleter interface {
    Delete(ctx context.Context, target string) error
}
```

**后端选择逻辑：**
- `WebDAVConfig.NativeConfigured()`（URL 非空）为 true 时使用 `WebDAVCopier`
- 否则回退到 `RcloneCopier`（需 rclone 可用）

**API 端点：**
- `POST /api/sessions/:sid/upload` -- 上传
- `POST /api/sessions/:sid/fetch` -- 取回

**前置条件：**
- 场次状态为 `asr_done` 或 `recap_done`
- 本地场次目录存在且为目录
- `webdav.remote` 或 `webdav.url` 配置非空
- 不能有同场次的活跃上传任务

## WebDAVCopier -- 原生 WebDAV 实现

`webdav_copier.go` 使用 gowebdav 库直接操作 WebDAV，无需 rclone 依赖：

| 方法 | 说明 |
|------|------|
| `NewWebDAVCopier(cfg)` | 创建实例（URL + 用户名 + 密码） |
| `Copy(ctx, source, target)` | 递归上传本地目录到 WebDAV 远端 |
| `Fetch(ctx, source, target)` | 递归从 WebDAV 远端下载到本地目录 |
| `Delete(ctx, target)` | 递归删除远端目录及所有文件 |

**实现细节：**
- `Copy`：遍历本地目录树，逐文件 `MkdirAll` + `WriteStreamWithLength`
- `Fetch`：递归读取远端目录，逐文件 `ReadStream` + 本地写入
- `Delete`：递归遍历远端目录，逐文件删除后删除目录本身
- 所有操作支持 context 取消
- 404 错误在 Fetch/Delete 中优雅处理
- 内部辅助函数：`joinWebDAVPath`（路径拼接）、`pathDir`（取父目录）、`relativeTarget`（相对路径）、`isWebDAVNotExist`（404 判断）

## 关键依赖与配置

- **rclone 后端**：`rclone copy {source} {target}`
- **原生 WebDAV 后端**：`github.com/studio-b12/gowebdav`
- `webdav.remote`: rclone 远端名称（如 `hikami-webdav:`）
- `webdav.base_path`: 远端基础路径
- `webdav.url`: WebDAV 服务 URL（原生模式）
- `webdav.username`: WebDAV 用户名（原生模式）
- `webdav.password`: WebDAV 密码（原生模式）
- `webdav.password_env`: WebDAV 密码环境变量名（优先于 password）
- `upload.cleanup_policy`: 上传后清理策略

## 清理策略

上传成功后根据 `upload.cleanup_policy` 执行清理：

| 策略 | 说明 |
|------|------|
| `none` | 不清理（默认） |
| `temp` | 删除 ASR 临时公开音频和远端临时对象 |
| `generated` | 删除 `asr/` 目录（可重新生成的中间文件） |
| `all` | 确认上传状态后删除整个本地场次目录，并置 `session.local_available=false`（驱动 glossary/recap/publisher 守卫，需先 `Fetch` 取回） |

> `local_available` 闭环：`all` 清理 → 置 `false`；`Fetch` 取回成功 → 置回 `true`。`none`/`temp`/`generated` 不删除 `package/`+`recap/`，故不置位。

> 清理逻辑已抽出为共享函数 `CleanupSession`（派发到 `cleanupTempShared`/`cleanupGeneratedShared`/`cleanupAllShared`），`internal/archive` 归档后复用同一套清理，以 `guardStatus` 区分守卫态（upload=`uploaded`、archive=`published`）。

## 测试与质量

- `upload_test.go`: 28 个测试用例，覆盖：
  - CreateTask: 成功（asr_done/recap_done）、错误状态、无远端配置、目录不存在、非目录、活跃冲突、session 不存在
  - Fetch: 成功、无远端配置、session 不存在、复制失败、取回后置 `local_available=true`
  - 清理策略: none（含空值）、unknown、generated（删除 asr/）、temp（删除公开音频+远端删除+本地文件不存在）、all（删除整个目录+置 `local_available=false`/跳过未上传/session 查询失败）
  - HandleTask: 成功流程、复制失败
- `webdav_copier_test.go`: 10 个测试用例，覆盖：
  - joinWebDAVPath: 多部分拼接、斜杠清理、空部分过滤
  - pathDir: 含斜杠取父目录、无斜杠返回空
  - relativeTarget: 正常去除前缀、空 basePath、target 等于 basePath
  - isWebDAVNotExist: os.ErrNotExist 返回 true、其他错误返回 false

## 相关文件清单

- `upload.go` -- Handler、Copier/Deleter 接口、RcloneCopier、NewConfiguredCopier/NewConfiguredDeleter 工厂方法、清理策略
- `webdav_copier.go` -- WebDAVCopier 原生 WebDAV 实现（Copy/Fetch/Delete）、路径安全处理、辅助函数
- `upload_test.go` -- 单元测试（25 个用例）
- `webdav_copier_test.go` -- WebDAV Copier 测试（10 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-23 | 重构 | 抽出共享清理函数 `CleanupSession(ctx, sessions, deleter, cfg, sessionDir, sessionInfo, policy, guardStatus)` 及 `cleanupTempShared`/`cleanupGeneratedShared`/`cleanupAllShared`，`internal/archive` 归档后复用同一套清理逻辑，以 `guardStatus` 区分守卫态（upload=`uploaded`、archive=`published`）。upload.go 内原有的 cleanup 逻辑改为调用这些共享函数 |
| 2026-06-17 | 修复 | `cleanupAll` 恢复为删除整个本地场次目录（撤销此前退化为仅删 `raw/` 的改动），删除成功后置 `session.local_available=false`；`Fetch` 取回成功后置回 `true`，形成「上传→清理→取回」闭环，驱动 glossary/recap/publisher 守卫 |
| 2026-06-04 | 测试补充 | 新增 webdav_copier_test.go（10 用例）：joinWebDAVPath 3 个、pathDir 2 个、relativeTarget 3 个、isWebDAVNotExist 2 个。总用例从 25 增至 35 |
| 2026-06-03 | 重大更新 | 新增 webdav_copier.go（WebDAVCopier 原生 WebDAV 实现）：使用 gowebdav 库直接操作，支持 Copy/Fetch/Delete 递归操作，无需 rclone 依赖。upload.go 新增 NewConfiguredCopier/NewConfiguredDeleter 工厂方法，根据 WebDAVConfig.NativeConfigured() 自动选择后端 |
| 2026-05-01 | 更新 | 新增上传后清理策略（temp/generated/all） |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
