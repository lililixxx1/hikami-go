<!-- web/src/components/ui/HDialog.vue -->
<script setup lang="ts">
defineProps<{
  visible: boolean
  title?: string
  width?: string  // 默认 480px
}>()
const emit = defineEmits<{ 'update:visible': [value: boolean] }>()
function close() { emit('update:visible', false) }
</script>
<template>
  <Teleport to="body">
    <template v-if="visible">
      <div class="dialog-overlay" @click.self="close">
        <div class="dialog" :style="{ width: width ?? '480px' }" role="dialog" aria-modal="true">
          <div v-if="title || $slots.header" class="dialog-header">
            <slot name="header"><span class="dialog-title">{{ title }}</span></slot>
            <button type="button" class="dialog-close" aria-label="关闭" @click="close">
              <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M4 4l8 8M12 4l-8 8" />
              </svg>
            </button>
          </div>
          <div class="dialog-body"><slot /></div>
          <div v-if="$slots.footer" class="dialog-footer"><slot name="footer" /></div>
        </div>
      </div>
    </template>
  </Teleport>
</template>
