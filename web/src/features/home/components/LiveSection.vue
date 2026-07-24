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
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
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
  transition: all 0.22s cubic-bezier(0.22, 1, 0.36, 1);
  position: relative;
  overflow: hidden;
}

.live-card:hover {
  box-shadow: var(--shadow-card-hover);
  transform: translateY(-2px);
}

/* v2 视觉统一:用渐变底色替代原左侧 3px 色条(原型 line 240-241),
   识别度由 badge 文字 + pulse 点 + 渐变三重编码,删色条无 a11y 损失 */
.live-card.recording {
  border-color: rgba(208, 0, 0, 0.12);
  background: linear-gradient(135deg, var(--canvas) 60%, var(--recording-bg) 100%);
}
.live-card.streaming {
  border-color: rgba(232, 93, 4, 0.12);
  background: linear-gradient(135deg, var(--canvas) 60%, var(--live-bg) 100%);
}

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

.live-card-badge.recording { background: var(--recording-bg); color: var(--recording); }
.live-card-badge.streaming { background: var(--live-bg); color: var(--live); }

.live-card-badge .pulse {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  animation: live-pulse 1.5s ease-in-out infinite;
}

.live-card-badge.recording .pulse { background: var(--recording); }
.live-card-badge.streaming .pulse { background: var(--live); }

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
