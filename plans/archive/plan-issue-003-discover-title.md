# 计划：ISSUE-003 发现回放建场次时标题为空，显示为 BV 号

> **分支**：`fix/known-issues-2026-07-11`
> **创建日期**：2026-07-11
> **状态**：APPROVED (codex v1)

## 目标

发现回放路径创建的 download 场次 title 从 BV 号改为视频真实标题。

## Codex 调研结论

采用**选项 1**：定义 `TitleResolver` 接口，`download.Handler` 实现，通过 functional option 注入 `discover.Manager`。

## 修改方案

### 步骤 1：discover 包定义 TitleResolver 接口

```go
// TitleResolver 按 channelID + sourceID 解析视频真实标题。
// 空标题时 discover 调用它取真实标题；失败时返回 sourceID 作为兜底。
type TitleResolver interface {
    ResolveDownloadTitle(ctx context.Context, channelID, sourceID string) string
}
```

### 步骤 2：discover.Manager 加 titleResolver 字段 + functional option

```go
type Manager struct {
    channels     *channel.Store
    sessions     *session.Store
    workers      *worker.Pool
    lister       Lister
    titleResolver TitleResolver // 可选，nil 时不解析
}

type Option func(*Manager)

func WithTitleResolver(r TitleResolver) Option {
    return func(m *Manager) { m.titleResolver = r }
}
```

`NewManager` 签名保持兼容，新增 options 变参。

### 步骤 3：discover 内部 resolveTitle helper

```go
func (m *Manager) resolveTitle(ctx context.Context, channelID, sourceID, currentTitle string) string {
    if strings.TrimSpace(currentTitle) != "" {
        return currentTitle
    }
    if m.titleResolver == nil {
        return currentTitle // 无 resolver 时保持原行为（兜底 sourceID）
    }
    return m.titleResolver.ResolveDownloadTitle(ctx, channelID, sourceID)
}
```

### 步骤 4：DiscoverChannel 在 title_prefix 判断前解析

```go
for _, entry := range entries {
    title := m.resolveTitle(ctx, item.ID, entry.ID, entry.Title)
    // 用 title 做 title_prefix 匹配 + CreateDownload
}
```

### 步骤 5：PreviewChannel 同样解析

前端预览阶段也显示真实标题。

### 步骤 6：Execute 防御性兜底解析

前端传来的 item.Title 可能为空（如果 Preview 没解析成功），Execute 也调 resolveTitle。

### 步骤 7：download.Handler 导出 ResolveDownloadTitle 方法

把现有的 `resolveDownloadTitle`（小写）改为导出方法 `ResolveDownloadTitle`（大写），实现 `discover.TitleResolver` 接口。

### 步骤 8：cmd/hikami/main.go 注入

在创建 discover.Manager 时传入 `discover.WithTitleResolver(downloadHandler)`。

### 步骤 9：测试

- discover_test.go：加 fakeTitleResolver，覆盖空标题解析 + 非空标题不调 + 解析失败兜底
- 现有测试不受影响（NewManager 签名兼容）

## 风险控制

- 串行解析，不引入并发
- API 失败不阻断发现流程
- titleResolver 为 nil 时保持原行为（向后兼容）
- 首版不加缓存/TTL/限流
