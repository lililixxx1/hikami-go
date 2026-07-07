<!-- web/src/features/streamers/components/StreamerCard.vue -->
<script setup lang="ts">
import type { Channel } from '@/api/types-derived'
import { HPill } from '@/components/ui'
import type { CookieStatus } from '../composables/useStreamerDetail'

// 单个主播卡片:名称 + cookie 状态点 + 4 个自动化 HPill + 最近场次。
// 纯展示 + 点击打开详情抽屉;cookieStatus 由父级(壳/网格)计算后透传,避免每张卡各自取 runtime。
const props = defineProps<{
  channel: Channel
  cookieStatus: CookieStatus
  lastSessionDate: string
}>()

const emit = defineEmits<{
  'open-detail': [channel: Channel]
}>()

// cookieStatus 颜色映射:ok=绿 missing=红 unknown=灰
function dotClass(s: CookieStatus): string {
  if (s === 'ok') return 'dot-ok'
  if (s === 'missing') return 'dot-missing'
  return 'dot-unknown'
}

// 开关 HPill:开启=success(绿),关闭=neutral(灰)
function pillVariant(on: boolean): 'success' | 'neutral' {
  return on ? 'success' : 'neutral'
}

function onClick() {
  emit('open-detail', props.channel)
}
</script>

<template>
  <div class="streamer-card" @click="onClick">
    <div class="card-top">
      <strong class="card-name">{{ channel.name }}</strong>
      <span class="cookie-dot" :class="dotClass(cookieStatus)" :title="cookieStatus" />
    </div>
    <div class="card-auto">
      <HPill :variant="pillVariant(channel.auto_record)">录制{{ channel.auto_record ? '✓' : '×' }}</HPill>
      <HPill :variant="pillVariant(channel.auto_asr)">ASR{{ channel.auto_asr ? '✓' : '×' }}</HPill>
      <HPill :variant="pillVariant(channel.auto_recap)">回顾{{ channel.auto_recap ? '✓' : '×' }}</HPill>
      <HPill :variant="pillVariant(channel.auto_publish)">发布{{ channel.auto_publish ? '✓' : '×' }}</HPill>
    </div>
    <div class="card-footer">
      <span>最近场次: {{ lastSessionDate }}</span>
    </div>
  </div>
</template>

<style scoped>
.streamer-card {
  padding: 16px 18px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--canvas);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
  transition: border-color 0.15s, box-shadow 0.15s;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.streamer-card:hover {
  border-color: var(--accent);
  box-shadow: var(--shadow-md);
}

.card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.card-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cookie-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.cookie-dot.dot-ok { background: var(--success); }
.cookie-dot.dot-missing { background: var(--danger); }
.cookie-dot.dot-unknown { background: var(--text-muted); }

.card-auto {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.card-footer {
  font-size: 12px;
  color: var(--text-muted);
}
</style>
