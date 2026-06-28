[Hikami-Go](../CLAUDE.md) > **web**

# web -- Vue 3 前端管理界面

## 技术栈

| 组件 | 版本/选型 |
|------|-----------|
| 框架 | Vue 3.5 |
| 状态管理 | Pinia |
| 路由 | Vue Router 4 |
| UI 组件库 | Element Plus 2.9 |
| HTTP 客户端 | Axios |
| Markdown 渲染 | marked |
| 类型系统 | TypeScript |
| 构建工具 | Vite |
| 测试框架 | Vitest |

## 目录结构

> 经多阶段重构（阶段 3~6），自管理组件已收敛为 composables，大视图拆分为 `features/` 分域子组件。

```
web/src/
├── api/            # HTTP API 封装（Axios 实例 + 各模块请求）
│   ├── client.ts   # Axios 实例配置（get/post/put/del），注入 X-Admin-Token
│   ├── channels.ts # 主播 CRUD API
│   ├── sessions.ts # 场次 CRUD/ASR/回顾/上传/发布/发现/导入 API（含 editOpus/removeOpus）
│   ├── tasks.ts    # 任务查询/取消/删除 API
│   ├── live.ts     # 直播状态/录制 API
│   ├── health.ts   # 运行时健康检查 API
│   ├── stats.ts    # 统计仪表板 API
│   ├── bili.ts     # B 站 QR Login 与 Cookie Account API
│   ├── settings.ts # API Key 管理 + 全局发布配置 + 回顾模型列表 API
│   ├── glossary.ts # 术语表 CRUD/导入导出 API
│   ├── recap-templates.ts # 回顾模板 CRUD API + 预设列表
│   ├── types.ts    # 共享 TypeScript 类型定义
│   └── index.ts    # 统一导出
├── stores/         # Pinia 状态管理
│   ├── channels.ts    # 主播列表状态（含 fetchDetail）
│   ├── sessions.ts    # 场次列表状态（含 currentDetail + fetchDetail）
│   ├── tasks.ts       # 任务列表状态
│   ├── liveStatus.ts  # 直播状态轮询
│   └── runtime.ts     # 运行时能力状态（fetchRuntime 支持 force 强制刷新）
├── features/       # 分域特性（自管理逻辑收敛为 composables + 拆分子组件）
│   ├── recaps/
│   │   ├── sessionActions.ts          # 场次操作纯函数（getRowActions/getDrawerActions/canFetchLocal/decideRetry/...）
│   │   ├── sessionActions.test.ts     # sessionActions 单测（41 用例）
│   │   └── components/                # RecapsView 拆分子组件（阶段 3）
│   │       ├── RecapDrawer.vue        # 回顾预览/编辑抽屉
│   │       ├── RecapToolbar.vue       # 工具栏（导入/链接下载/刷新）
│   │       ├── SessionFilters.vue     # 筛选器
│   │       └── SessionTable.vue       # 场次表格
│   ├── settings/
│   │   └── components/                # SettingsView 拆分为卡片组件（阶段 4a/4b）
│   │       ├── PublishSettingsCard.vue    # 全局发布配置卡
│   │       ├── RecapSettingsCard.vue      # 回顾配置卡（含 DashScope / ASR S3 卡内密钥管理）
│   │       ├── WebDAVSettingsCard.vue     # WebDAV 配置卡
│   │       ├── AdminTokenCard.vue         # 管理员令牌卡（阶段 4b）
│   │       ├── BiliAccountsCard.vue       # B 站账号卡（阶段 4b）
│   │       ├── ConfigBackupCard.vue       # 配置备份（导入/导出）卡（阶段 4b）
│   │       ├── ArchiveSettingsCard.vue    # 发布后归档配置卡（auto_after_publish / cleanup_policy）
│   │       └── settings-cards.css         # 卡片共享样式
│   ├── channel/                       # 主播组件自管理逻辑（阶段 5 收敛）
│   │   ├── useBiliQRCodeLogin.ts      # QR 登录状态机（props 全 getter 化）
│   │   ├── useGlossaryEntries.ts      # 术语条目 CRUD 状态
│   │   └── useRecapTemplateEditor.ts  # 模板编辑状态（含预设加载）
│   └── onboarding/
│       └── useOnboardingWizard.ts     # 三步引导向导状态机
├── components/     # 可复用 UI 组件（视图无关）
│   ├── channel/   # 主播相关组件（由 features/channel composables 驱动）
│   │   ├── ChannelIdentifyDialog.vue
│   │   ├── GlossaryEditor.vue            # 术语表编辑器（瘦组件，逻辑在 useGlossaryEntries）
│   │   ├── RecapTemplateEditor.vue       # 回顾模板编辑器（瘦组件，逻辑在 useRecapTemplateEditor）
│   │   └── BiliQRCodeLoginDialog.vue     # B 站 QR 登录弹窗（瘦组件，逻辑在 useBiliQRCodeLogin）
│   ├── session/   # 场次相关组件
│   │   ├── DiscoverResultDrawer.vue
│   │   ├── ImportSessionDrawer.vue
│   │   └── DownloadByURLDrawer.vue       # 按视频链接（BV 号等）触发下载的抽屉
│   ├── task/      # 任务相关组件
│   │   └── TaskProgressBar.vue
│   ├── layout/    # 布局组件
│   │   └── AppLayout.vue                 # 应用布局框架（挂载刷新协调器）
│   └── onboarding/
│       └── OnboardingWizard.vue          # 三步引导向导（瘦组件，逻辑在 useOnboardingWizard）
├── composables/    # Vue 组合式函数（全局级）
│   ├── useAppRefreshCoordinator.ts # 刷新协调器（WS + 降级轮询单一 owner，方案 §7.2）
│   ├── useWebSocket.ts     # WebSocket 进度推送
│   ├── usePolling.ts       # 轮询组合函数
│   ├── useExpertMode.ts    # 专家模式切换（localStorage 持久化）
│   ├── useRecapModels.ts   # 回顾模型快捷选项加载与分组
│   └── useAdminToken.ts    # 管理员令牌（localStorage 持久化，axios 拦截器注入）
├── views/          # 页面视图（路由对应，已瘦身为组装 features 子组件）
│   ├── HomeView.vue           # 首页工作台，专家模式显示统计仪表板
│   ├── StreamersView.vue      # 主播管理（替代旧 /channels）
│   ├── RecapsView.vue         # 场次列表（组装 features/recaps/components）
│   └── SettingsView.vue       # 设置中心（组装 features/settings/components）
├── utils/          # 工具函数
│   ├── lifecycle.ts          # 6 步生命周期映射+动作元数据+能力禁用判断
│   ├── friendlyStatus.ts     # 友好状态标签/颜色/进度映射+状态分组过滤
│   ├── constants.ts          # 常量定义
│   ├── status.ts             # 状态工具
│   └── format.ts             # 格式化工具
└── router/         # Vue Router 路由配置
    └── index.ts     # 路由表（含重定向规则）
```

## 路由

| 路径 | 视图 | 说明 |
|------|------|------|
| `/` | HomeView | 首页工作台，专家模式加载 `/api/stats/dashboard` |
| `/streamers` | StreamersView | 主播管理 |
| `/recaps` | RecapsView | 场次与回顾管理 |
| `/settings` | SettingsView | 设置中心 |
| `/live` | 重定向 `/` | 旧直播监控入口 |
| `/dashboard` | 重定向 `/` | 旧工作台入口 |
| `/sessions` | 重定向 `/recaps` | 旧场次列表入口 |
| `/sessions/:sid` | 重定向 `/recaps?sid=:sid` | 旧场次详情入口 |
| `/tasks` | 重定向 `/recaps` | 旧任务中心入口 |
| `/import` | 重定向 `/recaps?import=1` | 打开导入抽屉 |
| `/channels` | 重定向 `/streamers` | 旧主播列表入口 |
| `/channels/:id` | 重定向 `/streamers?id=:id` | 旧主播详情入口 |
| `/health` | 重定向 `/settings?section=runtime` | 系统能力（已合并到设置中心） |

### views/HomeView.vue -- 首页与专家统计

首页聚合直播状态、运行任务、最近场次和发现入口。专家模式启用时调用 `getDashboardStats()`，展示：
- 本月场次数
- 有场次的主播数
- 月度场次趋势
- 主播场次排名
- ASR 费用趋势

## 核心模块详解

### components/onboarding/OnboardingWizard.vue -- 新手引导向导

三步引导新用户完成初始配置：
1. **工具检查**: 显示外部工具安装状态和安装提示
2. **API Key 设置**: 输入 DashScope API Key 和 AI API Key，保存到 secrets
3. **添加主播**: 通过识别 API 添加首个主播

引导状态通过 `GET /api/onboarding/status` 检查（工具/Key/主播是否就绪）。跳过后通过 `POST /api/onboarding/dismiss` 持久化到 secrets。

### composables/useExpertMode.ts -- 专家模式

- `useExpertMode()` 返回 `{ expertMode, isExpert, toggleExpertMode }`
- 使用 localStorage 键 `hikami-expert-mode` 持久化
- watch 自动同步到 localStorage

### composables/useRecapModels.ts -- 回顾模型快捷选项

- `useRecapModels()` 返回 `{ models, groups, load }`
- `load()` 调用 `getRecapModels()`（GET /api/config/recap/models）拉取后端推荐模型列表，幂等（loaded 标记防重复）
- `groups` computed 按 `group` 字段聚合成 `{ name, models }[]`，供 el-option-group 分组渲染
- SettingsView（全局）与 StreamersView（主播级）下拉共享同一来源，避免两处硬编码不一致
- 模型名仍支持自由输入（el-select filterable+allow-create），列表仅为常用快捷选项
- 拉取失败时降级为空列表（下拉退化为纯自由输入）

### stores/runtime.ts -- 运行时能力状态

- `useRuntimeStore()` 暴露 `{ status, loading, fetchRuntime }`
- `fetchRuntime(force=false)`：默认按 30 秒节流（`lastFetchAt`），避免频繁轮询；`force=true` 时把 `lastFetchAt` 重置为 0 强制刷新
- SettingsView 在导入配置 / 保存密钥 / 保存发布或回顾配置 / 手动刷新等操作后调用 `fetchRuntime(true)`，确保前端立即反映最新能力/配置状态（不走 30s 节流）

### utils/friendlyStatus.ts -- 友好状态显示

- `getFriendlySessionStatus(session)` 返回 `{ label, color, progress, action }`
- 7 个状态组映射：处理中/音频就绪/转写中/转写完成/回顾已生成/已上传/已发布
- `statusGroupMap` 提供过滤用状态分组：processing/recap/published/failed

### views/SettingsView.vue -- 设置中心（5 分区）

设置中心扩展为 5 个分区导航：

1. **系统能力**: 4 个能力卡片 + 配置状态摘要 + 外部工具路径表格（含 InstallHint）
2. **API 密钥**: 密钥卡片列表 + 编辑/清除操作
3. **全局发布**: 4 个设置卡片
4. **全局术语**: 内嵌 GlossaryEditor（global 作用域）
5. **回顾模板**: 内嵌 RecapTemplateEditor（global 作用域）

**密钥环境变量动态化：** SettingsView 不再硬编码 `AI_API_KEY` / `DASHSCOPE_API_KEY`，改为从 `config_status.dashscope_key_env` / `recap_key_env` 派生：
- `dashScopeSecretKey` computed = `configStatus.dashscope_key_env || 'DASHSCOPE_API_KEY'`（兜底）
- `recapSecretKey` computed = `configStatus.recap_key_env || 'AI_API_KEY'`（兜底）
- 编辑密钥、能力卡 ASR 跳转、setupItems 等均使用上述 computed 的 key

**ASR 能力项 action 动态化：** `capItems` 中 `asr_submit` 项根据密钥状态选择动作类型（`CapActionType` 加 `'hint'`）：
- 密钥未配置 → `actionType: 'secret'`，按钮文案"配置密钥"，跳转到对应密钥编辑
- 密钥已配置但能力仍红 → `actionType: 'hint'`，按钮文案"配置 ASR 后端"，弹指引对话框说明需配置 `asr_temp` / `asr_s3` 后端和 `yt-dlp`

**写入后强制刷新：** `saveSecret` / `clearKey` / `savePublishConfig` / 保存回顾配置 / 导入配置 / 手动刷新 等所有写操作均调用 `runtimeStore.fetchRuntime(true)` 绕过 30s 节流，立即反映最新能力。

### views/RecapsView.vue -- 回顾与场次

回顾页管理场次列表、生命周期动作、手动导入、链接下载和回顾预览：
- 支持局部回顾，调用 `POST /api/sessions/:sid/recap-partial`
- 「链接下载」抽屉（DownloadByURLDrawer）按 BV 号等链接 + 主播触发 download→normalize→asr→recap
- 「取回」按钮在 `local_available=false`（上传清理后）时出现，调用 `POST /api/sessions/:sid/fetch`；`local_available=false` 时状态标签显示「本地已清理」、发布/生成回顾按钮置灰提示先取回
- 回顾内容读取 `suggested_terms` 并展示术语建议
- 术语建议可调用 `upsertChannelEntry` 一键加入主播术语表

### components/channel/RecapTemplateEditor.vue -- 回顾模板编辑器

可复用的回顾模板编辑组件，支持 `scope="global"` 和 `scope="channel"` 两种模式：

- **预设下拉选择**: 顶部下拉加载 `GET /api/recap/presets`（listRecapPresets），选择预设后覆盖 system_prompt/user_format/fan_name/extra_vars（含覆盖确认弹窗）；选"内置默认"清空字段，保留留空自动跟随内置语义的约定。channel 模式应用预设后自动 `useCustom=true`
- **内置默认预览**: 下拉旁的"内置默认"按钮展开 ResolvedTemplate 预览
- **变量参考表**: 折叠面板显示 10 个标准模板变量（channel_name/channel_id/date/title/duration/fan_name/danmaku_count/unique_users/avg_per_min/slug）
- **System Prompt**: 文本域编辑，留空使用内置默认
- **输出格式要求**: 文本域编辑，留空使用内置默认
- **粉丝称呼**: 单行输入（用于 {{fan_name}} 变量）
- **自定义变量**: JSON 格式编辑（extra_vars）
- **channel 模式特有**:
  - 使用自定义模板开关（el-switch）
  - 关闭时回退到全局模板预览（显示 ResolvedTemplate）
  - 删除主播模板按钮（调用 DELETE API）
- **操作**: 保存、重置为内置默认、删除主播模板（仅 channel 模式）、导出模板、导入模板

### api/recap-templates.ts -- 回顾模板 API 封装

| 函数 | 说明 |
|------|------|
| `listGlobalRecapTemplates()` | `GET /api/recap/templates` |
| `upsertGlobalRecapTemplate(data)` | `PUT /api/recap/templates` |
| `getChannelRecapTemplate(channelId)` | `GET /api/channels/:id/recap-template` |
| `upsertChannelRecapTemplate(channelId, data)` | `PUT /api/channels/:id/recap-template` |
| `deleteChannelRecapTemplate(channelId)` | `DELETE /api/channels/:id/recap-template` |
| `exportGlobalRecapTemplates()` | `GET /api/recap/templates/export` |
| `importGlobalRecapTemplates(data)` | `POST /api/recap/templates/import` |
| `exportChannelRecapTemplates(channelId)` | `GET /api/channels/:id/recap-template/export` |
| `importChannelRecapTemplates(channelId, data)` | `POST /api/channels/:id/recap-template/import` |
| `listRecapPresets()` | `GET /api/recap/presets`（返回 `{ presets: TemplatePreset[] }`） |

### components/channel/BiliQRCodeLoginDialog.vue -- B 站 QR 码登录

支持通过 B 站 QR 码扫码获取 Cookie，选择用途（download/publish），保存到主播配置；在设置页账号模式下可保存为全局 Cookie Account。

### components/channel/GlossaryEditor.vue -- 术语表编辑器

支持全局和主播作用域：
- Markdown 导入
- JSON 导入
- JSON 导出
- 批量删除、批量启停和单条启停
- **新增热词对话框**: 顶部工具栏"新增热词"按钮打开 el-dialog 表单（字段：词条 term、释义 canonical、分类 category），保存调用既有 `upsertGlobalEntry` / `upsertChannelEntry` API

### api/stats.ts -- 统计 API

| 函数 | 说明 |
|------|------|
| `getDashboardStats()` | `GET /api/stats/dashboard` |

> **数据契约**：`/api/stats/dashboard` 自 `a651fec` 起后端复用 `session.GetDashboardStats`（单次查询聚合），返回 `DashboardData`——前端 HomeView 专家表格绑定的 `sessions_by_month[].session_count` / `asr_hours` 等字段是**唯一契约**（旧 handler 的内联字段已删除）。改动这些字段需同步 `types.ts` 的 `DashboardData` 与 HomeView 表格 prop，否则专家统计表格会静默空列。

### api/sessions.ts -- 场次 API

| 函数 | 说明 |
|------|------|
| `generateRecapWithRange(sid, startTime, endTime)` | 指定时间段回顾（调用 recap-partial） |
| `getRecapContent(sid)` | 获取回顾内容（含 suggested_terms） |
| `updateRecapContent(sid, content)` | 更新回顾 Markdown 内容 |
| `downloadSessionByURL(channelId, url)` | 按视频链接（BV 号等）+ 主播 ID 触发下载（调用 download-by-url） |
| `fetchSession(sid)` | 从 WebDAV 取回本地目录（取回后 local_available 置 true） |

### api/bili.ts -- B 站账号 API

封装 QR Login 与 Cookie Account：
- `createQRCodeSession` / `pollQRCodeSession` / `saveQRCodeSession` / `cancelQRCodeSession`
- `saveQRCodeToAccount`
- `listBiliAccounts` / `updateBiliAccount` / `deleteBiliAccount`

## TypeScript 类型定义

### 回顾模板类型

```typescript
interface RecapTemplate {
  id: number; channel_id: string; name: string;
  system_prompt: string; user_format: string;
  fan_name: string; extra_vars: string;
  enabled: boolean; is_default: boolean;
  created_at: string; updated_at: string;
}

interface TemplatePreset {
  name: string;
  description: string;
  system_prompt: string;
  user_format: string;
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

### 来源模式类型

```typescript
interface Channel {
  // ...
  source_mode: string;    // both/live_only/replay_only/live_first/replay_first
  discover_limit: number; // 每次发现最大新建数（0=不限）
  download_account_id?: number | null;
  publish_account_id?: number | null;
  // ...
}
```

### 统计与账号类型

```typescript
interface DashboardData {
  sessions_by_month: DashboardMonth[];
  sessions_by_channel: DashboardChannel[];
  cost_trend: DashboardCost[];
  danmaku_top: DashboardDanmaku[];
  recap_count: number;
  publish_count: number;
}

interface BiliCookieAccount {
  id: number;
  uid: number;
  nickname: string;
  cookie_file: string;
  is_default_download: boolean;
  is_default_publish: boolean;
}
```

### 工具安装提示类型

```typescript
interface ToolStatus {
  // ...
  install_hint?: string; // 平台感知的安装提示（Linux/Windows）
}
```

### 运行时状态类型

```typescript
interface RuntimeStatus {
  checked_at: string;
  tools: Record<string, ToolStatus>;
  capabilities: Capabilities;
  config_status: ConfigStatus;
  cookie_warnings?: CookieWarning[];
  disk_usage?: DiskInfo[];
}
```

（其余类型定义同前：Channel, UpsertChannelInput, IdentifyInput, IdentifyResult, Session, Task, LiveStatus, PublishConfig, GlossaryEntry, RecapContent, TaskProgressEvent 等）

## API 模块

### recap-templates.ts

见上方"api/recap-templates.ts"章节。

### 其余 API 模块

同前：sessions.ts、settings.ts、glossary.ts、channels.ts、tasks.ts、live.ts、health.ts。

## 开发命令

```bash
make web-dev    # 启动 Vite 开发服务器（热重载）
make web-build  # 生产构建（输出到 web/dist/）
```

## 测试状态

已配置 Vitest 测试框架。现有 4 个测试文件，共 90 个测试用例：

- `features/recaps/sessionActions.test.ts`（41 个 it 测试，阶段 3 新增）：覆盖 `getRowActions`/`getDrawerActions`（各状态下可见动作）、`canFetchLocal`（local_available 守卫）、`decideRetry`/`isRetryable`/`retryHint`（重试决策）、`primaryActionType`（主按钮类型）、`UI_ACTION_REASON`（禁用原因）。从 RecapsView 视图中抽出的纯函数，便于单元覆盖。

- `utils/format.test.ts`（17 个 it 测试）：
  - formatDateTime: 空字符串返回"-"、ISO 日期格式化、无效日期回退
  - formatDate: 空字符串返回"-"、ISO 日期格式化、无效日期回退
  - formatFileSize: 负数返回"-"、零字节、B/KB/MB/GB 格式化
  - formatDuration: 零/负数返回"-"、秒、分秒、时分秒格式化

- `utils/lifecycle.test.ts`（19 个 it 测试）：
  - getNextAction: 6 种状态映射（recording/media_ready/asr_done/recap_done/uploaded/published）、未知状态/discovered 返回 null、capability 禁用、capabilities 为 null
  - getDisabledReason: 无 capability 空字符串、capability true 空字符串、capability false 原因、capabilities null 回退原因
  - getActionMeta: submit_asr/publish 元数据
  - LIFECYCLE_STEPS: 6 步验证、步骤键顺序验证

- `utils/friendlyStatus.test.ts`（13 个 it 测试）：
  - getFriendlySessionStatus: 7 种状态（discovered/media_ready/asr_submitted/asr_done/recap_done/uploaded/published）颜色/进度/动作映射、failed 危险色、unknown 信息色
  - statusGroupMap: processing 包含源到 ASR 状态、recap 包含 recap_done、published 包含 uploaded+published、failed 包含 failed

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-24 | 文档补注 | `api/stats.ts` 章节补「数据契约」说明：`/api/stats/dashboard` 自后端 `a651fec` 起复用 `session.GetDashboardStats`，HomeView 专家表格绑定的 `sessions_by_month[].session_count`/`asr_hours` 等字段现为**唯一契约**（旧 handler 内联字段已删），改动需同步 `types.ts` 的 `DashboardData` 与 HomeView 表格 prop。仅文档，无代码改动，Vitest 90 不变 |
| 2026-06-23 | 功能 | (1) 新增 `features/settings/components/ArchiveSettingsCard.vue`：发布后归档配置卡（`auto_after_publish` 开关 + `cleanup_policy` 下拉 none/temp/generated/all，含策略提示文案），保存后 emit `saved` 并 `fetchRuntime(true)` 刷新能力；接入 `api/settings.ts` 的 `getArchiveConfig` / `updateArchiveConfig`（GET/PUT `/api/config/archive`）与 `types.ts` 的 `ArchiveConfig` 类型。(2) 回顾配置卡（RecapSettingsCard）新增 DashScope / ASR S3 卡内密钥管理（密钥 env 改名 + 直接录入 secret，调用 `/api/config/dashscope`、`/api/config/asr-s3` 端点；后端实现 key env 改名的 secrets 迁移）。(3) 主播抽屉接入回顾模板编辑器入口（per-channel 回顾模板）。(4) `Session` 类型新增 `archived_at` 字段。无新增测试文件，Vitest 用例数 90 不变 |
| 2026-06-21 | 重构/增量 | **多阶段重构（阶段 3~6）+ 文档同步**：(1) **阶段 3 RecapsView 拆分**：新增 `features/recaps/` 分域，抽 `sessionActions.ts` 纯函数（getRowActions/getDrawerActions/canFetchLocal/decideRetry/isRetryable/retryHint/primaryActionType）+ `sessionActions.test.ts`（+41 测试，前端测试 49→90）；RecapsView 拆为 `features/recaps/components/` 下 RecapDrawer/RecapToolbar/SessionFilters/SessionTable。(2) **阶段 4a/4b SettingsView 拆分**：拆为 `features/settings/components/` 下 6 个卡片组件（PublishSettingsCard/RecapSettingsCard/WebDAVSettingsCard/AdminTokenCard/BiliAccountsCard/ConfigBackupCard）+ settings-cards.css；配置导入策略选择 bug 修正 + 覆盖按钮经 catch 映射。(3) **阶段 5 自管理组件收敛**：`features/channel/` 新增 useBiliQRCodeLogin/useGlossaryEntries/useRecapTemplateEditor，`features/onboarding/` 新增 useOnboardingWizard；GlossaryEditor/RecapTemplateEditor/BiliQRCodeLoginDialog/OnboardingWizard 瘦身为视图；props 全 getter 化。(4) **阶段 6 刷新协调器**：新增 `composables/useAppRefreshCoordinator.ts`（WS 连接 + task_progress 订阅 + WS 断线降级轮询 + WS 重连停轮询并全量拉回，方案 §7.2，单一 owner）；在 AppLayout 挂载，生命周期等同 app；新增 `composables/useAdminToken.ts`（管理员令牌 localStorage 持久化 + axios 拦截器注入 X-Admin-Token）。(5) 配套：`api/sessions.ts` 新增 editOpus/removeOpus；`api/client.ts` 注入 admin token。**新增 docs/FRONTEND_ARCHITECTURE.md** 作为重构后架构快照 |
| 2026-06-14 | 交互增强 | settings/recap-presets 三项增强：(1) `stores/runtime.ts` 的 `fetchRuntime(force=false)` 支持强制跳过 30s 节流；SettingsView 在导入配置/保存密钥/保存发布或回顾配置/手动刷新均调用 `fetchRuntime(true)` 立即反映最新能力；(2) `views/SettingsView.vue` 新增 `dashScopeSecretKey`/`recapSecretKey` computed（从 `config_status.dashscope_key_env`/`recap_key_env` 派生，兜底默认 key），替代硬编码 AI_API_KEY/DASHSCOPE_API_KEY；ASR 能力卡 action 动态化（密钥未配→"配置密钥"；密钥已配但能力红→"配置 ASR 后端"弹指引，说明需配 asr_temp/asr_s3 + yt-dlp）；`CapActionType` 新增 `'hint'`；(3) `components/channel/GlossaryEditor.vue` 顶部工具栏新增"新增热词"按钮 + el-dialog 表单（词条/释义/分类），复用既有 upsert API；(4) `components/channel/RecapTemplateEditor.vue` 接入 `GET /api/recap/presets`（listRecapPresets）加预设下拉（覆盖确认）+ 内置默认预览，选"内置默认"清空字段保留留空自动跟随语义；channel 模式应用预设自动 useCustom=true；(5) `api/recap-templates.ts` 新增 `listRecapPresets()`，`api/types.ts` 新增 `TemplatePreset` 类型 |
| 2026-06-13 | 功能补全 | 新增 composables/useRecapModels.ts（回顾模型快捷选项加载+按厂商分组，全局/主播下拉复用）；api/settings.ts 新增 getRecapModels（GET /api/config/recap/models）；types.ts 新增 RecapModelOption；SettingsView 与 StreamersView 回顾模型下拉改用后端动态分组（消除两处硬编码不一致），主播级下拉新增 filterable+allow-create；StreamersView 主播抽屉接入 GlossaryEditor（主播级术语表/ASR 热词编辑入口，懒加载折叠面板）；GlossaryEditor 文案补全 Fun-ASR 热词说明 |
| 2026-06-05 | 测试补充 | 初始化 Vitest 测试框架，新增 3 个测试文件：format.test.ts（17 用例：formatDateTime/formatDate/formatFileSize/formatDuration）、lifecycle.test.ts（19 用例：getNextAction/getDisabledReason/getActionMeta/LIFECYCLE_STEPS）、friendlyStatus.test.ts（13 用例：getFriendlySessionStatus/statusGroupMap）。技术栈新增 Vitest。总测试用例 49 |
| 2026-05-15 | 增量更新 | 发现并记录遗漏文件：components/onboarding/OnboardingWizard.vue（三步引导向导：工具/Key/主播）；composables/useExpertMode.ts（专家模式切换 + localStorage 持久化）；utils/friendlyStatus.ts（友好状态标签/颜色/进度映射 + statusGroupMap 过滤）；api/sessions.ts 新增 generateRecapWithRange/getRecapContent/updateRecapContent；types.ts 新增 RuntimeStatus 的 cookie_warnings/disk_usage 字段、DashboardData 新增 recap_count/publish_count |
| 2026-05-15 | 重大更新 | 路由重构为 HomeView/StreamersView/RecapsView/SettingsView，旧 /live、/dashboard、/sessions、/tasks、/channels 路由重定向；HomeView 专家模式新增统计仪表板（getDashboardStats）；RecapsView 新增局部回顾入口和 suggested_terms 展示/一键添加术语；GlossaryEditor 新增 Markdown/JSON 导入与 JSON 导出按钮；RecapTemplateEditor 新增模板导入/导出按钮；SettingsView 新增 Bili Cookie Account 管理；api 新增 stats.ts，bili.ts 扩展 Cookie Account，types.ts 新增 DashboardData/BiliCookieAccount 和账号关联字段 |
| 2026-05-14 | 重大更新 | 新增 RecapTemplateEditor.vue 组件（可复用于 global/channel 作用域）；新增 api/recap-templates.ts（9 个 API 函数）；types.ts 新增 RecapTemplate/ResolvedRecapTemplate/ChannelRecapTemplateResponse 类型；设置页新增"回顾模板"；ToolStatus 新增 install_hint 字段；Channel 类型新增 source_mode/discover_limit 字段；新增 BiliQRCodeLoginDialog.vue |
| 2026-05-12 | 重大更新 | Web UI 全面重设计：工作台、场次/回顾、主播、设置等视图重写；新增 GlossaryEditor/lifecycle.ts/useChannelHealth.ts；路由变更 |
| 2026-05-08 | 重大更新 | ChannelFormDialog 新增 per-channel 发布设置 |
| 2026-05-07 | 重大更新 | 新增 glossary.ts、ChannelGlossaryDialog.vue |
| 2026-05-04 | 更新 | 新增 SettingsView |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
