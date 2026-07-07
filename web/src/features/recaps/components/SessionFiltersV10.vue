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

const channelOptions = computed(() => [
  { label: '全部主播', value: '' },
  ...props.channels.map((c) => ({ label: c.name, value: c.id })),
])
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
