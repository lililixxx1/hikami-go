<!-- web/src/features/home/components/AttentionSection.vue -->
<script setup lang="ts">
import type { Session, CookieWarning, DiskInfo } from '@/api/types-derived'
import { HPill } from '@/components/ui'

defineProps<{
  failedSessions: Session[]
  cookieWarnings: CookieWarning[]
  diskWarnings: DiskInfo[]
}>()

const emit = defineEmits<{
  'open-recap': [sid: string]
}>()
</script>

<template>
  <section
    v-if="failedSessions.length > 0 || cookieWarnings.length > 0 || diskWarnings.length > 0"
    class="section"
  >
    <div class="section-header">
      <h3 class="section-title">需要注意</h3>
    </div>
    <div class="alert-list">
      <div
        v-for="s in failedSessions.slice(0, 5)"
        :key="s.id"
        class="alert-card danger"
        @click="emit('open-recap', s.id)"
      >
        <svg class="alert-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="8" cy="8" r="6" /><path d="M8 5v4M8 11.5v.5" />
        </svg>
        <span class="alert-text">{{ s.channel_name || s.channel_id }} · {{ s.title || s.id }} 处理失败</span>
        <HPill variant="danger">查看</HPill>
      </div>

      <div v-if="cookieWarnings.length > 0" class="alert-card warning">
        <svg class="alert-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M8 2L14 13H2L8 2Z" /><path d="M8 7v3M8 12v.5" />
        </svg>
        <span class="alert-text">Cookie 即将过期: {{ cookieWarnings.map((w) => w.channel_name).join(', ') }}</span>
      </div>

      <div v-if="diskWarnings.length > 0" class="alert-card warning">
        <svg class="alert-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="2" y="3" width="12" height="10" rx="1" /><path d="M8 7v4M8 11.5v.5" />
        </svg>
        <span class="alert-text">磁盘空间不足: {{ diskWarnings.map((d) => `${d.path} ${d.used_percent.toFixed(0)}%`).join(', ') }}</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.section-header {
  margin-bottom: 12px;
}

.section-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}

.alert-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.alert-card {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-radius: var(--radius-md);
  font-size: 13px;
  font-weight: 500;
  transition: opacity 0.15s;
}

.alert-card.danger {
  background: var(--danger-bg);
  color: var(--danger);
  cursor: pointer;
}

.alert-card.danger:hover { opacity: 0.85; }

.alert-card.warning {
  background: var(--warning-bg);
  color: var(--warning);
}

.alert-icon {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
}

.alert-text {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
