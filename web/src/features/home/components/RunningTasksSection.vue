<!-- web/src/features/home/components/RunningTasksSection.vue -->
<script setup lang="ts">
import type { Task } from '@/api/types-derived'
import { HButton, HProgress } from '@/components/ui'

defineProps<{
  tasks: Task[]
  cancellingId: string | null
}>()

const emit = defineEmits<{
  cancel: [taskId: string]
}>()

// 任务类型 → 中文标签
const typeLabels: Record<string, string> = {
  download: '下载',
  asr: '转写',
  recap: '回顾',
  upload: '上传',
  publish: '发布',
  archive: '归档',
  normalize: '标准化',
  fetch: '拉取',
}

function typeLabel(type: string): string {
  return typeLabels[type] || type
}
</script>

<template>
  <section class="section">
    <div class="section-header">
      <h3 class="section-title">运行中任务</h3>
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>类型</th>
            <th>主播</th>
            <th>进度</th>
            <th>消息</th>
            <th style="width: 80px;">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="task in tasks" :key="task.id">
            <td><span class="task-type">{{ typeLabel(task.type) }}</span></td>
            <td>{{ task.channel_name || task.channel_id }}</td>
            <td>
              <div class="progress-cell">
                <HProgress :progress="task.progress" :status="task.status === 'failed' ? 'failed' : 'active'" />
                <span class="progress-text">{{ task.progress }}</span>
              </div>
            </td>
            <td class="message-cell" :title="task.message">{{ task.message }}</td>
            <td>
              <HButton
                class="cancel-btn"
                variant="danger"
                size="xs"
                :loading="cancellingId === task.id"
                :disabled="cancellingId === task.id"
                @click="emit('cancel', task.id)"
              >
                取消
              </HButton>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<style scoped>
.section-header {
  margin-bottom: 12px;
}

.section-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.03em;
}

.table-wrap {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  overflow: hidden;
  box-shadow: var(--shadow-sm);
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

thead {
  background: var(--surface);
}

th {
  padding: 10px 14px;
  text-align: left;
  font-weight: 600;
  font-size: 11.5px;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.03em;
  border-bottom: 1px solid var(--border);
}

td {
  padding: 10px 14px;
  border-bottom: 1px solid var(--border);
  color: var(--text);
}

tr:last-child td {
  border-bottom: none;
}

tbody tr {
  transition: background 0.1s;
}

tbody tr:hover {
  background: var(--surface);
}

.task-type {
  font-weight: 500;
}

.progress-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.progress-cell :deep(.progress-bar) {
  width: 120px;
}

.progress-text {
  font-size: 12px;
  color: var(--text-muted);
}

.message-cell {
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-muted);
}
</style>
