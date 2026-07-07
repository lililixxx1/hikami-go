<!-- web/src/components/ui/HDrawer.vue -->
<script setup lang="ts">
defineProps<{
  visible: boolean
  title?: string
  size?: string  // 默认 520px
  direction?: 'rtl' | 'ltr'  // 默认 rtl
}>()
const emit = defineEmits<{ 'update:visible': [value: boolean] }>()
function close() { emit('update:visible', false) }
</script>
<template>
  <template v-if="visible">
    <div class="drawer-overlay" @click="close" />
    <div class="drawer" :class="[direction ?? 'rtl', { open: visible }]" :style="{ width: size ?? '520px' }">
      <div class="drawer-header">
        <span class="drawer-title">{{ title }}</span>
        <button type="button" class="drawer-close" aria-label="关闭" @click="close">
          <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M4 4l8 8M12 4l-8 8" />
          </svg>
        </button>
      </div>
      <div class="drawer-body"><slot /></div>
    </div>
  </template>
</template>
