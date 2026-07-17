# 前端类型与 API 模块

> 本文件由根 CLAUDE.md 拆分而来，作为 AI 上下文补充文档。

## TypeScript 类型定义

### 回顾配置类型

```typescript
interface RecapConfig {
  base_url: string;
  model: string;
  max_tokens: number;
  max_continuations: number;
  timeout_seconds: number;
}

// 推荐回顾模型快捷选项（GET /api/config/recap/models，按厂商分组）
interface RecapModelOption {
  value: string;
  label: string;
  group: string;
}
```

### WebDAV 配置类型

```typescript
interface WebDAVConfig {
  url: string;
  username: string;
  base_path: string;
  remote: string;
  password_env: string;
  password_set: boolean;
}
```

### 配置导入结果类型

```typescript
interface ConfigImportResult {
  imported: boolean;
  strategy: string;           // "merge" | "overwrite"
  warnings?: string[];
  details: {
    secrets_count: number;
    channels_count: number;
    glossary_count: number;
    templates_count: number;
    bili_accounts_count: number;
  };
}
```

### 回顾模板类型

```typescript
interface RecapTemplate {
  id: number; channel_id: string; name: string;
  system_prompt: string; user_format: string;
  fan_name: string; extra_vars: string;
  enabled: boolean; is_default: boolean;
  created_at: string; updated_at: string;
}

interface ResolvedRecapTemplate {
  system_prompt: string; user_format: string;
  fan_name: string; extra_vars: Record<string, string>;
}

interface ChannelRecapTemplateResponse {
  global: RecapTemplate | null;
  channel: RecapTemplate | null;
  resolved: ResolvedRecapTemplate;
}
```

### 来源模式与主播配置类型

```typescript
interface Channel {
  // ...
  source_mode: string;       // both/live_only/replay_only/live_first/replay_first
  discover_limit: number;    // 每次发现最大新建数（0=不限）
  recap_model: string;       // per-channel 回顾模型覆盖
  max_continuations: number; // per-channel 续写次数
  // ...
}
```

### 工具安装提示类型

```typescript
interface ToolStatus {
  // ...
  install_hint?: string; // 平台感知的安装提示（Linux/Windows）
}
```

## API 模块说明

### recap-templates.ts

| 函数 | 说明 |
|------|------|
| `listGlobalRecapTemplates()` | 列出全局回顾模板 |
| `upsertGlobalRecapTemplate(data)` | 新增/更新全局回顾模板 |
| `getChannelRecapTemplate(channelId)` | 获取主播回顾模板（含全局/主播/合并结果） |
| `upsertChannelRecapTemplate(channelId, data)` | 新增/更新主播回顾模板 |
| `deleteChannelRecapTemplate(channelId)` | 删除主播回顾模板 |

### settings.ts

| 函数 | 说明 |
|------|------|
| `getPublishConfig()` | 获取全局发布配置 |
| `updatePublishConfig(config)` | 更新全局发布配置 |
| `getRecapConfig()` | 获取回顾 AI 配置 |
| `updateRecapConfig(config)` | 更新回顾 AI 配置 |
| `getRecapModels()` | 获取推荐回顾模型列表（2026-07-15 起精简到 DeepSeek 2 个 flash/pro；前端用 `HCombobox` 组件承载，支持手动输入任意模型名 + 下拉快捷选项，clearable 提供清空回路） |
| `getWebDAVConfig()` | 获取 WebDAV 配置 |
| `updateWebDAVConfig(config)` | 更新 WebDAV 配置 |
| `exportConfig()` | 全量配置导出（返回 Blob 附件） |
| `importConfig(json, strategy)` | 全量配置导入（merge/overwrite 策略） |

### stats.ts

| 函数 | 说明 |
|------|------|
| `getDashboardStats()` | 获取专家模式统计仪表板数据 |

### UI 组件:HCombobox（`components/ui/HCombobox.vue`，2026-07-15 新增）

回顾模型选择原先用 `HSelect`（原生 `<select>`）只能从预设选。`HCombobox` 是 input + 原生 datalist 组合框：可输入任意文本 + 下拉快捷选项，`clearable` 有值时显示清空按钮（emit 空串，用于主播级"留空跟随全局"），渐进增强（旧浏览器/读屏不支持 datalist 时降级为普通 input）。被 `RecapCardV10.vue`（设置页回顾 AI 卡）和 `StreamerDrawer.vue`（主播抽屉回顾模型）使用。

### sessions.ts

| 函数 | 说明 |
|------|------|
| `generateRecapWithRange(sid, startTime, endTime)` | 指定时间段回顾（调用 recap-partial） |
| `getRecapContent(sid)` | 获取回顾内容（含 suggested_terms） |
| `updateRecapContent(sid, content)` | 更新回顾 Markdown 内容 |
