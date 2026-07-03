# 前端架构

> **本文档是前端架构的权威描述**(当前落地状态)。
> 日期:2026-06-21

---

## 0. 技术栈

Vue 3 (Composition API, `<script setup>`) + Element Plus + Pinia + vue-router 4 + Vite + TypeScript。
axios 单 client(`api/client.ts`)注入 X-Admin-Token,401 自动补登,错误 toast。
WebSocket 仅 `task_progress`(后端不推 session/live 实时事件)。

**架构原则:不换栈,分层清晰、view 退化为壳、业务逻辑下沉到 features/**。

---

## 1. 分层架构

```
web/src/
├── api/                         # 契约层:唯一 HTTP 出口,typed Promise,无 UI 副作用(新 wrapper)
│   └── client.ts                #   axios 单 client(401 补登/错误 toast 是全局横切,§5)
├── stores/                      # 实体缓存层:ensureLoaded + 刷新策略,不承载页面弹窗状态
│   ├── sessions.ts              #   loaded/byId/ensureLoaded(inflight 去重)/getByIdAfterLoad
│   ├── channels.ts              #   同上(对称)
│   ├── tasks.ts                 #   handleTaskProgress(WS 增量)+ 未知任务全量兜底
│   ├── runtime.ts               #   30s 节流 + force=true
│   └── liveStatus.ts            #   statusMap(后端无 live WS,只能轮询)
├── composables/                 # 跨域复用 hook
│   ├── useAdminToken.ts         #   X-Admin-Token 本地存储
│   ├── useExpertMode.ts         #   专家开关
│   ├── usePolling.ts            #   通用轮询(onUnmounted 自动清)
│   ├── useRecapModels.ts        #   模型下拉分组
│   ├── useWebSocket.ts          #   WS 连接/心跳/重连(mitt 事件总线转发)
│   └── useAppRefreshCoordinator.ts  # 【核心】刷新 ownership 唯一(§6)
├── features/                    # 按业务领域聚合
│   ├── recaps/
│   │   ├── sessionActions.ts    #   UI 动作矩阵(表A 行 / 表B 抽屉;§3)
│   │   ├── sessionActions.test.ts #  41 单测,锁定状态机行为
│   │   └── components/          #   RecapToolbar/SessionFilters/SessionTable/RecapDrawer
│   ├── settings/
│   │   └── components/          #   Publish/Recap/WebDAV/AdminToken/BiliAccounts/ConfigBackup + settings-cards.css
│   ├── channel/                 #   useBiliQRCodeLogin/useRecapTemplateEditor/useGlossaryEntries
│   └── onboarding/              #   useOnboardingWizard
├── components/                  # 共享/展示组件
│   ├── layout/AppLayout.vue     #   WS 经 coordinator 接入;导航;专家开关
│   ├── session/                 #   Discover/Import/DownloadByURL 抽屉
│   ├── channel/                 #   GlossaryEditor/RecapTemplateEditor(瘦身后调 features composable)
│   ├── onboarding/OnboardingWizard.vue
│   └── task/TaskProgressBar.vue
├── utils/                       # 纯函数:lifecycle/friendlyStatus/format/constants
└── views/                       # 路由壳:加载调度 + store 编排 + action handlers
    ├── HomeView.vue             #   liveStatus 30s 轮询(tasks 归 coordinator)
    ├── RecapsView.vue           #   455 行壳(原 984)+ features/recaps 子组件
    ├── SettingsView.vue         #   727 行壳(原 1622)+ features/settings 子组件
    └── StreamersView.vue
```

**职责边界(严格遵守)**:
- `api/*`:唯一 HTTP 出口。**新 wrapper 无 UI 副作用**(`client.ts` 的 401/toast 是既有全局横切,不改)。
- `stores/*`:实体缓存 + 刷新策略。**不承载页面弹窗状态**。
- `features/*/composables`:页面内业务流程(表单提交、状态机决策)。
- `features/*/components`:业务组件,**props 输入 / emit 输出**,不直接调 store/api。
- `components/shared`:纯展示,**绝不自取 store**。
- `views/*`:路由壳,加载调度 + action handlers(API 调用 + 刷 store 的单一出口)。

---

## 2. 状态管理

5 个 store,边界清晰(**不合并**,合并反增耦合):

| store | 关键方法 | 说明 |
|-------|---------|------|
| sessions | `ensureLoaded()`(inflight 去重)/ `getByIdAfterLoad(id)` / `fetchSessions()`(强刷) | 解决 `?sid` 竞态;ensureLoaded 供首载/query 消费,fetchSessions 供数据变更后强刷 |
| channels | 同上(对称) | 解决 `?id` 竞态 |
| tasks | `handleTaskProgress(event)` / `fetchTasks()` | WS 增量更新;未知任务全量兜底 |
| runtime | `fetchRuntime(force?)` | 30s 节流;配置保存后必须 force=true |
| liveStatus | `fetchAll()` | 后端无 live WS,Home 轮询 |

**`ensureLoaded` 并发去重**:`inflight` promise 复用,多入口(query watch + onMounted)同时进时只发一次 list 请求。

---

## 3. 状态机收敛(`features/recaps/sessionActions.ts`)

回顾页有两套动作入口,**不粗暴合并,用显式服务表达差异**:

| 入口 | recap_done | published | failed | 主动作 | local 不可用 |
|------|-----------|-----------|--------|--------|-------------|
| **列表行(表A)** | 阅读(打开抽屉) | edit + remove | retry(仅 retryable) | submit_asr/generate_recap/upload/publish | 独立 fetch 按钮(并存) |
| **抽屉(表B)** | upload | 无 | 无 | 同表A | 无 |

- `UIActionName`(6 个):`submit_asr/generate_recap/upload/publish/fetch/retry`。
  **不复用** `lifecycle.SessionActionName`(后者 6 个且含 `stop_record`,属首页)。**不是其超集**。`edit_opus`/`remove_opus` 已移除(B站专栏只能手动去 B站管理);「重新生成回顾」属非推进型动作,在 RecapDrawer 硬编码,不进 `UIActionName`。
- `getRowActions(session, capabilities, currentTask?)` — 列表行
- `getDrawerActions(session, capabilities)` — 抽屉
- `decideRetry(session, currentTask)` — retry 四边界(retryable / no_task_id / task_missing / task_not_failed)
- `UI_ACTION_REASON` — 独立于 `lifecycle.ACTION_DISABLED_REASON`(后者只覆盖 6 个 lifecycle 动作)
- **复用** `lifecycle.getNextAction` 做主动作能力判断,**不重写** status→action 映射(PLAN §8 红线)
- **41 个单测**锁定表A/B 全 `status × {local, caps, target}` 组合,防回归

---

## 4. 胖 View 拆解

两个最大 view 已退化为薄壳,业务下沉到子组件:

| view | 历史峰值 | 现壳行数 | 子组件 |
|------|--------|---------|--------|
| RecapsView | 984 行 | **455 行** | RecapToolbar / SessionFilters / SessionTable / RecapDrawer |
| SettingsView | 1622 行 | **727 行** | Publish / Recap / WebDAV / AdminToken / BiliAccounts / ConfigBackup SettingsCard |

**壳只保留**:路由/query 消费 + store 编排 + 过滤计算 + API handlers + 外部 Drawer。
**子组件**:props 输入 + emit 输出,不直接调 store/api;`data-section` 属性保留在根元素(scrollToSection 导航)。

**共享样式**:`features/settings/components/settings-cards.css`(非 scoped 全局),覆盖 `column-form/column-row/settings-card` 等多 section 共用类;SettingsView + 各子组件 import。scoped 隔离会导致子组件匹配不到父样式,故提取全局。

**留壳决策**(Secrets/SystemStatus/Setup 三者):强耦合 `openEdit`(跨 section 命令入口)+ `scrollToSection`(DOM)+ secrets 共享状态,强拆需 provide/inject 倒贴复杂度,违背渐进低风险。

---

## 5. 自管理组件 → composables

4 个原「自管理组件」的业务状态机抽到 `features/*/composables`,组件退化为纯展示:

| composable | 来源 | 抽出内容 | 组件保留 |
|-----------|------|---------|---------|
| `useBiliQRCodeLogin` | BiliQRCodeLoginDialog | DialogState 状态机 + 2s 轮询(复用 usePolling)+ 两路保存 | canvasRef + renderQRCode + emit 桥接 |
| `useRecapTemplateEditor` | RecapTemplateEditor | 四字段表单 + global/channel 双 scope 分发 + 预设 | selectedPresetName / defaultPreviewVisible / 常量 |
| `useGlossaryEntries` | GlossaryEditor | CRUD/批量/导入导出(global/channel 分发集中) | 8 个 dialog/form ref(纯 UI) + tableRef |
| `useOnboardingWizard` | OnboardingWizard | step 状态机 + 三步 API | 纯展示 |

**设计**:composable 用 getter(`() => props.xxx`)接收响应式 props(非 Ref,贴合调用方,避免静态快照过期);DOM ref(canvas/upload/tableRef)留组件;emit 留组件,composable 通过回调通知结果。

---

## 6. 刷新协调器(`composables/useAppRefreshCoordinator.ts`)

**单一 owner** 收口 WS + 降级轮询,消除「Home 轮询与 WS 各刷 tasks」的双刷问题(PLAN §7.2):

| 刷新源 | owner | 说明 |
|--------|-------|------|
| WS `task_progress` | **coordinator** | 增量更新 tasks + 终态(succeeded/failed/cancelled)刷 sessions |
| WS 断线降级轮询 | **coordinator** | 10s 刷 tasks+sessions;重连后停轮询 + 立即全量拉回 |
| Home 激活轮询 | **只刷 liveStatus** | 后端无 live WS 事件;tasks 归 coordinator |
| 动作后刷新 | **store 方法** | 动作 handler 内 fetchSessions/fetchTasks |

挂载在 `AppLayout`(与 WS 同生命周期),复用 `useWebSocket`(连接/心跳/重连)和 `usePolling`(interval/生命周期),不重写。

---

## 7. 测试

- `cd web && npm run type-check` — vue-tsc 全量类型检查
- `cd web && npx vitest run` — 单测(4 文件,90 测试):
  - `features/recaps/sessionActions.test.ts`(41)— 表A/B 状态机矩阵(防回归硬保障)
  - `utils/lifecycle.test.ts`(19)、`utils/friendlyStatus.test.ts`(13)、`utils/format.test.ts`(17)
- `cd web && npm run build` — 生产构建(改路由/import/Vite 配置时跑)

---

## 8. 历史易错点(已修复)

以下两个易错点已修复,记录在此供后续维护参考:
1. **配置导入策略**(SettingsView):`ElMessageBox` 无 input 时 resolve 返回 action 字符串,若误用 `{ value }` 解构 → 点「合并」实际执行「覆盖」。正确做法用 try/catch 区分 confirm/cancel/close。
2. **retry 死按钮**(RecapsView):failed 状态按钮点了空转,需接通 `retryTask` + `decideRetry` 五边界。

---

## 9. 红线(架构约束,后续开发须遵守)

- ❌ 不换 Vue/Pinia/Element Plus
- ❌ 不改后端 API 路径/方法/payload
- ❌ 不改 4 主路由 + 9 旧路径重定向
- ❌ 不改认证机制(X-Admin-Token、401 补登)
- ❌ 不扩展 WS 成 session/live 实时通道(除非后端加事件)
- ❌ 不把业务状态塞进单一全局 store
- ❌ 不重写 `utils/lifecycle.ts` 决策核心(只包装)
- ❌ 不改 `api/client.ts` 的 401 补登/错误 toast(全局横切)
- ❌ 新 wrapper 不加 UI 副作用(emit/弹窗)
- ❌ `components/shared` 不自取 store
