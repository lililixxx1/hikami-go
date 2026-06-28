<script setup lang="ts">
import '@/features/settings/components/settings-cards.css'
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, CircleCheck, CircleClose, ArrowDown, ArrowRight } from '@element-plus/icons-vue'
import { useRoute } from 'vue-router'
import { useRuntimeStore } from '@/stores/runtime'
import { useExpertMode } from '@/composables/useExpertMode'
import { listSecrets, updateSecret } from '@/api/settings'
import type { Capabilities, SecretEntry, ToolStatus } from '@/api/types'
import GlossaryEditor from '@/components/channel/GlossaryEditor.vue'
import RecapTemplateEditor from '@/components/channel/RecapTemplateEditor.vue'
import PublishSettingsCard from '@/features/settings/components/PublishSettingsCard.vue'
import DashScopeSettingsCard from '@/features/settings/components/DashScopeSettingsCard.vue'
import ASRS3SettingsCard from '@/features/settings/components/ASRS3SettingsCard.vue'
import RecapSettingsCard from '@/features/settings/components/RecapSettingsCard.vue'
import WebDAVSettingsCard from '@/features/settings/components/WebDAVSettingsCard.vue'
import ArchiveSettingsCard from '@/features/settings/components/ArchiveSettingsCard.vue'
import AdminTokenCard from '@/features/settings/components/AdminTokenCard.vue'
import BiliAccountsCard from '@/features/settings/components/BiliAccountsCard.vue'
import ConfigBackupCard from '@/features/settings/components/ConfigBackupCard.vue'

const route = useRoute()
const runtimeStore = useRuntimeStore()
const { isExpert } = useExpertMode()
// Secrets
const secrets = ref<SecretEntry[]>([])
const secretsLoading = ref(false)
const secretDialogVisible = ref(false)
const editingKey = ref('')
const editingValue = ref('')
const secretSaving = ref(false)

// Advanced sections
const showAdvanced = ref(false)

const keyMeta: Record<string, { label: string; description: string; helpUrl: string }> = {
  // ASR 密钥(DASHSCOPE_API_KEY)和回顾密钥(AI_API_KEY)已迁移到各自的设置卡片:
  // DashScopeSettingsCard / RecapSettingsCard,含完整字段配置与密钥清除。此处仅保留
  // 其他可能存在的通用密钥元信息(secrets store 中的其余 key 仍在此列表展示)。
}

type CapActionType = 'secret' | 'section' | 'hint'

const toolsList = computed(() => {
  if (!runtimeStore.status?.tools) return []
  return Object.values(runtimeStore.status.tools) as ToolStatus[]
})

const capabilities = computed(() => runtimeStore.status?.capabilities ?? null)
const configStatus = computed(() => runtimeStore.status?.config_status ?? null)
const dashScopeSecretKey = computed(() => configStatus.value?.dashscope_key_env || 'DASHSCOPE_API_KEY')

// 配置卡子组件引用(供配置导入后 reload)
const publishCard = ref<{ reload: () => Promise<void> } | null>(null)
const dashScopeCard = ref<{ reload: () => Promise<void> } | null>(null)
const asrS3Card = ref<{ reload: () => Promise<void> } | null>(null)
const recapCard = ref<{ reload: () => Promise<void> } | null>(null)
const webdavCard = ref<{ reload: () => Promise<void> } | null>(null)
const archiveCard = ref<{ reload: () => Promise<void> } | null>(null)
const biliAccountsCard = ref<{ reload: () => Promise<void> } | null>(null)

// 子配置卡保存后的统一处理:重拉 secrets,使顶部"API 密钥"卡片同步刷新
// (DashScope/Recap 卡片改了密钥后,通用密钥列表的 set/masked_value 才会更新)。
function onConfigSaved() {
  fetchSecrets()
}

const capItems = computed(() => {
  const caps = capabilities.value
  if (!caps) return []
  return [
    {
      key: 'asr_submit' as const,
      label: 'ASR 转写',
      ok: caps.asr_submit,
      reason: capReason('asr_submit', caps),
      // 密钥已配但能力仍红，根因通常是缺临时音频后端/yt-dlp，按钮指向后端配置指引而非密钥
      ...(configStatus.value?.dashscope_key_set
        ? { actionLabel: '配置 ASR 后端', actionType: 'hint' as CapActionType, actionTarget: 'asr_backend' }
        : { actionLabel: '配置密钥', actionType: 'secret' as CapActionType, actionTarget: dashScopeSecretKey.value }),
    },
    { key: 'recap_generate' as const, label: '回顾 AI', ok: caps.recap_generate, reason: capReason('recap_generate', caps), actionLabel: '配置回顾', actionType: 'section' as CapActionType, actionTarget: 'recap' },
    { key: 'webdav_upload' as const, label: 'WebDAV 上传', ok: caps.webdav_upload, reason: capReason('webdav_upload', caps), actionLabel: '配置 WebDAV', actionType: 'section' as CapActionType, actionTarget: 'webdav' },
    { key: 'publish_opus' as const, label: 'B站发布', ok: caps.publish_opus, reason: capReason('publish_opus', caps), actionLabel: '启用发布', actionType: 'section' as CapActionType, actionTarget: 'publish' },
  ]
})

const setupItems = computed(() => [
  {
    key: 'dashscope',
    label: 'ASR 转写',
    done: !!configStatus.value?.dashscope_key_set,
    actionLabel: '配置',
    action: () => scrollToSection('dashscope'),
  },
  {
    key: 'recap',
    label: '回顾 AI',
    done: !!capabilities.value?.recap_generate,
    actionLabel: '检查',
    action: () => scrollToSection('recap'),
  },
  {
    key: 'webdav',
    label: 'WebDAV 上传',
    done: !!capabilities.value?.webdav_upload,
    actionLabel: '配置',
    action: () => scrollToSection('webdav'),
  },
  {
    key: 'publish',
    label: 'B站发布',
    done: !!configStatus.value?.publish_enabled,
    actionLabel: '设置',
    action: () => scrollToSection('publish'),
  },
])

const setupDoneCount = computed(() => setupItems.value.filter(item => item.done).length)

function capReason(key: keyof Capabilities, caps: Capabilities): string {
  if (caps[key]) return ''
  if (key === 'asr_submit') return configStatus.value?.dashscope_key_set ? caps.reason : 'ASR 密钥未配置'
  if (key === 'recap_generate') return configStatus.value?.recap_key_set ? caps.reason : 'AI 密钥未配置'
  if (key === 'webdav_upload') return configStatus.value?.webdav_configured ? caps.reason : 'WebDAV 未配置'
  if (key === 'publish_opus') return configStatus.value?.publish_enabled ? caps.reason : '发布未启用'
  return caps.reason
}

onMounted(async () => {
  // publish/recap/webdav/bili/admin 各自在子组件 onMounted 加载;壳只负责 runtime/secrets
  await Promise.all([
    runtimeStore.fetchRuntime(),
    fetchSecrets(),
  ])
})

// Handle /health redirect -> /settings?section=runtime
watch(
  () => route.query.section,
  (section) => {
    if (section === 'runtime') {
      showAdvanced.value = true
    }
  },
  { immediate: true },
)

// Secrets
async function fetchSecrets() {
  secretsLoading.value = true
  try {
    const resp = await listSecrets()
    secrets.value = resp.items ?? []
  } finally {
    secretsLoading.value = false
  }
}

function openEdit(key: string) {
  editingKey.value = key
  editingValue.value = ''
  secretDialogVisible.value = true
}

async function saveSecret() {
  if (!editingValue.value.trim()) { ElMessage.warning('请输入密钥'); return }
  secretSaving.value = true
  try {
    await updateSecret(editingKey.value, editingValue.value.trim())
    ElMessage.success('已保存')
    secretDialogVisible.value = false
    await fetchSecrets()
    await runtimeStore.fetchRuntime(true)
  } finally {
    secretSaving.value = false
  }
}

async function clearKey(item: SecretEntry) {
  try {
    await ElMessageBox.confirm(`确认清除 ${label(item.key)}？`, '清除', {
      confirmButtonText: '清除', cancelButtonText: '取消', type: 'warning',
    })
  } catch { return }
  await updateSecret(item.key, '')
  ElMessage.success('已清除')
  await fetchSecrets()
  await runtimeStore.fetchRuntime(true)
}

function label(key: string): string {
  return keyMeta[key]?.label || key
}

function description(key: string): string {
  return keyMeta[key]?.description || ''
}

function helpUrl(key: string): string {
  return keyMeta[key]?.helpUrl || ''
}

function openHelp(url: string) {
  if (!url) return
  window.open(url, '_blank', 'noopener,noreferrer')
}

function scrollToSection(section: string) {
  const target = document.querySelector(`[data-section="${section}"]`)
  target?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function handleCapAction(item: { actionType: CapActionType; actionTarget: string }) {
  if (item.actionType === 'secret') {
    openEdit(item.actionTarget)
    return
  }
  if (item.actionType === 'hint') {
    if (item.actionTarget === 'asr_backend') {
      showASRBackendHint()
    }
    return
  }
  scrollToSection(item.actionTarget)
}

// ASR 转写能力红但密钥已配时，提示用户需配置临时音频发布后端（DashScope 通过 URL 拉取音频）+ yt-dlp
function showASRBackendHint() {
  const reason = capabilities.value?.reason || ''
  const needYtDlp = reason.includes('yt-dlp')
  ElMessageBox.alert(
    `ASR 密钥已配置，但转写还需要以下配置才能工作：\n\n${needYtDlp ? '① yt-dlp（下载 B站回放音频）：\n   pip install yt-dlp    （Windows: winget install yt-dlp）\n\n' : ''}② 临时音频发布后端（三选一，DashScope 需通过公网 URL 拉取音频）：\n\n方案 A：本地 HTTP 服务（需公网 IP，服务自动检测）\nasr_temp:\n  enabled: true\n  listen: ":6335"\n  local_dir: "./output/asr-temp"\n\n方案 B：S3 兼容对象存储（推荐，阿里云 OSS / MinIO）\nasr_s3:\n  endpoint: "https://oss-cn-xxx.aliyuncs.com"\n  bucket: "your-bucket"\n  access_key_id: "xxx"\n  access_key_secret: "xxx"\n  public_url_prefix: "https://your-bucket.xxx.aliyuncs.com/asr"\n\n方案 C：rclone 回退\nasr_temp:\n  rclone_remote: "your-remote:"\n  base_path: "asr/"\n  public_base_url: "https://你的公网URL/asr"\n\n修改 config.yaml 后重启服务生效。`,
    '配置 ASR 后端',
    { confirmButtonText: '我知道了', type: 'info' },
  ).catch(() => { /* 用户关闭弹窗 */ })
}

function copyInstallHint(hint: string) {
  navigator.clipboard.writeText(hint).then(() => ElMessage.success('已复制安装命令')).catch(() => ElMessage.error('复制失败'))
}

// 配置导入后(ConfigBackupCard emit imported)重拉所有配置
async function onConfigImported() {
  await Promise.all([
    fetchSecrets(),
    runtimeStore.fetchRuntime(true),
    publishCard.value?.reload(),
    dashScopeCard.value?.reload(),
    asrS3Card.value?.reload(),
    recapCard.value?.reload(),
    webdavCard.value?.reload(),
    archiveCard.value?.reload(),
    biliAccountsCard.value?.reload(),
  ])
}
</script>

<template>
  <div class="settings-page">
    <div class="page-header">
      <h2>设置</h2>
      <el-button size="small" :loading="runtimeStore.loading" @click="runtimeStore.fetchRuntime(true)">
        <el-icon><Refresh /></el-icon> 刷新状态
      </el-button>
    </div>

    <div class="settings-card setup-card">
      <div class="card-header-row">
        <h3>配置进度</h3>
        <el-tag size="small" :type="setupDoneCount === setupItems.length ? 'success' : 'warning'">
          {{ setupDoneCount }}/{{ setupItems.length }}
        </el-tag>
      </div>
      <div class="setup-list">
        <div v-for="item in setupItems" :key="item.key" class="setup-item" :class="{ done: item.done }">
          <el-icon :size="16"><CircleCheck v-if="item.done" /><CircleClose v-else /></el-icon>
          <span>{{ item.label }}</span>
          <el-button v-if="!item.done" link type="primary" size="small" @click="item.action">
            {{ item.actionLabel }}
          </el-button>
        </div>
      </div>
    </div>

    <!-- Card 1: System Status -->
    <div class="settings-card">
      <h3>系统状态</h3>
      <div class="cap-grid">
        <div v-for="item in capItems" :key="item.key" class="cap-item" :class="item.ok ? 'ok' : 'off'">
          <el-icon :size="18"><CircleCheck v-if="item.ok" /><CircleClose v-else /></el-icon>
          <div class="cap-text">
            <strong>{{ item.label }}</strong>
            <span>{{ item.ok ? '可用' : item.reason || '不可用' }}</span>
          </div>
          <el-button v-if="!item.ok" link type="danger" size="small" @click="handleCapAction(item)">
            {{ item.actionLabel }}
          </el-button>
        </div>
      </div>
      <div v-if="runtimeStore.status?.disk_usage?.length" class="disk-info">
        <span v-for="d in runtimeStore.status.disk_usage" :key="d.path" class="disk-item">
          {{ d.path }}: {{ d.used_percent.toFixed(0) }}%
        </span>
      </div>
    </div>

    <!-- Card 2: API Keys -->
    <div class="settings-card">
      <h3>API 密钥</h3>
      <div v-loading="secretsLoading" class="key-list">
        <div v-for="item in secrets" :key="item.key" class="key-row">
          <div class="key-info">
            <div class="key-title">
              <strong>{{ label(item.key) }}</strong>
              <span v-if="item.set" class="key-masked">{{ item.masked_value }}</span>
              <el-tag v-else type="danger" size="small">未配置</el-tag>
            </div>
            <span v-if="description(item.key)" class="key-description">{{ description(item.key) }}</span>
          </div>
          <div class="key-actions">
            <el-button v-if="helpUrl(item.key)" size="small" link type="primary" @click="openHelp(helpUrl(item.key))">获取</el-button>
            <el-button size="small" @click="openEdit(item.key)">{{ item.set ? '更新' : '配置' }}</el-button>
            <el-button v-if="item.set" size="small" type="danger" plain @click="clearKey(item)">清除</el-button>
          </div>
        </div>
      </div>
    </div>

    <BiliAccountsCard ref="biliAccountsCard" />

    <ConfigBackupCard @imported="onConfigImported" />
    <PublishSettingsCard ref="publishCard" :is-expert="isExpert" @saved="onConfigSaved" />
    <DashScopeSettingsCard ref="dashScopeCard" @saved="onConfigSaved" />
    <ASRS3SettingsCard ref="asrS3Card" @saved="onConfigSaved" />
    <RecapSettingsCard
      ref="recapCard"
      @saved="onConfigSaved"
    />
    <WebDAVSettingsCard ref="webdavCard" @saved="onConfigSaved" />
    <ArchiveSettingsCard ref="archiveCard" :is-expert="isExpert" @saved="onConfigSaved" />
    <AdminTokenCard />

    <!-- Advanced (collapsed) -->
    <div class="advanced-toggle" @click="showAdvanced = !showAdvanced">
      <el-icon><component :is="showAdvanced ? ArrowDown : ArrowRight" /></el-icon>
      <span>高级设置</span>
    </div>

    <template v-if="showAdvanced">
      <div class="settings-card">
        <h3>全局术语表</h3>
        <GlossaryEditor scope="global" />
      </div>
      <div class="settings-card">
        <h3>回顾模板</h3>
        <RecapTemplateEditor scope="global" />
      </div>
    </template>

    <!-- Expert: tools & config -->
    <template v-if="isExpert">
      <div class="settings-card">
        <h3>外部工具</h3>
        <el-table :data="toolsList" size="small" stripe>
          <el-table-column prop="name" label="工具" width="140" />
          <el-table-column prop="path" label="路径" min-width="200" show-overflow-tooltip />
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="row.available ? 'success' : 'danger'" size="small">
                {{ row.available ? '可用' : '缺失' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column v-if="toolsList.some((t) => t.install_hint)" label="安装提示" min-width="220">
            <template #default="{ row }">
              <div v-if="!row.available && row.install_hint" class="install-hint">
                <el-text type="info" size="small" style="font-family: monospace">
                  {{ row.install_hint }}
                </el-text>
                <el-button link size="small" type="primary" @click="copyInstallHint(row.install_hint)">
                  复制
                </el-button>
              </div>
            </template>
          </el-table-column>
        </el-table>
      </div>

      <div class="settings-card">
        <h3>配置状态</h3>
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="DashScope">
            <el-tag :type="configStatus?.dashscope_key_set ? 'success' : 'danger'" size="small">
              {{ configStatus?.dashscope_key_set ? '已配置' : '未配置' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="回顾 Provider">
            {{ configStatus?.recap_provider || '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="AI Key">
            <el-tag :type="configStatus?.recap_key_set ? 'success' : 'danger'" size="small">
              {{ configStatus?.recap_key_set ? '已配置' : '未配置' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="WebDAV">
            <el-tag :type="configStatus?.webdav_configured ? 'success' : 'danger'" size="small">
              {{ configStatus?.webdav_configured ? '已配置' : '未配置' }}
            </el-tag>
          </el-descriptions-item>
        </el-descriptions>
      </div>
    </template>

    <!-- Secret edit dialog -->
    <el-dialog v-model="secretDialogVisible" :title="`编辑 ${label(editingKey)}`" width="440px">
      <el-input v-model="editingValue" type="textarea" :rows="3" placeholder="输入 API Key" show-password />
      <template #footer>
        <el-button @click="secretDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="secretSaving" @click="saveSecret">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
/* 高级设置折叠入口(壳内专属,非共享) */
.advanced-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 0;
  cursor: pointer;
  color: #909399;
  font-size: 14px;
  user-select: none;
}

.advanced-toggle:hover {
  color: #409eff;
}

.settings-page {
  padding: 24px;
  max-width: 800px;
  margin: 0 auto;
  display: grid;
  gap: 16px;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.page-header h2 {
  margin: 0;
  font-size: 22px;
  color: #303133;
}

/* Setup checklist */
.setup-list {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}

.setup-item {
  min-height: 36px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: 1px solid #f1f3f5;
  border-radius: 8px;
  color: #909399;
  background: #fafafa;
}

.setup-item.done {
  color: #67c23a;
  background: #f0f9eb;
}

.setup-item span {
  min-width: 0;
  flex: 1;
  font-size: 13px;
}

/* Capability grid */
.cap-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.cap-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px;
  border-radius: 8px;
  border: 1px solid #ebeef5;
}

.cap-item.ok { background: #f0f9eb; }
.cap-item.off { background: #fef0f0; }

.cap-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.cap-text strong { font-size: 14px; }
.cap-text span { font-size: 12px; color: #909399; }
.cap-item.ok .cap-text strong { color: #67c23a; }
.cap-item.off .cap-text strong { color: #f56c6c; }

.disk-info {
  margin-top: 12px;
  display: flex;
  gap: 16px;
  font-size: 13px;
  color: #909399;
}

/* Keys */
.key-list {
  display: grid;
  gap: 12px;
}

.key-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 0;
  border-bottom: 1px solid #f0f0f0;
}

.key-row:last-child { border-bottom: none; }

.key-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.key-title {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.key-info strong {
  font-size: 14px;
}

.key-description {
  font-size: 12px;
  line-height: 1.5;
  color: #8a94a6;
}

.key-masked {
  font-size: 13px;
  color: #909399;
  font-family: monospace;
}

.key-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-shrink: 0;
}

/* Bili accounts */
.empty-hint {
  text-align: center;
  color: #909399;
  font-size: 14px;
  padding: 20px 0;
}

.account-list {
  display: grid;
  gap: 12px;
}

.account-card {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  gap: 16px;
}

.account-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.account-name {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
}

.account-uid {
  font-size: 12px;
  color: #909399;
  font-family: monospace;
}

.account-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}

/* Column-style form (B站专栏设置风格) */

@media (max-width: 600px) {
  .settings-page {
    padding: 16px;
  }

  .card-header-row {
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
  }

  .setup-list {
    grid-template-columns: 1fr;
  }

  .cap-grid {
    grid-template-columns: 1fr;
  }

  .form-row {
    flex-direction: column;
    align-items: flex-start;
  }

  .account-card {
    flex-direction: column;
    align-items: flex-start;
  }

  .account-actions {
    width: 100%;
    justify-content: flex-start;
    flex-wrap: wrap;
  }

  .key-row {
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
  }

  .key-actions {
    width: 100%;
    justify-content: flex-start;
    flex-wrap: wrap;
  }

  .publish-mode-control {
    width: 100%;
    align-items: flex-start;
  }

  .collapsed-settings {
    align-items: flex-start;
    flex-direction: column;
  }

  .column-row {
    grid-template-columns: 1fr;
    gap: 6px;
  }

  .column-label {
    line-height: 1.4;
  }

  .compact-control,
  .compact-number-control {
    max-width: none;
    width: 100%;
  }

  .column-actions {
    justify-content: stretch;
  }

  .column-actions .el-button {
    flex: 1;
  }
}
</style>
