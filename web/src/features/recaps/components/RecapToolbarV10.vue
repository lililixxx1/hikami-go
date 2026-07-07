<!-- web/src/features/recaps/components/RecapToolbarV10.vue -->
<script setup lang="ts">
import { computed } from 'vue'
import { HButton } from '@/components/ui'
import type { Capabilities } from '@/api/types-derived'

const props = defineProps<{
  /** 当前子 tab:录播(live) / 回放(replay) */
  activeTab: 'live' | 'replay'
  /** 失败场次数(清空失败徽标用;0 时不显示) */
  failedCount?: number
  /** 运行时能力(发现回放依赖 replay_download) */
  capabilities?: Capabilities | null
  /** 工具栏动作 loading(发现等异步中) */
  actionLoading?: boolean
}>()

const emit = defineEmits<{
  'update:activeTab': [value: 'live' | 'replay']
  discover: []
  import: []
  download: []
  'clear-failed': []
}>()

// 发现回放依赖 yt-dlp(replay_download 能力)。缺失时禁用按钮。
const discoverDisabled = computed(() => !props.capabilities?.replay_download)
const tabs: { value: 'live' | 'replay'; label: string }[] = [
  { value: 'live', label: '录播' },
  { value: 'replay', label: '回放' },
]
</script>

<template>
  <div class="recap-toolbar">
    <div class="h-tabs-bar" role="tablist">
      <button
        v-for="t in tabs"
        :key="t.value"
        type="button"
        role="tab"
        class="h-tab"
        :class="{ active: activeTab === t.value }"
        :aria-selected="activeTab === t.value"
        @click="emit('update:activeTab', t.value)"
      >
        {{ t.label }}
      </button>
    </div>

    <div class="toolbar-actions">
      <!-- 回放类(download/import)的创建入口仅在「回放」tab 显示 -->
      <template v-if="activeTab === 'replay'">
        <HButton
          variant="primary"
          size="sm"
          :disabled="discoverDisabled"
          :loading="actionLoading"
          :title="discoverDisabled ? (capabilities?.reason || 'yt-dlp 不可用，无法发现回放') : ''"
          @click="emit('discover')"
        >
          <svg class="btn-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="7" cy="7" r="4.5" /><path d="M10.5 10.5L14 14" stroke-linecap="round" /></svg>
          发现回放
        </HButton>
        <HButton variant="secondary" size="sm" @click="emit('import')">
          <svg class="btn-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M8 2v9M4.5 7.5L8 11l3.5-3.5M2.5 13.5h11" stroke-linecap="round" stroke-linejoin="round" /></svg>
          导入
        </HButton>
        <HButton variant="secondary" size="sm" @click="emit('download')">
          <svg class="btn-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M8 2v8M4.5 6.5L8 10l3.5-3.5M2.5 13.5h11" stroke-linecap="round" stroke-linejoin="round" /></svg>
          下载
        </HButton>
      </template>
      <HButton variant="danger" size="sm" :disabled="failedCount === 0" @click="emit('clear-failed')">
        清空失败<span v-if="failedCount" class="badge">{{ failedCount }}</span>
      </HButton>
    </div>
  </div>
</template>

<style scoped>
.recap-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}

.h-tabs-bar {
  display: inline-flex;
  gap: 4px;
  padding: 3px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 8px);
}

.h-tab {
  appearance: none;
  border: none;
  background: transparent;
  padding: 6px 16px;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.h-tab:hover {
  color: var(--text);
}

.h-tab.active {
  background: var(--bg, #fff);
  color: var(--text);
  box-shadow: var(--shadow-sm);
}

.toolbar-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}

.btn-icon {
  margin-right: 4px;
  vertical-align: -2px;
}

.badge {
  display: inline-block;
  margin-left: 6px;
  min-width: 18px;
  padding: 0 5px;
  font-size: 11px;
  line-height: 18px;
  text-align: center;
  border-radius: 9px;
  background: rgba(255, 255, 255, 0.25);
}

@media (max-width: 768px) {
  .recap-toolbar {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
