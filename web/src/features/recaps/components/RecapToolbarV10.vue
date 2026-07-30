<!-- web/src/features/recaps/components/RecapToolbarV10.vue -->
<script setup lang="ts">
import { computed } from 'vue'
import { HButton, HSwitch } from '@/components/ui'
import type { Capabilities } from '@/api/types-derived'
import type { ReplayConfig } from '@/api/settings'

const props = defineProps<{
  /** 当前子 tab:录播(live) / 回放(replay) */
  activeTab: 'live' | 'replay'
  /** 失败场次数(清空失败徽标用;0 时不显示) */
  failedCount?: number
  /** 运行时能力(发现回放依赖 replay_download) */
  capabilities?: Capabilities | null
  /** 工具栏动作 loading(发现等异步中) */
  actionLoading?: boolean
  /** 回放类全局自动开关(仅回放 tab 显示;壳拉取并持久化,组件纯展示+emit 变更) */
  replayConfig?: ReplayConfig | null
  /** 开关切换中的 loading(防抖动) */
  replayLoading?: boolean
}>()

const emit = defineEmits<{
  'update:activeTab': [value: 'live' | 'replay']
  discover: []
  import: []
  download: []
  'clear-failed': []
  /** 回放自动开关变更(auto_asr / auto_recap),壳负责调 API 持久化 */
  'update-replay': [field: 'auto_asr' | 'auto_recap', value: boolean]
}>()

// 发现回放依赖 yt-dlp(replay_download 能力)。缺失时禁用按钮。
const discoverDisabled = computed(() => !props.capabilities?.replay_download)
const tabs: { value: 'live' | 'replay'; label: string }[] = [
  { value: 'live', label: '录播' },
  { value: 'replay', label: '回放' },
]

function onToggleAutoASR(v: boolean): void {
  emit('update-replay', 'auto_asr', v)
}
function onToggleAutoRecap(v: boolean): void {
  emit('update-replay', 'auto_recap', v)
}
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
        <!-- 回放类全局自动开关(2026-07-30):开了之后回放视频自动转写+回顾,免全程手动点 -->
        <div v-if="replayConfig" class="replay-auto-toggles" :title="replayLoading ? '保存中…' : ''">
          <HSwitch :model-value="replayConfig.auto_asr" :disabled="replayLoading" @update:model-value="onToggleAutoASR">
            自动转写
          </HSwitch>
          <HSwitch :model-value="replayConfig.auto_recap" :disabled="replayLoading" @update:model-value="onToggleAutoRecap">
            自动回顾
          </HSwitch>
        </div>
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

.replay-auto-toggles {
  display: inline-flex;
  gap: 12px;
  align-items: center;
  padding-right: 8px;
  margin-right: 4px;
  border-right: 1px solid var(--border);
  font-size: 13px;
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
