<!-- web/src/features/streamers/components/ChannelAdvancedConfig.vue -->
<script setup lang="ts">
import { computed } from 'vue'
import type { Channel } from '@/api/types-derived'
import { HDescriptions } from '@/components/ui'

// 专家模式下的只读高级配置表:展示主播关键字段,ID/UID/Room/源模式/发现限制/
// 回顾模型/续写/弹幕/发布Cookie/下载Cookie/封面。空值由 HDescriptions 自动显示 '-'。
const props = defineProps<{
  channel: Channel
}>()

const items = computed(() => {
  const c = props.channel
  return [
    { label: 'ID', value: c.id },
    { label: 'UID', value: c.uid || null },
    { label: 'Room', value: c.live_room_id || null },
    { label: '来源模式', value: c.source_mode || 'both' },
    { label: '发现限制', value: c.discover_limit || '不限' },
    { label: '回顾模型', value: c.recap_model || '跟随全局' },
    { label: '最大续写', value: c.max_continuations >= 0 ? c.max_continuations : '跟随全局' },
    { label: '弹幕录制', value: c.record_danmaku ? '开' : '关' },
    { label: '发布Cookie', value: c.cookie_file || null },
    { label: '下载Cookie', value: c.download_cookie_file || null },
    { label: '自定义封面', value: c.publish_cover_url || '跟随全局' },
  ]
})
</script>

<template>
  <HDescriptions :items="items" :column="1" />
</template>
