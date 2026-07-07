<!-- web/src/components/ui/ConfirmHost.vue -->
<!-- 单例对话框宿主:读取 HConfirm.ts 响应式 state,渲染 HDialog(确认/提示/输入)。
     在 App.vue/AppLayout 挂载一次即全局可用。由 HConfirm/HAlert/HPrompt 命令式驱动。 -->
<script setup lang="ts">
import { computed } from 'vue'
import HDialog from './HDialog.vue'
import HButton from './HButton.vue'
import {
  confirmState,
  promptValue,
  promptOptions,
  resolveConfirm,
  setPromptValue,
} from './HConfirm'

const visible = computed({
  get: () => confirmState.value.visible,
  set: (v: boolean) => {
    // HDialog 通过 update:visible 通知关闭(点遮罩/关闭按钮)→ 视为取消
    if (!v) resolveConfirm(false)
  },
})

const isPrompt = computed(() => confirmState.value.kind === 'prompt')
const isAlert = computed(() => confirmState.value.kind === 'alert')

const title = computed(() => confirmState.value.options.title ?? '提示')
const confirmText = computed(() => confirmState.value.options.confirmText ?? '确认')
const cancelText = computed(() => confirmState.value.options.cancelText ?? (isAlert.value ? '' : '取消'))
const isDanger = computed(() => confirmState.value.options.type === 'danger')
const message = computed(() => confirmState.value.message)

function onConfirm() {
  resolveConfirm(true)
}
function onCancel() {
  resolveConfirm(false)
}

function onPromptEnter() {
  resolveConfirm(true)
}
</script>

<template>
  <HDialog v-model:visible="visible" :title="title">
    <div class="confirm-message">{{ message }}</div>
    <div v-if="isPrompt" class="confirm-prompt-input">
      <input
        class="input"
        :type="promptOptions.inputType ?? 'text'"
        :value="promptValue"
        :placeholder="promptOptions.placeholder ?? ''"
        @input="setPromptValue(($event.target as HTMLInputElement).value)"
        @keydown.enter="onPromptEnter"
      >
    </div>
    <template #footer>
      <HButton v-if="cancelText" variant="secondary" @click="onCancel">{{ cancelText }}</HButton>
      <HButton :variant="isDanger ? 'danger' : 'primary'" @click="onConfirm">{{ confirmText }}</HButton>
    </template>
  </HDialog>
</template>

<style scoped>
.confirm-message {
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.6;
  color: var(--text);
}

.confirm-prompt-input {
  margin-top: 12px;
}
</style>
