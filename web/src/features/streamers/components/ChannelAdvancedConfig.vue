<!-- web/src/features/streamers/components/ChannelAdvancedConfig.vue -->
<script setup lang="ts">
import { computed } from 'vue'
import type { Channel } from '@/api/types-derived'
import { HDescriptions } from '@/components/ui'

// 专家模式下的只读高级配置表:展示主播关键字段。
// 2026-07-20 补:per-channel 发布字段(账号/模式/可见范围/文集/声明)。
// 各字段「跟随全局」哨兵不同(publisher.go:48-99 真相源),不能统一处理。
const props = defineProps<{
  channel: Channel
}>()

function publishPrivatePubLabel(v: number | undefined): string {
  const n = v ?? 0
  if (n === 0) return '跟随全局' // ⚠️ private_pub 哨兵是 0
  if (n === 1) return '仅自己可见'
  if (n === 2) return '公开'
  return String(n)
}
function publishListLabel(v: number | undefined): string {
  const n = v ?? -1
  if (n === -1) return '跟随全局' // ⚠️ 只有 -1 是跟随全局;0 是「不加入文集」
  if (n === 0) return '不加入文集'
  return `文集 #${n}`
}
function publishOriginalLabel(v: number | undefined): string {
  const n = v ?? -1
  if (n === -1) return '跟随全局'
  if (n === 0) return '非原创'
  if (n === 1) return '原创'
  return String(n)
}
function publishAigcLabel(v: number | undefined): string {
  const n = v ?? -1
  if (n === -1) return '跟随全局'
  if (n === 0) return '否'
  if (n === 1) return '是'
  return String(n)
}

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
    { label: '发布账号', value: c.publish_account_id == null ? '跟随全局' : `账号 #${c.publish_account_id}` },
    { label: '发布模式', value: c.publish_mode || '跟随全局' },
    { label: '可见范围', value: publishPrivatePubLabel(c.publish_private_pub) },
    { label: '文集', value: publishListLabel(c.publish_list_id) },
    { label: '原创声明', value: publishOriginalLabel(c.publish_original) },
    { label: 'AIGC 声明', value: publishAigcLabel(c.publish_aigc) },
    { label: '发布Cookie', value: c.cookie_file || null },
    { label: '下载Cookie', value: c.download_cookie_file || null },
    { label: '自定义封面', value: c.publish_cover_url || '跟随全局' },
  ]
})
</script>

<template>
  <HDescriptions :items="items" :column="1" />
</template>
