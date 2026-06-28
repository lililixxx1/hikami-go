<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Plus, Search } from '@element-plus/icons-vue'
import { useChannelsStore } from '@/stores/channels'
import { useSessionsStore } from '@/stores/sessions'
import { useRuntimeStore } from '@/stores/runtime'
import { useExpertMode } from '@/composables/useExpertMode'
import { useRecapModels } from '@/composables/useRecapModels'
import { getFriendlySessionStatus } from '@/utils/friendlyStatus'
import { formatDateTime } from '@/utils/format'
import { updateChannel, deleteChannel } from '@/api/channels'
import ChannelIdentifyDialog from '@/components/channel/ChannelIdentifyDialog.vue'
import BiliQRCodeLoginDialog from '@/components/channel/BiliQRCodeLoginDialog.vue'
import GlossaryEditor from '@/components/channel/GlossaryEditor.vue'
import RecapTemplateEditor from '@/components/channel/RecapTemplateEditor.vue'
import type { Channel, Session, UpsertChannelInput, RuntimeStatus } from '@/api/types'

const route = useRoute()
const router = useRouter()
const channelsStore = useChannelsStore()
const sessionsStore = useSessionsStore()
const runtimeStore = useRuntimeStore()
const { isExpert } = useExpertMode()

const keyword = ref('')
const showIdentifyDialog = ref(false)
const selectedChannel = ref<Channel | null>(null)
const drawerVisible = ref(false)
const showQRDialog = ref(false)
const qrChannelId = ref('')
const updating = ref(false)
// 术语表面板懒加载：仅在展开时渲染 GlossaryEditor 并请求主播级词条
const glossaryCollapse = ref<string[]>([])
// 回顾模板面板懒加载：仅在展开时渲染 RecapTemplateEditor 并请求主播级模板
const recapTemplateCollapse = ref<string[]>([])
// 封面输入本地草稿：避免 v-model 直接改 store 对象造成脏状态；保存成功后再回写 store。
// selectedChannel.publish_cover_url 是真实来源，draft 仅在抽屉打开期间同步并承载编辑。
const coverDraft = ref('')
// 回顾模型快捷选项（与全局设置共享同一来源，避免两处硬编码不一致）
const { groups: recapModelGroups, load: loadRecapModels } = useRecapModels()

const filteredChannels = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  return channelsStore.items.filter((c) => {
    if (q && !c.name.toLowerCase().includes(q) && !c.id.toLowerCase().includes(q)) return false
    return true
  })
})

function channelSessions(cid: string): Session[] {
  return sessionsStore.items
    .filter((s) => s.channel_id === cid)
    .sort((a, b) => ts(b.created_at) - ts(a.created_at))
    .slice(0, 5)
}

function ts(v: string): number {
  const t = new Date(v || '').getTime()
  return Number.isNaN(t) ? 0 : t
}

function autoLabel(on: boolean): string { return on ? '✓' : '×' }
function autoType(on: boolean): 'success' | 'info' { return on ? 'success' : 'info' }

// cookieStatus 判定主播能否解析到 cookie：主播级文件路径优先，
// 否则回退到账号池默认账号（下载/发布任一存在即视为已配置，避免误报「未配置」）。
// runtime 未加载时返回 'unknown'（不武断判 missing），避免首屏/缓存过期期间误报。
function cookieStatus(c: Channel, rt?: RuntimeStatus): 'ok' | 'missing' | 'unknown' {
  if (c.cookie_file || c.download_cookie_file) return 'ok'
  if (!rt) return 'unknown'
  if (rt.has_default_download || rt.has_default_publish) return 'ok'
  return 'missing'
}

function lastSessionDate(cid: string): string {
  const sessions = channelSessions(cid)
  if (sessions.length === 0) return '-'
  return formatDateTime(sessions[0].created_at)
}

function openDetail(c: Channel) {
  selectedChannel.value = c
  coverDraft.value = c.publish_cover_url ?? ''
  drawerVisible.value = true
}

function closeDetail() {
  drawerVisible.value = false
  selectedChannel.value = null
  coverDraft.value = ''
}

function goToRecap(sid: string) {
  drawerVisible.value = false
  router.push(`/recaps?sid=${sid}`)
}

function openQRLogin(cid: string) {
  qrChannelId.value = cid
  showQRDialog.value = true
}

async function handleToggle(channel: Channel, field: 'auto_record' | 'auto_asr' | 'auto_recap' | 'auto_publish') {
  updating.value = true
  try {
    await updateChannel(channel.id, { ...toInput(channel), [field]: !channel[field] })
    await channelsStore.fetchChannels()
    if (selectedChannel.value?.id === channel.id) {
      selectedChannel.value = channelsStore.items.find((c) => c.id === channel.id) || null
    }
  } finally {
    updating.value = false
  }
}

async function handleRecapOverride(field: 'recap_model' | 'max_continuations' | 'publish_cover_url', value: string | number) {
  if (!selectedChannel.value) return
  updating.value = true
  try {
    await updateChannel(selectedChannel.value.id, { ...toInput(selectedChannel.value), [field]: value })
    await channelsStore.fetchChannels()
    if (selectedChannel.value?.id) {
      selectedChannel.value = channelsStore.items.find((c) => c.id === selectedChannel.value!.id) || null
    }
  } finally {
    updating.value = false
  }
}

// 封面专用保存：从本地 coverDraft 提交，避免 v-model 直接污染 store。
// 保存成功后 fetchChannels 刷新 store，并用返回值同步 draft（失败时 draft 保留用户输入不丢失）。
async function saveCover() {
  if (!selectedChannel.value) return
  const next = coverDraft.value.trim()
  if (next === (selectedChannel.value.publish_cover_url ?? '')) return
  updating.value = true
  try {
    await updateChannel(selectedChannel.value.id, { ...toInput(selectedChannel.value), publish_cover_url: next })
    await channelsStore.fetchChannels()
    if (selectedChannel.value?.id) {
      selectedChannel.value = channelsStore.items.find((c) => c.id === selectedChannel.value!.id) || null
      coverDraft.value = selectedChannel.value?.publish_cover_url ?? ''
    }
  } finally {
    updating.value = false
  }
}

async function handleDelete(channel: Channel) {
  try {
    await deleteChannel(channel.id)
    ElMessage.success('已删除')
    drawerVisible.value = false
    selectedChannel.value = null
    await channelsStore.fetchChannels()
  } catch {
    // 错误提示由 axios 拦截器统一处理
  }
}

function toInput(c: Channel): UpsertChannelInput {
  return {
    id: c.id, name: c.name, uid: c.uid, live_room_id: c.live_room_id,
    replay_source_url: c.replay_source_url, space_url: c.space_url,
    title_prefix: c.title_prefix, cookie_file: c.cookie_file,
    download_cookie_file: c.download_cookie_file, enabled: c.enabled,
    auto_record: c.auto_record, auto_asr: c.auto_asr, auto_recap: c.auto_recap,
    record_danmaku: c.record_danmaku, source_mode: c.source_mode,
    discover_limit: c.discover_limit, publish_enabled: c.publish_enabled,
    publish_mode: c.publish_mode, publish_category_id: c.publish_category_id,
    publish_list_id: c.publish_list_id, publish_private_pub: c.publish_private_pub,
    publish_original: c.publish_original, auto_publish: c.auto_publish,
    publish_aigc: c.publish_aigc, publish_timer_pub_time: c.publish_timer_pub_time,
    publish_cover_url: c.publish_cover_url, publish_topics: c.publish_topics,
    recap_model: c.recap_model, max_continuations: c.max_continuations,
    // 必须透传 download_account_id：后端 Update() 对缺失字段写 NULL，
    // 不带它会把已配置的下载账号关联静默清空（codex 审核发现）。
    download_account_id: c.download_account_id ?? null,
  }
}

function handleIdentifySuccess() {
  showIdentifyDialog.value = false
  channelsStore.fetchChannels()
}

// Open channel drawer from query param (redirected from /channels/:id)
// 注意:immediate watch 在 onMounted 的 fetchChannels 完成前触发,此时 store.items 可能为空,
// 直接 find 会错过 channel。改用 ensureLoaded 确保列表就绪后再取(并复用 inflight,不重复请求)。
watch(
  () => route.query.id,
  async (id) => {
    if (id) {
      const channel = await channelsStore.getByIdAfterLoad(String(id))
      if (channel) openDetail(channel)
      ElMessage.info('已跳转至主播详情')
    }
  },
  { immediate: true },
)

onMounted(async () => {
  // 用 ensureLoaded 而非 fetchChannels/fetchSessions:与 ?id watch 的 ensureLoaded 复用同一 inflight,
  // 避免 immediate watch 与 onMounted 并发各发一次 list 请求。
  await Promise.all([
    channelsStore.ensureLoaded(),
    sessionsStore.ensureLoaded(),
    // 强制刷新：绕过 30s 缓存，确保主播 cookie 状态反映最新账号池默认账号（用户可能在设置页刚改过）。
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

    <div v-if="filteredChannels.length === 0" class="empty-state">
      <el-empty description="还没有主播，点击上方添加">
        <el-button type="primary" @click="showIdentifyDialog = true">添加主播</el-button>
      </el-empty>
    </div>

    <div v-else class="card-grid">
      <div v-for="c in filteredChannels" :key="c.id" class="streamer-card" @click="openDetail(c)">
        <div class="card-top">
          <strong class="card-name">{{ c.name }}</strong>
          <span class="cookie-dot" :class="cookieStatus(c, runtimeStore.status ?? undefined)">
            {{ { ok: '已配置', missing: '无Cookie', unknown: '' }[cookieStatus(c, runtimeStore.status ?? undefined)] }}
          </span>
        </div>
        <div class="card-auto">
          <el-tag :type="autoType(c.auto_record)" size="small">录制{{ autoLabel(c.auto_record) }}</el-tag>
          <el-tag :type="autoType(c.auto_asr)" size="small">ASR{{ autoLabel(c.auto_asr) }}</el-tag>
          <el-tag :type="autoType(c.auto_recap)" size="small">回顾{{ autoLabel(c.auto_recap) }}</el-tag>
          <el-tag :type="autoType(c.auto_publish)" size="small">发布{{ autoLabel(c.auto_publish) }}</el-tag>
        </div>
        <div class="card-footer">
          <span class="last-session">最近场次: {{ lastSessionDate(c.id) }}</span>
        </div>
      </div>
    </div>

    <!-- Detail Drawer -->
    <el-drawer
      v-model="drawerVisible"
      :title="selectedChannel?.name || '主播详情'"
      direction="rtl"
      size="520px"
      :before-close="closeDetail"
    >
      <template v-if="selectedChannel">
        <!-- Recent Sessions -->
        <h4>最近场次</h4>
        <div v-if="channelSessions(selectedChannel.id).length > 0" class="drawer-sessions">
          <div
            v-for="s in channelSessions(selectedChannel.id)"
            :key="s.id"
            class="session-item"
            @click="goToRecap(s.id)"
          >
            <div class="session-left">
              <strong>{{ s.title || '无标题' }}</strong>
              <span>{{ formatDateTime(s.created_at) }}</span>
            </div>
            <el-tag
              :type="getFriendlySessionStatus(s).color === 'success' ? 'success' : getFriendlySessionStatus(s).color === 'danger' ? 'danger' : 'warning'"
              size="small"
            >
              {{ getFriendlySessionStatus(s).label }}
            </el-tag>
          </div>
        </div>
        <el-empty v-else description="暂无场次" :image-size="40" />

        <!-- Auto Switches -->
        <h4 style="margin-top: 24px;">自动化设置</h4>
        <div class="switch-group">
          <div class="switch-row">
            <span>自动录制</span>
            <el-switch :model-value="selectedChannel.auto_record" :loading="updating" @change="handleToggle(selectedChannel, 'auto_record')" />
          </div>
          <div class="switch-row">
            <span>自动 ASR</span>
            <el-switch :model-value="selectedChannel.auto_asr" :loading="updating" @change="handleToggle(selectedChannel, 'auto_asr')" />
          </div>
          <div class="switch-row">
            <span>自动回顾</span>
            <el-switch :model-value="selectedChannel.auto_recap" :loading="updating" @change="handleToggle(selectedChannel, 'auto_recap')" />
          </div>
          <div class="switch-row">
            <span>自动发布</span>
            <el-switch :model-value="selectedChannel.auto_publish" :loading="updating" @change="handleToggle(selectedChannel, 'auto_publish')" />
          </div>
        </div>

        <!-- Cookie -->
        <h4 style="margin-top: 24px;">Cookie 状态</h4>
        <div class="cookie-row">
          <span :class="['cookie-dot', cookieStatus(selectedChannel, runtimeStore.status ?? undefined)]">
            {{ { ok: 'Cookie 已配置', missing: '未配置 Cookie', unknown: '加载中…' }[cookieStatus(selectedChannel, runtimeStore.status ?? undefined)] }}
          </span>
          <el-button size="small" @click="openQRLogin(selectedChannel.id)">扫码登录</el-button>
          <el-popconfirm title="确定删除该主播？相关场次数据不会被删除。" @confirm="handleDelete(selectedChannel)" width="220">
            <template #reference>
              <el-button size="small" type="danger" style="margin-left: 8px">删除主播</el-button>
            </template>
          </el-popconfirm>
        </div>

        <!-- 术语表 / ASR 热词 -->
        <h4 style="margin-top: 24px;">术语表 / ASR 热词</h4>
        <el-collapse v-model="glossaryCollapse">
          <el-collapse-item
            name="glossary"
            title="管理主播术语表（启用的词条在使用 Fun-ASR 转写时作为热词生效）"
          >
            <GlossaryEditor
              v-if="glossaryCollapse.includes('glossary')"
              scope="channel"
              :channel-id="selectedChannel.id"
              :channel-name="selectedChannel.name"
              show-global-readonly
            />
          </el-collapse-item>
        </el-collapse>

        <!-- 回顾模板(主播级覆盖) -->
        <h4 style="margin-top: 24px;">回顾模板</h4>
        <el-collapse v-model="recapTemplateCollapse">
          <el-collapse-item
            name="recap-template"
            title="为主播单独定制回顾模板(留空字段跟随全局)"
          >
            <RecapTemplateEditor
              v-if="recapTemplateCollapse.includes('recap-template')"
              scope="channel"
              :channel-id="selectedChannel.id"
            />
          </el-collapse-item>
        </el-collapse>

        <!-- Expert: Full Config -->
        <template v-if="isExpert">
          <el-divider>回顾设置</el-divider>
          <div class="recap-override-form">
            <div class="switch-row">
              <span>回顾模型</span>
              <el-select
                v-model="selectedChannel.recap_model"
                size="small"
                clearable
                filterable
                allow-create
                placeholder="跟随全局"
                style="width: 180px"
                @change="handleRecapOverride('recap_model', $event || '')"
              >
                <el-option label="跟随全局" value="" />
                <el-option-group v-for="grp in recapModelGroups" :key="grp.name" :label="grp.name">
                  <el-option v-for="m in grp.models" :key="m.value" :label="m.label" :value="m.value" />
                </el-option-group>
              </el-select>
            </div>
            <div class="switch-row">
              <span>最大续写次数</span>
              <el-input-number
                :model-value="selectedChannel.max_continuations < 0 ? -1 : selectedChannel.max_continuations"
                size="small"
                :min="-1"
                :max="10"
                placeholder="-1=全局"
                @change="handleRecapOverride('max_continuations', $event)"
              />
            </div>
            <div class="override-hint">-1 表示跟随全局设置</div>
          </div>

          <el-divider>发布设置</el-divider>
          <div class="recap-override-form">
            <div class="switch-row">
              <span>自定义封面</span>
              <el-input
                v-model="coverDraft"
                size="small"
                clearable
                placeholder="跟随全局"
                style="width: 240px"
                @change="saveCover"
              />
            </div>
            <div class="override-hint">留空跟随全局；优先使用回顾目录封面，无回顾封面时才用此 URL 或本地路径（发布时自动上传）</div>
          </div>

          <el-divider>高级配置</el-divider>
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item label="ID">{{ selectedChannel.id }}</el-descriptions-item>
            <el-descriptions-item label="UID">{{ selectedChannel.uid || '-' }}</el-descriptions-item>
            <el-descriptions-item label="Room">{{ selectedChannel.live_room_id || '-' }}</el-descriptions-item>
            <el-descriptions-item label="来源模式">{{ selectedChannel.source_mode || 'both' }}</el-descriptions-item>
            <el-descriptions-item label="发现限制">{{ selectedChannel.discover_limit || '不限' }}</el-descriptions-item>
            <el-descriptions-item label="回顾模型">{{ selectedChannel.recap_model || '跟随全局' }}</el-descriptions-item>
            <el-descriptions-item label="最大续写">{{ selectedChannel.max_continuations >= 0 ? selectedChannel.max_continuations : '跟随全局' }}</el-descriptions-item>
            <el-descriptions-item label="弹幕录制">{{ selectedChannel.record_danmaku ? '开' : '关' }}</el-descriptions-item>
            <el-descriptions-item label="发布Cookie">{{ selectedChannel.cookie_file || '未配置' }}</el-descriptions-item>
            <el-descriptions-item label="下载Cookie">{{ selectedChannel.download_cookie_file || '未配置' }}</el-descriptions-item>
            <el-descriptions-item label="自定义封面">{{ selectedChannel.publish_cover_url || '跟随全局' }}</el-descriptions-item>
          </el-descriptions>
        </template>
      </template>
    </el-drawer>

    <ChannelIdentifyDialog v-model:visible="showIdentifyDialog" @success="handleIdentifySuccess" />
    <BiliQRCodeLoginDialog
      v-if="showQRDialog"
      v-model:visible="showQRDialog"
      :channel-id="qrChannelId"
      @saved="() => { ElMessage.success('Cookie 已更新'); channelsStore.fetchChannels() }"
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
  color: #303133;
}

.page-actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

.search-input {
  width: 200px;
}

.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 14px;
}

.streamer-card {
  padding: 16px;
  background: #fff;
  border: 1px solid #ebeef5;
  border-radius: 10px;
  cursor: pointer;
  transition: border-color 0.15s, box-shadow 0.15s;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.streamer-card:hover {
  border-color: #c6e2ff;
  box-shadow: 0 2px 12px rgb(0 0 0 / 6%);
}

.card-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.card-name {
  font-size: 16px;
}

.cookie-dot {
  font-size: 12px;
  font-weight: 500;
}

.cookie-dot.ok { color: #67c23a; }
.cookie-dot.missing { color: #f56c6c; }
.cookie-dot.unknown { color: #909399; }

.card-auto {
  display: flex;
  gap: 6px;
}

.card-footer {
  font-size: 12px;
  color: #909399;
}

/* Drawer */
h4 {
  margin: 0 0 12px;
  font-size: 14px;
  color: #303133;
}

.drawer-sessions {
  display: grid;
  gap: 8px;
}

.session-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  border: 1px solid #ebeef5;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s;
}

.session-item:hover {
  background: #f5f7fa;
}

.session-left {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
  flex: 1;
}

.session-left strong {
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-left span {
  font-size: 12px;
  color: #909399;
}

.switch-group {
  display: grid;
  gap: 12px;
}

.switch-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.cookie-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.empty-state {
  padding: 60px 0;
}

.recap-override-form {
  display: grid;
  gap: 12px;
}

.override-hint {
  font-size: 12px;
  color: #909399;
}

@media (max-width: 768px) {
  .page-header {
    flex-direction: column;
    align-items: stretch;
  }

  .search-input {
    width: 100%;
  }

  .card-grid {
    grid-template-columns: 1fr;
  }
}
</style>
