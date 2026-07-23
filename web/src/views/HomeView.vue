<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { HMessage } from '@/components/ui/message'
import { HConfirm } from '@/components/ui/HConfirm'
import { useTasksStore } from '@/stores/tasks'
import { useSessionsStore } from '@/stores/sessions'
import { useChannelsStore } from '@/stores/channels'
import { useRuntimeStore } from '@/stores/runtime'
import { useLiveStatusStore } from '@/stores/liveStatus'
import { useExpertMode } from '@/composables/useExpertMode'
import { useDiscoverReplay } from '@/composables/useDiscoverReplay'
import { usePolling } from '@/composables/usePolling'
import { checkAllLive, startRecord, stopRecord } from '@/api/live'
import { getDashboardStats } from '@/api/stats'
import { cancelTask } from '@/api/tasks'
import type { DashboardData, Session, Task, CookieWarning, DiskInfo } from '@/api/types-derived'
import OnboardingWizard from '@/components/onboarding/OnboardingWizard.vue'
import DiscoverResultDrawer from '@/components/session/DiscoverResultDrawer.vue'
import QuickActions from '@/features/home/components/QuickActions.vue'
import AttentionSection from '@/features/home/components/AttentionSection.vue'
import LiveSection, { type LiveItem } from '@/features/home/components/LiveSection.vue'
import RecentRecapsSection from '@/features/home/components/RecentRecapsSection.vue'
import RunningTasksSection from '@/features/home/components/RunningTasksSection.vue'
import CapabilitySection from '@/features/home/components/CapabilitySection.vue'
import DashboardSection from '@/features/home/components/DashboardSection.vue'

const router = useRouter()
const tasksStore = useTasksStore()
const sessionsStore = useSessionsStore()
const channelsStore = useChannelsStore()
const runtimeStore = useRuntimeStore()
const liveStatusStore = useLiveStatusStore()
const { isExpert } = useExpertMode()

const checkingLive = ref(false)
const cancellingTaskId = ref<string | null>(null)
const { drawerVisible: discoverDrawerVisible, openDiscover: handleDiscoverReplay, onExecuted: onDiscoverExecuted } = useDiscoverReplay()
const dashboard = ref<DashboardData | null>(null)

const capabilities = computed(() => runtimeStore.status?.capabilities ?? null)
const currentMonth = new Date().toISOString().slice(0, 7)

// Live status
const liveItems = computed<LiveItem[]>(() => {
  return (channelsStore.items as unknown as LiveItem['channel'][])
    .filter((c) => c.enabled && c.live_room_id > 0)
    .map((c) => {
      const status = (liveStatusStore.getStatus(c.id) as LiveItem['status'] | undefined) ?? { live: false, recording: false, channel_id: c.id, room_id: c.live_room_id, title: '', started_at: '', session_id: '', task_id: '', error: '' }
      return { channel: c, status }
    })
    .filter((item) => item.status.live || item.status.recording)
})

const recordingItems = computed(() => liveItems.value.filter((i) => i.status.recording))
const liveOnlyItems = computed(() => liveItems.value.filter((i) => i.status.live && !i.status.recording))

// Attention items
// store 仍返回旧手写类型(types.ts),实际运行时数据形态与 generated 派生类型一致
// (Phase 0 后端已补 channel_name / total_gb 等字段)。经 unknown 桥接到派生类型,供 V10 子组件消费。
const failedSessions = computed<Session[]>(() =>
  (sessionsStore.items as unknown as Session[]).filter((s) => s.status === 'failed'),
)
const cookieWarnings = computed<CookieWarning[]>(() =>
  ((runtimeStore.status?.cookie_warnings as unknown as CookieWarning[] | undefined) || []).filter((w) => !w.is_expired),
)
const diskWarnings = computed<DiskInfo[]>(() =>
  ((runtimeStore.status?.disk_usage as unknown as DiskInfo[] | undefined) || []).filter((d) => d.used_percent >= 85),
)

// Recent recaps (recap_done / published)
const recentRecaps = computed<Session[]>(() =>
  [...(sessionsStore.items as unknown as Session[])]
    .filter((s) => ['recap_done', 'uploaded', 'published'].includes(s.status))
    .sort((a, b) => ts(b.created_at) - ts(a.created_at))
    .slice(0, 6),
)

// Running tasks
const runningTasks = computed<Task[]>(() =>
  (tasksStore.items as unknown as Task[])
    .filter((t) => t.status === 'pending' || t.status === 'running')
    .sort((a, b) => ts(b.created_at) - ts(a.created_at)),
)

function ts(v: string): number {
  const t = new Date(v || '').getTime()
  return Number.isNaN(t) ? 0 : t
}

async function handleCheckLive() {
  checkingLive.value = true
  try {
    await checkAllLive()
    await liveStatusStore.fetchAll()
    HMessage.success('检查完成')
  } finally {
    checkingLive.value = false
  }
}

async function handleStartRecord(cid: string, name: string) {
  if (!(await HConfirm(`确定开始录制「${name}」的直播？`, {
    title: '开始录制', confirmText: '开始', cancelText: '取消', type: 'info',
  }))) return
  try {
    await startRecord(cid)
    HMessage.success('录制已开始')
    await liveStatusStore.fetchAll()
  } catch { /* handled by API */ }
}

async function handleStopRecord(cid: string, name: string) {
  if (!(await HConfirm(`确定停止录制「${name}」？`, {
    title: '停止录制', confirmText: '停止', cancelText: '取消', type: 'warning',
  }))) return
  try {
    await stopRecord(cid)
    HMessage.success('录制已停止')
    await liveStatusStore.fetchAll()
  } catch { /* handled by API */ }
}

async function handleCancelTask(taskId: string) {
  if (!(await HConfirm('确定取消该任务？', {
    title: '取消任务', confirmText: '取消任务', cancelText: '返回', type: 'warning',
  }))) return
  cancellingTaskId.value = taskId
  try {
    await cancelTask(taskId)
    HMessage.success('任务已取消')
    await tasksStore.fetchTasks()
  } catch { /* handled */ } finally {
    cancellingTaskId.value = null
  }
}

function handleOpenRecap(sid: string) { router.push(`/recaps?sid=${sid}`) }
function handleViewAllRecaps() { router.push('/recaps') }
function handleAddStreamer() { router.push('/streamers') }
function handleGoSettings() { router.push('/settings') }

// 轮询只刷 liveStatus(后端无 live WS 事件);tasks 由 coordinator 的 WS/降级轮询统一管理(§7.2)
const { start: startPolling } = usePolling(async () => {
  await liveStatusStore.fetchAll()
}, { interval: 30000, immediate: false })

onMounted(async () => {
  // tasks 由 AppLayout coordinator 统一加载(WS 增量 + refreshTasks 初载),此处不重复;
  // 首屏通过 store 响应式更新补齐(父子 onMounted 时序不保证先后)
  await Promise.all([
    sessionsStore.fetchSessions(),
    runtimeStore.fetchRuntime(),
    channelsStore.fetchChannels(),
    liveStatusStore.fetchAll(),
    getDashboardStats().then((data) => { dashboard.value = data }),
  ])
  startPolling()
})
</script>

<template>
  <OnboardingWizard />
  <div class="home-page">
    <QuickActions
      @add-streamer="handleAddStreamer"
      @discover="handleDiscoverReplay"
    />

    <AttentionSection
      :failed-sessions="failedSessions"
      :cookie-warnings="cookieWarnings"
      :disk-warnings="diskWarnings"
      @open-recap="handleOpenRecap"
    />

    <LiveSection
      :recording-items="recordingItems"
      :live-only-items="liveOnlyItems"
      :checking="checkingLive"
      @refresh="handleCheckLive"
      @start-record="handleStartRecord"
      @stop-record="handleStopRecord"
    />

    <RecentRecapsSection
      :recaps="recentRecaps"
      :capabilities="capabilities"
      @open-recap="handleOpenRecap"
      @view-all="handleViewAllRecaps"
    />

    <RunningTasksSection
      v-if="isExpert && runningTasks.length > 0"
      :tasks="runningTasks"
      :cancelling-id="cancellingTaskId"
      @cancel="handleCancelTask"
    />

    <CapabilitySection
      v-if="isExpert"
      :capabilities="capabilities"
      @go-settings="handleGoSettings"
    />

    <DashboardSection
      v-if="isExpert"
      :dashboard="dashboard"
      :current-month="currentMonth"
    />
  </div>

  <DiscoverResultDrawer
    v-model:visible="discoverDrawerVisible"
    @executed="onDiscoverExecuted"
  />
</template>

<style scoped>
.home-page {
  padding: 24px;
  max-width: 1200px;
  margin: 0 auto;
  display: grid;
  gap: 32px;
}

/* 首页内表单 focus ring(仅首页生效,不溢出到其他页面;叠加在 ui.css 既有 border-color 之上) */
.home-page :deep(.input:focus),
.home-page :deep(.select:focus),
.home-page :deep(.textarea:focus) {
  box-shadow: 0 0 0 3px var(--accent-glow);
}
</style>
