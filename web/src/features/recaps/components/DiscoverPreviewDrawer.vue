<!-- web/src/features/recaps/components/DiscoverPreviewDrawer.vue -->
<!--
  两步式发现回放预览抽屉(Phase 4,L3 视觉验证)。
  纯展示型:壳负责 preview/execute API 调用(见 DiscoverResultDrawer 的旧实现),本组件只接收
  预览结果 items + 执行态,本地维护勾选索引集合,提交时把勾选项映射成 DiscoverPickItem 回传。
  - exists=true 的项灰显 + 标「已存在」+ 禁止勾选(幂等,已建过 download 场次)。
  - footer:「全部下载」(直连旧一键下载) + 「执行勾选」(空选禁用)。
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { HDrawer, HButton, HCheckbox, HPill } from '@/components/ui'
import type { DiscoverResult, DiscoverPickItem } from '@/api/types-derived'

const props = defineProps<{
  visible: boolean
  items: DiscoverResult[]
  executing: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  execute: [picked: DiscoverPickItem[]]
  'discover-all': []
}>()

// 勾选索引集合(以 items 数组下标为 key;Set 不响应式,变更时整体替换)
const picked = ref<Set<number>>(new Set())

// 可勾选项:排除 error 项与空 source_id(无法建场次)。exists 项允许显示但禁止勾选。
const pickableIndices = computed(() =>
  props.items
    .map((it, idx) => ({ it, idx }))
    .filter(({ it }) => !it.error && it.channel_id && it.source_id),
)

const pickableCount = computed(() => pickableIndices.value.filter(({ it }) => !it.exists).length)
const pickedCount = computed(() => picked.value.size)

const allNewPicked = computed(() => {
  if (pickableCount.value === 0) return false
  return pickableIndices.value.every(({ it, idx }) => it.exists || picked.value.has(idx))
})

function isPicked(idx: number): boolean {
  return picked.value.has(idx)
}

function toggle(idx: number, checked: boolean): void {
  const next = new Set(picked.value)
  if (checked) next.add(idx)
  else next.delete(idx)
  picked.value = next
}

function toggleAll(checked: boolean): void {
  const next = new Set<number>()
  if (checked) {
    for (const { it, idx } of pickableIndices.value) {
      if (!it.exists) next.add(idx)
    }
  }
  picked.value = next
}

// 提交:把勾选索引映射成 DiscoverPickItem(channel_id/source_id/title/source_url)。
function handleExecute(): void {
  const picks: DiscoverPickItem[] = []
  for (const idx of picked.value) {
    const it = props.items[idx]
    if (!it) continue
    picks.push({
      channel_id: it.channel_id,
      source_id: it.source_id,
      title: it.title,
      source_url: it.source_url ?? '',
    })
  }
  if (picks.length === 0) return
  emit('execute', picks)
}

// 抽屉打开/关闭时重置勾选
watch(
  () => props.visible,
  (v) => {
    if (v) picked.value = new Set()
  },
)
</script>

<template>
  <HDrawer
    :visible="visible"
    title="发现回放预览"
    size="560px"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="discover-preview">
      <!-- 错误项(频道级 yt-dlp 失败等),单独展示、不可勾选 -->
      <div v-if="items.some((i) => i.error)" class="error-block">
        <div class="error-block-title">发现失败的主播</div>
        <div v-for="(item, idx) in items.filter((i) => i.error)" :key="`err-${idx}`" class="error-row">
          <span class="row-title">{{ item.channel_id || '-' }}</span>
          <span class="row-error">{{ item.error }}</span>
        </div>
      </div>

      <div v-if="pickableIndices.length === 0" class="empty">未发现任何回放</div>

      <!-- 全选栏(仅对「新」项有效) -->
      <div v-if="pickableCount > 0" class="select-all-row">
        <HCheckbox
          :model-value="allNewPicked"
          @update:model-value="toggleAll($event)"
        >
          全选新回放
        </HCheckbox>
        <span class="count-text">共 {{ pickableCount }} 条新</span>
      </div>

      <div class="item-list">
        <template v-for="{ it, idx } in pickableIndices" :key="`${it.channel_id}-${it.source_id}`">
          <div class="preview-row" :class="{ 'is-exists': it.exists }">
            <HCheckbox
              :model-value="it.exists ? false : isPicked(idx)"
              :disabled="it.exists"
              @update:model-value="toggle(idx, $event)"
            />
            <div class="row-body">
              <div class="row-title-line">
                <span class="row-title">{{ it.title || it.source_id }}</span>
                <HPill v-if="it.exists" variant="neutral">已存在</HPill>
                <HPill v-else variant="success">新</HPill>
              </div>
              <div class="row-sub">
                <span v-if="it.channel_id">{{ it.channel_id }}</span>
                <span v-if="it.source_url" class="row-url">{{ it.source_url }}</span>
              </div>
            </div>
          </div>
        </template>
      </div>

      <div class="drawer-footer">
        <HButton variant="secondary" size="sm" :loading="executing" @click="emit('discover-all')">
          全部下载
        </HButton>
        <HButton
          variant="primary"
          size="sm"
          :disabled="pickedCount === 0"
          :loading="executing"
          @click="handleExecute"
        >
          执行勾选{{ pickedCount > 0 ? ` (${pickedCount})` : '' }}
        </HButton>
      </div>
    </div>
  </HDrawer>
</template>

<style scoped>
.discover-preview {
  display: flex;
  flex-direction: column;
  gap: 12px;
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

.empty {
  text-align: center;
  color: var(--text-secondary);
  padding: 32px 0;
}

.select-all-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 10px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
}

.count-text {
  font-size: 12px;
  color: var(--text-secondary);
}

.item-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.preview-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg, #fff);
}

.preview-row.is-exists {
  opacity: 0.6;
}

.row-body {
  flex: 1;
  min-width: 0;
}

.row-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.row-title {
  font-weight: 600;
  font-size: 13px;
  color: var(--text);
  word-break: break-word;
}

.row-sub {
  display: flex;
  gap: 12px;
  margin-top: 4px;
  font-size: 12px;
  color: var(--text-secondary);
  flex-wrap: wrap;
}

.row-url {
  color: var(--text-muted, var(--text-secondary));
  word-break: break-all;
}

.row-error {
  color: var(--danger, #f56c6c);
  font-size: 12px;
}

.drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
  position: sticky;
  bottom: 0;
  background: var(--bg, #fff);
}
</style>
