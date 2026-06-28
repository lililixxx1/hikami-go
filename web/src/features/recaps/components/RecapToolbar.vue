<script setup lang="ts">
import { Search, Upload, Download, ArrowDown } from '@element-plus/icons-vue'

defineProps<{
  discovering: boolean
}>()

const emit = defineEmits<{
  discover: []
  import: []
  download: []
  'clear-failed': []
}>()
</script>

<template>
  <div class="page-header">
    <h2>回顾</h2>
    <div class="page-actions">
      <el-button type="primary" :loading="discovering" @click="emit('discover')">
        <el-icon><Search /></el-icon> 发现回放
      </el-button>
      <el-button @click="emit('import')">
        <el-icon><Upload /></el-icon> 导入
      </el-button>
      <el-button @click="emit('download')">
        <el-icon><Download /></el-icon> 链接下载
      </el-button>
      <el-dropdown trigger="click">
        <el-button>更多 <el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item @click="emit('clear-failed')">清空失败场次</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>
  </div>
</template>

<style scoped>
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.page-header h2 {
  margin: 0;
  font-size: 20px;
}

.page-actions {
  display: flex;
  gap: 8px;
}

@media (max-width: 768px) {
  .page-header {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
