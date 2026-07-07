<script setup lang="ts">
import { ref, watch } from 'vue'
import { identifyChannel, identifyAndSave } from '@/api/channels'
import type { IdentifyResult } from '@/api/types-derived'
import { HMessage } from '@/components/ui/message'
import { HDialog, HButton, HInput, HDescriptions } from '@/components/ui'

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
    HMessage.warning('请输入直播链接、空间链接或 UID')
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
    HMessage.success('主播保存成功')
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
  <HDialog
    :visible="visible"
    title="识别主播"
    width="500px"
    @update:visible="(v) => { if (!v) handleClose() }"
  >
    <!-- Step 1: Input -->
    <div v-if="step === 1">
      <form @submit.prevent="handleIdentify">
        <label class="field-label">输入</label>
        <HInput v-model="inputText" placeholder="输入 B站 直播链接、空间链接或 UID" />
      </form>
      <div class="step-actions">
        <HButton variant="primary" :loading="loading" @click="handleIdentify">
          识别
        </HButton>
      </div>
    </div>

    <!-- Step 2: Result -->
    <div v-if="step === 2 && identifyResult">
      <HDescriptions
        :items="[
          { label: '主播名称', value: identifyResult.channel.name },
          { label: 'UID', value: String(identifyResult.channel.uid) },
          { label: '直播间ID', value: identifyResult.channel.live_room_id ? String(identifyResult.channel.live_room_id) : '-' },
          { label: '空间URL', value: identifyResult.channel.space_url },
          { label: '回放来源', value: identifyResult.channel.replay_source_url },
          { label: '来源方式', value: identifyResult.source },
        ]"
      />
    </div>

    <template #footer>
      <HButton variant="secondary" @click="step === 2 ? handleBack() : handleClose()">
        {{ step === 2 ? '返回' : '取消' }}
      </HButton>
      <HButton
        v-if="step === 2"
        variant="primary"
        :loading="loading"
        @click="handleSave"
      >
        确认保存
      </HButton>
    </template>
  </HDialog>
</template>

<style scoped>
.field-label {
  display: block;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
  margin-bottom: 6px;
}

.step-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
