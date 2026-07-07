## 审核结论: NEEDS_CHANGES

### 摘要
Phase 5 设置页 V10 重写在**架构层面完整、实现质量高**，但存在 **1 个 Blocking 问题**（组件计数偏差）和若干非阻塞性改进点。代码已通过类型检查、149 个测试、构建验证，三态密钥、Glossary 端点、QR 编排等关键设计均正确落地。

### 逐项审核（1-10，带证据）

#### 1. 14 个 V10 卡完整 + 契约 ⚠️
**发现偏差**：
- **实际文件数**：15 个 `.vue` 文件（`ls` 输出）
- **业务卡**：12 个 `*CardV10.vue` + `OverviewCard.vue` = **13 张卡**
- **非卡组件**：`Sidebar.vue` + `PipelineBar.vue` = 2 个布局组件
- **计划承诺**：14 个卡（但 Sidebar/PipelineBar 为布局，不计入"卡"）

**证据**：
```bash
web/src/features/settings/components-v10/AccountsCardV10.vue
web/src/features/settings/components-v10/AdminTokenCardV10.vue
web/src/features/settings/components-v10/ArchiveCardV10.vue
web/src/features/settings/components-v10/ASRS3CardV10.vue
web/src/features/settings/components-v10/BackupCardV10.vue
web/src/features/settings/components-v10/DashScopeCardV10.vue
web/src/features/settings/components-v10/GlossaryCardV10.vue
web/src/features/settings/components-v10/OverviewCard.vue
web/src/features/settings/components-v10/PipelineBar.vue      # 布局组件
web/src/features/settings/components-v10/PublishCardV10.vue
web/src/features/settings/components-v10/RecapCardV10.vue
web/src/features/settings/components-v10/Sidebar.vue          # 布局组件
web/src/features/settings/components-v10/TemplateCardV10.vue
web/src/features/settings/components-v10/ToolsCardV10.vue
web/src/features/settings/components-v10/WebDAVCardV10.vue
```

**结论**：若计划"14 卡"指**业务卡**，则实为 13 张（12 个 `*CardV10` + OverviewCard）；若含布局则 15 个组件。需明确计数口径。

**HCard 包裹验证**：✅ 所有业务卡（12 个 `*CardV10.vue` + OverviewCard）均用 `<HCard>` 包裹（`grep "<HCard"` 命中 12 次，OverviewCard 单独验证已确认）。

**H* 组件使用**：✅ 168 次引用 `HCard/HInput/HButton/HSelect/HSwitch`（`web/src/features/settings/components-v10/*.vue`），所有卡已完成 EP → V10 UI 替换。

**props/emit 契约**：✅ 抽查验证：
- `DashScopeCardV10.vue:20` — `emit('saved')`
- `AccountsCardV10.vue:24-31` — 6 个 emit（generate-qr/poll/save-qr/set-default/delete/reload）
- `GlossaryCardV10.vue:22` — `emit('saved')`

---

#### 2. 三态密钥模式（DashScope/ASRS3/Recap/WebDAV 4 卡） ✅
**DashScope**（`DashScopeCardV10.vue`）：
- **L91-93**：`api_key_set` → `HPill` 显示"密钥已保存"/"密钥未保存"
- **L119-128**：输入框 + masked 标签 + `HCheckbox clearKey`（L128）
- **L69-70**：payload 带 `clear_key: clearKey.value`

**ASRS3**（`ASRS3CardV10.vue:29-32,48`）：
- **L69-71**：`access_key_set` → `HPill` 显示"密钥已保存"/"未配置"
- **L29-30**：`accessKeySecret` ref + `clearSecret` ref
- **L48**：payload 带 `clear_secret: clearSecret.value`

**Recap**（`RecapCardV10.vue:36-37,127`）：
- **L122-124**：`api_key_set` → `HPill` 显示"已配置"/"未配置"
- **L127**：`HCheckbox clearKey`（✅ **gap-analysis 标注 EP 缺失的补项**）
- **L76-77**：payload 带 `clear_key: clearKey.value`

**WebDAV**（`WebDAVCardV10.vue:32,49`）：
- **L72**：`password_set` → `HPill` 显示"密码已保存"
- **L32**：`clearPassword` ref（与 EP 版对齐）
- **L49**：payload 带 `clear_password: clearPassword.value`

**结论**：✅ 4 卡三态密钥模式完整，Recap 卡 `clear_key` checkbox 已补齐（审核 v1 遗漏点）。

---

#### 3. Glossary 卡端点完整性 ✅
**GlossaryCardV10.vue 实现的端点**（L18-19,99-106,117-125,154-167,189-194）：
- ✅ **CRUD**：`addEntry`（L68）/ `deleteEntry`（L82-83）/ `toggleEntry`（L88）/ `fetchData`（L26）
- ✅ **候选审批**：`listGlobalCandidates`（L149）/ `approveGlobalCandidate`（L156）/ `rejectGlobalCandidate`（L164）
- ✅ **批量删除**：`batchDelete`（L99）
- ✅ **批量切换**：`batchToggle`（L102-105）
- ✅ **导入导出**：`importEntries`（L120,136）/ `exportJSON`（L27,212）
- ✅ **备注编辑**：`getGlobalNote`（L178）/ `updateGlobalNote`（L190）

**候选端点 client wrapper**（`git diff main..HEAD -- web/src/api/glossary.ts`）：
```typescript
+export function listGlobalCandidates(status?: 'pending' | 'approved' | 'rejected' | 'all'): Promise<{ items: GlossaryCandidate[] }>
+export function approveGlobalCandidate(cid: number, term?: string): Promise<GlossaryCandidate>
+export function rejectGlobalCandidate(cid: number): Promise<GlossaryCandidate>
```
✅ 3 个候选端点已加入 `glossary.ts`（L106-117），类型安全（`GlossaryCandidate` 从 `types.ts` 导入）。

**结论**：✅ Glossary 卡端点完整，候选审批 UI（L282-296）+ API 层均已实现。

---

#### 4. SettingsView 壳重写 ✅
**代码行数**：`wc -l` → **322 行**（从旧版 436 行减少 26%）

**布局**：
- **L235-236**：`<Sidebar>` + `<main class="settings-content">`（左右分栏）
- **L302-315**：`.settings-v10 { display: flex }` + `.settings-content { flex: 1 }`
- **L7**：`import '@/features/settings/components-v10/settings-v10.css'`（共享样式）

**4 分组 + 14 卡编排**（L60-77,241-294）：
```typescript
// Sidebar sections（4 分组）
{ id: 'overview', group: '总览' }
{ id: 'dashscope'...,'recap'...,'webdav'...,'publish'...,'archive'...,'template'...,'glossary', group: '流水线配置' }
{ id: 'accounts'...,'admin-token'...,'backup', group: '账号与备份' }
{ id: 'tools', group: '高级' }
```

**data-section 锚点**（L241-294）：每个 `<section data-section="xxx">` 对应 sidebar id。

**?section 消费**（L224-231）：
```typescript
watch(() => route.query.section, (section) => {
  if (section) scrollToSection(String(section))
}, { immediate: true })
```

**scrollIntoView**（L84-91）：
```typescript
function scrollToSection(id: string) {
  activeSection.value = id
  if (id === 'asr_backend') { showASRBackendHint(); return }
  document.querySelector(`[data-section="${id}"]`)?.scrollIntoView({ behavior: 'smooth' })
}
```

**onSaved → fetchRuntime(true)**（L191-193）：所有配置卡 `@saved="onSaved"`，触发 runtime 刷新。

**onImported → reloadKey re-mount + fetchRuntime**（L199-203）：
```typescript
async function onImported() {
  await runtimeStore.fetchRuntime(true)
  await fetchAccounts()
  reloadKey.value++  // 强制重挂载所有 :key="`xxx-${reloadKey}`" 卡
}
```

**结论**：✅ SettingsView 壳重写完整，行数减少 26%，所有计划特性已实现。

---

#### 5. AccountsCardV10 QR 编排 ✅
**设计决策说明**（`SettingsView.vue:102-105`）：
> 不复用 useBiliQRCodeLogin:其为 dialog 驱动(watch visible→startLogin + onSessionReady canvas 回调),
> 与 AccountsCardV10 的「自带 canvas + emit generate-qr/poll/save-qr」契约不匹配,故在此用 bili API +
> usePolling 复刻状态机(session/pollResult/2s 轮询/账号保存)。逻辑等价,接口更贴合受控组件。

**壳持有状态机**（L106-155）：
- **L106-109**：`qrSession` / `pollResult` / `qrLoading` / `qrSaving` refs
- **L111-114**：`usePolling(() => pollQR(), { interval: 2000 })`（2s 轮询）
- **L116-128**：`generateQR` → `createQRCodeSession` + `startPollTimer`
- **L130-139**：`pollQR` → `pollQRCodeSession`（过期检查）
- **L141-155**：`saveQR` → `saveQRCodeToAccount` + `stopPollTimer` + 清理

**传给 AccountsCardV10**（L271-283）：
```vue
<AccountsCardV10
  :accounts="accounts"
  :qr-session="qrSession"
  :poll-result="pollResult"
  :qr-loading="qrLoading"
  :qr-saving="qrSaving"
  @generate-qr="generateQR"
  @poll="pollQR"
  @save-qr="saveQR"
  ...
/>
```

**AccountsCardV10 受控展示**（L45-52,65-70）：
- **L45-52**：`watch(() => props.qrSession?.url)` → `QRCode.toCanvas(canvasRef.value, url)`
- **L54-63**：倒计时 computed（从 `expires_at` 计算）
- **L65**：轮询状态文本（`pollResult?.message || '等待扫码…'`）
- **L67-70**：保存按钮 → `emit('save-qr', nickname.value.trim())`

**unmount 清理**（L181-188）：
```typescript
onBeforeUnmount(() => {
  stopPollTimer()
  if (qrSession.value && pollResult.value?.status !== 'succeeded') {
    cancelQRCodeSession(qrSession.value.session_id).catch(() => {})
  }
})
```

**结论**：✅ QR 编排内联到壳属于**合理设计决策**——`useBiliQRCodeLogin` 为 dialog-coupled（耦合弹窗 visible），与受控卡组件的 emit-driven 契约不兼容。壳复刻状态机（session/poll/2s timer/save）+ 传给卡展示，逻辑清晰、清理完备。**非计划偏离**（实为适配决策）。

---

#### 6. useSettingsOverview composable 提取 ✅
**提取范围**（`useSettingsOverview.ts:1-10`）：
> 从旧 SettingsView.vue 行 55-113 抽出:合并 capabilities + config_status,
> 产出 4 个能力卡(asr/recap/webdav/publish)的 done 状态 + 原因 + 跳转目标。

**接口**（L32-35,96-100）：
```typescript
export function useSettingsOverview(
  capabilities: MaybeRefOrGetter<Capabilities | null>,
  configStatus: MaybeRefOrGetter<ConfigStatus | null>,
) {
  return { overviewItems, overviewDoneCount, capReason }
}
```

**overviewItems 派生**（L45-92）：
- **L50-60**：ASR（`asr_submit` + `dashscope_key_set` → done/reason/actionTarget）
- **L61-70**：Recap（`recap_generate` + `recap_key_set`）
- **L71-80**：WebDAV（`webdav_upload` + `webdav_configured`）
- **L81-90**：Publish（`publish_opus` + `publish_enabled`）

**复用点**：
- ✅ `OverviewCard.vue:19-22` — `useSettingsOverview(() => props.capabilities, () => props.configStatus)`
- ✅ `SettingsView.vue` 未直接使用（壳仅传 props 给 OverviewCard），但设计预留共用能力

**capReason 逻辑保留**（L36-43）：
```typescript
if (key === 'asr_submit') return cs?.dashscope_key_set ? caps.reason : 'ASR 密钥未配置'
if (key === 'recap_generate') return cs?.recap_key_set ? caps.reason : 'AI 密钥未配置'
...
```

**结论**：✅ composable 正确提取，OverviewCard 已复用，设计为后续 shell 直接使用预留可能性。

---

#### 7. 类型安全 + API ✅
**API 调用名一致性**（抽查）：
- `DashScopeCardV10.vue:16` — `getDashScopeConfig, updateDashScopeConfig` ✅ 存在于 `@/api/settings`
- `GlossaryCardV10.vue:18-19` — `listGlobalCandidates, approveGlobalCandidate, rejectGlobalCandidate` ✅ 存在于 `@/api/glossary`（新增）
- `AccountsCardV10.vue` — 壳调用 `listBiliAccounts, deleteBiliAccount, updateBiliAccount` ✅ 存在于 `@/api/bili`

**types-derived 派生类型**（`web/src/api/types.ts` 已含 `DashScopeConfig/ASRS3Config/RecapConfig/WebDAVConfig`）：
- ✅ 各卡 import `type { XXXConfig } from '@/api/types'`（L18,14,18,14）

**glossary.ts 新增类型**（`git diff` L2）：
```typescript
-import type { GlossaryEntry, GlossaryNote } from './types'
+import type { GlossaryEntry, GlossaryNote, GlossaryCandidate } from './types'
```
✅ `GlossaryCandidate` 类型从 `types.ts` 导入，wrapper 函数签名正确（L106-117）。

**结论**：✅ 类型安全，API 调用名与实际 export 一致，新增 glossary candidate wrapper 类型正确。

---

#### 8. EP 共存 + 无回归 ✅
**main.ts EP 保留**（`grep` 输出）：
```typescript
import ElementPlus from 'element-plus'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
```
✅ ElementPlus 全局注册未移除。

**既有 EP 卡未删**（`ls web/src/features/settings/components/*.vue`）：
```
AdminTokenCard.vue
ArchiveSettingsCard.vue
ASRS3SettingsCard.vue
BiliAccountsCard.vue
ConfigBackupCard.vue
DashScopeSettingsCard.vue
PublishSettingsCard.vue
RecapSettingsCard.vue
WebDAVSettingsCard.vue
```
✅ 9 个旧 EP 卡保留（按计划 Phase 6 才删）。

**ElMessage 保留**（抽查 `DashScopeCardV10.vue:14,75` / `RecapCardV10.vue:14,82`）：
```typescript
import { ElMessage } from 'element-plus'
ElMessage.success('ASR 设置已保存')
```
✅ Toast 消息仍用 `ElMessage`（V10 UI 库尚无 Toast 组件）。

**测试全过**：
```
Test Files  23 passed (23)
Tests  149 passed (149)
```
✅ 无回归（测试计数与 Phase 4 一致）。

**结论**：✅ EP 共存良好，既有 EP 卡未删（Phase 6 计划），测试无回归。

---

#### 9. 验证命令 ✅
| 命令 | 结果 | 状态 |
|------|------|------|
| `npm --prefix web run type-check` | `vue-tsc -b` PASS（无输出 = 通过） | ✅ |
| `npm --prefix web test` | `149 passed (149)` | ✅ |
| `npm --prefix web run build` | `✓ built in 8.63s`（warning 仅 chunk size） | ✅ |
| `ls web/src/features/settings/components-v10/*.vue \| wc -l` | `15`（13 卡 + 2 布局） | ⚠️ 见维度 1 |

**结论**：✅ 所有验证命令通过，仅组件计数需明确口径。

---

#### 10. 计划偏离 ✅/⚠️
**implementer 标注的 3 项偏离**：
1. ✅ **AccountsCardV10 QR 编排内联到 shell**（已审核，合理设计决策）
2. ✅ **glossary.ts 加 candidate wrapper**（后端有端点但前端缺 client，合理补全）
3. ✅ **settings-v10.css 共享结构 CSS + .settings-v10 wrapper**（合理，避免重复样式）

**额外偏离（未列在 implementer 标注）**：
- ⚠️ **组件计数偏差**：计划"14 卡"实为 **13 张卡**（12 个 `*CardV10` + OverviewCard）或 **15 个组件**（含 Sidebar/PipelineBar 布局）。需明确计划"14 卡"的定义（是否含布局组件）。

**结论**：✅ 3 项已知偏离均合理；⚠️ 组件计数需澄清（Blocking 问题）。

---

### Blocking 问题

#### 🔴 #1 组件计数偏差（维度 1）
**问题**：计划承诺"14 个 V10 卡"，实际：
- **业务卡**：12 个 `*CardV10.vue` + 1 个 `OverviewCard.vue` = **13 张卡**
- **布局组件**：`Sidebar.vue` + `PipelineBar.vue` = **2 个非卡组件**
- **总计**：15 个 `.vue` 文件

**根因**：计划未明确"14 卡"是否包含 Sidebar/PipelineBar 布局组件。

**要求**：
1. 若"14 卡"指**业务卡**：补充 1 张卡（或说明为何从 14 减为 13）。
2. 若"14 卡"含**布局组件**：更新计划为"13 卡 + 2 布局组件"，或解释为何 15 个文件对应"14 卡"。

**影响**：文档与实际不一致，影响后续维护理解。

---

### 建议（非阻塞）

#### 💡 #1 统一 `reload()` ref 调用（可选优化）
**现状**：`SettingsView.vue:196-202` 用 `reloadKey++` 强制重挂所有配置卡：
```typescript
async function onImported() {
  await runtimeStore.fetchRuntime(true)
  await fetchAccounts()
  reloadKey.value++  // 强制重挂载所有 :key="`xxx-${reloadKey}`" 卡
}
```
**注释说明**：比逐卡调用 `reload()` ref 更可靠（无需为每卡维护模板 ref）。

**建议**：现有方案已达成可靠性目标，保持即可。若后续需单卡 reload（如仅刷新 Glossary），可考虑用 `defineExpose({ reload })` + `ref()` 混合模式。

---

#### 💡 #2 ASR 后端 hint 改为弹窗内按钮跳转（UX 改进）
**现状**：`showASRBackendHint()`（L206-214）弹长文本 `ElMessageBox.alert`（200+ 字配置说明），用户关闭后无直达配置卡入口。

**建议**：弹窗底部加"前往配置 ASR S3"按钮（`confirmButtonText` 改为"前往配置"，点击后 `scrollToSection('asr-s3')`），减少用户手动滚动成本。

---

#### 💡 #3 补充 V10 组件单元测试（质量改进）
**现状**：所有 V10 卡标注"L3 视觉验证,无单测"，依赖人工验证。

**风险**：配置卡逻辑复杂（三态密钥、高级参数折叠、批量操作），无测试覆盖易在重构时引入回归。

**建议**：Phase 6 后补充关键卡的单测（如 Glossary 批量操作、Recap 三态密钥、AccountsCardV10 QR 状态机），复用 `vitest` + `@vue/test-utils`。

---

#### 💡 #4 Glossary 候选审批 UI 加载状态（细节打磨）
**现状**：`GlossaryCardV10.vue:154-160` 的 `handleApprove` 无 loading 状态，点击后需等待后端响应。

**建议**：加 `approvingIds` ref（`Set<number>`），审批中禁用按钮并显示 loading：
```vue
<HButton :loading="approvingIds.has(c.id)" @click="handleApprove(c)">加入术语表</HButton>
```

---

### 验证命令结果
```bash
# 类型检查
npm --prefix web run type-check
✅ PASS（vue-tsc -b 无输出）

# 单元测试
npm --prefix web test
✅ Test Files 23 passed (23)
✅ Tests 149 passed (149)

# 构建
npm --prefix web run build
✅ built in 8.63s（仅 chunk size warning，非错误）

# 组件计数
ls web/src/features/settings/components-v10/*.vue | wc -l
⚠️ 15 个文件（13 卡 + 2 布局）≠ 计划的"14 卡"
```

---

## 总结
Phase 5 改动**架构设计优秀**（sidebar/content 分栏 + 三态密钥 + 受控 QR 编排 + composable 抽取），代码质量高（类型安全、测试全过、无回归），但需**澄清组件计数偏差**（13 卡 vs 14 卡承诺）。解决 Blocking 问题后可 APPROVED。

---

## 控制者裁定(对 Claude "Blocking #1 组件计数"的回应)

**判定:非 blocking,实质 APPROVED。**

Claude 的"blocking"基于控制者派发 prompt 中"14 个 V10 settings components"的笔误,与计划无关。核查计划原文:
- Line 2666:「**本页文件最多(15 个子组件)**」
- Line 2799:「**15 个 section** 按 sidebar 分组顺序排列」
- Line 2814/2916 commit msg 模板:「sidebar + **15 卡**」
- File Structure 清单(line 147-163):列 **15 个 .vue**(Sidebar + PipelineBar + OverviewCard + 12 *CardV10)

实际实现 `ls components-v10/*.vue | wc -l` = **15**,与计划完全一致。

Claude 的 4 条建议(reload ref 统一 / ASR hint 加跳转按钮 / 补单测 / Glossary 审批 loading)均为非阻塞细节优化,记入 Phase 6 cleanup 候选,不阻塞合并。

**Phase 5 实质 APPROVED,合并到 main。**
