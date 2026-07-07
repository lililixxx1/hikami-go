<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Search, Refresh, Warning, VideoCamera, VideoPause, CircleCheck, CircleClose } from '@element-plus/icons-vue'
import { useTasksStore } from '@/stores/tasks'
import { useSessionsStore } from '@/stores/sessions'
import { useChannelsStore } from '@/stores/channels'
import { useRuntimeStore } from '@/stores/runtime'
import { useLiveStatusStore } from '@/stores/liveStatus'
import { useExpertMode } from '@/composables/useExpertMode'
import { useDiscoverReplay } from '@/composables/useDiscoverReplay'
import { usePolling } from '@/composables/usePolling'
import { getFriendlySessionStatus } from '@/utils/friendlyStatus'
import { checkAllLive, startRecord, stopRecord } from '@/api/live'
import { getDashboardStats } from '@/api/stats'
import { cancelTask } from '@/api/tasks'
import { formatDateTime } from '@/utils/format'
import TaskProgressBar from '@/components/task/TaskProgressBar.vue'
import OnboardingWizard from '@/components/onboarding/OnboardingWizard.vue'
import type { DashboardData, Task } from '@/api/types-derived'
import DiscoverResultDrawer from '@/components/session/DiscoverResultDrawer.vue'

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
const currentMonthSessions = computed(() =>
  dashboard.value?.sessions_by_month.find((item) => item.month === currentMonth)?.session_count ?? 0,
)
const totalDashboardChannels = computed(() => dashboard.value?.sessions_by_channel.length ?? 0)

// Live status
const liveItems = computed(() => {
  return channelsStore.items
    .filter((c) => c.enabled && c.live_room_id > 0)
    .map((c) => {
      const status = liveStatusStore.getStatus(c.id) ?? { live: false, recording: false, channel_id: c.id, room_id: c.live_room_id, title: '', started_at: '', session_id: '', task_id: '', error: '' }
      return { channel: c, status }
    })
    .filter((item) => item.status.live || item.status.recording)
})

const recordingItems = computed(() => liveItems.value.filter((i) => i.status.recording))
const liveOnlyItems = computed(() => liveItems.value.filter((i) => i.status.live && !i.status.recording))

// Attention items
const failedSessions = computed(() =>
  sessionsStore.items.filter((s) => s.status === 'failed'),
)
const cookieWarnings = computed(() =>
  (runtimeStore.status?.cookie_warnings || []).filter((w) => !w.is_expired),
)
const diskWarnings = computed(() =>
  (runtimeStore.status?.disk_usage || []).filter((d) => d.used_percent >= 85),
)

// Recent recaps (recap_done / published)
const recentRecaps = computed(() =>
  [...sessionsStore.items]
    .filter((s) => ['recap_done', 'uploaded', 'published'].includes(s.status))
    .sort((a, b) => ts(b.created_at) - ts(a.created_at))
    .slice(0, 6),
)

// Running tasks
const runningTasks = computed(() =>
  tasksStore.items
    .filter((t) => t.status === 'pending' || t.status === 'running')
    .sort((a, b) => ts(b.created_at) - ts(a.created_at)),
)

// Capability signals
const capSignals = computed(() => {
  const caps = capabilities.value
  return [
    { key: 'asr_submit' as const, label: 'ASR', ok: Boolean(caps?.asr_submit) },
    { key: 'recap_generate' as const, label: '回顾', ok: Boolean(caps?.recap_generate) },
    { key: 'webdav_upload' as const, label: '上传', ok: Boolean(caps?.webdav_upload) },
    { key: 'publish_opus' as const, label: '发布', ok: Boolean(caps?.publish_opus) },
  ]
})

function ts(v: string): number {
  const t = new Date(v || '').getTime()
  return Number.isNaN(t) ? 0 : t
}

function channelName(cid: string): string {
  return channelsStore.items.find((c) => c.id === cid)?.name || cid
}

function durationText(startedAt: string): string {
  const start = new Date(startedAt || '').getTime()
  if (!start || Number.isNaN(start)) return '-'
  const sec = Math.max(0, Math.floor((Date.now() - start) / 1000))
  const h = String(Math.floor(sec / 3600)).padStart(2, '0')
  const m = String(Math.floor((sec % 3600) / 60)).padStart(2, '0')
  return `${h}:${m}`
}

function fixedNumber(value: number, digits = 1): string {
  return Number(value || 0).toFixed(digits)
}

async function handleCheckLive() {
  checkingLive.value = true
  try {
    await checkAllLive()
    await liveStatusStore.fetchAll()
    ElMessage.success('检查完成')
  } finally {
    checkingLive.value = false
  }
}

async function handleStartRecord(cid: string, name: string) {
  try {
    await ElMessageBox.confirm(`确定开始录制「${name}」的直播？`, '开始录制', {
      confirmButtonText: '开始', cancelButtonText: '取消', type: 'info',
    })
  } catch { return }
  try {
    await startRecord(cid)
    ElMessage.success('录制已开始')
    await liveStatusStore.fetchAll()
  } catch { /* handled by API */ }
}

async function handleStopRecord(cid: string, name: string) {
  try {
    await ElMessageBox.confirm(`确定停止录制「${name}」？`, '停止录制', {
      confirmButtonText: '停止', cancelButtonText: '取消', type: 'warning',
    })
  } catch { return }
  try {
    await stopRecord(cid)
    ElMessage.success('录制已停止')
    await liveStatusStore.fetchAll()
  } catch { /* handled by API */ }
}

async function handleCancelTask(task: Task) {
  cancellingTaskId.value = task.id
  try {
    await ElMessageBox.confirm('确定取消该任务？', '取消任务', {
      confirmButtonText: '取消任务', cancelButtonText: '返回', type: 'warning',
    })
  } catch { return }
  try {
    await cancelTask(task.id)
    ElMessage.success('任务已取消')
    await tasksStore.fetchTasks()
  } catch { /* handled */ }
}

function goToRecap(sid: string) { router.push(`/recaps?sid=${sid}`) }
function goToStreamers() { router.push('/streamers') }

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
    <!-- Section 1: Live Status -->
    <section class="section">
      <div class="section-header">
        <h3>直播状态</h3>
        <el-button size="small" :loading="checkingLive" @click="handleCheckLive">
          <el-icon><Refresh /></el-icon> 刷新
        </el-button>
      </div>
      <div v-if="liveItems.length > 0" class="live-grid">
        <div v-for="item in recordingItems" :key="item.channel.id" class="live-card recording">
          <div class="live-card-head">
            <strong>{{ item.channel.name }}</strong>
            <el-tag type="warning" size="small">录制中</el-tag>
          </div>
          <p class="live-title">{{ item.status.title || '直播中' }}</p>
          <span class="live-meta">已录制 {{ durationText(item.status.started_at) }}</span>
          <el-button type="danger" plain size="small" @click="handleStopRecord(item.channel.id, item.channel.name)">
            <el-icon><VideoPause /></el-icon> 停止
          </el-button>
        </div>
        <div v-for="item in liveOnlyItems" :key="item.channel.id" class="live-card live-only">
          <div class="live-card-head">
            <strong>{{ item.channel.name }}</strong>
            <el-tag type="success" effect="dark" size="small">LIVE</el-tag>
          </div>
          <p class="live-title">{{ item.status.title || '直播中' }}</p>
          <span class="live-meta">自动录制: {{ item.channel.auto_record ? '开' : '关' }}</span>
          <el-button type="primary" size="small" @click="handleStartRecord(item.channel.id, item.channel.name)">
            <el-icon><VideoCamera /></el-icon> 录制
          </el-button>
        </div>
      </div>
      <el-empty v-else description="当前没有直播" :image-size="60" />
    </section>

    <!-- Section 2: Attention -->
    <section v-if="failedSessions.length > 0 || cookieWarnings.length > 0 || diskWarnings.length > 0" class="section">
      <div class="section-header">
        <h3>需要注意</h3>
      </div>
      <div class="alert-list">
        <div v-for="s in failedSessions.slice(0, 5)" :key="s.id" class="alert-card danger" @click="goToRecap(s.id)">
          <el-icon><Warning /></el-icon>
          <span class="alert-text">{{ channelName(s.channel_id) }} - {{ s.title || s.id }} 处理失败</span>
          <el-button type="danger" size="small" link>查看</el-button>
        </div>
        <div v-if="cookieWarnings.length > 0" class="alert-card warning">
          <el-icon><Warning /></el-icon>
          <span class="alert-text">Cookie 即将过期: {{ cookieWarnings.map((w) => w.channel_name).join(', ') }}</span>
        </div>
        <div v-if="diskWarnings.length > 0" class="alert-card warning">
          <el-icon><Warning /></el-icon>
          <span class="alert-text">磁盘空间不足: {{ diskWarnings.map((d) => `${d.path} ${d.used_percent.toFixed(0)}%`).join(', ') }}</span>
        </div>
      </div>
    </section>

    <!-- Section 3: Recent Recaps -->
    <section class="section">
      <div class="section-header">
        <h3>最近回顾</h3>
        <el-button size="small" link type="primary" @click="$router.push('/recaps')">查看全部</el-button>
      </div>
      <div v-if="recentRecaps.length > 0" class="recap-grid">
        <div v-for="s in recentRecaps" :key="s.id" class="recap-card" @click="goToRecap(s.id)">
          <div class="recap-card-head">
            <strong class="recap-title">{{ s.title || '无标题' }}</strong>
          </div>
          <div class="recap-meta">
            <span>{{ channelName(s.channel_id) }}</span>
            <span>{{ formatDateTime(s.created_at) }}</span>
          </div>
          <div class="recap-status">
            <el-tag
              :type="getFriendlySessionStatus(s).color === 'success' ? 'success' : getFriendlySessionStatus(s).color === 'danger' ? 'danger' : 'warning'"
              size="small"
            >
              {{ getFriendlySessionStatus(s).label }}
            </el-tag>
          </div>
        </div>
      </div>
      <el-empty v-else description="暂无回顾" :image-size="60" />
    </section>

    <!-- Expert: Quick Actions -->
    <div class="quick-actions">
      <el-button type="primary" @click="goToStreamers">
        <el-icon><Plus /></el-icon> 添加主播
      </el-button>
      <el-button :loading="false" @click="handleDiscoverReplay">
        <el-icon><Search /></el-icon> 发现回放
      </el-button>
    </div>

    <!-- Expert: Running Tasks -->
    <section v-if="isExpert && runningTasks.length > 0" class="section">
      <div class="section-header">
        <h3>运行中任务</h3>
      </div>
      <el-table :data="runningTasks" stripe size="small">
        <el-table-column label="类型" width="120">
          <template #default="{ row }">{{ row.type }}</template>
        </el-table-column>
        <el-table-column label="主播" width="120">
          <template #default="{ row }">{{ channelName(row.channel_id) }}</template>
        </el-table-column>
        <el-table-column label="进度" min-width="180">
          <template #default="{ row }">
            <TaskProgressBar :progress="row.progress" :status="row.status" />
          </template>
        </el-table-column>
        <el-table-column prop="message" label="消息" min-width="180" show-overflow-tooltip />
        <el-table-column label="操作" width="80" align="center">
          <template #default="{ row }">
            <el-button type="danger" link size="small" :loading="cancellingTaskId === row.id" @click="handleCancelTask(row)">取消</el-button>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <!-- Expert: Capability Signals -->
    <section v-if="isExpert" class="section">
      <div class="section-header">
        <h3>系统能力</h3>
        <el-button link size="small" type="primary" @click="$router.push('/settings')">设置</el-button>
      </div>
      <div class="cap-row">
        <span v-for="s in capSignals" :key="s.key" class="cap-item" :class="s.ok ? 'ok' : 'off'">
          <el-icon><CircleCheck v-if="s.ok" /><CircleClose v-else /></el-icon>
          {{ s.label }}{{ s.ok ? '可用' : '不可用' }}
        </span>
      </div>
    </section>

    <!-- Expert: Dashboard -->
    <section v-if="isExpert && dashboard" class="section dashboard-section">
      <div class="section-header">
        <h3>统计仪表板</h3>
      </div>
      <el-descriptions :column="2" border size="small" class="dashboard-summary">
        <el-descriptions-item label="本月场次">{{ currentMonthSessions }}</el-descriptions-item>
        <el-descriptions-item label="总主播数">{{ totalDashboardChannels }}</el-descriptions-item>
      </el-descriptions>

      <div class="dashboard-grid">
        <div>
          <h4>月度场次</h4>
          <el-table :data="dashboard.sessions_by_month" stripe size="small">
            <el-table-column prop="month" label="月份" width="110" />
            <el-table-column prop="session_count" label="场次数" width="100" />
          </el-table>
        </div>
        <div>
          <h4>主播场次排名</h4>
          <el-table :data="dashboard.sessions_by_channel" stripe size="small">
            <el-table-column prop="channel_name" label="主播" min-width="140" show-overflow-tooltip />
            <el-table-column prop="session_count" label="场次" width="80" />
          </el-table>
        </div>
        <div>
          <h4>费用趋势</h4>
          <el-table :data="dashboard.cost_trend" stripe size="small">
            <el-table-column prop="month" label="月份" width="110" />
            <el-table-column label="ASR 时长" min-width="100">
              <template #default="{ row }">{{ fixedNumber(row.asr_hours, 1) }}</template>
            </el-table-column>
            <el-table-column label="ASR 成本" width="100">
              <template #default="{ row }">¥{{ fixedNumber(row.asr_cost) }}</template>
            </el-table-column>
          </el-table>
        </div>
      </div>
    </section>
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
  gap: 20px;
}

.section {
  background: #fff;
  border: 1px solid #ebeef5;
  border-radius: 10px;
  padding: 20px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.section-header h3 {
  margin: 0;
  font-size: 16px;
  color: #303133;
}

/* Live Grid */
.live-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 12px;
}

.live-card {
  padding: 16px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.live-card.recording {
  border-left: 4px solid #e6a23c;
}

.live-card.live-only {
  border-left: 4px solid #67c23a;
}

.live-card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.live-title {
  margin: 0;
  color: #606266;
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.live-meta {
  font-size: 12px;
  color: #909399;
}

/* Alert */
.alert-list {
  display: grid;
  gap: 8px;
}

.alert-card {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 14px;
  cursor: pointer;
}

.alert-card.danger {
  background: #fef0f0;
  color: #f56c6c;
}

.alert-card.warning {
  background: #fdf6ec;
  color: #e6a23c;
  cursor: default;
}

.alert-text {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Recap Grid */
.recap-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
}

.recap-card {
  padding: 14px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  cursor: pointer;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.recap-card:hover {
  border-color: #c6e2ff;
  box-shadow: 0 2px 12px rgb(0 0 0 / 6%);
}

.recap-card-head {
  margin-bottom: 8px;
}

.recap-title {
  font-size: 14px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.recap-meta {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: #909399;
  margin-bottom: 8px;
}

.recap-status {
  display: flex;
  justify-content: flex-end;
}

/* Quick Actions */
.quick-actions {
  display: flex;
  gap: 12px;
  justify-content: center;
}

/* Expert */
.cap-row {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.cap-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  font-weight: 500;
}

.cap-item.ok { color: #67c23a; }
.cap-item.off { color: #909399; }

.dashboard-summary {
  margin-bottom: 16px;
}

.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.dashboard-grid h4 {
  margin: 0 0 10px;
  font-size: 14px;
  color: #606266;
}

@media (max-width: 900px) {
  .dashboard-grid {
    grid-template-columns: 1fr;
  }
}
</style>
