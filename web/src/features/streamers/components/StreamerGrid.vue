<!-- web/src/features/streamers/components/StreamerGrid.vue -->
<script setup lang="ts">
import type { Channel } from '@/api/types-derived'
import { HEmpty } from '@/components/ui'
import type { CookieStatus } from '../composables/useStreamerDetail'
import StreamerCard from './StreamerCard.vue'

// 主播卡片网格:遍历 channels 渲染 StreamerCard。
// cookieStatusFn / lastSessionFn 由壳提供(壳持有 runtime + sessions store),
// 网格仅做映射分发,不直接依赖 store。
defineProps<{
  channels: Channel[]
  cookieStatusFn: (c: Channel) => CookieStatus
  lastSessionFn: (cid: string) => string
}>()

const emit = defineEmits<{
  'open-detail': [channel: Channel]
}>()
</script>

<template>
  <div v-if="channels.length > 0" class="card-grid">
    <StreamerCard
      v-for="c in channels"
      :key="c.id"
      :channel="c"
      :cookie-status="cookieStatusFn(c)"
      :last-session-date="lastSessionFn(c.id)"
      @open-detail="emit('open-detail', $event)"
    />
  </div>
  <HEmpty v-else description="还没有主播,点击上方添加" />
</template>

<style scoped>
.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 12px;
}

@media (max-width: 768px) {
  .card-grid { grid-template-columns: 1fr; }
}
</style>
