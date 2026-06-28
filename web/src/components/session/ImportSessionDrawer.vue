<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { FormInstance, UploadFile, UploadFiles } from 'element-plus'
import { ArrowRight, Upload } from '@element-plus/icons-vue'
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

const formRef = ref<FormInstance>()
const submitting = ref(false)
const submittedTask = ref<Task | null>(null)

const form = ref({
  channel_id: '',
  title: '',
  started_at: '',
  source_url: '',
})

const mediaFiles = ref<UploadFile[]>([])
const danmakuFiles = ref<UploadFile[]>([])

const rules = {
  channel_id: [{ required: true, message: '请选择主播', trigger: 'change' }],
  title: [{ required: true, message: '请输入标题', trigger: 'blur' }],
}

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const selectedChannel = computed(() => {
  return channelsStore.items.find((channel) => channel.id === form.value.channel_id) ?? null
})

const asrAvailable = computed(() => Boolean(runtimeStore.status?.capabilities.asr_submit))
const asrReason = computed(() => runtimeStore.status?.capabilities.reason || 'ASR 能力不可用')

function syncMediaFiles(_file: UploadFile, fileList: UploadFiles): void {
  mediaFiles.value = fileList
}

function syncDanmakuFiles(_file: UploadFile, fileList: UploadFiles): void {
  danmakuFiles.value = fileList
}

function resetForm(): void {
  form.value = {
    channel_id: '',
    title: '',
    started_at: '',
    source_url: '',
  }
  mediaFiles.value = []
  danmakuFiles.value = []
  submittedTask.value = null
  formRef.value?.clearValidate()
}

async function handleSubmit(): Promise<void> {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }

  if (mediaFiles.value.length === 0) {
    ElMessage.warning('请上传媒体文件')
    return
  }

  submitting.value = true
  try {
    const formData = new FormData()
    formData.append('channel_id', form.value.channel_id)
    formData.append('title', form.value.title)
    if (form.value.started_at) formData.append('started_at', form.value.started_at)
    if (form.value.source_url) formData.append('source_url', form.value.source_url)
    for (const file of mediaFiles.value) {
      if (file.raw) formData.append('media', file.raw)
    }
    for (const file of danmakuFiles.value) {
      if (file.raw) formData.append('danmaku', file.raw)
    }

    const task = await importSession(formData)
    submittedTask.value = task
    emit('submitted', task)
    ElMessage.success('导入任务已提交')
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
  <el-drawer
    v-model="drawerVisible"
    title="手动导入"
    direction="rtl"
    size="520px"
  >
    <div class="import-drawer">
      <el-alert
        v-if="submittedTask"
        type="success"
        show-icon
        :closable="false"
        class="submit-result"
      >
        <template #title>
          任务已提交
        </template>
        <div class="result-actions">
          <span>任务 ID：{{ submittedTask.id }}</span>
          <span>场次 ID：{{ submittedTask.session_id || '-' }}</span>
          <div>
            <el-button type="primary" link @click="openTask">查看任务</el-button>
            <el-button
              v-if="submittedTask.session_id"
              type="primary"
              link
              @click="openSession"
            >
              查看场次
            </el-button>
          </div>
        </div>
      </el-alert>

      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-width="88px"
      >
        <section class="form-section">
          <h3>1. 基本信息</h3>
          <el-form-item label="主播" prop="channel_id">
            <el-select
              v-model="form.channel_id"
              placeholder="选择主播"
              filterable
              style="width: 100%"
            >
              <el-option
                v-for="channel in channelsStore.items"
                :key="channel.id"
                :label="channel.name"
                :value="channel.id"
              />
            </el-select>
          </el-form-item>

          <el-form-item label="标题" prop="title">
            <el-input v-model="form.title" placeholder="直播标题" />
          </el-form-item>

          <el-form-item label="时间">
            <el-date-picker
              v-model="form.started_at"
              type="datetime"
              placeholder="选择开始时间"
              value-format="YYYY-MM-DDTHH:mm:ssZ"
              style="width: 100%"
            />
          </el-form-item>

          <el-form-item label="来源 URL">
            <el-input v-model="form.source_url" placeholder="来源链接（可选）" />
          </el-form-item>
        </section>

        <section class="form-section">
          <h3>2. 文件上传</h3>
          <el-form-item label="媒体文件" required>
            <el-upload
              v-model:file-list="mediaFiles"
              :auto-upload="false"
              multiple
              drag
              accept="audio/*,video/*"
              @change="syncMediaFiles"
              @remove="syncMediaFiles"
            >
              <el-icon :size="32"><Upload /></el-icon>
              <div class="el-upload__text">拖拽或 <em>点击上传</em> 媒体文件</div>
              <template #tip>
                <div class="el-upload__tip">支持音频/视频文件，可多选</div>
              </template>
            </el-upload>
            <div v-if="mediaFiles.length > 0" class="file-summary">
              <div v-for="file in mediaFiles" :key="file.uid" class="file-row">
                <span>{{ file.name }}</span>
                <small>{{ formatFileSize(file.size || 0) }} · {{ file.raw?.type || '未知类型' }}</small>
              </div>
            </div>
          </el-form-item>

          <el-form-item label="弹幕文件">
            <el-upload
              v-model:file-list="danmakuFiles"
              :auto-upload="false"
              multiple
              drag
              accept=".json,.jsonl,.txt"
              @change="syncDanmakuFiles"
              @remove="syncDanmakuFiles"
            >
              <el-icon :size="32"><Upload /></el-icon>
              <div class="el-upload__text">拖拽或 <em>点击上传</em> 弹幕文件</div>
              <template #tip>
                <div class="el-upload__tip">支持 JSON、JSONL、TXT（可选）</div>
              </template>
            </el-upload>
            <div v-if="danmakuFiles.length > 0" class="file-summary">
              <div v-for="file in danmakuFiles" :key="file.uid" class="file-row">
                <span>{{ file.name }}</span>
                <small>{{ formatFileSize(file.size || 0) }} · {{ file.raw?.type || '未知类型' }}</small>
              </div>
            </div>
          </el-form-item>
        </section>

        <section class="form-section">
          <h3>3. 后续处理</h3>
          <div class="process-line">
            <el-tag type="info">import</el-tag>
            <el-icon><ArrowRight /></el-icon>
            <el-tag type="info">normalize</el-tag>
            <el-icon><ArrowRight /></el-icon>
            <el-tag type="success">media_ready</el-tag>
          </div>
          <div class="capability-line">
            <span>该主播自动 ASR：{{ selectedChannel?.auto_asr ? '开' : '关' }}</span>
            <span>
              ASR 能力：
              <el-tag :type="asrAvailable ? 'success' : 'warning'" size="small">
                {{ asrAvailable ? '可用' : '不可用' }}
              </el-tag>
            </span>
          </div>
          <div v-if="!asrAvailable" class="capability-reason">{{ asrReason }}</div>
        </section>
      </el-form>
    </div>

    <template #footer>
      <div class="drawer-footer">
        <el-button @click="drawerVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          提交导入
        </el-button>
      </div>
    </template>
  </el-drawer>
</template>

<style scoped>
.import-drawer {
  padding-bottom: 16px;
}

.submit-result {
  margin-bottom: 16px;
}

.result-actions {
  display: grid;
  gap: 6px;
  line-height: 1.5;
}

.form-section {
  padding: 14px 0;
  border-bottom: 1px solid #ebeef5;
}

.form-section:first-of-type {
  padding-top: 0;
}

.form-section h3 {
  margin: 0 0 14px;
  color: #303133;
  font-size: 15px;
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
  color: #606266;
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
  color: #909399;
  flex-shrink: 0;
}

.process-line,
.capability-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  color: #606266;
  font-size: 13px;
  line-height: 1.6;
}

.capability-line {
  margin-top: 12px;
}

.capability-reason {
  margin-top: 8px;
  color: #e6a23c;
  font-size: 12px;
  line-height: 1.5;
}

.drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
