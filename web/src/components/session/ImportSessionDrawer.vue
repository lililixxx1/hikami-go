<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { HMessage } from '@/components/ui/message'
import { HDrawer, HButton, HInput, HSelect, HPill } from '@/components/ui'
import { useChannelsStore } from '@/stores/channels'
import { useRuntimeStore } from '@/stores/runtime'
import { importSession } from '@/api/sessions'
import { formatFileSize } from '@/utils/format'
import type { Task } from '@/api/types'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  submitted: [task: Task]
}>()

const router = useRouter()
const channelsStore = useChannelsStore()
const runtimeStore = useRuntimeStore()

const submitting = ref(false)
const submittedTask = ref<Task | null>(null)

interface ImportFile {
  name: string
  size: number
  type: string
  raw: File
}

const form = ref({
  channel_id: '',
  title: '',
  started_at: '',
  source_url: '',
})

const errors = ref<{ channel_id?: string; title?: string }>({})
const mediaFiles = ref<ImportFile[]>([])
const danmakuFiles = ref<ImportFile[]>([])

const channelOptions = computed(() =>
  channelsStore.items.map((c) => ({ label: c.name, value: c.id })),
)

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const selectedChannel = computed(() => {
  return channelsStore.items.find((channel) => channel.id === form.value.channel_id) ?? null
})

const asrAvailable = computed(() => Boolean(runtimeStore.status?.capabilities.asr_submit))
const asrReason = computed(() => runtimeStore.status?.capabilities.reason || 'ASR 能力不可用')

function onMediaChange(e: Event): void {
  const input = e.target as HTMLInputElement
  if (!input.files) return
  mediaFiles.value = Array.from(input.files).map((f) => ({ name: f.name, size: f.size, type: f.type, raw: f }))
}

function onDanmakuChange(e: Event): void {
  const input = e.target as HTMLInputElement
  if (!input.files) return
  danmakuFiles.value = Array.from(input.files).map((f) => ({ name: f.name, size: f.size, type: f.type, raw: f }))
}

// 把 datetime-local 值(本地时间,无时区)转成 RFC3339 带偏移
function toRFC3339(localValue: string): string {
  if (!localValue) return ''
  const d = new Date(localValue)
  if (Number.isNaN(d.getTime())) return ''
  return d.toISOString()
}

function validate(): boolean {
  const e: { channel_id?: string; title?: string } = {}
  if (!form.value.channel_id) e.channel_id = '请选择主播'
  if (!form.value.title.trim()) e.title = '请输入标题'
  errors.value = e
  return Object.keys(e).length === 0
}

function resetForm(): void {
  form.value = { channel_id: '', title: '', started_at: '', source_url: '' }
  errors.value = {}
  mediaFiles.value = []
  danmakuFiles.value = []
  submittedTask.value = null
}

async function handleSubmit(): Promise<void> {
  if (!validate()) return

  if (mediaFiles.value.length === 0) {
    HMessage.warning('请上传媒体文件')
    return
  }

  submitting.value = true
  try {
    const formData = new FormData()
    formData.append('channel_id', form.value.channel_id)
    formData.append('title', form.value.title)
    if (form.value.started_at) {
      const iso = toRFC3339(form.value.started_at)
      if (iso) formData.append('started_at', iso)
    }
    if (form.value.source_url) formData.append('source_url', form.value.source_url)
    for (const file of mediaFiles.value) {
      formData.append('media', file.raw)
    }
    for (const file of danmakuFiles.value) {
      formData.append('danmaku', file.raw)
    }

    const task = await importSession(formData)
    submittedTask.value = task
    emit('submitted', task)
    HMessage.success('导入任务已提交')
  } finally {
    submitting.value = false
  }
}

function openSession(): void {
  if (!submittedTask.value?.session_id) return
  drawerVisible.value = false
  router.push(`/sessions/${submittedTask.value.session_id}`)
}

function openTask(): void {
  if (!submittedTask.value) return
  drawerVisible.value = false
  router.push({
    path: '/tasks',
    query: {
      task_id: submittedTask.value.id,
      session_id: submittedTask.value.session_id,
    },
  })
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
    title="手动导入"
    size="520px"
    @update:visible="drawerVisible = $event"
  >
    <div class="import-drawer">
      <div v-if="submittedTask" class="submit-result">
        <strong>任务已提交</strong>
        <div class="result-actions">
          <span>任务 ID：{{ submittedTask.id }}</span>
          <span>场次 ID：{{ submittedTask.session_id || '-' }}</span>
          <div class="result-links">
            <HButton variant="ghost" size="sm" @click="openTask">查看任务</HButton>
            <HButton
              v-if="submittedTask.session_id"
              variant="ghost"
              size="sm"
              @click="openSession"
            >
              查看场次
            </HButton>
          </div>
        </div>
      </div>

      <section class="form-section">
        <h3>1. 基本信息</h3>
        <div class="field">
          <label class="field-label">主播 <span v-if="errors.channel_id" class="field-error">{{ errors.channel_id }}</span></label>
          <HSelect v-model="form.channel_id" :options="channelOptions" />
        </div>
        <div class="field">
          <label class="field-label">标题 <span v-if="errors.title" class="field-error">{{ errors.title }}</span></label>
          <HInput v-model="form.title" placeholder="直播标题" />
        </div>
        <div class="field">
          <label class="field-label">时间</label>
          <input
            v-model="form.started_at"
            type="datetime-local"
            class="input datetime-input"
          >
        </div>
        <div class="field">
          <label class="field-label">来源 URL</label>
          <HInput v-model="form.source_url" placeholder="来源链接（可选）" />
        </div>
      </section>

      <section class="form-section">
        <h3>2. 文件上传</h3>
        <div class="field">
          <label class="field-label">媒体文件（必需）</label>
          <label class="upload-drop">
            <input type="file" multiple accept="audio/*,video/*" @change="onMediaChange">
            <div class="upload-text">点击选择媒体文件（可多选）</div>
          </label>
          <div v-if="mediaFiles.length > 0" class="file-summary">
            <div v-for="(file, idx) in mediaFiles" :key="`m-${idx}`" class="file-row">
              <span>{{ file.name }}</span>
              <small>{{ formatFileSize(file.size) }} · {{ file.type || '未知类型' }}</small>
            </div>
          </div>
        </div>
        <div class="field">
          <label class="field-label">弹幕文件</label>
          <label class="upload-drop">
            <input type="file" multiple accept=".json,.jsonl,.txt" @change="onDanmakuChange">
            <div class="upload-text">点击选择弹幕文件（JSON/JSONL/TXT，可选）</div>
          </label>
          <div v-if="danmakuFiles.length > 0" class="file-summary">
            <div v-for="(file, idx) in danmakuFiles" :key="`d-${idx}`" class="file-row">
              <span>{{ file.name }}</span>
              <small>{{ formatFileSize(file.size) }} · {{ file.type || '未知类型' }}</small>
            </div>
          </div>
        </div>
      </section>

      <section class="form-section">
        <h3>3. 后续处理</h3>
        <div class="process-line">
          <HPill variant="info">import</HPill>
          <span class="arrow">→</span>
          <HPill variant="info">normalize</HPill>
          <span class="arrow">→</span>
          <HPill variant="success">media_ready</HPill>
        </div>
        <div class="capability-line">
          <span>该主播自动 ASR：{{ selectedChannel?.auto_asr ? '开' : '关' }}</span>
          <span>
            ASR 能力：
            <HPill :variant="asrAvailable ? 'success' : 'warning'">
              {{ asrAvailable ? '可用' : '不可用' }}
            </HPill>
          </span>
        </div>
        <div v-if="!asrAvailable" class="capability-reason">{{ asrReason }}</div>
      </section>

      <div class="drawer-footer">
        <HButton variant="secondary" @click="drawerVisible = false">取消</HButton>
        <HButton variant="primary" :loading="submitting" @click="handleSubmit">
          提交导入
        </HButton>
      </div>
    </div>
  </HDrawer>
</template>

<style scoped>
.import-drawer {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.submit-result {
  margin-bottom: 16px;
  padding: 12px;
  border: 1px solid var(--success-border, #d1edc4);
  border-radius: 8px;
  background: var(--success-bg, #f0f9eb);
}

.result-actions {
  display: grid;
  gap: 6px;
  line-height: 1.5;
  margin-top: 6px;
  font-size: 13px;
}

.result-links {
  display: flex;
  gap: 8px;
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

.field {
  margin-bottom: 14px;
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

.datetime-input {
  font-family: inherit;
  font-size: 13px;
}

.upload-drop {
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
  padding: 24px 12px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}

.upload-drop:hover {
  border-color: var(--accent);
  background: var(--accent-bg);
}

.upload-drop input[type="file"] {
  position: absolute;
  width: 0;
  height: 0;
  opacity: 0;
  pointer-events: none;
}

.upload-text {
  color: var(--text-muted);
  font-size: 13px;
}

.file-summary {
  width: 100%;
  margin-top: 8px;
  display: grid;
  gap: 6px;
}

.file-row {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.file-row span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-row small {
  color: var(--text-muted);
  flex-shrink: 0;
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
