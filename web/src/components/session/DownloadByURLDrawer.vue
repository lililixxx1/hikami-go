<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HDrawer, HButton, HInput, HSelect, HPill } from '@/components/ui'
import { useChannelsStore } from '@/stores/channels'
import { useRuntimeStore } from '@/stores/runtime'
import { downloadSessionByURL } from '@/api/sessions'
import type { Task } from '@/api/types-derived'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  submitted: [task: Task]
}>()

const channelsStore = useChannelsStore()
const runtimeStore = useRuntimeStore()

const submitting = ref(false)
const errors = ref<{ channel_id?: string; url?: string }>({})

const form = ref({
  channel_id: '',
  url: '',
})

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const replayAvailable = computed(() => Boolean(runtimeStore.status?.capabilities.replay_download))
const replayReason = computed(() => runtimeStore.status?.capabilities.reason || 'yt-dlp 不可用')

const channelOptions = computed(() => [
  // 2026-07-19 解耦:主播可选。第一项 value='' 表示「不选」(后端挂到 _unassigned「未分类」)。
  { label: '(不选) 归入未分类', value: '' },
  ...channelsStore.items.map((c) => ({ label: c.name, value: c.id })),
])

function validate(): boolean {
  const e: { channel_id?: string; url?: string } = {}
  // 2026-07-19 解耦:主播字段改为可选(不选则后端挂到系统占位 channel _unassigned,即「未分类」)。
  if (!form.value.url.trim()) e.url = '请输入视频链接'
  errors.value = e
  return Object.keys(e).length === 0
}

function resetForm(): void {
  form.value = { channel_id: '', url: '' }
  errors.value = {}
}

async function handleSubmit(): Promise<void> {
  if (!validate()) return

  submitting.value = true
  try {
    const task = await downloadSessionByURL(form.value.channel_id, form.value.url.trim())
    emit('submitted', task)
    HMessage.success('下载任务已提交')
    drawerVisible.value = false
  } finally {
    submitting.value = false
  }
}

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      channelsStore.fetchChannels()
      runtimeStore.fetchRuntime()
    } else {
      resetForm()
    }
  },
)

onMounted(() => {
  channelsStore.fetchChannels()
  runtimeStore.fetchRuntime()
})
</script>

<template>
  <HDrawer
    :visible="drawerVisible"
    title="链接下载"
    size="520px"
    @update:visible="drawerVisible = $event"
  >
    <div class="download-drawer">
      <section class="form-section">
        <label class="field-label">
          主播 <span class="field-optional">(可选)</span>
          <span v-if="errors.channel_id" class="field-error">{{ errors.channel_id }}</span>
        </label>
        <HSelect v-model="form.channel_id" :options="channelOptions" />
      </section>

      <section class="form-section">
        <label class="field-label">视频链接 <span v-if="errors.url" class="field-error">{{ errors.url }}</span></label>
        <HInput v-model="form.url" placeholder="粘贴 B 站 BV 号或视频链接" />
      </section>

      <section class="form-section">
        <h3>处理流程</h3>
        <div class="process-line">
          <HPill variant="info">download</HPill>
          <span class="arrow">→</span>
          <HPill variant="info">normalize</HPill>
          <span class="arrow">→</span>
          <HPill variant="info">asr</HPill>
          <span class="arrow">→</span>
          <HPill variant="success">recap</HPill>
        </div>
        <div class="capability-line">
          <span>
            下载能力：
            <HPill :variant="replayAvailable ? 'success' : 'warning'">
              {{ replayAvailable ? '可用' : '不可用' }}
            </HPill>
          </span>
        </div>
        <div v-if="!replayAvailable" class="capability-reason">{{ replayReason }}</div>
        <div class="hint">
          提交后自动下载、转写并生成回顾。同一视频重复提交会被识别为已存在。
        </div>
      </section>

      <div class="drawer-footer">
        <HButton variant="secondary" @click="drawerVisible = false">取消</HButton>
        <HButton
          variant="primary"
          :loading="submitting"
          :disabled="!replayAvailable"
          @click="handleSubmit"
        >
          提交下载
        </HButton>
      </div>
    </div>
  </HDrawer>
</template>

<style scoped>
.download-drawer {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.form-section {
  padding: 14px 0;
  border-bottom: 1px solid var(--border-light);
}

.form-section:last-of-type {
  border-bottom: none;
}

.form-section h3 {
  margin: 0 0 14px;
  color: var(--text);
  font-size: 15px;
}

.field-label {
  display: block;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
  margin-bottom: 6px;
}

.field-error {
  color: var(--danger, #e03e2d);
  font-weight: 400;
  margin-left: 6px;
}

.field-optional {
  color: var(--text-muted, var(--text-secondary));
  font-weight: 400;
  font-size: 12px;
}

.process-line,
.capability-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.6;
}

.arrow {
  color: var(--text-muted);
}

.capability-line {
  margin-top: 12px;
}

.capability-reason {
  margin-top: 8px;
  color: var(--warning, #e6a23c);
  font-size: 12px;
  line-height: 1.5;
}

.hint {
  margin-top: 12px;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
  position: sticky;
  bottom: 0;
  background: var(--canvas);
}
</style>
