[根目录](../../CLAUDE.md) > **internal/importer**

# internal/importer -- 手动导入

## 模块职责

接收 multipart 上传的音视频和可选弹幕文件，保存到场次目录，使用 ffmpeg 提取/转码为 `raw/audio.m4a`，完成后自动排队标准化任务。

## 入口与启动

- **入口文件**: `importer.go`
- **任务类型**: `import`

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewHandler(cfg, sessions, states, workers, converter)` | 创建 Handler |
| `Register(pool)` | 注册 import 任务处理器 |
| `CreateFromMultipart(ctx, input, mediaHeader, danmakuHeader)` | 处理 multipart 上传并排队任务 |

**接口：**

```go
type MediaConverter interface {
    Convert(ctx context.Context, inputPath string, outputPath string) error
}
```

**API 端点：** `POST /api/sessions/import` (multipart/form-data)

**必填字段：** `channel_id`, `title`, `media_file`
**可选字段：** `started_at` (RFC3339), `ended_at` (RFC3339), `danmaku_file`, `source_url`

## 关键依赖与配置

- ffmpeg: `-vn -c:a aac` 转码
- 输出: `raw/import.source.{ext}`, `raw/audio.m4a`, `raw/danmaku.jsonl` (可选), `raw/import.raw.json`

## 任务流程

1. 创建导入场次
2. 创建 `raw/` 目录
3. 保存上传的媒体文件为 `import.source.{ext}`
4. 保存弹幕文件（如有）
5. 写入 `import.raw.json` 元数据
6. 排队 import 任务
7. 任务执行：查找 `import.source.*` 文件
8. ffmpeg 转码为 `audio.m4a`（原子写入）
9. 提交 `import_succeeded` 事件
10. 排队 `normalize` 任务

## 测试与质量

- `importer_test.go`: 15 个测试用例，覆盖：
  - 文件查找: findImportSource（正常/不同扩展名/无扩展名/未找到/跳过目录/空目录/目录不存在/多文件匹配）
  - JSON 写入: writeJSON（正常写入+换行符验证）、writeJSON 元数据写入
  - 集成: CreateFromMultipart_Success（完整上传+文件验证）、CreateFromMultipart_WithDanmaku（带弹幕上传）
  - HandleTask: FFmpegConvert（转码成功+audio.m4a 验证）、ConvertFail（ffmpeg 错误传播）、SourceMissing（源文件缺失报错）

## 相关文件清单

- `importer.go` -- 唯一源文件
- `importer_test.go` -- 导入模块测试（15 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-24 | 重构 | **双重降级收敛**（`5fadea4`）：移除 `importer.go` HandleTask 中冗余的 `Apply(EventTaskFailed)` 调用（3 处）。任务失败降级统一由 `worker` 处理（普通任务 `EventTaskFailed` 全局特判降级；旁路任务经 `Register(..., WithBypassFailState())` 声明后仅写 `last_error`），各业务 handler 不再自行 `Apply`，避免双写。本模块无新增对外接口，测试数无变化（仍 15） |
| 2026-06-01 | 测试扩充 | `importer_test.go` 从 1 扩充至 15 个用例：findImportSource 8 个场景、writeJSON 2 个场景、CreateFromMultipart 2 个集成场景、HandleTask 3 个场景（转码成功/失败/源缺失） |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
