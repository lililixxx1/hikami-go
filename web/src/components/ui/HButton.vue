<!-- web/src/components/ui/HButton.vue -->
<script setup lang="ts">
const props = defineProps<{
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'sm' | 'xs' | 'md'  // 默认 md（无 size 类，原 .btn 基类尺寸）
  disabled?: boolean
  loading?: boolean
  type?: 'button' | 'submit'
}>()
const emit = defineEmits<{ click: [event: MouseEvent] }>()
function onClick(e: MouseEvent) {
  // loading/disabled 时阻止冒泡（避免提交表单等副作用）
  if (props.disabled || props.loading) return
  emit('click', e)
}
</script>
<template>
  <button
    :type="type ?? 'button'"
    class="btn"
    :class="[variant ? `btn-${variant}` : 'btn-primary', size ? `btn-${size}` : '']"
    :disabled="disabled || loading"
    @click="onClick($event)"
  >
    <span v-if="loading" class="btn-spinner" aria-hidden="true" />
    <slot />
  </button>
</template>
