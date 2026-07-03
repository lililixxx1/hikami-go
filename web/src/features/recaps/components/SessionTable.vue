<script setup lang="ts">
import { Download, Warning } from '@element-plus/icons-vue'
import TaskProgressBar from '@/components/task/TaskProgressBar.vue'
import { getFriendlySessionStatus, statusGroupMap } from '@/utils/friendlyStatus'
import { formatDateTime } from '@/utils/format'
import {
  getRowActions,
  decideRetry,
  retryHint,
  primaryActionType,
  type PrimaryAction,
} from '@/features/recaps/sessionActions'
import type { Session, Task, Capabilities, Channel } from '@/api/types'

const props = defineProps<{
  sessions: Session[]
  total: number
  loading: boolean
  channels: Channel[]
  tasks: Task[]
  capabilities: Capabilities | null
  actionLoadingId: string
}>()

const emit = defineEmits<{
  'open-recap': [session: Session]
  'run-action': [session: Session, action: PrimaryAction]
  fetch: [session: Session]
  retry: [session: Session]
}>()

const currentPage = defineModel<number>('currentPage', { default: 1 })
const pageSize = defineModel<number>('pageSize', { default: 20 })

// 行状态分级视觉(色条/失败底色/实心标签),复用 statusGroupMap
function statusRowClass(s: Session): string {
  for (const [group, states] of Object.entries(statusGroupMap)) {
    if (states.includes(s.status)) return `is-${group}`
  }
  return ''
}

function isCriticalStatus(s: Session): boolean {
  return s.status === 'failed' || statusGroupMap.published.includes(s.status)
}

function channelName(cid: string): string {
  return props.channels.find((c) => c.id === cid)?.name || cid
}

function sessionTask(s: Session): Task | null {
  return props.tasks.find((t) => t.id === s.current_task_id) ?? null
}

function sessionProgress(s: Session): number {
  return sessionTask(s)?.progress ?? 0
}

// 列表行(表A)动作集合
function rowActions(s: Session) {
  return getRowActions(s, props.capabilities, sessionTask(s))
}
</script>

<template>
  <div v-loading="loading" class="session-list">
    <div v-if="sessions.length === 0" class="empty-state">
      <el-empty description="没有匹配的场次" />
    </div>
    <div
      v-for="s in sessions"
      :key="s.id"
      class="session-row"
      :class="statusRowClass(s)"
      @click="emit('open-recap', s)"
    >
      <div class="session-left">
        <strong class="session-title">{{ s.title || '无标题' }}</strong>
        <div class="session-meta">
          <span>{{ channelName(s.channel_id) }}</span>
          <span>{{ formatDateTime(s.created_at) }}</span>
        </div>
      </div>
      <div
        v-if="['discovered', 'downloading', 'recording', 'importing', 'media_ready', 'asr_submitted', 'asr_done'].includes(s.status)"
        class="session-center"
      >
        <TaskProgressBar :progress="sessionProgress(s)" :status="sessionTask(s)?.status || 'pending'" />
        <span class="progress-label">{{ getFriendlySessionStatus(s).label }}</span>
      </div>
      <div class="session-right">
        <el-tag
          :type="getFriendlySessionStatus(s).color === 'success' ? 'success' : getFriendlySessionStatus(s).color === 'danger' ? 'danger' : getFriendlySessionStatus(s).color === 'warning' ? 'warning' : 'info'"
          :effect="isCriticalStatus(s) ? 'dark' : 'light'"
        >
          <el-icon v-if="s.status === 'failed'"><Warning /></el-icon>
          {{ getFriendlySessionStatus(s).label }}
        </el-tag>

        <!-- Action buttons: 由 sessionActions.getRowActions(表A) 统一决策 -->
        <!-- recap_done → 阅读回顾(打开抽屉,非动作;抽屉内才有 upload) -->
        <el-button v-if="rowActions(s).read" type="primary" size="small" @click.stop="emit('open-recap', s)">
          阅读回顾
        </el-button>
        <!-- published 行无「状态推进型」动作(B站专栏只能手动去 B站管理);
             「重新生成回顾」入口在阅读抽屉内(RecapDrawer)。local 不可用时显示取回。 -->
        <!-- failed → 重试(仅 retryable 才渲染;不可重试按原因给细化文案) -->
        <template v-if="s.status === 'failed'">
          <el-button
            v-if="rowActions(s).retry"
            type="danger"
            size="small"
            plain
            :loading="actionLoadingId === `${s.id}:retry`"
            @click.stop="emit('retry', s)"
          >
            重试
          </el-button>
          <span v-else-if="retryHint(decideRetry(s, sessionTask(s)))" class="no-retry-hint">
            {{ retryHint(decideRetry(s, sessionTask(s))) }}
          </span>
        </template>
        <!-- 主动作(media_ready→submit_asr / asr_done→generate_recap / uploaded→publish) -->
        <el-tooltip
          v-if="rowActions(s).primary"
          :content="rowActions(s).primary?.disabledReason"
          :disabled="!rowActions(s).primary?.disabled"
        >
          <el-button
            :type="primaryActionType(rowActions(s).primary!.name)"
            size="small"
            :disabled="rowActions(s).primary?.disabled"
            :loading="actionLoadingId === `${s.id}:${rowActions(s).primary?.name}`"
            @click.stop="emit('run-action', s, rowActions(s).primary!)"
          >
            {{ rowActions(s).primary?.label }}
          </el-button>
        </el-tooltip>
        <!-- 本地已清理：独立「取回」入口(与其它动作并存) -->
        <el-tooltip v-if="rowActions(s).fetch" content="本地文件已清理，点击从归档取回">
          <el-button
            type="info"
            size="small"
            plain
            :icon="Download"
            :loading="actionLoadingId === `${s.id}:fetch`"
            @click.stop="emit('fetch', s)"
          >
            取回
          </el-button>
        </el-tooltip>
      </div>
    </div>

    <!-- Pagination -->
    <div v-if="total > pageSize" class="pagination-row">
      <span>共 {{ total }} 条</span>
      <el-pagination
        v-model:current-page="currentPage"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="sizes, prev, pager, next"
      />
    </div>
  </div>
</template>

<style scoped>
.session-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.session-row {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 14px 16px;
  background: #fff;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  cursor: pointer;
  transition: border-color 0.15s;
}

/* 状态色条：左 4px 边框按状态着色，失败项整行加底色，关键状态一眼可扫 */
.session-row.is-published {
  border-left: 4px solid #67c23a;
}

.session-row.is-recap {
  border-left: 4px solid #409eff;
}

.session-row.is-processing {
  border-left: 4px solid #e6a23c;
}

.session-row.is-failed {
  border-left: 4px solid #f56c6c;
  background: var(--el-color-danger-light-9, #fef0f0);
}

.session-right .el-tag .el-icon {
  margin-right: 2px;
  vertical-align: middle;
}

.session-row:hover {
  border-color: #c6e2ff;
}

.session-left {
  flex: 1;
  min-width: 0;
}

.session-title {
  display: block;
  font-size: 14px;
  margin-bottom: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-meta {
  display: flex;
  gap: 12px;
  font-size: 12px;
  color: #909399;
}

.session-center {
  flex: 0 0 200px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.progress-label {
  font-size: 12px;
  color: #606266;
}

.session-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.no-retry-hint {
  color: var(--el-text-color-placeholder);
  font-size: 12px;
}

.empty-state {
  padding: 48px 0;
  text-align: center;
}

.pagination-row {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 12px;
  margin-top: 16px;
}

@media (max-width: 768px) {
  .session-row {
    flex-direction: column;
    align-items: flex-start;
  }

  .session-center {
    flex: 0 0 auto;
    width: 100%;
  }
}
</style>
