<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { HMessage } from '@/components/ui/message'
import { HConfirm } from '@/components/ui/HConfirm'
import { useSessionsStore } from '@/stores/sessions'
import { useChannelsStore } from '@/stores/channels'
import { useRuntimeStore } from '@/stores/runtime'
import { useTasksStore } from '@/stores/tasks'
import { useExpertMode } from '@/composables/useExpertMode'
import { statusGroupMap } from '@/utils/friendlyStatus'
import {
  decideRetry,
  type PrimaryAction,
  type PrimaryActionName,
} from '@/features/recaps/sessionActions'
// V10 组件(Phase 4):替代旧 EP 版 RecapToolbar/SessionFilters/SessionTable/RecapDrawer/DiscoverResultDrawer。
import RecapToolbarV10 from '@/features/recaps/components/RecapToolbarV10.vue'
import SessionFiltersV10 from '@/features/recaps/components/SessionFiltersV10.vue'
import SessionTableV10 from '@/features/recaps/components/SessionTableV10.vue'
import RecapDrawerV10 from '@/features/recaps/components/RecapDrawerV10.vue'
import DiscoverPreviewDrawer from '@/features/recaps/components/DiscoverPreviewDrawer.vue'
// EP 抽屉(Phase 6 才迁移):导入 + 下载仍是 el-drawer 实现。
import ImportSessionDrawer from '@/components/session/ImportSessionDrawer.vue'
import DownloadByURLDrawer from '@/components/session/DownloadByURLDrawer.vue'
import {
  fetchSession,
  generateRecap,
  generateRecapWithRange,
  publishSession,
  regenerateRecap,
  submitASR,
  uploadSession,
  getRecapContent,
  // 发现回放(2026-07-19 解耦重写:URL 驱动入口)。
  previewDiscoverSessionsByURL,
  executeDiscoverSessions,
  deleteFailedSessions,
} from '@/api/sessions'
import { retryTask } from '@/api/tasks'
import { upsertChannelEntry } from '@/api/glossary'
// V10 组件 + DiscoverPreviewDrawer 消费 generated 派生类型(optional 字段);stores/API/状态机仍消费
// 旧手写 types.ts(全必填)。两者 TS 层不完全兼容(Phase 6 统一迁移),在 view 边界用 as unknown as 窄化,
// 与已迁移的 HomeView/StreamersView 一致;状态机调用边界窄化转换与 V10 组件内部一致(见组件头注释)。
import type {
  Capabilities as DerivedCapabilities,
  Channel as DerivedChannel,
  DiscoverResult as DerivedDiscoverResult,
  DiscoverPickItem as DerivedDiscoverPickItem,
  RecapContent as DerivedRecapContent,
  Session as DerivedSession,
} from '@/api/types-derived'
import type {
  DiscoverPickItem,
  Session,
  Task,
} from '@/api/types-derived'

const router = useRouter()
const route = useRoute()
const sessionsStore = useSessionsStore()
const channelsStore = useChannelsStore()
const runtimeStore = useRuntimeStore()
const tasksStore = useTasksStore()
const { isExpert } = useExpertMode()

// ---------- 过滤状态(传给 SessionFiltersV10) ----------
const keyword = ref('')
const statusFilter = ref<'all' | 'processing' | 'recap' | 'published' | 'failed'>('all')
const channelFilter = ref('')

// ---------- 子 tab:录播(live_record) / 回放(download + import) ----------
type RecapTab = 'live' | 'replay'
const REPLAY_TYPES = ['download', 'import']
const activeTab = ref<RecapTab>('live')

// ---------- 工具栏相关 ----------
const importDrawerVisible = ref(false)
const downloadDrawerVisible = ref(false)

// ---------- 发现回放两步式(壳编排;DiscoverPreviewDrawer 仅展示 items/executing) ----------
const discoverDrawerVisible = ref(false)
const discoverItems = ref<DerivedDiscoverResult[]>([])
const discoverExecuting = ref(false)
const discoverLoading = ref(false)

// ---------- 动作 loading ----------
const actionLoadingId = ref('')

// ---------- 抽屉相关 ----------
// selectedSession/recapContent 喂给 RecapDrawerV10(派生类型);API 调用边界窄化为 loose。
const recapDrawerVisible = ref(false)
const selectedSession = ref<DerivedSession | null>(null)
const recapContent = ref<DerivedRecapContent | null>(null)
const recapLoading = ref(false)
const addingSuggestedTerm = ref('')
const partialLoading = ref(false)
const clearFailedLoading = ref(false)
// 抽屉内术语「已添加」标记:API 成功后才写入(避免失败时按钮误显示已添加)
const addedSuggestedTerms = ref<Set<string>>(new Set())

// capabilities 传给 V10 组件(消费派生类型),从 store 窄化转换。
const capabilities = computed<DerivedCapabilities | null>(
  () => (runtimeStore.status?.capabilities as unknown as DerivedCapabilities) ?? null,
)

// channels 传给 V10 组件(消费派生类型)。
const channels = computed<DerivedChannel[]>(() => channelsStore.items as unknown as DerivedChannel[])

// 失败场次数(RecapToolbarV10 清空失败徽标用)
const failedCount = computed(
  () => sessionsStore.items.filter((s) => s.status === 'failed').length,
)

// ---------- 过滤 + 分页(壳持有,SessionTableV10 用 v-model 双向) ----------
const currentPage = ref(1)
const pageSize = ref(20)

const filteredSessions = computed<DerivedSession[]>(() => {
  const q = keyword.value.trim().toLowerCase()
  const statuses = statusFilter.value !== 'all' ? (statusGroupMap[statusFilter.value] ?? []) : []
  // 子 tab 按 source_type 过滤:录播=live_record;回放=download+import
  const isReplayTab = activeTab.value === 'replay'

  return (sessionsStore.items as unknown as DerivedSession[])
    .filter((s) => {
      const isReplay = REPLAY_TYPES.includes(s.source_type)
      if (isReplayTab !== isReplay) return false
      if (q && !matchesKeyword(s, q)) return false
      if (channelFilter.value && s.channel_id !== channelFilter.value) return false
      if (statuses.length > 0 && !statuses.includes(s.status)) return false
      return true
    })
    .slice()
    .sort((a, b) => ts(b.created_at) - ts(a.created_at))
})

const pagedSessions = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return filteredSessions.value.slice(start, start + pageSize.value)
})

watch([keyword, statusFilter, channelFilter, activeTab], () => { currentPage.value = 1 })

// ---------- query 消费 ----------
// RecapToolbarV10 自管 tab 栏,点击 tab 通过 update:activeTab 回传;壳负责同步 ?tab query。
function changeTab(tab: RecapTab) {
  activeTab.value = tab
  const query = { ...route.query, tab }
  router.replace({ path: '/recaps', query })
}

// 初始 ?tab 读入(刷新/分享链接落到正确 tab)
watch(
  () => route.query.tab,
  (value) => {
    const next = value === 'replay' ? 'replay' : 'live'
    if (activeTab.value !== next) activeTab.value = next
  },
  { immediate: true },
)

watch(
  () => route.query.import,
  (value) => {
    if (value === '1') {
      // 导入产出 import 场次,属回放 tab
      if (activeTab.value !== 'replay') changeTab('replay')
      importDrawerVisible.value = true
    }
  },
  { immediate: true },
)

watch(importDrawerVisible, (visible) => {
  if (!visible && route.query.import === '1') {
    const query = { ...route.query }
    delete query.import
    router.replace({ path: '/recaps', query })
  }
})

// Open session detail from query param
// 注意:immediate watch 在 onMounted 的 fetchSessions 完成前触发,此时 store.items 可能为空,
// 直接 find 会错过 session。改用 ensureLoaded 确保列表就绪后再取(并复用 inflight,不重复请求)。
// 拿到 session 后按 source_type 自动切到对应子 tab,再打开抽屉。
watch(
  () => route.query.sid,
  async (sid) => {
    if (sid) {
      const session = await sessionsStore.getByIdAfterLoad(String(sid))
      if (session) {
        const wantTab: RecapTab = REPLAY_TYPES.includes(session.source_type) ? 'replay' : 'live'
        if (activeTab.value !== wantTab) changeTab(wantTab)
        openRecap(session)
      }
    }
  },
  { immediate: true },
)

// ---------- 辅助 ----------
function matchesKeyword(s: DerivedSession, q: string): boolean {
  return [s.title ?? '', s.id, s.slug ?? '', s.source_id ?? ''].some((v) =>
    v.toLowerCase().includes(q),
  )
}

function ts(v: string | undefined): number {
  const t = new Date(v || '').getTime()
  return Number.isNaN(t) ? 0 : t
}

// 当前 session 关联的任务(供 retry 二次校验读取 task 状态)。tasksStore 仍是旧类型,窄化为 loose。
function sessionTask(s: DerivedSession): Task | null {
  const tid = s.current_task_id ?? ''
  if (!tid) return null
  return tasksStore.items.find((t) => t.id === tid) ?? null
}

// ---------- 抽屉:打开/复制 ----------
async function openRecap(s: DerivedSession) {
  selectedSession.value = s
  recapDrawerVisible.value = true
  recapContent.value = null
  addedSuggestedTerms.value = new Set()
  recapLoading.value = true
  try {
    recapContent.value = (await getRecapContent(s.id)) as unknown as DerivedRecapContent
  } catch {
    recapContent.value = null
  } finally {
    recapLoading.value = false
  }
}

// 回顾内容编辑保存后重新拉取，使预览区域显示最新内容。
// 竞态保护：保存期间用户可能切换了 session，只在 sessionID 匹配时更新。
// 二次保护：请求返回后再次检查，防止切换后旧响应覆盖新 session 内容。
async function onRecapSaved(sessionId: string) {
  if (!selectedSession.value || selectedSession.value.id !== sessionId) return
  recapLoading.value = true
  try {
    const fresh = (await getRecapContent(sessionId)) as unknown as DerivedRecapContent
    // 请求返回后再次检查：若期间用户切换了 session，不覆盖新 session 的内容
    if (selectedSession.value?.id === sessionId) {
      recapContent.value = fresh
    }
  } catch {
    // 刷新失败由 client.ts 拦截器提示；保持旧内容
  } finally {
    // 只在仍是同一 session 时清除 loading（切换后的 session 有自己的 loading 生命周期）
    if (selectedSession.value?.id === sessionId) {
      recapLoading.value = false
    }
  }
}

function handleCopyRecap() {
  if (recapContent.value?.markdown) {
    navigator.clipboard.writeText(recapContent.value.markdown)
    HMessage.success('已复制到剪贴板')
  }
}

// ---------- 抽屉:主动作(表B) + 部分回顾 + 术语 ----------
// 抽屉主动作与列表行主动作一致:确认后提交(防误触)
// 抽屉 emit 的 session 是派生类型(V10 组件内);executeAction 只用 id,无需窄化。
async function handleDrawerAction(session: DerivedSession, action: PrimaryAction) {
  if (action.disabled || actionLoadingId.value) return
  if (!(await HConfirm(action.confirmText, {
    title: '操作确认', confirmText: '确认', cancelText: '取消', type: 'info',
  }))) return
  actionLoadingId.value = `${session.id}:${action.name}`
  try {
    await executeAction(session.id, action.name)
    HMessage.success(`${action.label}任务已提交`)
    await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
  } finally {
    actionLoadingId.value = ''
  }
}

async function handlePartialRecap(startSeconds: number, endSeconds: number) {
  if (!selectedSession.value) return
  partialLoading.value = true
  try {
    await generateRecapWithRange(selectedSession.value.id, startSeconds, endSeconds)
    HMessage.success('时间段回顾任务已提交')
    await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
  } finally {
    partialLoading.value = false
  }
}

async function handleAddSuggestedTerm(term: string) {
  const normalized = term.trim()
  if (!normalized || !selectedSession.value || addedSuggestedTerms.value.has(normalized)) return
  addingSuggestedTerm.value = normalized
  try {
    await upsertChannelEntry(selectedSession.value.channel_id, normalized, normalized, '')
    // API 成功后才标记「已添加」,避免失败时按钮误显示
    const next = new Set(addedSuggestedTerms.value)
    next.add(normalized)
    addedSuggestedTerms.value = next
    HMessage.success('词条已添加')
  } finally {
    addingSuggestedTerm.value = ''
  }
}

// ---------- 主动作调度(列表行 + 抽屉共用) ----------
async function executeAction(sid: string, name: PrimaryActionName) {
  if (name === 'submit_asr') await submitASR(sid)
  else if (name === 'generate_recap') await generateRecap(sid)
  else if (name === 'upload') await uploadSession(sid)
  else if (name === 'publish') await publishSession(sid)
}

// ---------- 列表行动作(SessionTableV10 emit) ----------
// SessionTableV10 emit 的 session 是派生类型;executeAction 只用 id,无需窄化。
async function handleRowAction(session: DerivedSession, action: PrimaryAction) {
  if (action.disabled || actionLoadingId.value) return
  if (!(await HConfirm(action.confirmText, {
    title: '操作确认', confirmText: '确认', cancelText: '取消', type: 'info',
  }))) return
  actionLoadingId.value = `${session.id}:${action.name}`
  try {
    await executeAction(session.id, action.name)
    HMessage.success(`${action.label}任务已提交`)
    await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
  } finally {
    actionLoadingId.value = ''
  }
}

// 从 WebDAV 取回本场文件：上传清理策略删除本地目录后，需先取回才能发布/生成回顾。
async function handleFetch(session: DerivedSession) {
  if (actionLoadingId.value) return
  if (!(await HConfirm('确定要从归档取回本场文件？', {
    title: '操作确认', confirmText: '确认', cancelText: '取消', type: 'info',
  }))) return
  actionLoadingId.value = `${session.id}:fetch`
  try {
    await fetchSession(session.id)
    HMessage.success('取回任务已提交')
    await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
  } finally {
    actionLoadingId.value = ''
  }
}

// 重新生成回顾：覆盖本地 md，不碰 B站专栏。仅 recap_done/published 状态可用。
// 任务带 BypassFailState，失败时不降级主状态（published/recap_done 保持，仅写 last_error）。
// 抽屉只针对当前选中场次(selectedSession)，故 emit 不带参。
async function handleRegenerate() {
  const session = selectedSession.value
  if (!session || actionLoadingId.value) return
  if (!(await HConfirm(
    '将重新生成本场回顾（覆盖当前回顾内容，不改动B站专栏）。生成是异步任务，完成后请重新打开查看。是否继续？',
    { title: '重新生成回顾', confirmText: '确认生成', cancelText: '取消', type: 'info' },
  ))) return
  actionLoadingId.value = `${session.id}:regenerate`
  try {
    await regenerateRecap(session.id)
    HMessage.success('重新生成任务已提交，完成后请重新打开查看')
    await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
  } finally {
    actionLoadingId.value = ''
  }
}

// 重试失败任务(§7.1 + 吸收 codex 阶段1 建议):
//  - 用户确认后、调 retryTask 前用 decideRetry 二次校验(防弹窗期间 WS 改了任务状态)
async function handleRetry(session: DerivedSession) {
  if (actionLoadingId.value) return
  if (!(await HConfirm('确定重试该失败任务？', {
    title: '操作确认', confirmText: '确认', cancelText: '取消', type: 'info',
  }))) return

  // 二次校验:弹窗期间 WS 可能已把任务推进/清理。decideRetry 消费 loose 类型,窄化转换。
  if (decideRetry(session as unknown as Session, sessionTask(session)) !== 'retryable') {
    HMessage.info('任务状态已变化，无需重试')
    return
  }

  const taskId = session.current_task_id ?? ''
  actionLoadingId.value = `${session.id}:retry`
  try {
    await retryTask(taskId)
    HMessage.success('重试任务已提交')
    await tasksStore.fetchTasks()
    await sessionsStore.fetchSessions()
  } catch {
    // 失败已由 client 拦截器 toast;任务可能已过期,刷一次 tasks 让 UI 同步
    await tasksStore.fetchTasks()
  } finally {
    actionLoadingId.value = ''
  }
}

// ---------- 发现回放(壳编排:openDiscover 打开抽屉;preview-submit 按 URL 发现;execute 提交) ----------
// DiscoverPreviewDrawer 是纯展示组件,preview-by-url/execute 的 API 调用搬到这里。
// 2026-07-19 解耦重写:不再「打开即自动遍历主播表」,改为用户在抽屉里输入 URL 后才调 preview-by-url。
function openDiscover() {
  discoverDrawerVisible.value = true
  discoverItems.value = []
  discoverExecuting.value = false
  discoverLoading.value = false
}

// 用户在抽屉里点「发现」:调 previewDiscoverSessionsByURL(后端 POST /api/sessions/discover/preview-by-url)。
async function handleDiscoverSubmit(input: { url: string; cookie_file?: string; title_prefix?: string }) {
  discoverLoading.value = true
  discoverItems.value = []
  try {
    const result = await previewDiscoverSessionsByURL(input)
    discoverItems.value = result.items as unknown as DerivedDiscoverResult[]
    const validNew = result.items.filter((i) => !i.error && !i.exists && i.channel_id && i.source_id).length
    const errorCount = result.items.filter((i) => i.error).length
    if (validNew > 0) {
      HMessage.info(`发现 ${validNew} 条新回放，请勾选后下载`)
    } else if (errorCount > 0) {
      HMessage.warning(`部分发现失败（${errorCount} 条错误），其余回放均已处理`)
    } else {
      HMessage.info('未发现新回放（全部已处理或该 URL 无回放）')
    }
  } catch {
    // previewDiscoverSessionsByURL 失败由 client.ts 拦截器统一 toast;items 为空,抽屉展示空态
  } finally {
    discoverLoading.value = false
  }
}

async function handleDiscoverExecute(picks: DerivedDiscoverPickItem[]) {
  if (picks.length === 0) {
    HMessage.warning('请先勾选要下载的回放')
    return
  }
  if (!(await HConfirm(`确定下载选中的 ${picks.length} 个回放？将自动开始下载。`, {
    title: '下载确认', confirmText: '下载', cancelText: '取消', type: 'warning',
  }))) return
  discoverExecuting.value = true
  try {
    // executeDiscoverSessions 消费旧 types.ts 的 DiscoverPickItem(全必填);派生类型窄化转换。
    const result = await executeDiscoverSessions(picks as unknown as DiscoverPickItem[])
    const created = result.items.filter((i) => i.created && !i.error).length
    if (created > 0) HMessage.success(`已开始下载 ${created} 个新回放`)
    else HMessage.info('选中项均已处理，无新下载')
    await onDiscoverExecuted()
    discoverDrawerVisible.value = false
  } finally {
    discoverExecuting.value = false
  }
}

// 执行/全下完成后的刷新(发现产出属回放 tab)
async function onDiscoverExecuted() {
  await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
}

// ---------- 工具栏动作 ----------
function handleImportSubmitted() {
  sessionsStore.fetchSessions()
  tasksStore.fetchTasks()
}

async function handleClearFailed() {
  const count = failedCount.value
  if (count === 0) { HMessage.info('没有失败场次'); return }
  if (clearFailedLoading.value) return
  if (!(await HConfirm(`确定清空 ${count} 个失败场次？`, {
    title: '清空', confirmText: '清空', cancelText: '取消', type: 'warning',
  }))) return
  clearFailedLoading.value = true
  try {
    const result = await deleteFailedSessions()
    HMessage.success(`已删除 ${result.deleted} 个`)
  } catch {
    // API 错误已由 client.ts 拦截器 toast
  } finally {
    // 无论成功失败都刷新列表（对账），刷新失败不传播
    try { await sessionsStore.forceRefresh() } catch { /* ignore */ }
    clearFailedLoading.value = false
  }
}

onMounted(async () => {
  // 用 fetchSessions 而非 ensureLoaded:每次进入页面都拉最新列表（避免跨路由返回时显示旧数据）。
  // fetchSessions 内部有 inflight 去重,与 ?sid watch 的 getByIdAfterLoad→ensureLoaded→fetchSessions 复用同一请求。
  await Promise.all([
    channelsStore.ensureLoaded(),
    runtimeStore.fetchRuntime(),
    sessionsStore.fetchSessions(),
    tasksStore.fetchTasks(),
  ])
})
</script>

<template>
  <div class="recaps-view">
    <RecapToolbarV10
      v-model:active-tab="activeTab"
      :failed-count="failedCount"
      :capabilities="capabilities"
      :action-loading="discoverExecuting"
      @discover="openDiscover"
      @import="importDrawerVisible = true"
      @download="downloadDrawerVisible = true"
      @clear-failed="handleClearFailed"
    />

    <SessionFiltersV10
      v-model:keyword="keyword"
      v-model:status-filter="statusFilter"
      v-model:channel-filter="channelFilter"
      :channels="channels"
      :active-tab="activeTab"
    />

    <SessionTableV10
      v-model:current-page="currentPage"
      v-model:page-size="pageSize"
      :sessions="pagedSessions"
      :tasks="tasksStore.items"
      :capabilities="capabilities"
      :channels="channels"
      :action-loading-id="actionLoadingId"
      @open-recap="openRecap"
      @run-action="handleRowAction"
      @fetch="handleFetch"
      @retry="handleRetry"
    />

    <RecapDrawerV10
      v-model:visible="recapDrawerVisible"
      :session="selectedSession"
      :content="recapContent"
      :loading="recapLoading"
      :capabilities="capabilities"
      :is-expert="isExpert"
      :channels="channels"
      :action-loading-id="actionLoadingId"
      :adding-term="addingSuggestedTerm"
      :partial-loading="partialLoading"
      :added-terms="addedSuggestedTerms"
      @copy="handleCopyRecap"
      @run-action="handleDrawerAction"
      @regenerate="handleRegenerate"
      @partial-range="handlePartialRecap"
      @add-term="handleAddSuggestedTerm"
      @saved="onRecapSaved"
    />

    <DiscoverPreviewDrawer
      v-model:visible="discoverDrawerVisible"
      :items="discoverItems"
      :executing="discoverExecuting"
      :loading="discoverLoading"
      @preview-submit="handleDiscoverSubmit"
      @execute="handleDiscoverExecute"
    />

    <ImportSessionDrawer
      v-model:visible="importDrawerVisible"
      @submitted="handleImportSubmitted"
    />

    <DownloadByURLDrawer
      v-model:visible="downloadDrawerVisible"
      @submitted="handleImportSubmitted"
    />
  </div>
</template>

<style scoped>
.recaps-view {
  padding: 24px;
  max-width: 1200px;
  margin: 0 auto;
}
</style>
