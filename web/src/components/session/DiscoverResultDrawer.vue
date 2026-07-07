<script setup lang="ts">
/**
 * 发现回放抽屉（两步式：预览 → 勾选 → 下载）。
 *
 * 三态：
 *  - loading：打开后立即调 previewDiscoverSessions() 列出所有频道会发现什么（不建场次）
 *  - preview：按频道分组展示，每项标 [新]/[已处理]，默认勾选「新」、不勾「已处理」
 *  - done：执行 executeDiscoverSessions() 后展示结果（新建/跳过/错误）
 *
 * 自管理 preview/execute 调用；父组件只需 v-model:visible + @executed（刷新 sessions 列表）。
 * 保留「全部下载」按钮调旧的 discoverSessions()（一键行为，等于改版前的流程）。
 */
import { computed, ref, watch } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HConfirm } from '@/components/ui/HConfirm'
import { HDrawer, HButton, HCheckbox, HEmpty, HPill } from '@/components/ui'
import {
  discoverSessions,
  previewDiscoverSessions,
  executeDiscoverSessions,
} from '@/api/sessions'
import type { DiscoverResult, DiscoverPickItem } from '@/api/types'

type Phase = 'loading' | 'preview' | 'done'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  executed: []
}>()

const phase = ref<Phase>('loading')
const previewItems = ref<DiscoverResult[]>([])
const doneItems = ref<DiscoverResult[]>([])
const selected = ref<Set<string>>(new Set()) // key = channel_id + '|' + source_id
const executing = ref(false)

// 预览阶段的错误项（频道级 yt-dlp 失败等），单独展示、不参与分组勾选。
const previewErrors = computed(() => previewItems.value.filter((i) => Boolean(i.error)))

// 可勾选的有效预览项（排除错误项和空 source_id——后者无法建场次）。
const validPreviewItems = computed(() =>
  previewItems.value.filter((i) => !i.error && i.channel_id && i.source_id),
)

// 按频道分组的预览结果（仅有效项）
const grouped = computed(() => {
  const map = new Map<string, DiscoverResult[]>()
  for (const item of validPreviewItems.value) {
    const list = map.get(item.channel_id) ?? []
    list.push(item)
    map.set(item.channel_id, list)
  }
  return Array.from(map.entries()).map(([channelId, items]) => ({ channelId, items }))
})

const selectedCount = computed(() => selected.value.size)

const doneStats = computed(() => {
  const created = doneItems.value.filter((i) => i.created && !i.error).length
  const skipped = doneItems.value.filter((i) => !i.created && !i.error).length
  const error = doneItems.value.filter((i) => Boolean(i.error)).length
  return { created, skipped, error }
})

function itemKey(channelId: string, sourceId: string): string {
  return `${channelId}|${sourceId}`
}

function isSelected(item: DiscoverResult): boolean {
  return selected.value.has(itemKey(item.channel_id, item.source_id))
}

function toggleItem(item: DiscoverResult, checked: boolean): void {
  const key = itemKey(item.channel_id, item.source_id)
  if (checked) selected.value.add(key)
  else selected.value.delete(key)
  // 触发响应式更新（Set 的 add/delete 不被 Vue 直接追踪）
  selected.value = new Set(selected.value)
}

function toggleGroup(channelId: string, checked: boolean): void {
  const group = grouped.value.find((g) => g.channelId === channelId)
  if (!group) return
  for (const item of group.items) {
    const key = itemKey(item.channel_id, item.source_id)
    if (checked) selected.value.add(key)
    else selected.value.delete(key)
  }
  selected.value = new Set(selected.value)
}

function groupAllSelected(channelId: string): boolean {
  const group = grouped.value.find((g) => g.channelId === channelId)
  if (!group || group.items.length === 0) return false
  return group.items.every((i) => selected.value.has(itemKey(i.channel_id, i.source_id)))
}

// 打开抽屉时触发预览
watch(
  () => props.visible,
  async (visible) => {
    if (!visible) return
    phase.value = 'loading'
    previewItems.value = []
    selected.value = new Set()
    try {
      const result = await previewDiscoverSessions()
      previewItems.value = result.items
      // 默认勾选「新」（未处理的）项；排除错误项和空 source_id（无法建场次——codex 审核 P2）
      const validNew = result.items.filter((i) => !i.error && !i.exists && i.channel_id && i.source_id)
      for (const item of validNew) {
        selected.value.add(itemKey(item.channel_id, item.source_id))
      }
      selected.value = new Set(selected.value)
      phase.value = 'preview'
      const errorCount = result.items.filter((i) => i.error).length
      if (validNew.length > 0) {
        HMessage.info(`预览到 ${validNew.length} 条新回放，请勾选后下载`)
      } else if (errorCount > 0) {
        HMessage.warning(`部分主播发现失败（${errorCount} 条错误），其余回放均已处理`)
      } else {
        HMessage.info('未发现新回放（全部已处理）')
      }
    } finally {
      // previewDiscoverSessions 失败由 client.ts 拦截器统一 toast；
      // 这里只需确保 phase 不卡在 loading（出错时 items 为空，展示空态）
      if (phase.value === 'loading') phase.value = 'preview'
    }
  },
)

async function handleExecuteSelected(): Promise<void> {
  const picks: DiscoverPickItem[] = []
  for (const item of previewItems.value) {
    if (selected.value.has(itemKey(item.channel_id, item.source_id))) {
      picks.push({
        channel_id: item.channel_id,
        source_id: item.source_id,
        title: item.title,
        source_url: item.source_url,
      })
    }
  }
  if (picks.length === 0) {
    HMessage.warning('请先勾选要下载的回放')
    return
  }
  if (!(await HConfirm(`确定下载选中的 ${picks.length} 个回放？将自动开始下载。`, {
    title: '下载确认',
    confirmText: '下载',
    cancelText: '取消',
    type: 'warning',
  }))) return // 用户取消
  executing.value = true
  try {
    const result = await executeDiscoverSessions(picks)
    doneItems.value = result.items
    phase.value = 'done'
    const created = result.items.filter((i) => i.created && !i.error).length
    if (created > 0) HMessage.success(`已开始下载 ${created} 个新回放`)
    else HMessage.info('选中项均已处理，无新下载')
    emit('executed')
  } finally {
    executing.value = false
  }
}

async function handleDownloadAll(): Promise<void> {
  if (!(await HConfirm('将立即下载所有新回放（不经过勾选），确定继续？', {
    title: '全部下载',
    confirmText: '全部下载',
    cancelText: '取消',
    type: 'warning',
  }))) return
  executing.value = true
  try {
    const result = await discoverSessions()
    doneItems.value = result.items
    phase.value = 'done'
    const created = result.items.filter((i) => i.created && !i.error).length
    if (created > 0) HMessage.success(`已开始下载 ${created} 个新回放`)
    else HMessage.info('未发现新回放')
    emit('executed')
  } finally {
    executing.value = false
  }
}

function handleClose(value: boolean): void {
  emit('update:visible', value)
}
</script>

<template>
  <HDrawer
    :visible="visible"
    direction="rtl"
    size="560px"
    title="发现回放"
    @update:visible="handleClose"
  >
    <!-- 顶部动作栏：预览态显示下载按钮 -->
    <div v-if="phase === 'preview'" class="toolbar-row">
      <HButton variant="primary" :loading="executing" :disabled="selectedCount === 0" @click="handleExecuteSelected">
        下载选中 ({{ selectedCount }})
      </HButton>
      <HButton variant="secondary" :loading="executing" @click="handleDownloadAll">全部下载</HButton>
    </div>

    <!-- loading 态 -->
    <div v-if="phase === 'loading'" class="discover-loading">
      <span class="loading-text">正在发现回放…</span>
    </div>

    <!-- 预览勾选态 -->
    <div v-else-if="phase === 'preview'" class="discover-body">
      <!-- 错误项（频道级 yt-dlp 失败等），单独展示、不可勾选 -->
      <div v-if="previewErrors.length > 0" class="error-block">
        <div class="error-block-title">发现失败的主播（{{ previewErrors.length }}）</div>
        <div v-for="(item, idx) in previewErrors" :key="`err-${idx}`" class="error-row">
          <span class="row-title">{{ item.channel_id || '-' }}</span>
          <span class="row-error">{{ item.error }}</span>
        </div>
      </div>

      <HEmpty v-if="validPreviewItems.length === 0" description="未发现任何回放" />

      <div v-for="group in grouped" :key="group.channelId" class="group-block">
        <div class="group-header">
          <HCheckbox
            :model-value="groupAllSelected(group.channelId)"
            @update:model-value="(v) => toggleGroup(group.channelId, v)"
          >
            <strong>{{ group.channelId }}</strong>
            <span class="group-count">({{ group.items.length }})</span>
          </HCheckbox>
        </div>

        <div
          v-for="item in group.items"
          :key="`${item.channel_id}-${item.source_id}`"
          class="preview-row"
          :class="{ 'is-exists': item.exists }"
        >
          <HCheckbox
            :model-value="isSelected(item)"
            @update:model-value="(v) => toggleItem(item, v)"
          >
            <div class="row-title">{{ item.title || item.source_id }}</div>
          </HCheckbox>
          <HPill v-if="item.exists" variant="neutral">已处理</HPill>
          <HPill v-else variant="success">新</HPill>
        </div>
      </div>
    </div>

    <!-- 执行结果态 -->
    <div v-else class="discover-body">
      <div class="result-stats">
        <div class="stat-item success">
          <span>新建</span>
          <strong>{{ doneStats.created }}</strong>
        </div>
        <div class="stat-item">
          <span>跳过</span>
          <strong>{{ doneStats.skipped }}</strong>
        </div>
        <div class="stat-item danger">
          <span>错误</span>
          <strong>{{ doneStats.error }}</strong>
        </div>
      </div>

      <div class="result-list">
        <div
          v-for="item in doneItems"
          :key="`${item.channel_id}-${item.source_id}-${item.task_id ?? ''}`"
          class="result-row"
          :class="{ 'is-error': item.error, 'is-created': item.created && !item.error }"
        >
          <div class="row-main">
            <div class="row-title">{{ item.title || item.source_id || '-' }}</div>
            <HPill v-if="item.error" variant="danger">错误</HPill>
            <HPill v-else-if="item.created" variant="success">新建</HPill>
            <HPill v-else variant="neutral">跳过</HPill>
          </div>
          <div class="row-fields">
            <span>主播：{{ item.channel_id || '-' }}</span>
            <span>来源：{{ item.source_id || '-' }}</span>
            <span v-if="item.session_id">场次：{{ item.session_id }}</span>
            <span v-if="item.task_id">任务：{{ item.task_id }}</span>
          </div>
          <div v-if="item.error" class="row-error">{{ item.error }}</div>
        </div>
      </div>
    </div>
  </HDrawer>
</template>

<style scoped>
.toolbar-row {
  display: flex;
  gap: 10px;
  margin-bottom: 16px;
}

.discover-loading {
  min-height: 240px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.loading-text {
  color: var(--text-muted);
  font-size: 14px;
}

.discover-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.error-block {
  border: 1px solid var(--danger-border, #fcd3d3);
  border-radius: 8px;
  padding: 10px 12px;
  background: var(--danger-bg, #fef0f0);
}

.error-block-title {
  color: var(--danger, #f56c6c);
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 8px;
}

.error-row {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px 0;
  border-top: 1px solid var(--danger-border-light, #fde2e2);
}

.error-row:first-of-type {
  border-top: none;
}

.group-block {
  border: 1px solid var(--border-light, #ebeef5);
  border-radius: 8px;
  padding: 10px 12px;
  background: var(--canvas);
}

.group-header {
  padding-bottom: 8px;
  margin-bottom: 8px;
  border-bottom: 1px solid var(--border-lighter, #f0f0f0);
}

.group-count {
  color: var(--text-muted);
  font-weight: normal;
  margin-left: 4px;
}

.preview-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
}

.preview-row.is-exists {
  opacity: 0.7;
}

.preview-row .row-title {
  color: var(--text);
  font-size: 13px;
  word-break: break-word;
}

.result-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

.stat-item {
  border: 1px solid var(--border-light);
  border-radius: 8px;
  padding: 12px;
  background: var(--canvas);
}

.stat-item span {
  display: block;
  color: var(--text-muted);
  font-size: 12px;
  margin-bottom: 6px;
}

.stat-item strong {
  color: var(--text);
  font-size: 22px;
}

.stat-item.success strong {
  color: var(--success);
}

.stat-item.danger strong {
  color: var(--danger, #f56c6c);
}

.result-list {
  display: grid;
  gap: 10px;
}

.result-row {
  border: 1px solid var(--border-light);
  border-radius: 8px;
  padding: 12px;
  background: var(--canvas);
}

.result-row.is-created {
  border-color: var(--success-border, #d1edc4);
}

.result-row.is-error {
  border-color: var(--danger-border, #fcd3d3);
}

.row-main {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
}

.row-title {
  color: var(--text);
  font-weight: 600;
  line-height: 1.4;
  word-break: break-word;
}

.row-fields {
  display: grid;
  gap: 4px;
  margin-top: 10px;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.row-error {
  margin-top: 8px;
  color: var(--danger, #f56c6c);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
}
</style>
