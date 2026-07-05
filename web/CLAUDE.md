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
│   ├── sessions.ts # 场次 CRUD/ASR/回顾/上传/发布/发现/导入 API（含 regenerateRecap 重新生成）
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
│   │   ├── sessionActions.ts          # 场次操作纯函数（getRowActions/getDrawerActions/canFetchLocal/decideRetry/...；UIActionName 6 个状态推进型动作，edit/remove 已移除；isReplaySource 回放类隐藏 publish）
│   │   ├── sessionActions.test.ts     # sessionActions 单测（47 用例）
│   │   └── components/                # RecapsView 拆分子组件（阶段 3）
│   │       ├── RecapDrawer.vue        # 回顾预览/编辑抽屉
│   │       ├── RecapToolbar.vue       # 工具栏（导入/链接下载/刷新）
│   │       ├── SessionFilters.vue     # 筛选器
│   │       └── SessionTable.vue       # 场次表格
│   ├── settings/
│   │   └── components/                # SettingsView 拆分为卡片组件（阶段 4a/4b）
│   │       ├── DashScopeSettingsCard.vue  # DashScope ASR 配置卡（API Key env + 转写参数）
│   │       ├── ASRS3SettingsCard.vue      # ASR S3 兼容存储配置卡（endpoint/bucket/public_url）
│   │       ├── RecapSettingsCard.vue      # 回顾 AI 配置卡（provider/model/prompt 概要）
│   │       ├── PublishSettingsCard.vue    # 全局发布配置卡
│   │       ├── WebDAVSettingsCard.vue     # WebDAV 配置卡
│   │       ├── AdminTokenCard.vue         # 管理员令牌卡（阶段 4b）
│   │       ├── BiliAccountsCard.vue       # B 站账号卡（阶段 4b）
│   │       ├── ConfigBackupCard.vue       # 配置备份（导入/导出）卡（阶段 4b）
│   │       ├── ArchiveSettingsCard.vue    # 发布后归档配置卡（auto_after_publish / cleanup_policy）
│   │       └── settings-cards.css         # 卡片共享样式（.column-row grid + 卡片外观）
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
├── composables/    # Vue 组合式函数（全局级，共 7 个）
│   ├── useAppRefreshCoordinator.ts # 刷新协调器（WS + 降级轮询单一 owner，方案 §7.2）
│   ├── useWebSocket.ts     # WebSocket 进度推送
│   ├── usePolling.ts       # 轮询组合函数
│   ├── useExpertMode.ts    # 专家模式切换（localStorage 持久化）
│   ├── useRecapModels.ts   # 回顾模型快捷选项加载与分组
│   ├── useDiscoverReplay.ts # 「发现回放」抽屉可见性 + 执行后刷新（RecapsView/HomeView 共用，2026-07-02）
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

### composables/useDiscoverReplay.ts -- 发现回放抽屉状态（2026-07-02）

- `useDiscoverReplay()` 返回 `{ drawerVisible, openDiscover, onExecuted }`
- 抽屉自管理 preview/execute 调用（调 `previewDiscoverSessions` / `executeDiscoverSessions`），本 composable 只保留最小状态：`drawerVisible`（抽屉开关）、`openDiscover()`（按钮 @click 绑定）、`onExecuted()`（抽屉执行完成后刷新 sessions+tasks 列表）
- RecapsView 和 HomeView 共用，替代原本各自重复的 handleDiscover（含 loading/result 状态），改版后那些职责收敛进抽屉

### stores/runtime.ts -- 运行时能力状态

- `useRuntimeStore()` 暴露 `{ status, loading, fetchRuntime }`
- `fetchRuntime(force=false)`：默认按 30 秒节流（`lastFetchAt`），避免频繁轮询；`force=true` 时把 `lastFetchAt` 重置为 0 强制刷新
- SettingsView 在导入配置 / 保存密钥 / 保存发布或回顾配置 / 手动刷新等操作后调用 `fetchRuntime(true)`，确保前端立即反映最新能力/配置状态（不走 30s 节流）

### utils/friendlyStatus.ts -- 友好状态显示

- `getFriendlySessionStatus(session)` 返回 `{ label, color, progress, action }`
- 7 个状态组映射：处理中/音频就绪/转写中/转写完成/回顾已生成/已上传/已发布
- `statusGroupMap` 提供过滤用状态分组：processing/recap/published/failed

### views/SettingsView.vue -- 设置中心（4 折叠分组）

设置页自 `af9df47`（2026-07-02）由原 13 张平铺卡片重构为 **4 个 `el-collapse` 折叠分组**（顶层 `activeGroups` 默认展开 `grp-overview`+`grp-pipeline`，收起 `grp-accounts`+`grp-advanced`；`grp-` 前缀避免与子卡内部 collapse-item name 混淆）：

1. **总览（grp-overview）**：合并原「配置进度」+「系统状态」+「专家配置状态」三处重叠为单个总览卡。`overviewItems` computed 渲染 4 项能力（ASR 转写/回顾 AI/WebDAV 上传/B站发布），每项含完成度 `done` + 能力红绿灯 `ok` + 根因 `reason` + 跳转动作；磁盘用量挂载底部；专家段仍门控（`el-descriptions` 显配置状态明细，`isExpert` 时展开）。
2. **流水线配置（grp-pipeline）**：6 张配置卡——DashScopeSettingsCard / ASRS3SettingsCard / RecapSettingsCard / WebDAVSettingsCard / PublishSettingsCard / ArchiveSettingsCard，各卡 `@saved` → `onConfigSaved()` → `fetchRuntime(true)` 同步总览。
3. **账号与备份（grp-accounts）**：BiliAccountsCard / AdminTokenCard / ConfigBackupCard（导入后 `onConfigImported()` 并发重拉 runtime + 各配置卡 reload）。BiliAccountsCard 对 `cookie_file` 为空的账号（备份导入的裸元数据）`isLoggedIn()` 返回 false → 显示灰色「未登录」标签 + 卡片 `account-card--logged-out`（`opacity: 0.6`）置灰，避免误读为已扫码登录。
4. **高级（grp-advanced）**：全局术语表（GlossaryEditor `scope="global"`）+ 回顾模板（RecapTemplateEditor `scope="global"`）+ 专家段外部工具表格（tools install_hint 复制）。

**跨分组跳转：** `scrollToSection(section)` 查 `groupOf` 映射，若目标分组收起则先展开并等 `setTimeout(320ms)`（`el-collapse` 高度过渡 ~300ms，nextTick 只等 DOM patch 不等 transitionend，不等会定位偏）再 `scrollIntoView`。`?section=runtime`（`/health` 重定向）归并入 grp-overview 展开逻辑。

**ASR 能力项动态动作（CapActionType `'section' | 'hint'`）：** `overviewItems` 中 ASR 项根据密钥状态分流——密钥未配 → `actionType: 'section'`、文案"配置"、跳 dashscope 卡；密钥已配但能力仍红 → `actionType: 'hint'`、文案"配置 ASR 后端"、`showASRBackendHint()` 弹指引（说明需配 `asr_temp`/`asr_s3` 后端 + yt-dlp）。

**密钥管理：** 原"API 密钥"空壳卡已删除（`af9df47`），密钥改由 DashScopeSettingsCard / ASRS3SettingsCard / RecapSettingsCard 各自内联管理；密钥 env 名后端动态化（`config_status.dashscope_key_env` / `recap_key_env`）。

**写入后强制刷新：** 所有写操作（`onConfigSaved` / `onConfigImported` / 手动刷新）均调用 `runtimeStore.fetchRuntime(true)` 绕过 30s 节流，立即反映最新能力。

### views/RecapsView.vue -- 回顾与场次

回顾页管理场次列表、生命周期动作、手动导入、链接下载和回顾预览：
- **录播/回放子 tab（2026-07-02）**：场次列表按 `session.source_type` 拆「录播」(live_record) 与「回放」(download+import) 两个子 tab，`filteredSessions` 按 source_type 过滤；`?sid` 按 source_type 自动切 tab，`?import=1` 落回放 tab
- **回放类动作隐藏（2026-07-02）**：RecapToolbar 录播 tab 隐藏「发现/导入/链接下载」入口（仅产生回放类）；`sessionActions` 对回放类隐藏 `uploaded→publish` 主动作（归档 upload 对两类保留）
- 支持局部回顾，调用 `POST /api/sessions/:sid/recap-partial`
- 「链接下载」抽屉（DownloadByURLDrawer）按 BV 号等链接 + 主播触发 download→normalize→asr→recap
- 「发现回放」抽屉（DiscoverResultDrawer）两步式：第一步 `previewDiscoverSessions` 预览（按主播分组、标注已处理项、默认勾选新回放）→ 第二步 `executeDiscoverSessions` 按勾选项下载；抽屉自管理 preview/execute，view 通过 `useDiscoverReplay` 控制可见性 + 执行后刷新
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

已配置 Vitest 测试框架。现有 4 个测试文件，`vitest run` 运行时共 **97 个用例**（静态 `it` 声明 93 个；`sessionActions.test.ts` 内 `describe.each(['download','import'])` 将回放类用例 ×2 展开）：

- `features/recaps/sessionActions.test.ts`（`vitest run` 48 个用例 / 静态 44 个 it）：覆盖 `getRowActions`/`getDrawerActions`（各状态下可见动作；published 无状态推进型动作，专栏删除能力已移除；回放类隐藏 publish）、`canFetchLocal`（local_available 守卫）、`decideRetry`/`isRetryable`/`retryHint`（重试决策）、`primaryActionType`（主按钮类型）、`UI_ACTION_REASON`（禁用原因，6 个 UIActionName）、`isReplaySource`（download/import 判定）。从 RecapsView 视图中抽出的纯函数，便于单元覆盖。其中回放类行/抽屉动作用例包在 `describe.each(['download','import'])` 中，按两种 source_type 各跑一遍。

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
| 2026-07-05 | 功能 | **B站账号卡片区分登录态**（`a449d7e`）：`BiliAccountsCard.vue` 新增 `isLoggedIn(account)` 判断（`cookie_file !== ''`）。`cookie_file` 为空的账号（如从配置备份导入的元数据，无 cookie 文件）显示灰色 `type="info"`「未登录」标签 + 卡片加 `account-card--logged-out` class（`opacity: 0.6`）整体置灰，与已扫码登录的账号视觉区分，避免把导入的裸元数据误读为已登录账号。无新增测试（纯展示性 UI 改动） |
| 2026-07-03 | 重构 | **移除专栏删除/编辑 + 新增重新生成回顾**：① 砍掉 `removeOpus`/`editOpus`（删B站专栏）——B站内容只能手动去 B站管理，本系统不删不改。`sessionActions.ts` 的 `UIActionName` 8→6（删 `edit_opus`/`remove_opus`）、删 `publishOpusAction`、`RowActions` 删 edit/remove 字段、`getRowActions` 删 published 分支（published 行现在无状态推进型动作，local 不可用时仅显示取回）。`SessionTable.vue` 删 published 编辑/删除按钮块 + emit。`api/sessions.ts` 删 `editOpus`/`removeOpus`。② 新增「重新生成回顾」：纯本地覆盖 md，不碰 B站。`RecapDrawer.vue` `.drawer-actions` 加硬编码「重新生成」按钮（`v-if recap_done/published`，非状态推进型不进 `getDrawerActions`），emit `regenerate` → `RecapsView.handleRegenerate` → `regenerateRecap(sid)`（`POST /api/sessions/:sid/recap/regenerate`）。配合后端 worker 任务实例级 `BypassFailState`（失败不降级 published/recap_done 主状态）。`sessionActions.test.ts` 删 edit/remove 用例（运行时 100→97）、published 用例改为断言无 primary。 |
| 2026-07-03 | 重构 | **设置页折叠分组**（`af9df47` + `be509b6`）：`views/SettingsView.vue` 由 13 张平铺卡片重组为 4 个 `el-collapse` 折叠分组——① 总览（grp-overview，合并原「配置进度」+「系统状态」+「专家配置状态」三处重叠为单个总览卡，`overviewItems` computed 渲染 4 项能力）；② 流水线配置（grp-pipeline，6 张配置卡）；③ 账号与备份（grp-accounts）；④ 高级（grp-advanced，术语表/模板/专家工具表）。删除与子卡重复的"API 密钥"空壳卡（密钥改由 DashScope/ASRS3/Recap 各卡内联管理）。`scrollToSection` 适配跨分组跳转（先展开并等 ~320ms 过渡再滚动）。`BiliAccountsCard` 背景/圆角统一（`#fafafa`/8px），页面宽度 800→960。`be509b6` 修 `.column-row > .column-note { grid-column: 2 }`（专栏投稿 column-note 被 2 列 grid auto-placement 挤进 label 列导致竖排）。无新增/删除测试，Vitest 运行时用例 100 不变（`sessionActions.test.ts` 运行时 51，因 `describe.each` 展开；本轮仅核对计数口径，测试代码无改动）。目录树补登遗漏的 `DashScopeSettingsCard.vue`/`ASRS3SettingsCard.vue`（实为 9 `.vue`，此前文档仅列 7） |
| 2026-07-02 | 功能 | **(1) 两步式发现回放**（`83ef024`）：DiscoverResultDrawer 改两步式——第一步 `previewDiscoverSessions`（`POST /api/sessions/discover/preview`）预览、按主播分组、标注已处理项（`exists`）、默认勾选新回放，第二步 `executeDiscoverSessions`（`POST /api/sessions/discover/execute`）按勾选项下载；抽屉自管理 preview/execute。新增 `composables/useDiscoverReplay.ts`（抽屉可见性 + 执行后刷新 sessions/tasks，RecapsView/HomeView 共用，composables 6→7）。`api/sessions.ts` 新增 `previewDiscoverSessions`/`executeDiscoverSessions`，旧 `discoverSessions`（一步式）保留为抽屉「全部下载」回退。`types.ts` 新增 `DiscoverPickItem`，`DiscoverResult` 字段改可选 + 增 `exists`/`source_url`。RecapToolbar 发现按钮补 yt-dlp 能力守卫。**(2) 录播/回放子 tab + 回放类动作隐藏**（`e9cb624`）：RecapsView 新增「录播/回放」子 tab，`filteredSessions` 按 `session.source_type` 过滤；`?sid` 按 source_type 自动切 tab，`?import=1` 落回放 tab。RecapToolbar 录播 tab 隐藏发现/导入/链接下载入口（仅产生回放类）。`sessionActions.ts` 新增 `isReplaySource`，回放类隐藏 `uploaded→publish` 主动作与 `published` 专栏 edit/remove（归档 upload 对两类保留）。`sessionActions.test.ts` 静态 41→47（+回放类隐藏用例，其中 6 个在 `describe.each(['download','import'])` 内运行时展开为 12）。Vitest 运行时 94→100。配合后端 `main.go` recap→publish 回调按 source_type 拦截回放类自动发布 |
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
