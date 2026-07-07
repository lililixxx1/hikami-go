<script setup lang="ts">
// V10 设置页(Phase 5 Task 5.5)。
// 旧 EP 版(436 行,el-collapse 4 分组 + 内联总览卡 + 表格)重写为 V10 壳:
// 左 Sidebar + 右 content(PipelineBar + 14 张 V10 卡)。业务逻辑全保留——
// runtime 拉取/能力派生、?section 滚动、配置导入 reload、ASR 后端 hint、B站 QR 登录状态机。
// 14 张卡均为「受控展示」或「自加载卡」;壳只负责 runtime + accounts + QR 状态机 + 滚动 + reload。
import '@/features/settings/components-v10/settings-v10.css'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HConfirm, HAlert } from '@/components/ui/HConfirm'
import { useRoute } from 'vue-router'
import { useRuntimeStore } from '@/stores/runtime'
import { useExpertMode } from '@/composables/useExpertMode'
import { usePolling } from '@/composables/usePolling'
import {
  cancelQRCodeSession,
  createQRCodeSession,
  deleteBiliAccount,
  listBiliAccounts,
  pollQRCodeSession,
  saveQRCodeToAccount,
  updateBiliAccount,
} from '@/api/bili'
import type { BiliCookieAccount, QRCodePollResult, QRCodeSession, ToolStatus } from '@/api/types'
import Sidebar from '@/features/settings/components-v10/Sidebar.vue'
import PipelineBar from '@/features/settings/components-v10/PipelineBar.vue'
import OverviewCard from '@/features/settings/components-v10/OverviewCard.vue'
import DashScopeCardV10 from '@/features/settings/components-v10/DashScopeCardV10.vue'
import ASRS3CardV10 from '@/features/settings/components-v10/ASRS3CardV10.vue'
import RecapCardV10 from '@/features/settings/components-v10/RecapCardV10.vue'
import WebDAVCardV10 from '@/features/settings/components-v10/WebDAVCardV10.vue'
import PublishCardV10 from '@/features/settings/components-v10/PublishCardV10.vue'
import ArchiveCardV10 from '@/features/settings/components-v10/ArchiveCardV10.vue'
import TemplateCardV10 from '@/features/settings/components-v10/TemplateCardV10.vue'
import GlossaryCardV10 from '@/features/settings/components-v10/GlossaryCardV10.vue'
import AccountsCardV10 from '@/features/settings/components-v10/AccountsCardV10.vue'
import AdminTokenCardV10 from '@/features/settings/components-v10/AdminTokenCardV10.vue'
import BackupCardV10 from '@/features/settings/components-v10/BackupCardV10.vue'
import ToolsCardV10 from '@/features/settings/components-v10/ToolsCardV10.vue'

interface SidebarSection {
  id: string
  label: string
  group: string
  done?: boolean
}

const route = useRoute()
const runtimeStore = useRuntimeStore()
const { isExpert } = useExpertMode()

const capabilities = computed(() => runtimeStore.status?.capabilities ?? null)
const configStatus = computed(() => runtimeStore.status?.config_status ?? null)
const toolsList = computed<ToolStatus[]>(() => {
  if (!runtimeStore.status?.tools) return []
  return Object.values(runtimeStore.status.tools)
})

// ── Sidebar sections(4 分组,done 派生自 config_status / accounts) ──
// PipelineBar 的 navigate target 与本处 id 对齐(accounts/dashscope/asr-s3/recap/webdav/publish)。
const sections = computed<SidebarSection[]>(() => {
  const cs = configStatus.value
  return [
    { id: 'overview', label: '能力总览', group: '总览' },
    { id: 'dashscope', label: 'ASR 转写', group: '流水线配置', done: !!cs?.dashscope_key_set },
    { id: 'asr-s3', label: '临时音频', group: '流水线配置', done: !!cs?.asr_temp_configured },
    { id: 'recap', label: '回顾 AI', group: '流水线配置', done: !!cs?.recap_key_set },
    { id: 'webdav', label: 'WebDAV 上传', group: '流水线配置', done: !!cs?.webdav_configured },
    { id: 'publish', label: 'B站发布', group: '流水线配置', done: !!cs?.publish_enabled },
    { id: 'archive', label: '归档', group: '流水线配置' },
    { id: 'template', label: '回顾模板', group: '流水线配置' },
    { id: 'glossary', label: '全局术语表', group: '流水线配置', done: !!cs?.glossary_configured },
    { id: 'accounts', label: 'B站账号', group: '账号与备份', done: accounts.value.length > 0 },
    { id: 'admin-token', label: '管理员令牌', group: '账号与备份' },
    { id: 'backup', label: '配置备份', group: '账号与备份' },
    { id: 'tools', label: '外部工具', group: '高级' },
  ]
})

// 当前激活 section(点击 sidebar 后高亮)
const activeSection = ref<string>('overview')

// ── 滚动到 section(PipelineBar / OverviewCard / Sidebar 共用) ──
// OverviewCard 的 hint 情况会 emit navigate('asr_backend'):此时不滚动,弹 ASR 后端配置提示。
async function scrollToSection(id: string) {
  activeSection.value = id
  if (id === 'asr_backend') {
    showASRBackendHint()
    return
  }
  document.querySelector(`[data-section="${id}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

// ── B站账号(壳持有列表,AccountsCardV10 受控展示) ──
const accounts = ref<BiliCookieAccount[]>([])

async function fetchAccounts() {
  try {
    accounts.value = await listBiliAccounts()
  } catch { /* error shown by interceptor */ }
}

// ── B站 QR 登录状态机(壳持有;AccountsCardV10 受控,emit 驱动) ──
// 不复用 useBiliQRCodeLogin:其为 dialog 驱动(watch visible→startLogin + onSessionReady canvas 回调),
// 与 AccountsCardV10 的「自带 canvas + emit generate-qr/poll/save-qr」契约不匹配,故在此用 bili API +
// usePolling 复刻状态机(session/pollResult/2s 轮询/账号保存)。逻辑等价,接口更贴合受控组件。
const qrSession = ref<QRCodeSession | null>(null)
const pollResult = ref<QRCodePollResult | null>(null)
const qrLoading = ref(false)
const qrSaving = ref(false)

const { start: startPollTimer, stop: stopPollTimer } = usePolling(() => pollQR(), {
  interval: 2000,
  immediate: false,
})

async function generateQR() {
  qrLoading.value = true
  try {
    stopPollTimer()
    qrSession.value = await createQRCodeSession()
    pollResult.value = null
    await pollQR()
    startPollTimer()
  } catch { /* error shown by interceptor */ }
  finally {
    qrLoading.value = false
  }
}

async function pollQR() {
  if (!qrSession.value) return
  if (new Date(qrSession.value.expires_at).getTime() <= Date.now()) {
    stopPollTimer()
    return
  }
  try {
    pollResult.value = await pollQRCodeSession(qrSession.value.session_id)
  } catch { /* error shown by interceptor */ }
}

async function saveQR(nickname: string) {
  if (!qrSession.value || pollResult.value?.status !== 'succeeded') return
  qrSaving.value = true
  try {
    await saveQRCodeToAccount(qrSession.value.session_id, nickname || undefined)
    HMessage.success('账号已保存')
    stopPollTimer()
    qrSession.value = null
    pollResult.value = null
    await fetchAccounts()
  } catch { /* error shown by interceptor */ }
  finally {
    qrSaving.value = false
  }
}

async function onSetDefault(id: number, usage: 'download' | 'publish') {
  try {
    await updateBiliAccount(id, usage === 'download' ? { is_default_download: true } : { is_default_publish: true })
    await fetchAccounts()
  } catch { /* error shown by interceptor */ }
}

async function onDeleteAccount(id: number) {
  const acc = accounts.value.find(a => a.id === id)
  if (!(await HConfirm(
    `确认删除账号「${acc?.nickname || String(acc?.uid ?? id)}」？`,
    { title: '删除账号', confirmText: '删除', cancelText: '取消', type: 'warning' },
  ))) return
  try {
    await deleteBiliAccount(id)
    HMessage.success('已删除')
    await fetchAccounts()
  } catch { /* error shown by interceptor */ }
}

// 离开页面:停止轮询 + 尽力取消未完成会话(防 session 泄漏)
onBeforeUnmount(() => {
  stopPollTimer()
  const current = qrSession.value
  qrSession.value = null
  if (current && pollResult.value?.status !== 'succeeded') {
    cancelQRCodeSession(current.session_id).catch(() => { /* 尽力而为 */ })
  }
})

// ── 配置卡 saved:重拉 runtime,使总览卡/PipelineBar 能力状态同步刷新 ──
function onSaved() {
  runtimeStore.fetchRuntime(true)
}

// ── 配置导入 reload:re-key 所有「onMounted 自加载」配置卡 → 强制重挂 → 重新拉取 ──
// 比逐卡调用 reload() ref 更可靠(无需为每卡维护模板 ref);AccountsCardV10 受控,直接重拉 accounts。
const reloadKey = ref(0)

async function onImported() {
  await runtimeStore.fetchRuntime(true)
  await fetchAccounts()
  reloadKey.value++
}

// ASR 转写能力红但密钥已配时,提示用户需配置临时音频发布后端 + yt-dlp(移植自旧 SettingsView)
function showASRBackendHint() {
  const reason = capabilities.value?.reason || ''
  const needYtDlp = reason.includes('yt-dlp')
  HAlert(
    `ASR 密钥已配置，但转写还需要以下配置才能工作：\n\n${needYtDlp ? '① yt-dlp（下载 B站回放音频）：\n   pip install yt-dlp    （Windows: winget install yt-dlp）\n\n' : ''}② 临时音频发布后端（三选一，DashScope 需通过公网 URL 拉取音频）：\n\n方案 A：本地 HTTP 服务（需公网 IP，服务自动检测）\nasr_temp:\n  enabled: true\n  listen: ":6335"\n  local_dir: "./output/asr-temp"\n\n方案 B：S3 兼容对象存储（推荐，阿里云 OSS / MinIO）\nasr_s3:\n  endpoint: "https://oss-cn-xxx.aliyuncs.com"\n  bucket: "your-bucket"\n  access_key_id: "xxx"\n  access_key_secret: "xxx"\n  public_url_prefix: "https://your-bucket.xxx.aliyuncs.com/asr"\n\n方案 C：rclone 回退\nasr_temp:\n  rclone_remote: "your-remote:"\n  base_path: "asr/"\n  public_base_url: "https://你的公网URL/asr"\n\n修改 config.yaml 后重启服务生效。`,
    { title: '配置 ASR 后端', confirmText: '我知道了', type: 'info' },
  )
}

onMounted(async () => {
  // 配置卡各自 onMounted 自加载;壳负责 runtime + accounts
  await Promise.all([
    runtimeStore.fetchRuntime(),
    fetchAccounts(),
  ])
})

// ?section=xxx → 滚动到对应 section(保留旧路由跳转能力)
watch(
  () => route.query.section,
  (section) => {
    if (section) scrollToSection(String(section))
  },
  { immediate: true },
)
</script>

<template>
  <div class="settings-v10">
    <Sidebar :sections="sections" :active-id="activeSection" @navigate="scrollToSection" />

    <main class="settings-content">
      <PipelineBar :capabilities="capabilities" :config-status="configStatus" @navigate="scrollToSection" />

      <section data-section="overview">
        <OverviewCard :capabilities="capabilities" :config-status="configStatus" @navigate="scrollToSection" />
      </section>

      <section data-section="dashscope">
        <DashScopeCardV10 :key="`dashscope-${reloadKey}`" @saved="onSaved" />
      </section>
      <section data-section="asr-s3">
        <ASRS3CardV10 :key="`asrs3-${reloadKey}`" @saved="onSaved" />
      </section>
      <section data-section="recap">
        <RecapCardV10 :key="`recap-${reloadKey}`" @saved="onSaved" />
      </section>
      <section data-section="webdav">
        <WebDAVCardV10 :key="`webdav-${reloadKey}`" @saved="onSaved" />
      </section>
      <section data-section="publish">
        <PublishCardV10 :key="`publish-${reloadKey}`" :is-expert="isExpert" @saved="onSaved" />
      </section>
      <section data-section="archive">
        <ArchiveCardV10 :key="`archive-${reloadKey}`" @saved="onSaved" />
      </section>
      <section data-section="template">
        <TemplateCardV10 :key="`template-${reloadKey}`" @saved="onSaved" />
      </section>
      <section data-section="glossary">
        <GlossaryCardV10 :key="`glossary-${reloadKey}`" @saved="onSaved" />
      </section>

      <section data-section="accounts">
        <AccountsCardV10
          :accounts="accounts"
          :qr-session="qrSession"
          :poll-result="pollResult"
          :qr-loading="qrLoading"
          :qr-saving="qrSaving"
          @generate-qr="generateQR"
          @poll="pollQR"
          @save-qr="saveQR"
          @set-default="onSetDefault"
          @delete="onDeleteAccount"
          @reload="fetchAccounts"
        />
      </section>
      <section data-section="admin-token">
        <AdminTokenCardV10 />
      </section>
      <section data-section="backup">
        <BackupCardV10 @imported="onImported" />
      </section>

      <section data-section="tools">
        <ToolsCardV10 :tools="toolsList" />
      </section>
    </main>
  </div>
</template>

<style scoped>
/* V10 页面结构:左侧 sidebar(固定宽,由 settings-v10.css 定样式)+ 右侧 content。
   settings-v10.css 只定义 sidebar/pipeline/card 内部类,这里补顶层 flex 分栏。 */
.settings-v10 {
  display: flex;
  align-items: flex-start;
  min-height: 100%;
}

.settings-content {
  flex: 1;
  min-width: 0;
  padding: 24px 28px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}

@media (max-width: 860px) {
  .settings-content {
    padding: 16px;
  }
}
</style>
