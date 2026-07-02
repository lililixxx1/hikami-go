<script setup lang="ts">
import '@/features/settings/components/settings-cards.css'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, CircleCheck, CircleClose } from '@element-plus/icons-vue'
import { useRoute } from 'vue-router'
import { useRuntimeStore } from '@/stores/runtime'
import { useExpertMode } from '@/composables/useExpertMode'
import type { Capabilities, ToolStatus } from '@/api/types'
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

// 顶层折叠分组(默认展开 overview + pipeline,收起 accounts + advanced)
// name 加 grp- 前缀避免与子卡片内部 collapse-item 常用的 name="advanced" 混淆
const activeGroups = ref<string[]>(['grp-overview', 'grp-pipeline'])

type CapActionType = 'section' | 'hint'

const toolsList = computed(() => {
  if (!runtimeStore.status?.tools) return []
  return Object.values(runtimeStore.status.tools) as ToolStatus[]
})

const capabilities = computed(() => runtimeStore.status?.capabilities ?? null)
const configStatus = computed(() => runtimeStore.status?.config_status ?? null)

// 配置卡子组件引用(供配置导入后 reload)
const publishCard = ref<{ reload: () => Promise<void> } | null>(null)
const dashScopeCard = ref<{ reload: () => Promise<void> } | null>(null)
const asrS3Card = ref<{ reload: () => Promise<void> } | null>(null)
const recapCard = ref<{ reload: () => Promise<void> } | null>(null)
const webdavCard = ref<{ reload: () => Promise<void> } | null>(null)
const archiveCard = ref<{ reload: () => Promise<void> } | null>(null)
const biliAccountsCard = ref<{ reload: () => Promise<void> } | null>(null)

// 子配置卡保存后重拉 runtime,使总览卡的能力状态同步刷新
function onConfigSaved() {
  runtimeStore.fetchRuntime(true)
}

// 统一总览项(合并原"配置进度"+"系统状态")。每项含完成度 + 能力红绿灯 + 跳转动作。
const overviewItems = computed(() => {
  const caps = capabilities.value
  const cs = configStatus.value
  if (!caps) return []
  return [
    {
      key: 'asr' as const,
      label: 'ASR 转写',
      done: !!cs?.dashscope_key_set,
      ok: caps.asr_submit,
      reason: capReason('asr_submit', caps),
      // 密钥已配但能力仍红,根因通常是缺临时音频后端/yt-dlp,指向 hint 而非跳卡片
      ...(cs?.dashscope_key_set
        ? { actionLabel: '配置 ASR 后端', actionType: 'hint' as CapActionType, actionTarget: 'asr_backend' }
        : { actionLabel: '配置', actionType: 'section' as CapActionType, actionTarget: 'dashscope' }),
    },
    {
      key: 'recap' as const,
      label: '回顾 AI',
      done: !!caps.recap_generate,
      ok: caps.recap_generate,
      reason: capReason('recap_generate', caps),
      actionLabel: '配置',
      actionType: 'section' as CapActionType,
      actionTarget: 'recap',
    },
    {
      key: 'webdav' as const,
      label: 'WebDAV 上传',
      done: !!cs?.webdav_configured,
      ok: caps.webdav_upload,
      reason: capReason('webdav_upload', caps),
      actionLabel: '配置',
      actionType: 'section' as CapActionType,
      actionTarget: 'webdav',
    },
    {
      key: 'publish' as const,
      label: 'B站发布',
      done: !!cs?.publish_enabled,
      ok: caps.publish_opus,
      reason: capReason('publish_opus', caps),
      actionLabel: '配置',
      actionType: 'section' as CapActionType,
      actionTarget: 'publish',
    },
  ]
})

const overviewDoneCount = computed(() => overviewItems.value.filter(i => i.done).length)

function capReason(key: keyof Capabilities, caps: Capabilities): string {
  if (caps[key]) return ''
  if (key === 'asr_submit') return configStatus.value?.dashscope_key_set ? caps.reason : 'ASR 密钥未配置'
  if (key === 'recap_generate') return configStatus.value?.recap_key_set ? caps.reason : 'AI 密钥未配置'
  if (key === 'webdav_upload') return configStatus.value?.webdav_configured ? caps.reason : 'WebDAV 未配置'
  if (key === 'publish_opus') return configStatus.value?.publish_enabled ? caps.reason : '发布未启用'
  return caps.reason
}

onMounted(async () => {
  // 各配置卡在子组件 onMounted 自加载;壳只负责 runtime
  await runtimeStore.fetchRuntime()
})

// Handle /health redirect -> ?section=runtime:系统状态已并入 overview 分组,展开它即可
watch(
  () => route.query.section,
  (section) => {
    if (section === 'runtime' && !activeGroups.value.includes('grp-overview')) {
      activeGroups.value = [...activeGroups.value, 'grp-overview']
    }
  },
  { immediate: true },
)

// scroll 目标 → 所属折叠分组(用于跨分组跳转前自动展开)
const groupOf: Record<string, string> = {
  dashscope: 'grp-pipeline',
  'asr-s3': 'grp-pipeline',
  recap: 'grp-pipeline',
  webdav: 'grp-pipeline',
  publish: 'grp-pipeline',
  archive: 'grp-pipeline',
}

async function scrollToSection(section: string) {
  const group = groupOf[section]
  if (group && !activeGroups.value.includes(group)) {
    activeGroups.value = [...activeGroups.value, group]
    await nextTick()
    // el-collapse 展开有 ~300ms 高度过渡,nextTick 只等 DOM patch 不等 transitionend;
    // 不等的话 scrollIntoView 在目标高度 0→auto 过渡中会定位偏。
    await new Promise(r => setTimeout(r, 320))
  }
  document.querySelector(`[data-section="${section}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function handleOverviewAction(item: { actionType: CapActionType; actionTarget: string }) {
  if (item.actionType === 'hint') {
    if (item.actionTarget === 'asr_backend') showASRBackendHint()
    return
  }
  scrollToSection(item.actionTarget)
}

// ASR 转写能力红但密钥已配时,提示用户需配置临时音频发布后端(DashScope 通过 URL 拉取音频)+ yt-dlp
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

// 配置导入后(ConfigBackupCard emit imported)重拉 runtime + 各配置卡
async function onConfigImported() {
  await Promise.all([
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

    <el-collapse v-model="activeGroups" class="settings-groups">
      <!-- 分组 1: 总览(默认展开) -->
      <el-collapse-item name="grp-overview" title="总览">
        <div class="settings-card overview-card">
          <div class="card-header-row">
            <h3>配置进度</h3>
            <el-tag size="small" :type="overviewDoneCount === overviewItems.length ? 'success' : 'warning'">
              {{ overviewDoneCount }}/{{ overviewItems.length }}
            </el-tag>
          </div>

          <div class="cap-grid">
            <div v-for="item in overviewItems" :key="item.key" class="cap-item" :class="item.ok ? 'ok' : 'off'">
              <el-icon :size="18"><CircleCheck v-if="item.ok" /><CircleClose v-else /></el-icon>
              <div class="cap-text">
                <strong>{{ item.label }}</strong>
                <span>{{ item.ok ? '可用' : item.reason || '不可用' }}</span>
              </div>
              <el-button v-if="!item.ok" link :type="item.ok ? 'primary' : 'danger'" size="small" @click="handleOverviewAction(item)">
                {{ item.actionLabel }}
              </el-button>
            </div>
          </div>

          <div v-if="runtimeStore.status?.disk_usage?.length" class="disk-info">
            <span v-for="d in runtimeStore.status.disk_usage" :key="d.path" class="disk-item">
              {{ d.path }}: {{ d.used_percent.toFixed(0) }}%
            </span>
          </div>

          <!-- 专家段:仍门控,不泄露给非专家 -->
          <template v-if="isExpert">
            <el-divider content-position="left" class="expert-divider">配置状态</el-divider>
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
          </template>
        </div>
      </el-collapse-item>

      <!-- 分组 2: 流水线配置(默认展开) -->
      <el-collapse-item name="grp-pipeline" title="流水线配置">
        <DashScopeSettingsCard ref="dashScopeCard" @saved="onConfigSaved" />
        <ASRS3SettingsCard ref="asrS3Card" @saved="onConfigSaved" />
        <RecapSettingsCard ref="recapCard" @saved="onConfigSaved" />
        <WebDAVSettingsCard ref="webdavCard" @saved="onConfigSaved" />
        <PublishSettingsCard ref="publishCard" :is-expert="isExpert" @saved="onConfigSaved" />
        <ArchiveSettingsCard ref="archiveCard" :is-expert="isExpert" @saved="onConfigSaved" />
      </el-collapse-item>

      <!-- 分组 3: 账号与备份(默认收起) -->
      <el-collapse-item name="grp-accounts" title="账号与备份">
        <BiliAccountsCard ref="biliAccountsCard" />
        <AdminTokenCard />
        <ConfigBackupCard @imported="onConfigImported" />
      </el-collapse-item>

      <!-- 分组 4: 高级(默认收起) -->
      <el-collapse-item name="grp-advanced" title="高级设置">
        <div class="settings-card">
          <h3>全局术语表</h3>
          <GlossaryEditor scope="global" />
        </div>
        <div class="settings-card">
          <h3>回顾模板</h3>
          <RecapTemplateEditor scope="global" />
        </div>

        <!-- Expert: tools -->
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
        </template>
      </el-collapse-item>
    </el-collapse>
  </div>
</template>

<style scoped>
.settings-page {
  padding: 24px;
  max-width: 960px;
  margin: 0 auto;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.page-header h2 {
  margin: 0;
  font-size: 22px;
  color: #303133;
}

/* 顶层折叠分组容器 */
.settings-groups {
  border-top: none;
  border-bottom: none;
}

/* 分组项之间留白 */
.settings-groups :deep(.el-collapse-item) {
  margin-bottom: 8px;
}

/* 分组标题:比卡片内 collapse 更醒目 */
.settings-groups :deep(.el-collapse-item__header) {
  font-size: 15px;
  font-weight: 600;
  color: #303133;
  height: 44px;
  line-height: 44px;
  padding: 0 4px;
}

.settings-groups :deep(.el-collapse-item__wrap) {
  background: transparent;
}

/* 分组内容区:卡片之间纵向间距 */
.settings-groups :deep(.el-collapse-item__content) {
  padding: 12px 4px 8px;
  display: grid;
  gap: 16px;
}

/* Capability grid(总览卡内) */
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

.expert-divider {
  margin: 16px 0 12px;
}

.install-hint {
  display: flex;
  align-items: center;
  gap: 8px;
}

@media (max-width: 600px) {
  .settings-page {
    padding: 16px;
  }

  .page-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
  }

  .cap-grid {
    grid-template-columns: 1fr;
  }
}
</style>
