<!-- web/src/features/home/components/RecentRecapsSection.vue -->
<script setup lang="ts">
import type { Session, Capabilities } from '@/api/types-derived'
import { HButton, HEmpty, HPill } from '@/components/ui'
import { getFriendlySessionStatus } from '@/utils/friendlyStatus'
import { formatDateTime } from '@/utils/format'

defineProps<{
  recaps: Session[]
  capabilities: Capabilities | null
}>()

const emit = defineEmits<{
  'open-recap': [sid: string]
  'view-all': []
}>()

// friendlyStatus 颜色 → HPill variant 映射
function pillVariant(color: string): 'success' | 'warning' | 'danger' | 'info' | 'neutral' {
  if (color === 'success') return 'success'
  if (color === 'danger') return 'danger'
  if (color === 'warning') return 'warning'
  if (color === 'info') return 'info'
  return 'neutral'
}
</script>

<template>
  <section class="section">
    <div class="section-header">
      <h3 class="section-title">最近回顾</h3>
      <HButton variant="ghost" size="sm" @click="emit('view-all')">查看全部</HButton>
    </div>
    <div v-if="recaps.length > 0" class="review-grid">
      <div v-for="s in recaps" :key="s.id" class="review-card" @click="emit('open-recap', s.id)">
        <div class="review-card-title">{{ s.title || '无标题' }}</div>
        <div class="review-card-meta">
          <span>{{ s.channel_name || s.channel_id }}</span>
          <span>·</span>
          <span>{{ formatDateTime(s.created_at) }}</span>
        </div>
        <div class="review-card-footer">
          <HPill :variant="pillVariant(getFriendlySessionStatus(s).color)">
            {{ getFriendlySessionStatus(s).label }}
          </HPill>
        </div>
      </div>
    </div>
    <HEmpty v-else description="暂无回顾" />
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
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}

.review-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 12px;
}

.review-card {
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--canvas);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
  transition: border-color 0.15s, box-shadow 0.15s, transform 0.15s;
}

.review-card:hover {
  border-color: var(--accent);
  box-shadow: var(--shadow-card-hover);
  transform: translateY(-1px);
}

.review-card-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
  margin-bottom: 8px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  line-height: 1.4;
}

.review-card-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 10px;
}

.review-card-footer {
  display: flex;
  align-items: center;
  justify-content: flex-start;
}

@media (max-width: 768px) {
  .review-grid { grid-template-columns: 1fr; }
}
</style>
