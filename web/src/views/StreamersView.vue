<script setup lang="ts">
// web/src/views/StreamersView.vue — V10 壳
//
// 职责:加载数据(channels/sessions/runtime/recap models)、消费 ?id query 打开抽屉、
// 把网格 + 详情抽屉 + 两个 EP 对话框组合起来。写操作全部委托给 useStreamerDetail composable,
// 成功后重新拉取 channels store 并同步选中主播(避免本地乐观更新与后端漂移)。
// EP 组件(ChannelIdentifyDialog / BiliQRCodeLoginDialog)Phase 6 统一换为 H* 实现。
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { HMessage } from '@/components/ui/message'
import { HConfirm } from '@/components/ui/HConfirm'
import { Plus, Search } from '@element-plus/icons-vue'
import { useChannelsStore } from '@/stores/channels'
import { useSessionsStore } from '@/stores/sessions'
import { useRuntimeStore } from '@/stores/runtime'
import { useExpertMode } from '@/composables/useExpertMode'
import { useRecapModels } from '@/composables/useRecapModels'
import { formatDateTime } from '@/utils/format'
import type { Channel, Session, RuntimeStatus } from '@/api/types-derived'
import ChannelIdentifyDialog from '@/components/channel/ChannelIdentifyDialog.vue'
import BiliQRCodeLoginDialog from '@/components/channel/BiliQRCodeLoginDialog.vue'
import StreamerGrid from '@/features/streamers/components/StreamerGrid.vue'
import StreamerDrawer from '@/features/streamers/components/StreamerDrawer.vue'
import { useStreamerDetail } from '@/features/streamers/composables/useStreamerDetail'
import type { CookieStatus } from '@/features/streamers/composables/useStreamerDetail'

const route = useRoute()
const router = useRouter()
const channelsStore = useChannelsStore()
const sessionsStore = useSessionsStore()
const runtimeStore = useRuntimeStore()
const { isExpert } = useExpertMode()
const { groups: recapModelGroups, load: loadRecapModels } = useRecapModels()

const keyword = ref('')
const showIdentifyDialog = ref(false)
// 选中主播 id(抽屉可见性 + 选中态的唯一来源;selectedChannel 由 store 派生,
// 这样 store 刷新后选中主播自动同步最新值,无需手动回填)
const selectedChannelId = ref<string | null>(null)
const showQRDialog = ref(false)
const qrChannelId = ref('')

const drawerVisible = computed({
  get: () => selectedChannelId.value !== null,
  set: (v: boolean) => { if (!v) selectedChannelId.value = null },
})

// store 仍返回旧手写 types.ts Channel;实际运行时形态与 derived 一致(Phase 0 后端补齐字段)。
// 经 unknown 桥接到派生类型,供 V10 子组件消费(与 HomeView 同款做法)。
const channels = computed<Channel[]>(() => channelsStore.items as unknown as Channel[])
const allSessions = computed<Session[]>(() => sessionsStore.items as unknown as Session[])
const runtime = computed<RuntimeStatus | null>(() => runtimeStore.status as unknown as RuntimeStatus | null)

// 选中主播:从 store 派生,store 刷新后自动同步
const selectedChannel = computed<Channel | null>(() => {
  const id = selectedChannelId.value
  if (!id) return null
  return channels.value.find((c) => c.id === id) ?? null
})

// 选中主播的 composable(写操作)。channel 以 Ref 传入;composable 内部读 channel.value。
// cookieStatus 由抽屉自行从 channel+runtime 现场计算(展示侧),composable 的 cookieStatus 此处不消费;
// 仅取写操作 + updating(透传给抽屉的 AutoSwitches 禁用态)。
// selectedChannel / runtime 为派生类型,composable 形参为结构兼容的手写类型,经 unknown 桥接(运行时同一对象)。
const { updating, handleToggle, handleRecapOverride, saveCover, handleDelete } = useStreamerDetail(
  selectedChannel as unknown as Parameters<typeof useStreamerDetail>[0],
  runtime as unknown as Parameters<typeof useStreamerDetail>[1],
)

// 过滤后的主播列表(按关键字)
const filteredChannels = computed<Channel[]>(() => {
  const q = keyword.value.trim().toLowerCase()
  if (!q) return channels.value
  return channels.value.filter((c) => c.name.toLowerCase().includes(q) || c.id.toLowerCase().includes(q))
})

// cookieStatusFn / lastSessionFn:供 StreamerGrid 逐卡计算(壳持有 runtime + sessions store)
function cookieStatusFn(c: Channel): CookieStatus {
  return computeCookieStatus(c, runtime.value)
}

function lastSessionFn(cid: string): string {
  const sessions = channelSessions(cid)
  if (sessions.length === 0) return '-'
  return formatDateTime(sessions[0].created_at)
}

// 选中主播最近场次(抽屉顶部列表)
const recentSessions = computed<Session[]>(() => {
  const id = selectedChannelId.value
  if (!id) return []
  return channelSessions(id)
})

// ---- 工具函数 ----
function ts(v: string): number {
  const t = new Date(v || '').getTime()
  return Number.isNaN(t) ? 0 : t
}

function channelSessions(cid: string): Session[] {
  return allSessions.value
    .filter((s) => s.channel_id === cid)
    .sort((a, b) => ts(b.created_at) - ts(a.created_at))
    .slice(0, 5)
}

// cookieStatus 判定(与 useStreamerDetail 同逻辑;此处用于网格卡片,逐卡传入 runtime)
function computeCookieStatus(c: Channel, rt: RuntimeStatus | null | undefined): CookieStatus {
  if (c.cookie_file || c.download_cookie_file) return 'ok'
  if (!rt) return 'unknown'
  if (rt.has_default_download || rt.has_default_publish) return 'ok'
  return 'missing'
}

// ---- 事件处理 ----
function openDetail(c: Channel) {
  selectedChannelId.value = c.id
}

function openQrLogin(cid: string) {
  qrChannelId.value = cid
  showQRDialog.value = true
}

function goToRecap(sid: string) {
  selectedChannelId.value = null
  router.push(`/recaps?sid=${sid}`)
}

// 抽屉动作 → composable → 成功后刷新 store(选中主播由 store 派生自动同步)
async function onToggle(field: Parameters<typeof handleToggle>[0]) {
  try {
    await handleToggle(field)
    await channelsStore.fetchChannels()
  } catch {
    // 错误提示由 axios 拦截器统一处理
  }
}

async function onRecapOverride(field: Parameters<typeof handleRecapOverride>[0], value: string | number) {
  try {
    await handleRecapOverride(field, value)
    await channelsStore.fetchChannels()
  } catch { /* handled */ }
}

async function onSaveCover(value: string) {
  try {
    await saveCover(value)
    await channelsStore.fetchChannels()
  } catch { /* handled */ }
}

async function onDelete() {
  const c = selectedChannel.value
  if (!c) return
  if (!(await HConfirm('确定删除该主播?相关场次数据不会被删除。', {
    title: '删除主播', confirmText: '删除', cancelText: '取消', type: 'warning',
  }))) return
  try {
    await handleDelete()
    HMessage.success('已删除')
    selectedChannelId.value = null
    await channelsStore.fetchChannels()
  } catch { /* handled */ }
}

function handleIdentifySuccess() {
  showIdentifyDialog.value = false
  channelsStore.fetchChannels()
}

// ---- ?id query 消费 + 初载 ----
// 注意:immediate watch 在 onMounted 的 fetchChannels 完成前触发,此时 store.items 可能为空,
// 直接 find 会错过 channel。改用 getByIdAfterLoad 确保列表就绪后再取(并复用 inflight)。
watch(
  () => route.query.id,
  async (id) => {
    if (id) {
      const channel = await channelsStore.getByIdAfterLoad(String(id))
      if (channel) {
        selectedChannelId.value = channel.id
        HMessage.info('已跳转至主播详情')
      }
    }
  },
  { immediate: true },
)

onMounted(async () => {
  // 用 ensureLoaded 而非 fetchChannels/fetchSessions:与 ?id watch 复用同一 inflight,
  // 避免 immediate watch 与 onMounted 并发各发一次 list 请求。
  await Promise.all([
    channelsStore.ensureLoaded(),
    sessionsStore.ensureLoaded(),
    // 强制刷新:绕过 30s 缓存,确保主播 cookie 状态反映最新账号池默认账号(用户可能在设置页刚改过)。
    runtimeStore.fetchRuntime(true),
    loadRecapModels(),
  ])
})
</script>

<template>
  <div class="streamers-page">
    <div class="page-header">
      <h2>我的主播</h2>
      <div class="page-actions">
        <el-input v-model="keyword" clearable placeholder="搜索主播" class="search-input">
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-button type="primary" @click="showIdentifyDialog = true">
          <el-icon><Plus /></el-icon> 添加主播
        </el-button>
      </div>
    </div>

    <StreamerGrid
      :channels="filteredChannels"
      :cookie-status-fn="cookieStatusFn"
      :last-session-fn="lastSessionFn"
      @open-detail="openDetail"
    />

    <StreamerDrawer
      :visible="drawerVisible"
      :channel="selectedChannel"
      :runtime="runtime"
      :is-expert="isExpert"
      :updating="updating"
      :recap-model-groups="recapModelGroups"
      :recent-sessions="recentSessions"
      @update:visible="drawerVisible = $event"
      @open-recap="goToRecap"
      @qr-login="openQrLogin"
      @toggle="onToggle"
      @recap-override="onRecapOverride"
      @save-cover="onSaveCover"
      @delete="onDelete"
    />

    <ChannelIdentifyDialog v-model:visible="showIdentifyDialog" @success="handleIdentifySuccess" />
    <BiliQRCodeLoginDialog
      v-if="showQRDialog"
      v-model:visible="showQRDialog"
      :channel-id="qrChannelId"
      @saved="() => { HMessage.success('Cookie 已更新'); channelsStore.fetchChannels() }"
    />
  </div>
</template>

<style scoped>
.streamers-page {
  padding: 24px;
  max-width: 1200px;
  margin: 0 auto;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  margin-bottom: 20px;
}

.page-header h2 {
  margin: 0;
  font-size: 22px;
  color: var(--text);
}

.page-actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

.search-input {
  width: 200px;
}

@media (max-width: 768px) {
  .page-header {
    flex-direction: column;
    align-items: stretch;
  }

  .search-input {
    width: 100%;
  }
}
</style>
