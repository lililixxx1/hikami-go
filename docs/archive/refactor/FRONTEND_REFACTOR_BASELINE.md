# 前端重构基线测绘文档

> **用途**:重构前端前的「现状固化」。逐页面、逐区块列出前端元素 → 调用 API → 后端 handler 的对应关系,以及现有架构分层。重构时以此为准绳,确保功能零丢失、API 契约有意变更而非意外破坏。
>
> **粒度**:区块级(每个功能区为一个条目)+ handler 级(映射到 Gin handler 方法名)。不细化到单个按钮。
>
> **生成日期**:2026-06-20
> **前端代码基准**:`web/src/` | **后端代码基准**:`internal/handler/server.go`(3372 行,路由表 249-431)

---

## 0. 怎么用这份文档

1. **重构前**:通读「架构总览(§1)」+「逐页面目录(§2)」,确认你对现有结构理解无误。
2. **重构中**:每改一个区块,回到 §2 对照「该区块调了哪些 API / 倚仗哪些 store 和 composable」,确认新实现覆盖等价能力。
3. **重构后**:用「API 全集反向索引(§3)」逐条核对——每个 endpoint 至少有一个新前端入口承载,或被有意废弃(需在此文档登记)。
4. **契约红线**:除非在此文档显式登记,API 路径、方法、payload 不得静默改变。后端是稳态,前端重构应假定后端不变。

---

## 1. 前端架构总览

### 1.1 技术栈

| 层 | 技术 | 版本 |
|----|------|------|
| 框架 | Vue 3(Composition API,`<script setup>`) | ^3.5 |
| 构建 | Vite | ^6.0 |
| 路由 | vue-router 4(history 模式) | ^4.5 |
| 状态 | Pinia(setup store 风格) | ^2.3 |
| UI | Element Plus(全量注册 + 全量图标) | ^2.9 |
| HTTP | axios(单一 `client` 实例) | ^1.7 |
| Markdown | marked + dompurify | ^18 / ^3.4 |
| QR | qrcode | ^1.5 |
| 事件总线 | mitt | ^3.0 |

> ⚠️ `main.ts:16-18` **全量注册所有 Element Plus 图标为全局组件**——重构若改按需引入,需同步清理模板里裸用的 `<el-icon><Xxx /></el-icon>`。

### 1.2 目录结构与职责分层

```
web/src/
├── main.ts                  # 入口:挂载 Pinia / router / ElementPlus / 全局图标
├── App.vue                  # 根(仅 <AppLayout/>)
├── router/index.ts          # 路由表 + 旧路径 301 重定向
├── api/                     # 【契约层】纯函数,封装每个 endpoint,返回 typed Promise
│   ├── client.ts            #   axios 实例 + 拦截器(token 注入 / 401 弹窗 / 错误 toast)
│   ├── types.ts             #   全部 TS 类型(前后端 DTO 对齐)
│   ├── sessions.ts channels.ts live.ts tasks.ts
│   ├── settings.ts glossary.ts bili.ts recap-templates.ts
│   ├── stats.ts health.ts
│   └── index.ts             #   re-export 聚合
├── stores/                  # 【状态层】Pinia,sessions/channels/tasks/runtime/liveStatus
├── composables/             # 【逻辑复用】useExpertMode/useAdminToken/usePolling/useWebSocket/useRecapModels
├── utils/                   # 【纯工具】friendlyStatus/lifecycle/format/constants/status
├── components/              # 【通用组件】按域分组:channel/ session/ task/ layout/ onboarding/
└── views/                   # 【页面】HomeView/RecapsView/StreamersView/SettingsView
```

**分层约定**(重构应保留或明确替代):
- `views` 只做页面编排与区块布局,业务逻辑下沉到 `stores` / `composables` / `api`。
- `api/*` 是**唯一**的 HTTP 出口,返回 typed Promise;`stores` 调 `api`,`views` 调 `stores`(也允许 view 直接调无状态需要的 api,如一次性动作)。
- `components` 不直接持有业务状态,数据由 props 注入或自取 store。

### 1.3 路由表

| 路径 | name | 视图 | 说明 |
|------|------|------|------|
| `/` | home | HomeView | 首页:直播/告警/最近回顾/专家区 |
| `/streamers` | streamers | StreamersView | 主播管理 + 详情抽屉 |
| `/recaps` | recaps | RecapsView | 回顾列表 + 内容抽屉 |
| `/settings` | settings | SettingsView | 设置(长表单分段) |

**旧路径客户端重定向**(`router/index.ts:30-39`,Vue Router 客户端 `redirect`,**非 HTTP 301**——后端 `NoRoute` 仅做 SPA fallback 到 `index.html`,不参与重定向;真正的路径替换发生在前端。重构必须保留兼容):
`/live`→`/`、`/dashboard`→`/`、`/sessions`→`/recaps`、`/sessions/:sid`→`/recaps?sid=`、`/tasks`→`/recaps`、`/import`→`/recaps?import=1`、`/channels`→`/streamers`、`/channels/:id`→`/streamers?id=`、`/health`→`/settings?section=runtime`。

### 1.4 横切机制(全局生效,重构需逐一移植)

| 机制 | 位置 | 作用 |
|------|------|------|
| **管理员令牌** | `composables/useAdminToken.ts` + `api/client.ts:13-18` | token 存 localStorage,axios 请求拦截器注入 `X-Admin-Token` 头 |
| **401 自动补登** | `api/client.ts:20-65` | 401 时弹窗输 token → 重放原请求;并发共享同一弹窗;`WeakSet` 防重放死循环 |
| **错误 toast** | `api/client.ts:67-74` | 统一 `ElMessage.error`,取 `data.error`/`data.reason` |
| **WebSocket** | `composables/useWebSocket.ts` | `/ws` 连接,自动重连(指数退避 1s→30s)+ 30s 心跳检测,mitt 分发 `task_progress` 事件 |
| **任务进度推送** | `AppLayout.vue:42-55` | WS `task_progress` → `tasksStore.handleTaskProgress` 实时更新任务 |
| **专家模式** | `composables/useExpertMode.ts` | localStorage 持久化开关,控制大量 `v-if="isExpert"` 区块的显隐 |
| **轮询** | `composables/usePolling.ts` | 通用 setInterval 封装,首页用(直播+任务,30s);组件卸载自动停 |
| **运行时态节流** | `stores/runtime.ts:15` | `fetchRuntime` 30s 内去重,避免频繁探测 |
| **运行时态自动刷新** | 后端 `server.go:218-232` | 配置变更后后端异步刷新 runtime status(`refreshRuntimeStatus`) |

---

## 2. 逐页面区块目录(主体)

每个区块列:前端位置、交互、调用 API、后端 handler、底层功能。
后端 handler 方法名均在 `internal/handler/server.go`,底层包在 `internal/<pkg>`。

### 2.1 AppLayout(全局骨架,`components/layout/AppLayout.vue`)

| 区块 | 交互 | 调用 API | 后端 handler | 底层功能 |
|------|------|----------|--------------|----------|
| 顶部导航栏 | 点击切换路由 | — | — | 路由 4 项:首页/主播/回顾/设置 |
| 连接状态绿点 | WS 连接指示 | `ws://.../ws` | `s.websocket` (`server.go:250`) | 实时推送通道 |
| 任务徽标点 | ⚠️ **视觉上只有连接绿点**,运行中任务数仅在 `title` 属性里(不可见);见 §4.6 | `listTasks`(挂载时) | `s.listTasks` | 后台任务可视化(弱) |
| 专家模式开关 | 切换全局专家态 | — | — | 控制各页专家区块显隐 |

### 2.2 首页 HomeView(`views/HomeView.vue`)

| 区块 | 交互 | 调用 API | 后端 handler | 底层功能 |
|------|------|----------|--------------|----------|
| 直播状态区 | 刷新/开始录制/停止录制 | `checkAllLive`、`getAllLiveStatus`、`startRecord`、`stopRecord` | `s.checkLive`、`s.liveStatus`、`s.startLiveRecord`、`s.stopLiveRecord` | `internal/live_record` + `internal/discover`(开播探测) |
| 需要注意(告警) | 点击跳转回顾 | `sessionsStore.items`(派生)、`runtimeStore.status.cookie_warnings`、`disk_usage` | `s.listSessions`、`s.runtimeHealth` | 失败场次 / Cookie 过期 / 磁盘告警聚合 |
| 最近回顾 | 点击进回顾 | `sessionsStore.items`(派生 recap_done/uploaded/published) | `s.listSessions` | 最近 6 条已生成回顾 |
| 快捷操作 | 添加主播/发现回放 | `discoverSessions` | `s.discoverSessions` | `internal/discover` 扫描回放 |
| 运行中任务(专家) | 取消任务 | `cancelTask`、`listTasks` | `s.cancelTask`、`s.listTasks` | `internal/worker` 任务管理 |
| 系统能力(专家) | 跳设置 | `runtimeStore.status.capabilities` | `s.runtimeHealth` | `internal/runtime` 能力探测 |
| 统计仪表板(专家) | 只读 | `getDashboardStats` | `s.handleStatsDashboard` | 月度场次/主播排名/费用趋势 |

> 首页轮询(`usePolling`,30s):`liveStatusStore.fetchAll()` + `tasksStore.fetchTasks()`。

### 2.3 回顾页 RecapsView(`views/RecapsView.vue`)

| 区块 | 交互 | 调用 API | 后端 handler | 底层功能 |
|------|------|----------|--------------|----------|
| 标题区操作 | 发现回放/导入/链接下载/更多(清空失败) | `discoverSessions`、`importSession`、`downloadSessionByURL`、`deleteFailedSessions` | `s.discoverSessions`、`s.importSession`、`s.downloadSessionByURL`、`s.deleteFailedSessions` | `internal/discover` / `importer` / `download` |
| 筛选栏 | 状态 tab / 主播 / 搜索 | — (前端过滤 `sessionsStore.items`) | — | 客户端筛选,不发请求 |
| 回顾列表行 | 点击打开回顾抽屉 | `listSessions`(store) | `s.listSessions` | 场次列表 |
| 列表行动作按钮 | ASR/生成回顾/发布/编辑专栏/删除专栏/重试/取回(**上传仅抽屉**,见 §2.3.1) | `submitASR`、`generateRecap`、`publishSession`、`editOpus`、`removeOpus`、`fetchSession` | `s.submitASR`、`s.generateRecap`、`s.publishSession`、`s.editOpus`、`s.removeOpus`、`s.fetchSession` | `internal/asr` / `recap` / `upload` / `publisher` |
| 分页 | 翻页 | — (前端分页) | — | 客户端分页 [10/20/50] |
| 回顾内容抽屉 | 查看/复制回顾,**无编辑 UI**;自定义时间段重生成 | `getRecapContent`、`generateRecapWithRange`(→`recap-partial`) | `s.getRecapContent`、`s.generateRecapPartial` | `internal/recap` |
| 建议术语条目 | 添加到术语表 | `upsertChannelEntry`(glossary) | `s.upsertChannelGlossary` | `internal/glossary` |
| 技术信息(专家) | 只读 | — (用已选 session 字段) | — | session 元数据展示 |
| 发现结果抽屉 | 只读 | (由发现动作填充) | — | `DiscoverResultDrawer` |
| 导入/链接下载抽屉 | 表单提交 | `importSession`、`downloadSessionByURL` | `s.importSession`、`s.downloadSessionByURL` | `ImportSessionDrawer` / `DownloadByURLDrawer` |

> 列表行内动作的实际可用性由 `utils/lifecycle.ts` 的 `getNextAction` / `nextActionFor` 驱动——这是状态机的核心,**重构务必保留或等价重写**。

#### 2.3.1 回顾页状态机矩阵(重构必备)

回顾页有**两套独立的动作入口**——「列表行右侧按钮」和「抽屉内动作栏」,它们渲染逻辑不同、覆盖的动作也不同(这是 §3.4 upload 状态误记的根因)。**重构必须分别复刻两套,不能合并。**

驱动源:`lifecycle.ts:getActionNameForStatus(status)` 算「下一个动作」,叠加 `local_available`/`capabilities` 定禁用;`isPrimaryAction`(`RecapsView.vue:142-145`)只认 `submit_asr`/`generate_recap`/`upload`/`publish` 四个为「主动作」,`stop_record`/`fetch` 被排除。

**表 A:列表行右侧按钮**(`RecapsView.vue:462-532`,用 `v-if/v-else-if` 按 status 分支):

| status | nextAction | 列表行渲染 | 调用 API | endpoint | 禁用条件 |
|--------|-----------|-----------|----------|----------|----------|
| `discovered`/`downloading`/`importing`/`asr_submitted` | null/非主 | 仅进度条,无按钮 | — | — | — |
| `recording` | `stop_record` | ⚠️ **仅进度条,无按钮**(`stop_record` 非 isPrimaryAction,`:142-145`);停止只在首页直播卡 | — | — | — |
| `media_ready` | `submit_asr` | 「提交 ASR」 | `submitASR` | `POST .../asr/submit` | `capabilities.asr_submit` false |
| `asr_done` | `generate_recap` | 「生成回顾」 | `generateRecap` | `POST .../recap/generate` | `recap_generate` false;或 `local_available=false` |
| `recap_done` | `upload` | 「阅读回顾」(`openRecap`,**非 nextAction**) | — | — | — |
| `uploaded` | `publish` | 「发布」 | `publishSession` | `POST .../publish` | `publish_opus` false;或 `local_available=false` |
| `published` | (分支遮蔽 fetch) | 「编辑专栏」+「删除专栏」 | `editOpus`/`removeOpus` | `POST .../opus/edit`、`DELETE .../opus` | `isPublishDisabled()` |
| `failed` | **null** | ⚠️ **「重试」按钮存在但无效**(见已知问题①) | (无) | — | — |

**表 B:抽屉内动作栏**(`RecapsView.vue:606-613`,统一走 `nextActionFor(selectedSession)` + `isPrimaryAction`):

| status | nextAction | 抽屉渲染 | 调用 API | endpoint |
|--------|-----------|---------|----------|----------|
| `media_ready` | `submit_asr` | 「提交 ASR」 | `submitASR` | `POST .../asr/submit` |
| `asr_done` | `generate_recap` | 「生成回顾」 | `generateRecap` | `POST .../recap/generate` |
| `recap_done` | `upload` | ⚠️ **「上传归档」**(列表行没有,仅抽屉) | `uploadSession` | `POST .../upload` |
| `uploaded` | `publish` | 「发布」 | `publishSession` | `POST .../publish` |
| 其它 | null/非主 | 仅「复制 Markdown」 | — | — |

> 关键差异:**`recap_done` 在列表行只读(阅读),但抽屉里能上传**——这正是「状态机没区分两套入口」会漏记的点。同理 `published` 的「编辑/删除」只在列表行,抽屉不渲染(因为 published 的 nextAction `fetch` 不在 isPrimaryAction 里,且抽屉分支不处理 published)。

**叠加规则(跨两表)**:
- `local_available === false` 时,`generate_recap`/`publish` 禁用并提示「本地已清理,请先取回」(`lifecycle.ts:130,144`);**列表行额外**渲染独立「取回」按钮(`:516-531`,非 nextAction)→ `fetchSession` → `POST .../fetch`。
- 动作执行后统一刷新:`sessionsStore.fetchSessions()` + `tasksStore.fetchTasks()`(`runQuickAction:193`)。
- `executeAction`(`:199-204`)只覆盖 4 个主动作;`stop_record`(首页)、`fetch`/`editOpus`/`removeOpus` 走各自独立 handler。

**已知问题(重构应一并决策,不要原样复刻)**:
1. **失败状态无重试路径(死按钮)**:`failed` → nextAction 为 `null`,但列表行仍渲染「重试」(`RecapsView.vue:329-334` 的 `handleRetry` 拿到 null 后什么都不做)。后端有 `POST /api/tasks/:id/retry`,前端未导入 `retryTask`,也无 failed 分支。**重构需明确**:失败重试走任务重试还是场次级重置。
2. **published 的 fetch 被 UI 遮蔽**:lifecycle 给 `published` 返回 `fetch`,但列表行 `v-else-if="published"` 渲染「编辑/删除」,抽屉 isPrimaryAction 不含 fetch——fetch 仅通过 `local_available` 独立按钮生效。lifecycle 与 UI 存在语义错位。
3. **`updateRecapContent` 无 UI 入口**:API wrapper 已导出(`api/sessions.ts:79-81`),但 `RecapsView` 未导入、无编辑 UI,抽屉只有「复制 Markdown」。重构需决定是否补回顾编辑能力。
4. **两套入口的动作集合不对齐**:列表行对 `recap_done` 不给 upload(只读阅读),抽屉给;列表行对 `published` 给 edit/remove,抽屉不给。重构若想统一,需先决策「哪些动作应在哪暴露」。

### 2.4 主播页 StreamersView(`views/StreamersView.vue`)

| 区块 | 交互 | 调用 API | 后端 handler | 底层功能 |
|------|------|----------|--------------|----------|
| 标题区 | 搜索/添加主播 | `listChannels`(store)、identify 对话框 | `s.listChannels` | 主播列表 |
| 主播卡片网格 | 点击打开详情抽屉 | `listChannels`(store)、`listSessions`(store) | `s.listChannels`、`s.listSessions` | 展示能力标签/最近场次 |
| 详情抽屉·最近场次 | 跳回顾 | `sessionsStore` 派生 | `s.listSessions` | 该主播场次 |
| 详情抽屉·自动开关 | 切录制/ASR/发布开关 | `updateChannel`(单字段) | `s.updateChannel` | `internal/channel` 配置 |
| 详情抽屉·Cookie | 弹 QR 登录 | `BiliQRCodeLoginDialog` 组件 | (见 §2.6 bili) | `internal/biliutil` |
| 详情抽屉·术语表 | 折叠面板懒加载 | `GlossaryEditor(scope=channel)` | (见 §2.7 glossary) | `internal/glossary` |
| 详情抽屉·回顾设置(专家) | 覆盖模型/续写 | `updateChannel` | `s.updateChannel` | 主播级 recap 覆盖 |
| 详情抽屉·高级配置(专家) | 只读 | — (用 channel 字段) | — | ID/UID/Room/来源模式等 |
| 添加主播对话框 | 识别/保存 | `identifyChannel`、`identifyAndSave` | `s.identifyChannel`、`s.saveIdentifiedChannel` | `internal/biliutil`(UID 解析) |
| 删除主播 | 删除 | `deleteChannel` | `s.deleteChannel` | `internal/channel` |

#### 2.4.1 主播详情字段级契约(拆主播页时防丢字段 + 防误清空)

**关键机制**:`StreamersView.toInput()`(`:131-147`)把 `Channel` **全量 28 字段**转成 `UpsertChannelInput` 整包 PUT。**单个开关切换(如 auto_record)也会重发全部 28 字段**(`handleToggle:95`、`handleRecapOverride:109`)。

⚠️ **重构陷阱**:
1. `toInput` 当前**完整覆盖** `UpsertChannelInput`(已核对,28=28 无遗漏),所以现状不会丢字段。但若重构时改成「只发改动字段」,后端 PUT 是**全量覆盖语义**——漏带的字段会被清零。务必保持全量回带,或后端改 PATCH。
2. 抽屉里**实际可编辑的字段只有 5 个**(`auto_record`/`auto_asr`/`auto_publish`/`recap_model`/`max_continuations`),其余 23 字段是**只读展示**(高级配置 descriptions)。重构若想补编辑入口,要同步加进 toInput。

**字段分类(28 个,按用途)**:

| 分类 | 字段 | 抽屉可编辑? |
|------|------|------------|
| 标识 | `id`、`name`、`uid`、`live_room_id` | 否(只读) |
| 来源 | `replay_source_url`、`space_url`、`title_prefix`、`source_mode`、`discover_limit` | 否(只读,专家区展示) |
| Cookie | `cookie_file`、`download_cookie_file` | 否(只读,经扫码登录间接改) |
| 录制/ASR | `enabled`、`auto_record`、`auto_asr`、`record_danmaku` | **`auto_record`/`auto_asr` 可编辑**(开关);`enabled`/`record_danmaku` 无 UI 编辑入口 |
| 发布 | `publish_enabled`、`publish_mode`、`publish_category_id`、`publish_list_id`、`publish_private_pub`、`publish_original`、`auto_publish`、`publish_aigc`、`publish_timer_pub_time`、`publish_cover_url`、`publish_topics` | **仅 `auto_publish` 可编辑**(开关);其余 10 个发布字段无 UI 编辑入口(在设置页全局配) |
| 回顾覆盖 | `recap_model`、`max_continuations` | **可编辑**(专家区,`-1`/空=跟随全局) |

> 注意:Channel 的 `publish_*` 字段是**主播级发布覆盖**,与设置页的全局 `PublishConfig`(§2.5.1)是两套——主播级未设时回退全局。重构时不要把两者混为一谈。

### 2.5 设置页 SettingsView(`views/SettingsView.vue`)

分段由 `data-section` 锚点组织,左侧「配置进度」导航可滚动定位(`scrollToSection`,`SettingsView.vue:477`)。

| 区块(data-section) | 交互 | 调用 API | 后端 handler | 底层功能 |
|------|------|----------|--------------|----------|
| 配置进度导航 | 点击滚动到段 | `runtimeStore.status` 派生 | `s.runtimeHealth` | 4 项能力达标指示 |
| 系统状态 | 一键配置(跳段/配密钥) | `runtimeStore.status.capabilities` | `s.runtimeHealth` | `internal/runtime` 探测 |
| API 密钥 | 编辑/清空密钥 | `listSecrets`、`updateSecret` | `s.listSecrets`、`s.updateSecret` | `internal/secrets` |
| B站账号 | 设默认下载/发布,删除 | `listBiliAccounts`、`updateBiliAccount`、`deleteBiliAccount`、`BiliQRCodeLoginDialog` | `s.listBiliAccounts`、`s.updateBiliAccount`、`s.deleteBiliAccount` | `internal/biliutil` |
| 配置备份 | 导出/导入 JSON | `exportConfig`、`importConfig` | `s.handleExportConfig`、`s.handleImportConfig` | `internal/handler/config_export.go` |
| 专栏投稿设置(publish) | 表单保存/选题/系列 | `getPublishConfig`、`updatePublishConfig`、`searchBiliTopics`、`listBiliSeries` | `s.getPublishConfig`、`s.updatePublishConfig`、`s.searchBiliTopics`、`s.listBiliSeries` | `internal/publisher` + `biliutil` |
| 回顾 AI(recap) | 表单保存/高级参数 | `getRecapConfig`、`updateRecapConfig`、`updateSecret`(API key)、`useRecapModels` | `s.getRecapConfig`、`s.updateRecapConfig`、`s.getRecapModels` | `internal/recap` + `internal/aiprovider` |
| WebDAV 上传(webdav) | 表单保存/rclone 兼容 | `getWebDAVConfig`、`updateWebDAVConfig` | `s.getWebDAVConfig`、`s.updateWebDAVConfig` | `internal/upload` |
| 管理员令牌(admin) | 设置/清除 token | `useAdminToken`(本地) | — | `composables/useAdminToken` |
| 全局术语表(**高级折叠**,非专家) | 编辑 | `GlossaryEditor(scope=global)` | (见 §3.6 glossary) | `internal/glossary` |
| 回顾模板(**高级折叠**,非专家) | 编辑 | `RecapTemplateEditor` | (见 §3.7 templates) | `internal/recap` 模板 |
| 外部工具(专家) | 只读 | `runtimeStore.status.tools` | `s.runtimeHealth` | ffmpeg/yt-dlp/rclone 探测 |
| 配置状态(专家) | 只读 | `runtimeStore.status.config_status` | `s.runtimeHealth` | 配置项达标汇总 |

> ⚠️ **可见性机制区分**(重构易踩):设置页有两套独立的显隐开关,不要混淆——
> - `showAdvanced`(`SettingsView.vue:109`,**可点击折叠箭头切换** `:1071`):控制「全局术语表」「回顾模板」(普通用户也能展开)。
> - `isExpert`(顶部全局专家开关):控制「外部工具」「配置状态」(`:1088`)+ 发布分区某行(`:856`)。

#### 2.5.1 设置页字段级契约(拆子组件时防漏字段)

SettingsView 字段多且易漏。下表列出各表单段的**绑定字段、类型/取值、保存 payload**。重构拆子组件时必须逐字段迁移,尤其注意 `0/1` 整型布尔、`timer_pub_time` 时间戳、`clear_password` 清除标志这些非显然字段。

**publish 段(`publishConfig: PublishConfig`,保存 = 整个对象 PUT `/api/config/publish`)**:

| 字段 | 控件 | 取值约定 |
|------|------|----------|
| `enabled` | switch | boolean |
| `mode` | radio | string |
| `private_pub` | radio | **整型 `1`/`2`**(`2`=所有人可见,`1`=仅自己可见;后端校验 `must be 1 or 2`) |
| `summary_len` | (无控件) | int,默认 100;**随整对象 PUT 一并提交**,UI 不暴露但属 payload |
| `cover_url` | input | string,空时后端用 recap/cover.png |
| `close_comment` | switch | **`:active-value="0" :inactive-value="1"`**(反直觉,数字) |
| `up_choose_comment` | switch | **`:active-value="1" :inactive-value="0"`**(数字) |
| `timer_pub_time` | number + `publishTimerEnabled` 计算开关 | Unix 时间戳;北京时间 UTC+8 |
| `original` | checkbox | **数字 `:true-value="1" :false-value="0"`**(非字符串;后端校验 0/1) |
| `aigc` | checkbox | **数字 `:true-value="1" :false-value="0"`**(同上) |
| `topic_id` / `topic_name` | 异步搜索(B 站话题,带 debounce) | 选题时同步填两个字段 |
| `topics` | input | 逗号分隔字符串 |
| `list_id` | select(B 站系列) | number |
| `category_id` | input-number | number |

**recap 段(`recapConfig: RecapConfig`,保存 = 整个对象 PUT `/api/config/recap`;API key 单独 PUT `/api/secrets/AI_API_KEY`)**:

| 字段 | 控件 | 备注 |
|------|------|------|
| `enabled` | switch | boolean |
| `base_url` | input | 如 `https://api.deepseek.com` |
| `model` | select(useRecapModels 分组) | filterable+allow-create,可自由输入 |
| `include_speaker_info` | switch | boolean |
| `max_tokens` | input-number | step=1024 |
| `max_continuations` | input-number | 0-10 |
| `timeout_seconds` | input-number | min=30,step=30 |

**webdav 段(`webDAVConfig: WebDAVConfig`,保存 = 整个对象 PUT `/api/config/webdav`)**:

| 字段 | 控件 | 备注 |
|------|------|------|
| `url`、`username`、`base_path` | input | string |
| `password` | input(show-password) | 仅写;读时后端不回显 |
| `password_set` | tag(只读) | 后端返回,表示已存密码 |
| `clear_password` | checkbox(仅 `password_set` 时显示) | **清除标志**,勾选则清除已存密码 |
| `password_env` | input | 环境变量名兜底 |
| `remote` | input | rclone remote(折叠「rclone 兼容配置」) |

> ⚠️ **刷新时机**:publish/recap/webdav 保存后,多处显式调 `runtimeStore.fetchRuntime(true)`(force=true 绕过 30s 节流)以刷新能力探测——拆组件后保存逻辑必须保留这个 force 刷新,否则「系统状态」卡片不会即时更新。

### 2.6 跨页通用组件目录

| 组件 | 位置 | 用途 | 主要 API |
|------|------|------|----------|
| OnboardingWizard | `onboarding/` | 首次引导(健康检查→配密钥→加主播→关闭) | `GET /api/onboarding/status`、`POST /api/onboarding/dismiss`、`PUT /api/secrets/*`、`POST /api/channels/identify/save` |
| ChannelIdentifyDialog | `channel/` | 两步:识别(输入→预览)→保存 | `identifyChannel`、`identifyAndSave` |
| BiliQRCodeLoginDialog | `channel/` | B 站扫码登录,获取/保存 Cookie | bili.ts 全套(见 §3.5) |
| GlossaryEditor | `channel/` | 术语表 CRUD(scope=global/channel),复用于设置页与主播抽屉 | glossary.ts(见 §3.6) |
| RecapTemplateEditor | `channel/` | 回顾模板 CRUD + 预设 | recap-templates.ts(见 §3.7) |
| DiscoverResultDrawer | `session/` | 发现回放结果展示(props 注入) | —(数据由父填充) |
| ImportSessionDrawer | `session/` | 本地文件导入 | `importSession` |
| DownloadByURLDrawer | `session/` | 链接下载 | `downloadSessionByURL` |
| SessionActions | `session/` | 场次动作按钮组(props 注入 session+capabilities) | sessions.ts 多个动作 |
| TaskProgressBar | `task/` | 任务进度条(props 注入) | — |

---

## 3. API 全集反向索引(按 endpoint)

> 后端路由全表见 `internal/handler/server.go:249-397`(受保护组 `p`,token 中间件)。
>
> **三列含义(重构判断依据)**:
> - **后端 handler**:Gin 路由实际调用的方法。
> - **API wrapper**:`web/src/api/*.ts` 里对应的封装函数是否存在(`✓ 函数名` / `— 无`)。
> - **UI 使用点**:该函数在 `views/`、`components/` 里**实际被调用**的位置(`✓ 页面/组件` / `— 无 UI 入口`)。
>
> ⚠️ **关键区分**:「有 wrapper」≠「有 UI 入口」。下表标 `— 无 UI 入口` 的 wrapper 是**孤儿函数**(封装了但页面没接),重构时必须逐一决策「补 UI」或「删 wrapper」——这部分也汇总到 §5.2。
>
> 重构后每个 endpoint 必须有新入口承载,或在此登记废弃。

### 3.1 系统 / 运行时

| Method + Path | 后端 handler | API wrapper | UI 使用点 |
|---------------|--------------|-------------|-----------|
| `GET /api/healthz` | `s.healthz`(公开) | ✓ `checkHealth` | — 无 UI 入口 |
| `GET /api/health/runtime` | `s.runtimeHealth` | ✓ `getRuntimeStatus` | ✓ runtimeStore → 多页(首页/设置页能力探测) |
| `GET /api/onboarding/status` | `s.handleOnboardingStatus` | ✓ `getOnboardingStatus` | ✓ OnboardingWizard |
| `POST /api/onboarding/dismiss` | `s.handleOnboardingDismiss` | ✓ (内联 `post`) | ✓ OnboardingWizard |
| `GET /api/stats/dashboard` | `s.handleStatsDashboard` | ✓ `getDashboardStats` | ✓ HomeView 专家区 |
| `GET /api/stats/overview` | `s.handleStatsOverview` | — 无 | — 无 UI 入口(预留) |
| `GET /api/stats/cost` | `s.handleStatsCost` | — 无 | — 无 UI 入口(预留) |
| `GET /api/diagnostic/report` | `s.handleDiagnosticReport` | — 无 | — 无 UI 入口(诊断) |
| `POST /api/notify/test` | `s.handleNotifyTest` | — 无 | — 无 UI 入口(预留) |
| `GET /api/cookies/status` | `s.handleCookieStatus` | — 无 | — 无 UI 入口(已被 bili/accounts 取代) |
| `GET /ws` | `s.websocket` | ✓ `useWebSocket` | ✓ AppLayout(全局连接) |

### 3.2 主播 Channels

| Method + Path | 后端 handler | API wrapper | UI 使用点 |
|---------------|--------------|-------------|-----------|
| `GET /api/channels` | `s.listChannels` | ✓ `listChannels` | ✓ channelsStore.fetchChannels → 全站 |
| `POST /api/channels` | `s.createChannel` | ✓ `createChannel` | ✓ channelsStore.create |
| `PUT /api/channels/:id` | `s.updateChannel` | ✓ `updateChannel` | ✓ channelsStore.update / StreamersView 开关 |
| `DELETE /api/channels/:id` | `s.deleteChannel` | ✓ `deleteChannel` | ✓ channelsStore.remove |
| `POST /api/channels/identify` | `s.identifyChannel` | ✓ `identifyChannel` | ✓ ChannelIdentifyDialog |
| `POST /api/channels/identify/save` | `s.saveIdentifiedChannel` | ✓ `identifyAndSave` | ✓ ChannelIdentifyDialog / OnboardingWizard |
| `POST /api/channels/:id/copy-config` | `s.handleCopyChannelConfig` | — 无 | — 无 UI 入口(预留) |
| `POST /api/channels/:id/discover/preview` | `s.handleDiscoverPreview` | — 无 | — 无 UI 入口(预留) |

### 3.3 直播 Live

| Method + Path | 后端 handler | API wrapper | UI 使用点 |
|---------------|--------------|-------------|-----------|
| `POST /api/live/check` | `s.checkLive` | ✓ `checkAllLive` | ✓ HomeView「刷新」 |
| `GET /api/live/status` | `s.liveStatus` | ✓ `getAllLiveStatus` | ✓ liveStatusStore → HomeView |
| `GET /api/live/:channel_id/status` | `s.liveChannelStatus` | ✓ `getChannelLiveStatus` | — 无 UI 入口(列表用批量 getAllLiveStatus) |
| `POST /api/live/:channel_id/record/start` | `s.startLiveRecord` | ✓ `startRecord` | ✓ HomeView 直播卡 |
| `POST /api/live/:channel_id/record/stop` | `s.stopLiveRecord` | ✓ `stopRecord` | ✓ HomeView 直播卡 / SessionActions(组件未启用) |

### 3.4 场次 Sessions / 任务管道

| Method + Path | 后端 handler | API wrapper | UI 使用点 |
|---------------|--------------|-------------|-----------|
| `GET /api/sessions` | `s.listSessions` | ✓ `listSessions` | ✓ sessionsStore.fetchSessions → 多页 |
| `POST /api/sessions/discover` | `s.discoverSessions` | ✓ `discoverSessions` | ✓ HomeView / RecapsView「发现回放」 |
| `GET /api/sessions/:sid` | `s.getSession` | ✓ `getSessionDetail` | — 无 UI 入口(列表已含详情,无单独详情页) |
| `DELETE /api/sessions/:sid` | `s.deleteSession` | ✓ `deleteSession` | — 无 UI 入口(孤儿 wrapper) |
| `DELETE /api/sessions/failed` | `s.deleteFailedSessions` | ✓ `deleteFailedSessions` | ✓ RecapsView「更多」→ 清空失败 |
| `POST /api/sessions/download` | `s.downloadSession` | ✓ `downloadSession` | — 无 UI 入口(孤儿 wrapper;UI 用 download-by-url) |
| `POST /api/sessions/download-by-url` | `s.downloadSessionByURL` | ✓ `downloadSessionByURL` | ✓ DownloadByURLDrawer |
| `POST /api/sessions/import` | `s.importSession` | ✓ `importSession` | ✓ ImportSessionDrawer |
| `POST /api/sessions/:sid/asr/submit` | `s.submitASR` | ✓ `submitASR` | ✓ RecapsView(media_ready) |
| `POST /api/sessions/:sid/recap/generate` | `s.generateRecap` | ✓ `generateRecap` | ✓ RecapsView(asr_done) |
| `POST /api/sessions/:sid/recap-partial` | `s.generateRecapPartial` | ✓ `generateRecapWithRange` | ✓ RecapsView 抽屉「自定义时间段」 |
| `POST /api/sessions/:sid/recap-with-range` | `s.generateRecapWithRange` | — 无 | — 无 UI 入口(与 recap-partial 重叠,死路由) |
| `GET /api/sessions/:sid/recap` | `s.getRecapContent` | ✓ `getRecapContent` | ✓ RecapsView 抽屉 |
| `PUT /api/sessions/:sid/recap/content` | `s.handleUpdateRecapContent` | ✓ `updateRecapContent` | — 无 UI 入口(孤儿 wrapper,无编辑 UI) |
| `POST /api/sessions/:sid/upload` | `s.uploadSession` | ✓ `uploadSession` | ✓ RecapsView 抽屉(recap_done 的 nextAction) |
| `POST /api/sessions/:sid/fetch` | `s.fetchSession` | ✓ `fetchSession` | ✓ RecapsView(本地已清理取回) |
| `POST /api/sessions/:sid/publish` | `s.publishSession` | ✓ `publishSession` | ✓ RecapsView(uploaded 的 nextAction,列表行+抽屉) |
| `POST /api/sessions/:sid/opus/edit` | `s.editOpus` | ✓ `editOpus` | ✓ RecapsView(published「编辑」) |
| `DELETE /api/sessions/:sid/opus` | `s.removeOpus` | ✓ `removeOpus` | ✓ RecapsView(published「删除」) |
| `POST /api/sessions/:sid/glossary/discover` | `s.discoverSessionGlossary` | — 无 | — 无 UI 入口(预留) |
| `GET /api/tasks` | `s.listTasks` | ✓ `listTasks` | ✓ tasksStore.fetchTasks → 多页 |
| `GET /api/tasks/:id` | `s.getTask` | ✓ `getTask` | — 无 UI 入口(孤儿 wrapper;任务经 WS/列表刷新) |
| `POST /api/tasks/:id/retry` | `s.retryTask` | ✓ `retryTask` | — 无 UI 入口(孤儿 wrapper;**回顾页重试按钮实际不调它**,见 §2.3.1 已知问题①) |
| `POST /api/tasks/:id/cancel` | `s.cancelTask` | ✓ `cancelTask` | ✓ HomeView 专家区「取消」 |
| `DELETE /api/tasks/:id` | `s.deleteTask` | ✓ `deleteTask` | — 无 UI 入口(孤儿 wrapper) |
| `DELETE /api/tasks/failed` | `s.deleteFailedTasks` | ✓ `deleteFailedTasks` | — 无 UI 入口(孤儿 wrapper;场次级有同义入口) |
| `POST /api/tasks/batch-retry` | `s.handleBatchRetryTasks` | ✓ `batchRetryTasks` | — 无 UI 入口(孤儿 wrapper) |

### 3.5 B 站登录 / 账号

| Method + Path | 后端 handler | API wrapper | UI 使用点 |
|---------------|--------------|-------------|-----------|
| `POST /api/bili/login/qrcode` | `s.createBiliQRCodeLogin` | ✓ `createQRCodeSession` | ✓ BiliQRCodeLoginDialog |
| `GET /api/bili/login/qrcode/:session_id` | `s.pollBiliQRCodeLogin` | ✓ `pollQRCodeSession` | ✓ BiliQRCodeLoginDialog |
| `POST /api/bili/login/qrcode/:session_id/save` | `s.saveBiliQRCodeLogin` | ✓ `saveQRCodeSession` | ✓ BiliQRCodeLoginDialog |
| `POST /api/bili/login/qrcode/:session_id/save-account` | `s.saveBiliQRCodeToAccount` | ✓ `saveQRCodeToAccount` | ✓ BiliQRCodeLoginDialog |
| `DELETE /api/bili/login/qrcode/:session_id` | `s.deleteBiliQRCodeLogin` | ✓ `cancelQRCodeSession` | ✓ BiliQRCodeLoginDialog |
| `GET /api/bili/accounts` | `s.listBiliAccounts` | ✓ `listBiliAccounts` | ✓ SettingsView 账号卡 |
| `PUT /api/bili/accounts/:id` | `s.updateBiliAccount` | ✓ `updateBiliAccount` | ✓ SettingsView(默认下载/发布切换) |
| `DELETE /api/bili/accounts/:id` | `s.deleteBiliAccount` | ✓ `deleteBiliAccount` | ✓ SettingsView(删除账号) |
| `GET /api/bili/topics/search` | `s.searchBiliTopics` | ✓ `searchBiliTopics` | ✓ SettingsView 投稿区(本地 debounce) |
| `GET /api/bili/series/list` | `s.listBiliSeries` | ✓ `listBiliSeries` | ✓ SettingsView 投稿区 |
| `GET /api/cookie-accounts` | `s.listCookieAccounts` | — 无 | — 无 UI 入口(老接口,被 bili/accounts 取代,建议废弃) |
| `POST /api/cookie-accounts` | `s.createCookieAccount` | — 无 | — 无 UI 入口(同上) |
| `PUT /api/cookie-accounts/:id` | `s.updateCookieAccount` | — 无 | — 无 UI 入口(同上) |
| `DELETE /api/cookie-accounts/:id` | `s.deleteCookieAccount` | — 无 | — 无 UI 入口(同上) |
| `POST /api/cookie-accounts/:id/default-download` | `s.setDefaultDownloadCookieAccount` | — 无 | — 无 UI 入口(同上) |
| `POST /api/cookie-accounts/:id/default-publish` | `s.setDefaultPublishCookieAccount` | — 无 | — 无 UI 入口(同上) |

### 3.6 术语表 Glossary

> 两套并行:全局(`/api/glossary/*`)+ 主播级(`/api/channels/:id/glossary/*`),前端 `GlossaryEditor` 通过 `scope` prop 复用。候选词审批流(`candidates/*`)**前端完全未接**。

| Method + Path | 后端 handler | API wrapper | UI 使用点 |
|---------------|--------------|-------------|-----------|
| `GET /api/glossary/entries` | `s.listGlobalGlossary` | ✓ `listGlobalEntries` | ✓ GlossaryEditor(global) |
| `POST /api/glossary/entries` | `s.upsertGlobalGlossary` | ✓ `upsertGlobalEntry` | ✓ GlossaryEditor(global) |
| `DELETE /api/glossary/entries/:eid` | `s.deleteGlobalGlossary` | ✓ `deleteGlobalEntry` | ✓ GlossaryEditor(global) |
| `GET / PUT /api/glossary/note` | get/updateNote | ✓ `get/updateGlobalNote` | — 无 UI 入口(孤儿 wrapper;GlossaryEditor 无 note 调用) |
| 同构 `/api/channels/:id/glossary/note` | channel note* | ✓ `get/updateChannelNote` | — 无 UI 入口(孤儿 wrapper) |
| `POST /api/glossary/entries/:eid/toggle` | `s.toggleGlobalGlossary` | ✓ `toggleGlobalEntry` | ✓ GlossaryEditor |
| `POST /api/glossary/entries/batch-delete` | `s.batchDeleteGlobalGlossary` | ✓ `batchDeleteGlobalEntries` | ✓ GlossaryEditor |
| `POST /api/glossary/entries/batch-toggle` | `s.batchToggleGlobalGlossary` | ✓ `batchToggleGlobalEntries` | ✓ GlossaryEditor |
| `POST /api/glossary/import/markdown`、`/json` | import* | ✓ `importGlobalMarkdown/JSON` | ✓ GlossaryEditor(导入) |
| `GET /api/glossary/export/json` | `s.exportGlobalJSON` | ✓ `exportGlobalJSON` | ✓ GlossaryEditor(导出) |
| `GET /api/glossary/candidates` | `s.listGlobalGlossaryCandidates` | — 无 | — 无 UI 入口(候选词审批流,预留) |
| `POST /api/glossary/candidates/:cid/approve`、`reject` | approve/reject | — 无 | — 无 UI 入口(预留) |
| 同构 `/api/channels/:id/glossary/entries[...]` | channel glossary* | ✓ glossary.ts 主播级 | ✓ GlossaryEditor(channel) |
| `/api/channels/:id/glossary/candidates/batch-approve`、`reject` | batch* | — 无 | — 无 UI 入口(预留) |

### 3.7 回顾模板 Recap Templates

| Method + Path | 后端 handler | API wrapper | UI 使用点 |
|---------------|--------------|-------------|-----------|
| `GET /api/recap/templates` | `s.listGlobalRecapTemplates` | ✓ `listGlobalRecapTemplates` | ✓ RecapTemplateEditor(global) |
| `PUT /api/recap/templates` | `s.upsertGlobalRecapTemplate` | ✓ `upsertGlobalRecapTemplate` | ✓ RecapTemplateEditor(global) |
| `GET /api/recap/templates/export` | `s.exportGlobalRecapTemplates` | ✓ `exportGlobalRecapTemplates` | ✓ RecapTemplateEditor(导出) |
| `POST /api/recap/templates/import` | `s.importGlobalRecapTemplates` | ✓ `importGlobalRecapTemplates` | ✓ RecapTemplateEditor(导入) |
| `GET /api/recap/presets` | `s.handleListPresets` | ✓ `listRecapPresets` | ✓ RecapTemplateEditor(预设) |
| `GET /api/channels/:id/recap-template` | `s.getChannelRecapTemplate` | ✓ `getChannelRecapTemplate` | ✓ RecapTemplateEditor(channel) |
| `PUT /api/channels/:id/recap-template` | `s.upsertChannelRecapTemplate` | ✓ `upsertChannelRecapTemplate` | ✓ RecapTemplateEditor(channel) |
| `DELETE /api/channels/:id/recap-template` | `s.deleteChannelRecapTemplate` | ✓ `deleteChannelRecapTemplate` | ✓ RecapTemplateEditor(channel) |
| `GET/POST /api/channels/:id/recap-template/export`、`import` | channel import/export | ✓ recap-templates.ts | ✓ RecapTemplateEditor |

### 3.8 配置 / 密钥

| Method + Path | 后端 handler | API wrapper | UI 使用点 |
|---------------|--------------|-------------|-----------|
| `GET /api/secrets` | `s.listSecrets` | ✓ `listSecrets` | ✓ SettingsView 密钥卡 |
| `PUT /api/secrets/:key` | `s.updateSecret` | ✓ `updateSecret` | ✓ SettingsView 密钥卡 / Onboarding |
| `GET / PUT /api/config/publish` | get/update publish | ✓ `get/updatePublishConfig` | ✓ SettingsView 投稿区 |
| `GET / PUT /api/config/recap` | get/update recap | ✓ `get/updateRecapConfig` | ✓ SettingsView 回顾区 |
| `GET /api/config/recap/models` | `s.getRecapModels` | ✓ `getRecapModels` | ✓ useRecapModels → 设置页/主播页 |
| `GET / PUT /api/config/webdav` | get/update webdav | ✓ `get/updateWebDAVConfig` | ✓ SettingsView WebDAV 区 |
| `GET /api/config/export` | `s.handleExportConfig` | ✓ `exportConfig`(blob) | ✓ SettingsView 配置备份 |
| `POST /api/config/import` | `s.handleImportConfig` | ✓ `importConfig` | ✓ SettingsView 配置备份 |

---

## 4. 共享资产(重构需保留或迁移)

### 4.1 类型定义(`api/types.ts`)

前后端 DTO 唯一对齐源。核心实体:`Channel` / `UpsertChannelInput`、`Session` / `SessionDetail`、`Task` / `TaskProgressEvent`、`LiveStatus`、`RuntimeStatus`(含 `Capabilities`/`ConfigStatus`/`ToolStatus`)、`DashboardData`、`PublishConfig` / `RecapConfig` / `WebDAVConfig`、`GlossaryEntry`、`RecapTemplate`、B 站 `QRCode*` / `BiliCookieAccount`。

> `Channel` 字段有 30+ 个(录制/ASR/发布/来源/发现/模板覆盖等),重构表单时不要漏字段——见 `types.ts:3-34`。

### 4.2 工具/常量(`utils/`)

| 文件 | 作用 | 重构注意 |
|------|------|----------|
| `friendlyStatus.ts` | session 状态 → 友好标签/颜色/进度/动作;`statusGroupMap` 筛选用 | **状态机核心**,前端多处依赖 |
| `lifecycle.ts` | `getNextAction` / `nextActionFor` 决定每场次的下一个可用动作 | 回顾页按钮驱动,**必须等价重写** |
| `format.ts` | `formatDateTime` 等格式化 | 全站时间显示 |
| `status.ts` | 状态相关常量 | — |
| `constants.ts` | 通用常量 | — |

### 4.3 stores(`stores/`)

| Store | state | action | 备注 |
|-------|-------|--------|------|
| sessions | `items`、`loading` | `fetchSessions` | 纯列表,无单条缓存 |
| channels | `items`、`loading` | `fetchChannels`、`create`、`update`、`remove` | action 内带 ElMessage |
| tasks | `items`、`loading` | `fetchTasks`、`handleTaskProgress` | WS 推送增量更新 |
| runtime | `status`、`loading` | `fetchRuntime(force)` | 30s 节流 |
| liveStatus | `statusMap`、`loading` | `fetchAll`、`getStatus(id)` | 按 channel_id 索引 |

### 4.4 自管理状态组件(违反「理想分层」,重构需特别处理)

§1.2 的分层约定要求「components 不直接持有业务状态」,但以下 4 个组件**各自持有完整业务状态机**(loading/dialog/列表/导入导出/轮询),直接调 api wrapper 甚至原始 `api/client`。重构若想收紧分层,这 4 个是重点改造对象;若承认现状,则必须逐一迁移它们的状态机。

| 组件 | 内部状态 | 调用方式 | 状态机要点 |
|------|---------|----------|-----------|
| **OnboardingWizard** | `needed`、`step`(0-3)、`loading`、`runtimeData`、密钥/主播输入 | ⚠️ **绕过具名 wrapper**,直接用 `api/client` 的原始 `get/post/put`(`:4,20-66`) | 4 步向导;`onMounted` 查 `/api/onboarding/status`→`needed` 为真才查 runtime;step1 存密钥、step2 存主播、finish/dismiss 都 POST `/api/onboarding/dismiss` |
| **BiliQRCodeLoginDialog** | `state`(7 态:loading/showing_qr/scanned/saving/done/expired/failed)、`session`、`pollResult`、`usage`、`nickname` | ✓ 用 bili.ts 具名 wrapper | **自带 2s 轮询**(`pollTimer`,`:118`);`watch(visible)` 触发 startLogin;`succeeded` 后停止轮询等用户点保存;关闭时尽力 `cancelQRCodeSession` 清理;双模式(channel→`saved` / account→`saved-account` 事件) |
| **GlossaryEditor** | `loading`、`editableEntries`、`readonlyEntries`、`selectedEntries`(批量选)、`addDialog`、`importDialog` | ✓ 用 glossary.ts 具名 wrapper | `watch([scope,channelId])` 重新 fetch;channel 模式 `showGlobalReadonly` 时**额外拉全局词条**做只读联查(`:94-99`,codex 遗漏点);toggle 失败回滚 `entry.enabled`;导出用 `Blob+a.click()` |
| **RecapTemplateEditor** | `loading`、`saving`、`systemPrompt`/`userFormat`/`fanName`/`extraVars`、`useCustom`(channel 开关)、`presets`、`importing` | ✓ 用 recap-templates.ts 具名 wrapper | ⚠️ **`__builtin__` 哨兵**:空字符串或 `__builtin__` 都表示「用内置默认」(`:41-42,66-67`);channel 模式 `useCustom=false` 时显示全局 resolved 预览;`watch(channelId)` 重新 loadData |

> 这 4 个组件都不接受外部注入的业务数据,而是自己 onMounted 拉取——重构拆分时要么保留这个自取模式,要么把数据上提到父级/store。

### 4.5 路由 query 契约(深链 / 重定向入口)

旧路径重定向(§1.3)会产生 query 参数,各页用 `watch(immediate:true)` 消费。**关键风险:这些 watch 在 `immediate` 时立即触发,但 store 数据要等 `onMounted` 的 fetch 完成——存在竞态,可能找不到目标对象导致抽屉打不开。**

| Query | 消费页 | 逻辑 | 竞态风险 |
|-------|--------|------|----------|
| `?import=1` | RecapsView (`:97-111`) | 打开 ImportSessionDrawer;关闭时 `router.replace` 清掉 query | 无(纯开关,不依赖数据) |
| `?sid=<id>` | RecapsView (`:114-123`) | `sessionsStore.items.find(s=>s.id===sid)` → `openRecap` | ⚠️ **有**:`immediate` 时 items 多半为空,find 落空 → 抽屉不开 |
| `?id=<id>` | StreamersView (`:154-165`) | `channelsStore.items.find(c=>c.id===id)` → `openDetail` | ⚠️ **有**:同上,items 未加载时 find 落空 |
| `?section=runtime` | SettingsView (`:222-232`) | 仅设 `showAdvanced=true`(**展开高级折叠区,非 isExpert 专家区**);⚠️ `runtime` 无对应 `data-section` 锚点,不会滚动到系统状态区(现状缺口) | 无(只切布尔) |

> 重构建议:把「先确保数据就绪再消费 query」显式化(如 await fetch 后再 find,或用 store 的 loaded 标志位触发 watch),不要依赖当前「碰运气」的 immediate 时序。

### 4.6 WebSocket 事件契约

| 项 | 说明 |
|----|------|
| 连接 | `ws://<host>/ws`(`useWebSocket.ts:27`,自动按页面协议选 ws/wss) |
| 推送消息类型 | 仅 `task_progress`(其它消息 JSON.parse 失败则忽略,`:48-54`) |
| 消息字段 | `TaskProgressEvent`(`types.ts:244-253`):`type`/`task_id`/`channel_id`/`session_id`/`status`/`progress`/`message`/`error` |
| 分发路径 | WS → mitt `task_progress` → `AppLayout:42-55` → `tasksStore.handleTaskProgress` |
| **已知任务更新** | `tasksStore.handleTaskProgress`(`tasks.ts:20-36`):在 items 中找到 task 则**增量更新** status/progress/message/error,终态补 finished_at |
| **未知任务** | find 不到 → 触发 `fetchTasks()` **全量刷新**(`:33-35`)——这是 WS 与列表不一致时的兜底 |
| 重连 | 指数退避 1s→30s(`scheduleReconnect`);心跳:**每 5s 检查**一次,若距上次消息超 30s 则主动 close 触发重连(`:77-84`) |
| 连接指示 | AppLayout 绿点(`connected` ref);**注意:运行中任务数只在 `title` 属性里,不是可见徽标**(codex 准确性问题②) |

> 重构注意:WS 只推 task_progress,**不推 session/live 状态变更**。session 列表靠首页 30s 轮询 + 动作后手动 fetchSessions 刷新;live 靠首页轮询。重构若想做「发布完成实时刷新回顾列表」,需后端加 session 事件或前端在 task 终态后主动刷 sessions。

---

## 5. 重构风险与关注点(基于测绘发现)

### 5.1 高耦合点(改动影响面大)

1. **状态机(`friendlyStatus` + `lifecycle`)**:回顾页几乎所有按钮显隐/动作都依赖它。重构若调整 session 状态流转,这两处必须同步,否则按钮逻辑全乱。
2. **专家模式 vs 高级折叠(两套显隐,勿混)**:全站 `v-if="isExpert"` 散布(首页 3 处、回顾抽屉 1 处、主播抽屉 1 处、**设置页 2 处**:发布分区行 `:856` + 外部工具/配置状态 `:1088`)。**注意设置页另有 `showAdvanced` 折叠开关**(`:1071`,控制全局术语表/回顾模板),与 isExpert 是两套独立机制——重构信息架构时必须分清,否则会把普通用户也能展开的区块误挂到专家模式。
3. **`GlossaryEditor` / `RecapTemplateEditor` 双 scope 复用**:同一组件服务全局(设置页)与主播级(主播抽屉)两套 API。重构组件 API 时两处都得改。
4. **Element Plus 全量图标注册**:`main.ts:16-18`。改按需引入会触发大量图标组件找不到。
5. **4 个自管理状态组件**(§4.4):Onboarding/BiliQR/Glossary/RecapTemplate 各自持完整业务状态机,且 Onboarding 绕过具名 wrapper 直接用原始 `api/client`。重构收紧分层时它们是主要改造对象。
6. **路由 query 竞态**(§4.5):`?sid=`/`?id=` 的 `watch(immediate)` 依赖 store 数据已加载,实际多数落空——深链打开抽屉不可靠,重构需显式化「数据就绪再消费」。
7. **WS 覆盖有限**(§4.6):只推 task_progress,session/live 靠轮询。重构若想实时化需后端加事件。

### 5.2 后端有、前端未接的接口(决策:接 or 弃)

重构是「补全」这些的好时机,但需明确登记。**分两类**(与 §3 的 UI 使用点列呼应):

**A. 完全无 wrapper、无 UI(后端预留能力,前端从未接入)**:
- `stats/overview`、`stats/cost`(更细粒度统计)
- `diagnostic/report`(诊断报告)
- `notify/test`(通知测试)
- `cookies/status`、`cookie-accounts/*` 6 项(已被 `bili/accounts` 取代,**建议确认废弃**)
- `channels/:id/copy-config`、`discover/preview`
- `sessions/:sid/glossary/discover`、`sessions/:sid/recap-with-range`(死路由)
- `glossary/candidates/*`(候选词审批流,全局 + 主播级共 6 项路由:global list/approve/reject + channel list/batch-approve/batch-reject)

**B. 有 wrapper、无 UI(「孤儿函数」——封装了但页面没接,共 15 个)**:
- `getSessionDetail`、`deleteSession`(场次列表已含详情,无单独详情/删除页)
- `downloadSession`(UI 用的是 `download-by-url`)
- `getTask`、`retryTask`、`deleteTask`、`deleteFailedTasks`、`batchRetryTasks`(5 个 task wrapper)
- `updateRecapContent`(回顾编辑能力,见 §2.3.1 已知问题③)
- `getChannelLiveStatus`(单点查,列表用批量 `getAllLiveStatus`)
- `getGlobalNote`/`updateGlobalNote`、`getChannelNote`/`updateChannelNote`(4 个术语表备注 wrapper,GlossaryEditor 无 note UI;**隐藏漂移,容易误判为已覆盖**)
- `checkHealth`(`/api/healthz`,仅 onboarding 间接用 `/api/health/runtime`)

> ⚠️ B 类尤其危险:它们让人误以为「功能已实现」,实际 UI 没接。其中 `retryTask` 与回顾页那个**无效的重试按钮**(§2.3.1 已知问题①)直接相关——重构时要么补 UI 调用,要么删 wrapper,不要原样保留。

### 5.3 契约不一致与现存 bug(重构可顺手清理)

- `sessions/:sid/recap-partial` vs `recap-with-range`:两个语义重叠的 endpoint,前端只用前者(`recap-partial` → `generateRecapPartial`),`recap-with-range` 是死路由。建议后端确认废弃后者。
- **BiliQR 登录回调事件错配(功能性 bug)**:`BiliQRCodeLoginDialog` emit 的是 `saved` / `saved-account`(`:205,186`),但 `StreamersView:354` 监听的是 `@success` → **扫码保存成功后,主播列表不会自动刷新**,用户需手动刷新。重构时事件契约必须对齐。
- **`SessionActions.vue` 是死代码 + 逻辑不一致**:全项目无引用,内部却维护一套与 `RecapsView + lifecycle.ts` 不同的动作逻辑(直接调 `submitASR/generateRecap/uploadSession/fetchSession/stopRecord`)。重构时必须决策删除还是合并。

### 5.4 架构债务(重构应正面处理)

- **无单场缓存**:`sessions` store 只存列表,单场详情每次 `getSessionDetail` 重新请求(而该 wrapper 实际无 UI 调用)。重构可加 detail 缓存。
- **轮询 vs WS 并存**:首页轮询(30s)+ 全局 WS 推送并存,存在双更新路径。重构可统一为 WS + WS 断线降级轮询。
- **views 偏胖**:SettingsView 50KB、RecapsView 29KB,单文件承担过多。重构应按区块拆子组件。
- **分层约定未被遵守**:多个组件直接持业务状态并调原始 `api/client`(`OnboardingWizard` 直接用 `get/post/put`;`GlossaryEditor`/`RecapTemplateEditor` 自管列表/批量/导入导出状态)。§1.2 的「理想分层」与现状有偏差,重构应明确是收紧还是承认现状。

---

## 6. 校验清单(重构完成后逐项打勾)

- [ ] 4 个路由 + 9 条旧路径重定向全部保留
- [ ] §1.4 横切机制(token/401/WS/专家/轮询/错误 toast)全部移植
- [ ] §3 每个「✓ UI 使用点」的 endpoint 都有新入口承载
- [ ] §3 标 `— 无 UI 入口` 的 endpoint 已逐一决策「接入」或「登记废弃」(尤其 §5.2 B 类 15 个孤儿 wrapper)
- [ ] §2.3.1 状态机矩阵逐状态复刻,且三个已知问题(重试死按钮/published fetch 遮蔽/回顾无编辑)已决策
- [ ] §2.5.1 设置页字段逐个迁移(注意 `close_comment` 反直觉、`timer_pub_time` 时间戳、`clear_password` 清除标志、整型布尔)
- [ ] §4.4 四个自管理组件(Onboarding/BiliQR/Glossary/RecapTemplate)的状态机逐一迁移,`__builtin__`/`showGlobalReadonly` 等哨兵保留
- [ ] §4.5 路由 query(`?sid`/`?id`/`?import`/`?section`)的竞态已显式处理(数据就绪再消费)
- [ ] §4.6 WS 契约(task_progress 分发、未知任务全量刷新兜底)等价实现
- [ ] §4 共享资产(类型/状态机/stores)等价迁移
- [ ] §5.1 高耦合点逐一验证无回归
- [ ] BiliQR 事件错配(§5.3)、SessionActions 死代码 已处理
- [ ] `make web-build` 通过 + `npm run type-check` 通过
