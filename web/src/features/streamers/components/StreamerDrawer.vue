<!-- web/src/features/streamers/components/StreamerDrawer.vue -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Channel, Session, RuntimeStatus } from '@/api/types-derived'
import { HDrawer, HEmpty, HCollapse, HCollapseItem, HInput, HPill, HButton, HCombobox } from '@/components/ui'
import { getFriendlySessionStatus } from '@/utils/friendlyStatus'
import { formatDateTime } from '@/utils/format'
import type { CookieStatus as CookieStatusValue, AutoToggleField, RecapOverrideField } from '../composables/useStreamerDetail'
import AutoSwitches from './AutoSwitches.vue'
import CookieStatus from './CookieStatus.vue'
import ChannelAdvancedConfig from './ChannelAdvancedConfig.vue'
// 复用术语表/模板编辑器(Phase 6 已迁移为 H* 实现)
import GlossaryEditor from '@/components/channel/GlossaryEditor.vue'
import RecapTemplateEditor from '@/components/channel/RecapTemplateEditor.vue'

// 详情抽屉:纯展示 + emit。壳拥有 useStreamerDetail composable,本组件只把用户动作转发给壳。
// cookieStatus 由 channel + runtime 现场计算(逻辑与 useStreamerDetail 一致,无需 store)。
const props = defineProps<{
  visible: boolean
  channel: Channel | null
  runtime: RuntimeStatus | null
  isExpert: boolean
  updating: boolean
  recapModelGroups: { name: string; models: { value: string; label: string }[] }[]
  recentSessions: Session[]
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  'open-recap': [sid: string]
  'qr-login': [cid: string]
  toggle: [field: AutoToggleField]
  'recap-override': [field: RecapOverrideField, value: string | number]
  'save-cover': [value: string]
  delete: []
  reload: []
}>()

// cookieStatus 现场计算(与 useStreamerDetail.cookieStatus 同逻辑;runtime 未加载 → unknown)
const cookieStatus = computed<CookieStatusValue>(() => {
  const c = props.channel
  if (!c) return 'unknown'
  if (c.cookie_file || c.download_cookie_file) return 'ok'
  const rt = props.runtime
  if (!rt) return 'unknown'
  if (rt.has_default_download || rt.has_default_publish) return 'ok'
  return 'missing'
})

// 术语表 / 回顾模板面板懒加载:仅在展开 collapse 时挂载 EP 编辑器并请求主播级数据
const glossaryOpen = ref<string[]>([])
const recapTemplateOpen = ref<string[]>([])

// 本地编辑草稿(封面 / 回顾模型 / 续写次数):避免 v-model 直接污染 props.channel,
// 用户点"应用/保存"才 emit 给壳调 API。抽屉打开或切换主播时从 channel 同步。
const coverDraft = ref('')
const recapModelDraft = ref('')
const maxContinuationsDraft = ref('')
watch(
  () => [props.visible, props.channel?.id] as const,
  ([vis]) => {
    if (vis && props.channel) {
      const c = props.channel
      coverDraft.value = c.publish_cover_url ?? ''
      recapModelDraft.value = c.recap_model ?? ''
      maxContinuationsDraft.value = String(c.max_continuations < 0 ? -1 : c.max_continuations)
    }
  },
  { immediate: true },
)

// recap_model HCombobox 选项:跟随全局(空)+ 各分组模型;清空即回到"跟随全局"
const recapOptions = computed(() => {
  const base = [{ label: '跟随全局', value: '' }]
  for (const grp of props.recapModelGroups) {
    for (const m of grp.models) base.push({ label: m.label, value: m.value })
  }
  return base
})

// friendlyStatus 颜色 → HPill variant
function pillVariant(color: string): 'success' | 'warning' | 'danger' | 'info' | 'neutral' {
  if (color === 'success') return 'success'
  if (color === 'danger') return 'danger'
  if (color === 'warning') return 'warning'
  if (color === 'info') return 'info'
  return 'neutral'
}

function onOpenRecap(sid: string) {
  emit('open-recap', sid)
}

function onQrLogin() {
  if (props.channel) emit('qr-login', props.channel.id)
}

function onSaveCover() {
  emit('save-cover', coverDraft.value)
}

// 应用回顾设置:仅当草稿与当前值不同时才 emit 对应字段(避免无变化的多余请求)
function applyRecapOverrides() {
  const c = props.channel
  if (!c) return
  if (recapModelDraft.value !== (c.recap_model ?? '')) {
    emit('recap-override', 'recap_model', recapModelDraft.value)
  }
  const cur = c.max_continuations < 0 ? -1 : c.max_continuations
  const draftN = maxContinuationsDraft.value === '' ? -1 : Number(maxContinuationsDraft.value)
  const nextN = Number.isNaN(draftN) ? -1 : draftN
  if (nextN !== cur) {
    emit('recap-override', 'max_continuations', nextN)
  }
}
</script>

<template>
  <HDrawer
    :visible="visible"
    :title="channel?.name || '主播详情'"
    size="560px"
    @update:visible="emit('update:visible', $event)"
  >
    <template v-if="channel">
      <!-- 最近场次 -->
      <section class="detail-section">
        <h4 class="detail-section-title">最近场次</h4>
        <div v-if="recentSessions.length > 0" class="session-list">
          <div
            v-for="s in recentSessions"
            :key="s.id"
            class="session-item"
            @click="onOpenRecap(s.id)"
          >
            <div class="session-left">
              <strong>{{ s.title || '无标题' }}</strong>
              <span>{{ formatDateTime(s.created_at) }}</span>
            </div>
            <HPill :variant="pillVariant(getFriendlySessionStatus(s).color)">
              {{ getFriendlySessionStatus(s).label }}
            </HPill>
          </div>
        </div>
        <HEmpty v-else description="暂无场次" />
      </section>

      <!-- 自动化设置 -->
      <section class="detail-section">
        <h4 class="detail-section-title">自动化设置</h4>
        <AutoSwitches :channel="channel" :updating="updating" @toggle="emit('toggle', $event)" />
      </section>

      <!-- Cookie 状态 -->
      <section class="detail-section">
        <h4 class="detail-section-title">Cookie 状态</h4>
        <CookieStatus
          :status="cookieStatus"
          @qr-login="onQrLogin"
          @delete="emit('delete')"
        />
      </section>

      <!-- 术语表 -->
      <section class="detail-section">
        <HCollapse v-model="glossaryOpen">
          <HCollapseItem name="glossary" title="术语表 / ASR 热词">
            <GlossaryEditor
              v-if="glossaryOpen.includes('glossary')"
              scope="channel"
              :channel-id="channel.id"
              :channel-name="channel.name"
              show-global-readonly
            />
          </HCollapseItem>
        </HCollapse>
      </section>

      <!-- 回顾模板 -->
      <section class="detail-section">
        <HCollapse v-model="recapTemplateOpen">
          <HCollapseItem name="recap-template" title="回顾模板(主播级覆盖)">
            <RecapTemplateEditor
              v-if="recapTemplateOpen.includes('recap-template')"
              scope="channel"
              :channel-id="channel.id"
            />
          </HCollapseItem>
        </HCollapse>
      </section>

      <!-- 专家区 -->
      <template v-if="isExpert">
        <section class="detail-section">
          <h4 class="detail-section-title">回顾设置</h4>
          <div class="form-stack">
            <HCombobox
              :model-value="recapModelDraft"
              :options="recapOptions"
              placeholder="留空跟随全局"
              clearable
              @update:model-value="recapModelDraft = $event"
            >
              <template #label>回顾模型</template>
            </HCombobox>
            <div class="switch-row">
              <span class="switch-label">最大续写次数</span>
              <HInput
                :model-value="maxContinuationsDraft"
                size="sm"
                placeholder="-1=全局"
                @update:model-value="maxContinuationsDraft = $event"
              />
            </div>
            <div class="hint">-1 表示跟随全局设置</div>
            <HButton size="sm" variant="secondary" @click="applyRecapOverrides">应用回顾设置</HButton>
          </div>
        </section>

        <section class="detail-section">
          <h4 class="detail-section-title">发布设置</h4>
          <div class="form-stack">
            <HInput
              :model-value="coverDraft"
              size="sm"
              placeholder="自定义封面 URL(留空跟随全局)"
              @update:model-value="coverDraft = $event"
            >
              <template #label>自定义封面</template>
            </HInput>
            <div class="hint">留空跟随全局;优先使用回顾目录封面,无回顾封面时才用此 URL 或本地路径(发布时自动上传)</div>
            <HButton size="sm" variant="secondary" @click="onSaveCover">保存封面</HButton>
          </div>
        </section>

        <section class="detail-section">
          <h4 class="detail-section-title">高级配置</h4>
          <ChannelAdvancedConfig :channel="channel" />
        </section>
      </template>
    </template>
  </HDrawer>
</template>

<style scoped>
.detail-section {
  margin-bottom: 20px;
}

.detail-section:last-child {
  margin-bottom: 0;
}

.detail-section-title {
  margin: 0 0 10px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.03em;
}

.session-list {
  display: grid;
  gap: 8px;
}

.session-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.session-item:hover {
  background: var(--surface-hover, var(--canvas));
  border-color: var(--accent);
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
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-left span {
  font-size: 12px;
  color: var(--text-muted);
}

.form-stack {
  display: grid;
  gap: 12px;
}

.switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.switch-label {
  font-size: 13px;
  color: var(--text);
}

.hint {
  font-size: 12px;
  color: var(--text-muted);
}
</style>
