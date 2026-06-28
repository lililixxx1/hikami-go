# 前端重构方案

> **基础**:docs/FRONTEND_REFACTOR_BASELINE.md(95% codex 核对通过的基线)
> **生成方式**:两份独立架构分析(ZCode 主理 + codex)对照整合,关键决策均双视角收敛
> **日期**:2026-06-20

---

## 0. TL;DR

**结论:保持 Vue3 + Element Plus + Pinia,原地渐进重构,不换栈。** 按 6 个阶段推进,每阶段可独立合并、可回滚,不阻塞功能迭代。试点选 RecapsView。

---

## 1. 架构选型:为什么不换栈

**两份独立分析一致推荐原地渐进重构。** 理由全部基于基线证据(非主观):

1. **痛点根源是工程组织,不是技术栈**。基线揭示的所有问题(胖 view、分层未遵守 §1.2、死按钮 §2.3.1、事件错配 §5.3、孤儿 wrapper §5.2、query 竞态 §4.5)都是组织问题。换栈解决不了任何一个,只会重新制造。
2. **体量不支持重写**。4 view + 10 组件共约 3700 行,**单人维护的中型项目**。换栈 = 数周停滞 + 双写期 + 回归全重做,ROI 极差。
3. **契约层是健康资产,不能丢**。`api/*.ts` 是干净的 typed Promise 层(§1.2),换栈会丢掉它。
4. **Element Plus 深度嵌入**。全量图标注册(§5.1-4)、表单/抽屉/上传等稳定能力,换库等于全重写。"风格平"是设计问题,改 CSS/组件封装就够,不构成换库理由。
5. **状态机复杂度在 utils 不在框架**。lifecycle.ts + friendlyStatus.ts 换任何框架都要原样迁移——既然要迁,不如不换。
6. **后端是稳态**。基线 §0 红线:API 契约不变。前端应围绕现有 DTO/wrapper 做边界治理。

---

## 2. 目标分层架构

**核心改动:引入 `features/`(按业务领域聚合),view 退化为路由壳,领域逻辑下沉到 feature composable。**

```
web/src/
├── api/                         # 【不变】后端契约层:typed Promise,不弹 toast、不读 router
├── app/                         # 【新】应用级:路由 + 全局布局
│   ├── router/                  #   现有 router/index.ts(4 路由 + 9 重定向,必须保留)
│   └── layout/                  #   AppLayout(WS 连接、导航、专家开关)
├── views/                       # 【弱化】路由壳:加载 feature 页面容器,不含业务逻辑
├── features/                    # 【新】按业务领域聚合(核心改动)
│   ├── recaps/
│   │   ├── components/          #   RecapToolbar/SessionFilters/SessionTable/RecapDrawer...
│   │   ├── composables/         #   useRecapContentDrawer 等
│   │   └── sessionActions.ts    #   两套动作入口的显式矩阵(见 §4)
│   ├── settings/components/     #   按 data-section 拆 8 个 section(见 §3)
│   ├── streamers/
│   │   └── channelInput.ts      #   toUpsertChannelInput(channel) 全量回带(§2.4.1)
│   ├── home/
│   ├── onboarding/              #   useOnboardingFlow(收敛自管理组件)
│   ├── bili-login/              #   useBiliQrLoginFlow
│   ├── glossary/                #   useGlossaryEditor
│   └── recap-template/          #   useRecapTemplateEditor
├── stores/                      # 【增强】实体缓存 + ensureLoaded + 刷新协调(见 §5)
├── composables/                 # 【保留】真正跨域:useAdminToken/useExpertMode/usePolling/useWebSocket
├── components/
│   ├── layout/                  #   保留
│   └── shared/                  #   【新】纯展示、无业务自取(StatusTag/EmptyState)
└── utils/                       # 【不变】lifecycle/friendlyStatus/format/constants
```

**职责边界**:
- `api/*`:唯一 HTTP 出口,typed Promise,**无 UI 副作用**。
- `stores/*`:全局实体缓存 + 刷新策略,**不承载页面弹窗状态**。
- `features/*/composables`:页面内业务流程(如表单提交、状态机决策)。
- `features/*/components`:业务组件,props 输入 / emit 输出;可用同 feature 的 composable,不散落调 API。
- `components/shared`:**绝不自取 store**,纯展示。

---

## 3. 胖 View 拆解策略

### SettingsView(1622 行 → 壳 + 8 个 section)

| 子组件 | props | emit | 关键契约(引用 §2.5.1) |
|--------|-------|------|----------------------|
| SettingsProgressNav | setupItems, doneCount | scroll(section) | — |
| SystemStatusSection | runtimeStatus, capItems, loading | refresh, action | — |
| SecretsSection | secrets, loading | edit(key), clear(item) | — |
| BiliAccountsSection | accounts, loading | add-account, set-default-*(acc,val), delete(acc) | ⚠️ 监听 BiliQR `saved-account`(§5.3) |
| ConfigBackupSection | — | export, import(file,strategy) | — |
| **PublishConfigSection** | v-model:config, saving, topicOptions, topicsLoading, seriesOptions, seriesLoading, seriesError, isExpert | save, search-topic, load-series | **全字段**:enabled/mode/private_pub(1/2)/summary_len/cover_url/close_comment(反直觉)/up_choose_comment/timer_pub_time/original/aigc(数字)/topic_id/topic_name/topics/list_id/category_id;**行为细节**:`publishTimerEnabled` 计算开关(>0 为开)、topic 搜索本地 debounce、topic_name 与 topic_id 同步、series 加载失败显示 seriesError |
| **RecapConfigSection** | v-model:config, v-model:apiKey, modelGroups, saving | save | enabled/base_url/model/include_speaker_info/max_tokens/max_continuations/timeout_seconds;**保存后 force fetchRuntime(true)** |
| **WebDAVConfigSection** | v-model:config | save | url/username/password/password_set/clear_password(清除标志)/password_env/base_path/remote;**行为细节**:保存后清空 password/clear_password(一次性标志,提交即失效) |
| AdvancedKnowledgeSection | showAdvanced | — | ⚠️ 全局术语表/回顾模板,**showAdvanced 控制,非 isExpert**(§2.5) |
| RuntimeExpertSection | isExpert, tools, configStatus | — | 外部工具/配置状态 |

**壳(SettingsView.vue)只保留**:`?section=` 消费 + 数据加载调度 + `showAdvanced`/`isExpert` 两开关编排。section 组件用 props 接收可见性,**不在子组件内判断 isExpert**。

### RecapsView(943 行 → 壳 + 组件)

| 子组件 | props | emit | 关键契约(引用 §2.3.1) |
|--------|-------|------|----------------------|
| RecapToolbar | — | discover, open-import, open-download, clear-failed | 4 动作 |
| SessionFilters | v-model:keyword, v-model:statusFilter, v-model:channelFilter; channels | — | 纯前端过滤 |
| SessionTable | sessions, channels, tasks, capabilities | open-recap, quick-action, fetch, edit-opus, remove-opus, retry | 按状态渲染行 |
| **SessionRowActions** | session, capabilities, loadingKey | read, run(action), fetch, edit-opus, remove-opus, retry | ⚠️ 复刻**表 A**:recap_done 列表只"阅读"(不显示 upload);published 显示编辑/删除;local_available=false 显示取回 |
| **RecapDrawer** | session, content, loading, capabilities, isExpert | copy, run(action), partial-range, add-term | — |
| **RecapDrawerActions** | session, capabilities | run(action) | ⚠️ 复刻**表 B**:recap_done 抽屉显示 upload;uploaded 显示 publish;**published 不显示 edit/remove** |
| SuggestedTermsPanel | terms, addedTerms, loadingTerm | add(term) | 建议术语 |
| RecapPartialGenerateForm | v-model:start, v-model:end | submit | 自定义时间段 |

---

## 4. 状态机收敛(核心)

**关键决策:不粗暴合并两套动作入口,而是用显式服务表达差异。**

新增 `features/recaps/sessionActions.ts`。

**Action 类型(必须显式定义,避免回归)**:

> ⚠️ **不复用 `utils/lifecycle.ts` 的 `SessionActionName`**——它只含 `stop_record/submit_asr/generate_recap/upload/fetch/publish` 6 个,而 UI 层有额外的 `edit_opus/remove_opus/retry`。这里新建独立的 UI 动作类型。**注意:这不是 lifecycle 的超集**——`stop_record` 属首页直播卡(不进回顾页两入口),故 UIActionName **不含** `stop_record`;它只覆盖回顾页「列表行 + 抽屉」的 UI 动作。

```ts
// UI 层动作标识(覆盖回顾页列表行+抽屉全部动作,不含 stop_record——它属首页)
export type UIActionName =
  | 'submit_asr' | 'generate_recap' | 'upload' | 'publish'  // 主动作(经 lifecycle)
  | 'fetch'                                                  // 取回(local_available)
  | 'retry'                                                  // 重试(failed,基于 current_task_id)
  | 'edit_opus' | 'remove_opus'                              // published 专栏管理

export interface SessionAction {
  name: UIActionName
  label: string                    // 「提交 ASR」「编辑专栏」
  disabled: boolean
  disabledReason: string           // 见下方 UI_ACTION_REASON
  confirmText: string
  handler: (session: Session) => Promise<void>   // 传 session 而非 sid;retry 内部读 current_task_id
}

// UI 动作禁用文案(独立于 lifecycle.ACTION_DISABLED_REASON,后者只覆盖 lifecycle 6 个动作)
// 主动作复用 lifecycle 文案;edit_opus/remove_opus 复用 publish 文案;retry 走任务状态文案
export const UI_ACTION_REASON: Record<UIActionName, string> = {
  submit_asr: 'ASR 能力不可用，请检查 DashScope API Key 与 ASR 配置',
  generate_recap: '回顾生成能力不可用，请检查 AI 回顾配置',
  upload: 'WebDAV 上传能力不可用，请检查 WebDAV 配置',
  publish: '发布能力不可用，请检查发布配置与 Cookie',
  fetch: '',                                    // 取回无能力门槛
  retry: '无可重试任务',                          // 无 current_task_id / 任务不存在 / 非 failed 时
  edit_opus: '发布能力不可用，请检查发布配置与 Cookie',  // 复用 publish
  remove_opus: '发布能力不可用，请检查发布配置与 Cookie',
}
```

**导出函数(按 UI 入口分,复刻 §2.3.1 表A/B 的 UI 语义,而非 lifecycle 当唯一真相)**:

```ts
// 列表行(§2.3.1 表A):recap_done→只读阅读;published→edit/remove;local_available=false→fetch 独立按钮;failed→retry
// retry 的显隐依赖 currentTask 状态,故需传入;其它动作只用 session+capabilities
getRowActions(
  session: Session,
  capabilities: Capabilities | null,
  currentTask?: Task,            // 来自 tasksStore 按 session.current_task_id 查;retry 决策用它(§7.1)
): { primary?: SessionAction; edit?: SessionAction; remove?: SessionAction; fetch?: SessionAction; retry?: SessionAction; read?: boolean }

// 抽屉(§2.3.1 表B):recap_done→upload;uploaded→publish;published→无(isPrimaryAction 不含 fetch)
getDrawerActions(session: Session, capabilities: Capabilities | null): { primary?: SessionAction }

canFetchLocal(session: Session): boolean
```

**优先级规则(必须显式,按 §2.3.1)**:
1. `local_available === false` 且动作需读本地 → disabled,reason「本地已清理,请先取回」(`generate_recap`/`publish`)
2. `capabilities === null`(未加载)→ disabled,reason「运行时能力未加载,请稍后重试」(复刻 `getDisabledReason`);`capabilities` 存在但某能力 false → reason 取 `capabilities.reason || UI_ACTION_REASON[action.name]`(**注意用 UI_ACTION_REASON,不是 lifecycle 的 ACTION_DISABLED_REASON**——后者只覆盖 6 个 lifecycle 动作,不含 edit_opus/remove_opus/retry)
3. `published && !publish_target` → 边界:published 分支但不显示 edit/remove(无 dyn_id 无可编辑对象)
4. `failed` → 仅 retry;retry 决策见 §7.1

**测试矩阵(阶段 2 必须产出单测)**:把 §2.3.1 表A/表B 落成 `sessionActions.test.ts`,每个 status × {local_available, capabilities, publish_target} 组合断言渲染的动作集合。这是防止回归的硬保障。

> ⚠️ **不把 utils/lifecycle.ts 当唯一真相**:它给 `published` 返回 `fetch`(§2.3.1 已知问题②),但 UI 遮蔽它。新服务复刻的是 **UI 入口语义**,lifecycle 只作为能力判断的底层调用之一。

---

## 5. 状态管理重构(参考 §4.6)

**不合并 5 个 store**(边界清晰,合并反增耦合)。目标:实体 store + 刷新协调器。

| store | 改动 |
|-------|------|
| sessions | 加 `loaded`/`byId`/`ensureLoaded()`(**复用 inflight promise 防并发重复 list**)/`getByIdAfterLoad(id)` → 解决 §4.5 `?sid=` 竞态;加可选 `detailById`/`recapContentById` 缓存;加 `refreshAfterTaskSubmit()`(内部刷 tasks+sessions) |
| channels | 加 `loaded`/`byId`/`ensureLoaded()`(同样 inflight 去重)→ 解决 `?id=` 竞态;`toInput` 迁移为 `toUpsertChannelInput(channel): UpsertChannelInput`(**用类型约束防字段漂移**,§2.4.1 全 28 字段) |
| tasks | 保留 WS `task_progress` 消费;**未知任务全量刷新兜底保留**(§4.6);tasks 刷新 ownership 交给 coordinator(§7.2);**终态刷 sessions 也由 coordinator 统一触发**(不在 tasksStore 内部触发,避免双刷) |
| runtime | 保留 30s 节流 + `force=true`;设置保存后必须 force 刷新(§2.5.1) |
| liveStatus | 保留 statusMap;首页轮询继续(后端无 live WS 事件) |

**`ensureLoaded()` 并发去重(关键)**:

```ts
let inflight: Promise<void> | null = null
async function ensureLoaded(): Promise<void> {
  if (loaded.value) return
  if (inflight) return inflight      // 多页面/多组件同时进,复用同一个请求
  inflight = fetchSessions().finally(() => { inflight = null })
  return inflight
}
```

`getByIdAfterLoad(id)` = `await ensureLoaded()` 后 `byId.get(id)`,供 query 消费用。

**WS + 轮询统一**(新增 `composables/useAppRefreshCoordinator`,ownership 见 §7.2):
- WS connected:任务增量更新,终态后刷 tasks+sessions
- WS disconnected:**降级轮询** tasks+sessions(coordinator 负责)
- Home 激活:**只轮询 liveStatus**(tasks 交给 coordinator,避免双刷新)
- **不假装 WS 能实时 session/live**(后端只推 task_progress,§4.6)

---

## 6. 分阶段迁移路径(每阶段可独立合并)

> **阶段顺序原则**:基础设施(store loaded / ensureLoaded)先行,状态机服务次之,view 拆解在后,刷新协调器最后。query 竞态修复**只在阶段 1**(引入 ensureLoaded 时一并治),不分散到多个阶段。

| 阶段 | 目标 | 改动 | 风险 | 验证 |
|------|------|------|------|------|
| **1. 基础设施 + 修功能坑** | store loaded/ensureLoaded + 清死代码 + 修 bug | sessions/channels store 加 `loaded`/`byId`/`ensureLoaded()`(带 inflight 去重);BiliQR 用点改 `@saved`;重试接 `retryTask`(见 §7);删 SessionActions;**query 用 ensureLoaded 后消费**(`?sid`/`?id`);孤儿 wrapper 登记决策 | 低-中 | `?sid`/`?id` 稳定打开(竞态消除);扫码后列表刷新;失败重试有明确行为;并发进页面不重复 list |
| **2. 抽状态机服务** | sessionActions.ts | 新增 features/recaps/sessionActions.ts(契约见 §4);调整 RecapsView 调用 | 中 | 按 §2.3.1 表A/B 逐状态单测;local_available 取回仍在 |
| **3. 拆 RecapsView**(试点) | 5+ 子组件 | RecapsView.vue + features/recaps/components/* | 中高 | 发现/导入/下载/阅读/部分重生成/术语/发布编辑删除全走通 |
| **4. 拆 SettingsView** | 8 section | SettingsView.vue + features/settings/components/* | 高 | 按 §2.5.1 逐字段对照 payload;保存后 runtime 强刷;showAdvanced≠isExpert |
| **5. 收敛自管理组件** | 4 个组件状态机迁移 | features/onboarding/bili-login/glossary/recap-template | 中 | 保留 §4.4 的 showGlobalReadonly/__builtin__/2s 轮询/4步流程;**有回归用例** |
| **6. 刷新协调器** | useAppRefreshCoordinator | AppLayout + tasksStore | 中 | tasks 刷新 ownership 唯一;断线降级轮询;终态刷 sessions |

**试点选阶段 3(RecapsView)**:状态机最复杂、两套入口、还带已做的视觉改动——拆它能最早暴露所有重构难点,验证策略可行性。

**阶段独立性说明**:
- 阶段 1 的 ensureLoaded 是后续所有阶段的基础,必须最先。
- 阶段 2 的 sessionActions.ts 是阶段 3 拆 RecapsView 的前置(否则拆出来的子组件还得再改一次动作逻辑)。
- 阶段 6 可独立,但建议在 3-5 之后(那时各 view 已就位,刷新边界更清晰)。

---

## 7. 必须先修的坑(基线揭示)

**重构中必须处理**:
- 重试死按钮(§2.3.1①)→ 阶段1:接 `retryTask`(决策边界见 §7.1)
- BiliQR 事件错配(§5.3)→ 阶段1:StreamersView 监听 `saved`,设置页账号模式监听 `saved-account`
- SessionActions 死代码(§5.3)→ 阶段1:删除(不保留第二套状态机)
- 15 个孤儿 wrapper(§5.2-B)→ 阶段1:登记决策(保留/删除),尤其 retryTask/updateRecapContent/deleteSession/getTask
- query 竞态(§4.5)→ **阶段1**:引入 ensureLoaded 时一并治(不再放阶段6)
- 分层未遵守(§1.2/§4.4)→ 阶段2-5 渐进

**可暂缓**(后端预留能力,无当前需求):
- glossary candidates、diagnostic、notify/test、stats overview/cost(§5.2-A)
- updateRecapContent 编辑 UI(可作为后续功能,不混入架构阶段)
- recap-with-range 死路由、cookie-accounts 旧接口(登记废弃即可)

### 7.1 重试决策边界(阶段 1 必须明确)

`failed` 状态的重试不能只"接 retryTask",要覆盖所有边界:

| 情况 | UI 行为 | 调用 |
|------|---------|------|
| `current_task_id` 存在 + 任务为 failed | 显示「重试」 | `retryTask(current_task_id)` → 刷新 tasks+sessions |
| 无 `current_task_id` | 按钮置灰或文案改「无任务可重试」 | 不调 |
| 任务已不存在(404) | toast「任务已过期」+ 按钮隐藏 | 不调 |
| 任务非 failed(已成功/取消) | 按钮隐藏 | 不调 |
| retry 调用失败 | toast 错误(走 client 拦截器) | — |

**刷新顺序**:retry 成功 → 先 `tasksStore.fetchTasks()`(拿新 task 状态)→ 再 `sessionsStore.fetchSessions()`(状态会随任务推进变化)。

### 7.2 刷新协调器 ownership(阶段 6 必须唯一化)

当前 tasks 刷新散落三处(双刷新风险):AppLayout WS 推送(`:42`)、Home 30s 轮询(`:189`)、各动作后手动刷(`runQuickAction:193`)。

**唯一 owner 规则(避免双刷新)**:

| 刷新源 | owner | 说明 |
|--------|-------|------|
| WS `task_progress` | **coordinator** | 增量更新 + 终态触发 sessions 刷;AppLayout 只转发事件,不直接刷 |
| 轮询(WS 断线降级) | **coordinator** | 断线时启动 tasks+sessions 轮询;连接恢复即停 |
| Home 激活轮询 | **只刷 liveStatus** | Home 不再轮询 tasks(交给 coordinator) |
| 动作后刷新 | **store 方法** | `sessionsStore.refreshAfterTaskSubmit()` 内部刷 tasks+sessions,各 action handler 调它 |

> `useAppRefreshCoordinator` 在 AppLayout 挂载,统一管理 tasks 刷新。Home 的 `usePolling` 只保留 `liveStatusStore.fetchAll()`(后端无 live WS 事件,§4.6)。

---

## 8. 不做什么(范围控制)

- ❌ 不换 Vue/Pinia/Element Plus
- ❌ 不改后端 API 路径、方法、payload(基线红线)
- ❌ 不改 4 主路由 + 9 旧路径重定向
- ❌ 不改认证机制(X-Admin-Token、401 补登)
- ❌ 不扩展 WS 成 session/live 实时通道(除非后端加事件)
- ❌ 不重做视觉设计系统、不引入 Tailwind/NaiveUI/AntD Vue
- ❌ 不把业务状态塞进单一全局 store
- ❌ 不在重构中顺手实现大功能(候选词审批、诊断报告、完整回顾编辑器)
- ❌ 不改 PublishConfig/RecapConfig/WebDAVConfig 整对象 PUT 语义
- ❌ 不合并 showAdvanced 进 isExpert
- ❌ 不重写 utils/lifecycle.ts 决策核心(只包装)
- ❌ **不改 `api/client.ts` 的 401 补登 / 错误 toast 行为**(它确有 UI 副作用,但这是全局横切机制 §1.4;"api 无 UI 副作用"指新增的 wrapper,不是改写现有 client)
- ❌ **不顺手改 Element Plus 全量图标注册**(main.ts:16-18)——改按需引入会触发大量裸 `<el-icon><Xxx/></el-icon>` 找不到,属独立任务,不混入本次重构

---

## 9. 双方案对照说明

本方案由两份独立分析整合:
- **ZCode 主理**:侧重选型论证、不做什么、风险判断
- **codex 独立方案**:侧重分层目录、props/emit 契约、sessionActions.ts 显式服务、ensureLoaded 竞态解法、useAppRefreshCoordinator

两份在所有关键决策上**完全收敛**(选型/分层/胖view/状态机/状态管理/试点/必修坑均一致),codex 在子组件契约和刷新协调器上更细致,已采纳整合。这种独立视角收敛比任何单方面意见可信度更高。
