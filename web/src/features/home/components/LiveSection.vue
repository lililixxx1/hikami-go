<!-- web/src/features/home/components/LiveSection.vue -->
<script setup lang="ts">
import type { Channel, LiveStatus } from '@/api/types-derived'
import { HButton, HEmpty } from '@/components/ui'
import RecordingDuration from './RecordingDuration.vue'

export interface LiveItem {
  channel: Channel
  status: LiveStatus
}

defineProps<{
  recordingItems: LiveItem[]
  liveOnlyItems: LiveItem[]
  checking: boolean
}>()

const emit = defineEmits<{
  refresh: []
  'start-record': [channelId: string, name: string]
  'stop-record': [channelId: string, name: string]
}>()
</script>

<template>
  <section class="section">
    <div class="section-header">
      <h3 class="section-title">直播状态</h3>
      <HButton variant="ghost" size="sm" :loading="checking" @click="emit('refresh')">刷新</HButton>
    </div>
    <div v-if="recordingItems.length > 0 || liveOnlyItems.length > 0" class="live-grid">
      <!-- 录制中卡片 -->
      <div v-for="item in recordingItems" :key="item.channel.id" class="live-card recording">
        <div class="live-card-header">
          <span class="live-card-title">{{ item.channel.name }}</span>
          <span class="live-card-badge recording"><span class="pulse" /> 录制中</span>
        </div>
        <div class="live-card-body">{{ item.status.title || '直播中' }}</div>
        <div class="live-card-footer">
          <RecordingDuration :started-at="item.status.started_at || ''" />
          <HButton variant="danger" size="sm" @click="emit('stop-record', item.channel.id, item.channel.name)">停止</HButton>
        </div>
      </div>
      <!-- 直播中(未录制)卡片 -->
      <div v-for="item in liveOnlyItems" :key="item.channel.id" class="live-card streaming">
        <div class="live-card-header">
          <span class="live-card-title">{{ item.channel.name }}</span>
          <span class="live-card-badge streaming"><span class="pulse" /> 直播中</span>
        </div>
        <div class="live-card-body">{{ item.status.title || '直播中' }}</div>
        <div class="live-card-footer">
          <span class="live-meta">自动录制: {{ item.channel.auto_record ? '开' : '关' }}</span>
          <HButton variant="primary" size="sm" @click="emit('start-record', item.channel.id, item.channel.name)">录制</HButton>
        </div>
      </div>
    </div>
    <HEmpty v-else description="当前没有直播" />
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

.live-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
}

.live-card {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--canvas);
  padding: 16px 18px;
  box-shadow: var(--shadow-sm);
  transition: box-shadow 0.2s;
  position: relative;
  overflow: hidden;
}

.live-card:hover { box-shadow: var(--shadow-md); }

.live-card::before {
  content: "";
  position: absolute;
  left: 0; top: 0; bottom: 0;
  width: 3px;
  border-radius: 0 2px 2px 0;
}

.live-card.recording::before { background: var(--warning); }
.live-card.streaming::before { background: var(--success); }

.live-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.live-card-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}

.live-card-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 10px;
  font-size: 11.5px;
  font-weight: 600;
  border-radius: var(--radius-full);
}

.live-card-badge.recording { background: rgba(221, 91, 0, 0.1); color: var(--warning); }
.live-card-badge.streaming { background: rgba(26, 174, 57, 0.1); color: var(--success); }

.live-card-badge .pulse {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  animation: live-pulse 1.5s ease-in-out infinite;
}

.live-card-badge.recording .pulse { background: var(--warning); }
.live-card-badge.streaming .pulse { background: var(--success); }

@keyframes live-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}

.live-card-body {
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.6;
}

.live-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
}

.live-meta {
  font-size: 12px;
  color: var(--text-muted);
}

@media (max-width: 768px) {
  .live-grid { grid-template-columns: 1fr; }
}
</style>
