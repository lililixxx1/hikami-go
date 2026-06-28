[根目录](../../CLAUDE.md) > **internal/discover**

# internal/discover -- B 站回放发现

## 模块职责

遍历所有启用的主播，使用 yt-dlp 发现回放合集中的新视频，按标题前缀过滤，去重后创建场次并排队下载任务。支持来源模式过滤（source_mode）、每次发现数量限制（discover_limit）、发现预览和详细跳过/接受日志。

## 入口与启动

- **入口文件**: `discover.go`
- **核心类型**: `Manager`, `YTDLPLister`

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewManager(channels, sessions, workers, lister)` | 创建 Manager |
| `DiscoverAll(ctx)` | 发现所有主播的回放（跳过 source_mode=live_only 的主播） |
| `DiscoverChannel(ctx, channel)` | 发现单个主播的回放（受 discover_limit 限制） |
| `PreviewChannel(ctx, channel)` | 只预览可发现回放，不创建场次和任务 |

**接口：**

```go
type Lister interface {
    List(ctx context.Context, sourceURL string, cookieFile string) ([]Entry, error)
}
```

**API 端点：**
- `POST /api/sessions/discover`
- `POST /api/channels/:id/discover/preview`

## 关键依赖与配置

- 外部工具: yt-dlp (`--dump-json --flat-playlist`)
- 依赖: channel.Store (主播列表), session.Store (场次去重), worker.Pool (排队任务)
- 过滤: `channel.TitlePrefix` 非空时按逗号分隔前缀匹配；为空时跳过过滤
- 去重: 通过 `session.CreateDownload` 的唯一约束 `(channel_id, source_type, source_id)` 实现
- 来源模式: `DiscoverAll` 跳过 `source_mode == "live_only"` 的主播
- 发现限制: `DiscoverChannel` 在 `createdCount >= discover_limit` 时停止创建新场次（0 = 不限制）
- Cookie: 传递 `channel.DownloadCookieFile` 给 yt-dlp
- 日志: 对 title_prefix 不匹配、discover_limit 达到、创建失败、已存在、新建成功和预览结果输出结构化日志

## 数据模型

**Entry（yt-dlp 条目）：**

| 字段 | 说明 |
|------|------|
| `id` | BV 号 |
| `title` | 视频标题 |
| `url` | 原始 URL |
| `webpage_url` | 网页 URL |

**Result（发现结果）：**

| 字段 | 说明 |
|------|------|
| `channel_id`, `session_id`, `source_id`, `title` | 标识信息 |
| `created` | 是否为新发现 |
| `task_id` | 排队的下载任务 ID |
| `error` | 错误信息 |

## 相关文件清单

- `discover.go` -- 唯一源文件

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-05-15 | 更新 | 空 title_prefix 时不再过滤标题；DiscoverChannel/PreviewChannel 增加结构化日志，记录 title_prefix_mismatch、discover_limit_reached、create_session_failed、already_exists、accepted 等原因；新增 PreviewChannel 文档 |
| 2026-05-14 | 更新 | DiscoverAll 新增 source_mode 检查（跳过 live_only 主播）；DiscoverChannel 新增 discover_limit 限制（每次最多创建 N 个新场次）；Lister.List 签名新增 cookieFile 参数 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
