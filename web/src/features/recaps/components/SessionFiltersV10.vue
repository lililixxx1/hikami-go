<!-- web/src/features/recaps/components/SessionFiltersV10.vue -->
<script setup lang="ts">
import { computed } from 'vue'
import { HInput, HSelect } from '@/components/ui'
import type { Channel } from '@/api/types-derived'

const props = defineProps<{
  keyword: string
  statusFilter: string
  channelFilter: string
  channels: Channel[]
  /** 当前 tab:'live'(录播) | 'replay'(回放)。replay tab 隐藏主播筛选下拉
   * (2026-07-19:回放类不再绑真实主播,可挂 _unassigned「未分类」)。 */
  activeTab?: 'live' | 'replay'
}>()

const emit = defineEmits<{
  'update:keyword': [value: string]
  'update:statusFilter': [value: string]
  'update:channelFilter': [value: string]
}>()

// 状态分组选项:全部 / 处理中 / 已生成 / 已发布 / 失败(键沿用 statusGroupMap)
const statusOptions = computed(() => [
  { label: '全部', value: 'all' },
  { label: '处理中', value: 'processing' },
  { label: '已生成', value: 'recap' },
  { label: '已发布', value: 'published' },
  { label: '失败', value: 'failed' },
])

// 主播筛选下拉选项(2026-07-19:只在 'live' tab 显示;replay tab 隐藏)
const channelOptions = computed(() => [
  { label: '全部主播', value: '' },
  ...props.channels.map((c) => ({ label: c.name, value: c.id })),
])

// 是否显示主播筛选下拉(只有录播 tab 才有意义)
const showChannelFilter = computed(() => props.activeTab !== 'replay')
</script>

<template>
  <div class="filter-bar">
    <HInput
      :model-value="keyword"
      placeholder="搜索标题/ID"
      @update:model-value="emit('update:keyword', $event)"
    />
    <HSelect
      :model-value="statusFilter"
      :options="statusOptions"
      @update:model-value="emit('update:statusFilter', String($event))"
    />
    <HSelect
      v-if="showChannelFilter"
      :model-value="channelFilter"
      :options="channelOptions"
      @update:model-value="emit('update:channelFilter', String($event))"
    />
  </div>
</template>

<style scoped>
.filter-bar {
  display: flex;
  gap: 12px;
  margin-bottom: 16px;
  flex-wrap: wrap;
  align-items: center;
}

.filter-bar :deep(.input) {
  width: 220px;
}

.filter-bar :deep(.select) {
  width: 160px;
}

@media (max-width: 768px) {
  .filter-bar {
    flex-direction: column;
    align-items: stretch;
  }

  .filter-bar :deep(.input),
  .filter-bar :deep(.select) {
    width: 100%;
  }
}
</style>
