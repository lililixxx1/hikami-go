<!-- web/src/features/streamers/components/AutoSwitches.vue -->
<script setup lang="ts">
import type { Channel } from '@/api/types-derived'
import { HSwitch } from '@/components/ui'
import type { AutoToggleField } from '../composables/useStreamerDetail'

// 4 个自动化开关:自动录制/ASR/回顾/发布。
// HSwitch :modelValue 双向绑定的值仅在用户点击时触发 update:modelValue → emit toggle(field)。
// 真正翻转由壳(composable.handleToggle)调 API 完成后刷新 store 回填,避免乐观更新与后端不一致。
const props = defineProps<{
  channel: Channel
  updating: boolean
}>()

const emit = defineEmits<{
  toggle: [field: AutoToggleField]
}>()

// 开关配置:field → 标签
const switches: { field: AutoToggleField; label: string }[] = [
  { field: 'auto_record', label: '自动录制' },
  { field: 'auto_asr', label: '自动 ASR' },
  { field: 'auto_recap', label: '自动回顾' },
  { field: 'auto_publish', label: '自动发布' },
]

// HSwitch 点击即 emit update:modelValue(新值=取反当前)。
// 我们不直接消费新值(壳会用 !channel[field] 翻转),仅作为 toggle 触发信号。
function onToggle(field: AutoToggleField) {
  if (props.updating) return
  emit('toggle', field)
}
</script>

<template>
  <div class="switch-group">
    <div v-for="s in switches" :key="s.field" class="switch-row">
      <span class="switch-label">{{ s.label }}</span>
      <HSwitch
        :model-value="channel[s.field]"
        :disabled="updating"
        @update:model-value="onToggle(s.field)"
      />
    </div>
  </div>
</template>

<style scoped>
.switch-group {
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
</style>
