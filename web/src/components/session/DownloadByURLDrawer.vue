<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { HMessage } from '@/components/ui/message'
import type { FormInstance } from 'element-plus'
import { ArrowRight } from '@element-plus/icons-vue'
import { useChannelsStore } from '@/stores/channels'
import { useRuntimeStore } from '@/stores/runtime'
import { downloadSessionByURL } from '@/api/sessions'
import type { Task } from '@/api/types'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  submitted: [task: Task]
}>()

const channelsStore = useChannelsStore()
const runtimeStore = useRuntimeStore()

const formRef = ref<FormInstance>()
const submitting = ref(false)

const form = ref({
  channel_id: '',
  url: '',
})

const rules = {
  channel_id: [{ required: true, message: '请选择主播', trigger: 'change' }],
  url: [{ required: true, message: '请输入视频链接', trigger: 'blur' }],
}

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const replayAvailable = computed(() => Boolean(runtimeStore.status?.capabilities.replay_download))
const replayReason = computed(() => runtimeStore.status?.capabilities.reason || 'yt-dlp 不可用')

function resetForm(): void {
  form.value = { channel_id: '', url: '' }
  formRef.value?.clearValidate()
}

async function handleSubmit(): Promise<void> {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }

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
  <el-drawer
    v-model="drawerVisible"
    title="链接下载"
    direction="rtl"
    size="520px"
  >
    <div class="download-drawer">
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-width="88px"
      >
        <section class="form-section">
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

          <el-form-item label="视频链接" prop="url">
            <el-input
              v-model="form.url"
              placeholder="粘贴 B 站 BV 号或视频链接"
              clearable
            />
          </el-form-item>
        </section>

        <section class="form-section">
          <h3>处理流程</h3>
          <div class="process-line">
            <el-tag type="info">download</el-tag>
            <el-icon><ArrowRight /></el-icon>
            <el-tag type="info">normalize</el-tag>
            <el-icon><ArrowRight /></el-icon>
            <el-tag type="info">asr</el-tag>
            <el-icon><ArrowRight /></el-icon>
            <el-tag type="success">recap</el-tag>
          </div>
          <div class="capability-line">
            <span>
              下载能力：
              <el-tag :type="replayAvailable ? 'success' : 'warning'" size="small">
                {{ replayAvailable ? '可用' : '不可用' }}
              </el-tag>
            </span>
          </div>
          <div v-if="!replayAvailable" class="capability-reason">{{ replayReason }}</div>
          <div class="hint">
            提交后自动下载、转写并生成回顾。同一视频重复提交会被识别为已存在。
          </div>
        </section>
      </el-form>
    </div>

    <template #footer>
      <div class="drawer-footer">
        <el-button @click="drawerVisible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="submitting"
          :disabled="!replayAvailable"
          @click="handleSubmit"
        >
          提交下载
        </el-button>
      </div>
    </template>
  </el-drawer>
</template>

<style scoped>
.download-drawer {
  padding-bottom: 16px;
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

.hint {
  margin-top: 12px;
  color: #909399;
  font-size: 12px;
  line-height: 1.6;
}

.drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
