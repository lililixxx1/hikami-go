<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useSessionsStore } from '@/stores/sessions'
import { useChannelsStore } from '@/stores/channels'
import { useRuntimeStore } from '@/stores/runtime'
import { useTasksStore } from '@/stores/tasks'
import { useExpertMode } from '@/composables/useExpertMode'
import { useDiscoverReplay } from '@/composables/useDiscoverReplay'
import { statusGroupMap } from '@/utils/friendlyStatus'
import {
  decideRetry,
  type PrimaryAction,
  type PrimaryActionName,
} from '@/features/recaps/sessionActions'
import RecapToolbar from '@/features/recaps/components/RecapToolbar.vue'
import SessionFilters from '@/features/recaps/components/SessionFilters.vue'
import SessionTable from '@/features/recaps/components/SessionTable.vue'
import RecapDrawer from '@/features/recaps/components/RecapDrawer.vue'
import DiscoverResultDrawer from '@/components/session/DiscoverResultDrawer.vue'
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
} from '@/api/sessions'
import { deleteFailedSessions } from '@/api/sessions'
import { retryTask } from '@/api/tasks'
import { upsertChannelEntry } from '@/api/glossary'
import type { RecapContent, Session, Task } from '@/api/types'

const router = useRouter()
const route = useRoute()
const sessionsStore = useSessionsStore()
const channelsStore = useChannelsStore()
const runtimeStore = useRuntimeStore()
const tasksStore = useTasksStore()
const { isExpert } = useExpertMode()

// ---------- 过滤状态(传给 SessionFilters) ----------
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
const { drawerVisible: discoverDrawerVisible, openDiscover, onExecuted: onDiscoverExecuted } = useDiscoverReplay()

// ---------- 动作 loading ----------
const actionLoadingId = ref('')

// ---------- 抽屉相关 ----------
const recapDrawerVisible = ref(false)
const selectedSession = ref<Session | null>(null)
const recapContent = ref<RecapContent | null>(null)
const recapLoading = ref(false)
const addingSuggestedTerm = ref('')
const partialLoading = ref(false)
// 抽屉内术语「已添加」标记:API 成功后才写入(避免失败时按钮误显示已添加)
const addedSuggestedTerms = ref<Set<string>>(new Set())

const capabilities = computed(() => runtimeStore.status?.capabilities ?? null)

// ---------- 过滤 + 分页(壳持有,SessionTable 用 v-model 双向) ----------
const currentPage = ref(1)
const pageSize = ref(20)

const filteredSessions = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  const statuses = statusFilter.value !== 'all' ? (statusGroupMap[statusFilter.value] ?? []) : []
  // 子 tab 按 source_type 过滤:录播=live_record;回放=download+import
  const isReplayTab = activeTab.value === 'replay'

  return sessionsStore.items
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
// 切换子 tab 时同步 ?tab query。注意:el-tabs 的 v-model 会先于 tab-change 更新 activeTab,
// 故此处不做 activeTab 判断,只负责把 URL 补齐(刷新/分享链接落到正确 tab)。
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
function matchesKeyword(s: Session, q: string): boolean {
  return [s.title, s.id, s.slug, s.source_id].some((v) => v.toLowerCase().includes(q))
}

function ts(v: string): number {
  const t = new Date(v || '').getTime()
  return Number.isNaN(t) ? 0 : t
}

// 当前 session 关联的任务(供 retry 二次校验读取 task 状态)
function sessionTask(s: Session): Task | null {
  return tasksStore.items.find((t) => t.id === s.current_task_id) ?? null
}

// ---------- 抽屉:打开/复制 ----------
async function openRecap(s: Session) {
  selectedSession.value = s
  recapDrawerVisible.value = true
  recapContent.value = null
  addedSuggestedTerms.value = new Set()
  recapLoading.value = true
  try {
    recapContent.value = await getRecapContent(s.id)
  } catch {
    recapContent.value = null
  } finally {
    recapLoading.value = false
  }
}

function handleCopyRecap() {
  if (recapContent.value?.markdown) {
    navigator.clipboard.writeText(recapContent.value.markdown)
    ElMessage.success('已复制到剪贴板')
  }
}

// ---------- 抽屉:主动作(表B) + 部分回顾 + 术语 ----------
// 抽屉主动作与列表行主动作一致:确认后提交(防误触)
async function handleDrawerAction(session: Session, action: PrimaryAction) {
  if (action.disabled || actionLoadingId.value) return
  try {
    await ElMessageBox.confirm(action.confirmText, '操作确认', {
      confirmButtonText: '确认', cancelButtonText: '取消', type: 'info',
    })
  } catch { return }
  actionLoadingId.value = `${session.id}:${action.name}`
  try {
    await executeAction(session.id, action.name)
    ElMessage.success(`${action.label}任务已提交`)
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
    ElMessage.success('时间段回顾任务已提交')
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
    ElMessage.success('词条已添加')
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

// ---------- 列表行动作(SessionTable emit) ----------
async function handleRowAction(session: Session, action: PrimaryAction) {
  if (action.disabled || actionLoadingId.value) return
  try {
    await ElMessageBox.confirm(action.confirmText, '操作确认', {
      confirmButtonText: '确认', cancelButtonText: '取消', type: 'info',
    })
  } catch { return }
  actionLoadingId.value = `${session.id}:${action.name}`
  try {
    await executeAction(session.id, action.name)
    ElMessage.success(`${action.label}任务已提交`)
    await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
  } finally {
    actionLoadingId.value = ''
  }
}

// 从 WebDAV 取回本场文件：上传清理策略删除本地目录后，需先取回才能发布/生成回顾。
async function handleFetch(session: Session) {
  if (actionLoadingId.value) return
  try {
    await ElMessageBox.confirm('确定要从归档取回本场文件？', '操作确认', {
      confirmButtonText: '确认', cancelButtonText: '取消', type: 'info',
    })
  } catch { return }
  actionLoadingId.value = `${session.id}:fetch`
  try {
    await fetchSession(session.id)
    ElMessage.success('取回任务已提交')
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
  try {
    await ElMessageBox.confirm(
      '将重新生成本场回顾（覆盖当前回顾内容，不改动B站专栏）。生成是异步任务，完成后请重新打开查看。是否继续？',
      '重新生成回顾',
      { confirmButtonText: '确认生成', cancelButtonText: '取消', type: 'info' },
    )
  } catch { return }
  actionLoadingId.value = `${session.id}:regenerate`
  try {
    await regenerateRecap(session.id)
    ElMessage.success('重新生成任务已提交，完成后请重新打开查看')
    await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
  } finally {
    actionLoadingId.value = ''
  }
}

// 重试失败任务(§7.1 + 吸收 codex 阶段1 建议):
//  - 用户确认后、调 retryTask 前用 decideRetry 二次校验(防弹窗期间 WS 改了任务状态)
async function handleRetry(session: Session) {
  if (actionLoadingId.value) return
  try {
    await ElMessageBox.confirm('确定重试该失败任务？', '操作确认', {
      confirmButtonText: '确认', cancelButtonText: '取消', type: 'info',
    })
  } catch { return }

  // 二次校验:弹窗期间 WS 可能已把任务推进/清理
  if (decideRetry(session, sessionTask(session)) !== 'retryable') {
    ElMessage.info('任务状态已变化，无需重试')
    return
  }

  actionLoadingId.value = `${session.id}:retry`
  try {
    await retryTask(session.current_task_id)
    ElMessage.success('重试任务已提交')
    await tasksStore.fetchTasks()
    await sessionsStore.fetchSessions()
  } catch {
    // 失败已由 client 拦截器 toast;任务可能已过期,刷一次 tasks 让 UI 同步
    await tasksStore.fetchTasks()
  } finally {
    actionLoadingId.value = ''
  }
}

// ---------- 工具栏动作 ----------
function handleImportSubmitted() {
  sessionsStore.fetchSessions()
  tasksStore.fetchTasks()
}

async function handleClearFailed() {
  const count = sessionsStore.items.filter((s) => s.status === 'failed').length
  if (count === 0) { ElMessage.info('没有失败场次'); return }
  try {
    await ElMessageBox.confirm(`确定清空 ${count} 个失败场次？`, '清空', {
      confirmButtonText: '清空', cancelButtonText: '取消', type: 'warning',
    })
  } catch { return }
  const result = await deleteFailedSessions()
  ElMessage.success(`已删除 ${result.deleted} 个`)
  await sessionsStore.fetchSessions()
}

onMounted(async () => {
  // 用 ensureLoaded 而非 fetchSessions/fetchChannels:与 ?sid watch 的 ensureLoaded 复用同一 inflight,
  // 避免 immediate watch 与 onMounted 并发各发一次 list 请求。
  await Promise.all([
    channelsStore.ensureLoaded(),
    runtimeStore.fetchRuntime(),
    sessionsStore.ensureLoaded(),
    tasksStore.fetchTasks(),
  ])
})
</script>

<template>
  <div class="recaps-page">
    <RecapToolbar
      :discovering="false"
      :tab="activeTab"
      :capabilities="capabilities"
      @discover="openDiscover"
      @import="importDrawerVisible = true"
      @download="downloadDrawerVisible = true"
      @clear-failed="handleClearFailed"
    />

    <el-tabs v-model="activeTab" class="recap-tabs" @tab-change="changeTab($event as RecapTab)">
      <el-tab-pane label="录播" name="live" />
      <el-tab-pane label="回放" name="replay" />
    </el-tabs>

    <SessionFilters
      v-model:keyword="keyword"
      v-model:status-filter="statusFilter"
      v-model:channel-filter="channelFilter"
      :channels="channelsStore.items"
    />

    <SessionTable
      v-model:current-page="currentPage"
      v-model:page-size="pageSize"
      :sessions="pagedSessions"
      :total="filteredSessions.length"
      :loading="sessionsStore.loading"
      :channels="channelsStore.items"
      :tasks="tasksStore.items"
      :capabilities="capabilities"
      :action-loading-id="actionLoadingId"
      @open-recap="openRecap"
      @run-action="handleRowAction"
      @fetch="handleFetch"
      @retry="handleRetry"
    />

    <RecapDrawer
      v-model:visible="recapDrawerVisible"
      :session="selectedSession"
      :content="recapContent"
      :loading="recapLoading"
      :capabilities="capabilities"
      :is-expert="isExpert"
      :channels="channelsStore.items"
      :action-loading-id="actionLoadingId"
      :adding-term="addingSuggestedTerm"
      :partial-loading="partialLoading"
      :added-terms="addedSuggestedTerms"
      @copy="handleCopyRecap"
      @run-action="handleDrawerAction"
      @regenerate="handleRegenerate"
      @partial-range="handlePartialRecap"
      @add-term="handleAddSuggestedTerm"
    />

    <DiscoverResultDrawer
      v-model:visible="discoverDrawerVisible"
      @executed="onDiscoverExecuted"
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
.recaps-page {
  padding: 24px;
  max-width: 1200px;
  margin: 0 auto;
}
</style>
