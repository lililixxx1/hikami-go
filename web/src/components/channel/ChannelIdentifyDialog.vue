<script setup lang="ts">
import { ref, watch } from 'vue'
import { identifyChannel, identifyAndSave } from '@/api/channels'
import type { IdentifyResult } from '@/api/types'
import { ElMessage } from 'element-plus'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  success: []
}>()

const step = ref<1 | 2>(1)
const inputText = ref('')
const loading = ref(false)
const identifyResult = ref<IdentifyResult | null>(null)

watch(
  () => props.visible,
  (val) => {
    if (val) {
      reset()
    }
  },
)

function reset(): void {
  step.value = 1
  inputText.value = ''
  loading.value = false
  identifyResult.value = null
}

async function handleIdentify(): Promise<void> {
  const text = inputText.value.trim()
  if (!text) {
    ElMessage.warning('请输入直播链接、空间链接或 UID')
    return
  }
  loading.value = true
  try {
    const result = await identifyChannel({ input: text })
    identifyResult.value = result
    step.value = 2
  } catch {
    // error handled by API client
  } finally {
    loading.value = false
  }
}

async function handleSave(): Promise<void> {
  if (!identifyResult.value) return
  loading.value = true
  try {
    await identifyAndSave({ input: inputText.value.trim() })
    ElMessage.success('主播保存成功')
    emit('success')
  } catch {
    // error handled by API client
  } finally {
    loading.value = false
  }
}

function handleClose(): void {
  emit('update:visible', false)
}

function handleBack(): void {
  step.value = 1
  identifyResult.value = null
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="识别主播"
    width="500px"
    @close="handleClose"
  >
    <!-- Step 1: Input -->
    <div v-if="step === 1">
      <el-form @submit.prevent="handleIdentify">
        <el-form-item label="输入">
          <el-input
            v-model="inputText"
            placeholder="输入 B站 直播链接、空间链接或 UID"
            :loading="loading"
            clearable
          />
        </el-form-item>
      </el-form>
      <div class="step-actions">
        <el-button type="primary" :loading="loading" @click="handleIdentify">
          识别
        </el-button>
      </div>
    </div>

    <!-- Step 2: Result -->
    <div v-if="step === 2 && identifyResult">
      <el-descriptions :column="1" border>
        <el-descriptions-item label="主播名称">
          {{ identifyResult.channel.name }}
        </el-descriptions-item>
        <el-descriptions-item label="UID">
          {{ identifyResult.channel.uid }}
        </el-descriptions-item>
        <el-descriptions-item label="直播间ID">
          {{ identifyResult.channel.live_room_id || '-' }}
        </el-descriptions-item>
        <el-descriptions-item label="空间URL">
          {{ identifyResult.channel.space_url }}
        </el-descriptions-item>
        <el-descriptions-item label="回放来源">
          {{ identifyResult.channel.replay_source_url }}
        </el-descriptions-item>
        <el-descriptions-item label="来源方式">
          {{ identifyResult.source }}
        </el-descriptions-item>
      </el-descriptions>
    </div>

    <template #footer>
      <el-button @click="step === 2 ? handleBack() : handleClose()">
        {{ step === 2 ? '返回' : '取消' }}
      </el-button>
      <el-button
        v-if="step === 2"
        type="primary"
        :loading="loading"
        @click="handleSave"
      >
        确认保存
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.step-actions {
  display: flex;
  justify-content: flex-end;
}
</style>
