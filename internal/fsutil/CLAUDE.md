[根目录](../../CLAUDE.md) > **internal/fsutil**

# internal/fsutil -- 原子文件写入辅助

## 模块职责

提供文件原子写入辅助函数，采用"临时文件 + rename"策略，避免异常中断留下半成品产物。落实项目规范"标准产物采用临时文件 + 校验 + rename 的原子写入方式"（见根 CLAUDE.md 编码规范）。

## 入口与启动

- **入口文件**: `fsutil.go`
- **零外部依赖**（仅标准库 `encoding/json`、`os`）

## 对外接口

| 函数 | 说明 |
|------|------|
| `WriteFileAtomic(path, data, perm)` | 写入 `path+".tmp"` 后 `os.Rename` 原子落盘；rename 失败时清理临时文件 |
| `WriteJSONAtomic(path, value, perm)` | `json.MarshalIndent` 序列化（**检查错误，不吞**）后调用 `WriteFileAtomic` |

## 关键设计决策

- **临时文件 + rename**：写入 `path+".tmp"`，成功后 `os.Rename` 到目标路径。同一文件系统内 rename 原子，异常中断不会留下半成品目标文件（最多残留 `.tmp`，由下次写入覆盖）。
- **marshal 错误不吞**：`WriteJSONAtomic` 检查 `json.MarshalIndent` 错误并返回，避免写出空/损坏 JSON。ISS-3 的直接修复点：原 `internal/asr/asr.go` 用 `data, _ := json.MarshalIndent(...)` 吞掉序列化错误。
- **权限透传**：临时文件以调用方指定的 perm 创建。
- **不迁移既有实现**：`internal/normalize.writeJSONAtomic`（私有）、`internal/biliutil/cookie_writer` 等已有原子写入本次未迁移到本包，控回归面；后续可统一复用。

## 使用方

- `internal/asr` -- ASR 产物（transcript.txt/srt、segments.json、result.raw.json）原子写入（ISS-3）

## 测试与质量

- `fsutil_test.go`: 4 个测试用例：
  - `TestWriteFileAtomic_Success`：写入内容与权限正确
  - `TestWriteFileAtomic_NoTmpResidue`：无 `.tmp` 残留
  - `TestWriteJSONAtomic_Success`：JSON 正确 + 结尾换行
  - `TestWriteJSONAtomic_MarshalError`：不可序列化值（chan）返回错误，不创建目标文件

## 相关文件清单

- `fsutil.go` -- `WriteFileAtomic` / `WriteJSONAtomic`
- `fsutil_test.go` -- 单元测试（4 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-16 | 初始化 | 新增 fsutil 包（`WriteFileAtomic`/`WriteJSONAtomic`），供 asr 原子写入产物使用（ISS-3） |
