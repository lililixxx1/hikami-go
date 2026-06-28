<script setup lang="ts">
import { Search } from '@element-plus/icons-vue'
import type { Channel } from '@/api/types'

defineProps<{
  channels: Channel[]
}>()

const keyword = defineModel<string>('keyword', { default: '' })
const statusFilter = defineModel<'all' | 'processing' | 'recap' | 'published' | 'failed'>('statusFilter', {
  default: 'all',
})
const channelFilter = defineModel<string>('channelFilter', { default: '' })
</script>

<template>
  <div class="filter-bar">
    <el-radio-group v-model="statusFilter" size="default">
      <el-radio-button label="all">全部</el-radio-button>
      <el-radio-button label="processing">处理中</el-radio-button>
      <el-radio-button label="recap">已生成</el-radio-button>
      <el-radio-button label="published">已发布</el-radio-button>
      <el-radio-button label="failed">失败</el-radio-button>
    </el-radio-group>
    <el-select v-model="channelFilter" clearable placeholder="按主播" class="channel-select">
      <el-option label="全部主播" value="" />
      <el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id" />
    </el-select>
    <el-input v-model="keyword" clearable placeholder="搜索标题" class="search-input">
      <template #prefix><el-icon><Search /></el-icon></template>
    </el-input>
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

.channel-select {
  width: 160px;
}

.search-input {
  width: 220px;
  margin-left: auto;
}

@media (max-width: 768px) {
  .filter-bar {
    flex-direction: column;
    align-items: stretch;
  }

  .channel-select,
  .search-input {
    width: 100%;
    margin-left: 0;
  }
}
</style>
