<script setup lang="ts">
import { computed } from 'vue'
import { Search, Upload, Download, ArrowDown } from '@element-plus/icons-vue'
import type { Capabilities } from '@/api/types'

const props = defineProps<{
  discovering: boolean
  /** 当前子 tab:录播页隐藏「发现回放/导入/链接下载」(这些只产生回放类场次) */
  tab: 'live' | 'replay'
  /** 运行时能力（发现回放依赖 replay_download/yt-dlp） */
  capabilities?: Capabilities | null
}>()

const emit = defineEmits<{
  discover: []
  import: []
  download: []
  'clear-failed': []
}>()

// 发现回放依赖 yt-dlp（replay_download 能力）。缺失时禁用按钮并提示。
const discoverDisabled = computed(() => !props.capabilities?.replay_download)
const discoverDisabledReason = computed(() =>
  props.capabilities?.reason || 'yt-dlp 不可用，无法发现回放',
)
</script>

<template>
  <div class="page-header">
    <h2>回顾</h2>
    <div class="page-actions">
      <!-- 回放类(download/import)的创建入口仅在「回放」tab 显示 -->
      <template v-if="tab === 'replay'">
        <el-tooltip :content="discoverDisabled ? discoverDisabledReason : ''" :disabled="!discoverDisabled" placement="top">
          <span>
            <el-button type="primary" :loading="discovering" :disabled="discoverDisabled" @click="emit('discover')">
              <el-icon><Search /></el-icon> 发现回放
            </el-button>
          </span>
        </el-tooltip>
        <el-button @click="emit('import')">
          <el-icon><Upload /></el-icon> 导入
        </el-button>
        <el-button @click="emit('download')">
          <el-icon><Download /></el-icon> 链接下载
        </el-button>
      </template>
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
