<!-- web/src/features/home/components/CapabilitySection.vue -->
<script setup lang="ts">
import type { Capabilities } from '@/api/types-derived'
import { HButton } from '@/components/ui'
import { computed } from 'vue'

const props = defineProps<{
  capabilities: Capabilities | null
}>()

const emit = defineEmits<{
  'go-settings': []
}>()

const signals = computed(() => {
  const caps = props.capabilities
  return [
    { key: 'asr_submit', label: '转写', ok: Boolean(caps?.asr_submit) },
    { key: 'recap_generate', label: '回顾', ok: Boolean(caps?.recap_generate) },
    { key: 'webdav_upload', label: 'WebDAV', ok: Boolean(caps?.webdav_upload) },
    { key: 'publish_opus', label: '发布', ok: Boolean(caps?.publish_opus) },
  ]
})
</script>

<template>
  <section class="section">
    <div class="section-header">
      <h3 class="section-title">系统能力</h3>
      <HButton variant="ghost" size="sm" @click="emit('go-settings')">设置</HButton>
    </div>
    <div class="cap-row">
      <div v-for="s in signals" :key="s.key" class="cap-item" :class="s.ok ? 'ok' : 'off'">
        <span class="cap-dot" :class="s.ok ? 'success' : 'danger'" />
        <span class="cap-label">{{ s.label }}{{ s.ok ? '可用' : '不可用' }}</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.section-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.03em;
}

.cap-row {
  display: flex;
  gap: 20px;
  flex-wrap: wrap;
  padding: 16px 20px;
  background: var(--canvas);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-sm);
}

.cap-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.cap-item.ok { color: var(--text); }
.cap-item.off { color: var(--text-muted); }

.cap-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}

.cap-dot.success { background: var(--success); }
.cap-dot.danger { background: var(--danger); }

.cap-label {
  font-weight: 500;
}
</style>
