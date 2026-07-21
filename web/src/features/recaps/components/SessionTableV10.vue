<!-- web/src/features/recaps/components/SessionTableV10.vue -->
<!--
  场次表(Phase 4)。
  列:标题 / 主播(session.channel_name) / 状态(HPill via friendlyStatus) / 创建时间 / 进度(WS task by current_task_id) / 操作。
  用原生 <table>(非 HTable):集成测试断言 tbody tr 点击 + tbody td 内容 + .progress-bar-fill style,
  HTable 的 cell-slot 无法稳定满足这些断言(参考 Phase 2 RunningTasksSection 的原生 table 模式)。
  动作矩阵复用 sessionActions.ts(getRowActions / decideRetry / primaryActionType),不改状态机。
-->
<script setup lang="ts">
import { computed } from 'vue'
import { HButton, HPill, HProgress } from '@/components/ui'
import { getFriendlySessionStatus } from '@/utils/friendlyStatus'
import { formatDateTime } from '@/utils/format'
import {
  getRowActions,
  decideRetry,
  retryHint,
  canFetchLocal,
  primaryActionType,
  type PrimaryAction,
} from '@/features/recaps/sessionActions'
import type { Session, Task, Capabilities, Channel } from '@/api/types-derived'
// sessionActions.ts 消费的是旧手写 types.ts 的 Session/Task/Capabilities(全必填),与 generated
// 派生类型(optional 字段)在 TS 层不完全兼容(Phase 6 才统一迁移)。状态机仅读取 status/id/
// local_available 等字段,运行时安全;这里在调用边界窄化转换,避免修改 41 测试保护的 sessionActions.ts。
import type { Session as LooseSession, Task as LooseTask, Capabilities as LooseCapabilities } from '@/api/types-derived'

const props = defineProps<{
  sessions: Session[]
  tasks: Task[]
  capabilities: Capabilities | null
  channels: Channel[]
  actionLoadingId: string
  currentPage: number
  pageSize: number
}>()

const emit = defineEmits<{
  'open-recap': [session: Session]
  'run-action': [session: Session, action: PrimaryAction]
  fetch: [session: Session]
  retry: [session: Session]
  reset: [session: Session] // 修复 2026-07-20 BUG #2:重置失败场次到 media_ready
  'update:currentPage': [value: number]
  'update:pageSize': [value: number]
}>()

// 当前页任务总数(壳做过滤+分页后传入 sessions;totalPages 仅用于翻页按钮显示)
const totalPages = computed(() => Math.max(1, Math.ceil(props.sessions.length / props.pageSize)))

// 按 current_task_id 关联任务(WS 进度对接):只在 status 处于「运行中族」时显示进度条
const PROGRESS_STATUSES = new Set([
  'discovered', 'downloading', 'recording', 'importing',
  'media_ready', 'asr_submitted', 'asr_done',
])
function sessionTask(s: Session): Task | null {
  return props.tasks.find((t) => t.id === s.current_task_id) ?? null
}
function showProgress(s: Session): boolean {
  return PROGRESS_STATUSES.has(s.status) && !!sessionTask(s)
}

function statusVariant(s: Session): 'success' | 'warning' | 'danger' | 'info' {
  const c = getFriendlySessionStatus(s).color
  if (c === 'success') return 'success'
  if (c === 'warning') return 'warning'
  if (c === 'danger') return 'danger'
  return 'info'
}

// UnassignedChannelLabel 是系统占位 channel _unassigned 的展示标签(2026-07-19 解耦改动)。
// 回放类下载/导入不选主播时场次挂到 _unassigned,UI 统一显示「未分类」而非原始 id。
const UnassignedChannelID = '_unassigned'
const UnassignedChannelLabel = '未分类'

function channelName(s: Session): string {
  // 优先用 session 自带 channel_name(后端已填充),回退到 channels prop
  if (s.channel_name && s.channel_name !== UnassignedChannelID) return s.channel_name
  if (s.channel_id === UnassignedChannelID) return UnassignedChannelLabel
  return props.channels.find((c) => c.id === s.channel_id)?.name || s.channel_id
}

// 列表行(表A)动作集合(复用状态机)。派生→loose 窄化转换(见文件头注释)。
function rowActions(s: Session) {
  return getRowActions(s as unknown as LooseSession, props.capabilities as LooseCapabilities | null, sessionTask(s) as unknown as LooseTask | null)
}

// failed 行的重试占位文案(模板用)。状态机仅读 status,窄化转换安全。
function retryHintText(s: Session): string {
  return retryHint(decideRetry(s as unknown as LooseSession, sessionTask(s) as unknown as LooseTask | null))
}

// local_available=false → 独立取回按钮(状态机 canFetchLocal 仅读 local_available)。
function canFetch(s: Session): boolean {
  return canFetchLocal(s as unknown as LooseSession)
}

function onPrev() {
  if (props.currentPage > 1) emit('update:currentPage', props.currentPage - 1)
}
function onNext() {
  if (props.currentPage < totalPages.value) emit('update:currentPage', props.currentPage + 1)
}
</script>

<template>
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th>标题</th>
          <th>主播</th>
          <th>状态</th>
          <th>创建时间</th>
          <th style="width: 140px;">进度</th>
          <th style="width: 200px;">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="s in sessions"
          :key="s.id"
          class="session-row"
          @click="emit('open-recap', s)"
        >
          <td>{{ s.title || s.id }}</td>
          <td>{{ channelName(s) }}</td>
          <td>
            <HPill :variant="statusVariant(s)">{{ getFriendlySessionStatus(s).label }}</HPill>
          </td>
          <td class="muted">{{ formatDateTime(s.created_at) }}</td>
          <td>
            <HProgress
              v-if="showProgress(s)"
              :progress="sessionTask(s)?.progress ?? 0"
              :status="sessionTask(s)?.status === 'failed' ? 'failed' : 'active'"
            />
            <span v-else class="muted">—</span>
          </td>
          <td class="actions-cell" @click.stop>
            <!-- recap_done → 阅读回顾(打开抽屉,非状态推进型动作) -->
            <HButton
              v-if="rowActions(s).read"
              variant="primary"
              size="xs"
              @click="emit('open-recap', s)"
            >
              阅读回顾
            </HButton>
            <!-- failed → 重试(仅 retryable 才渲染) -->
            <HButton
              v-if="rowActions(s).retry"
              variant="danger"
              size="xs"
              :loading="actionLoadingId === `${s.id}:retry`"
              @click="emit('retry', s)"
            >
              重试
            </HButton>
            <span v-else-if="s.status === 'failed' && retryHintText(s)" class="no-retry-hint">
              {{ retryHintText(s) }}
            </span>
            <!-- failed → 重置到 media_ready(仅 ASR 失败 + local_available,与 retry 并存)
                 修复 2026-07-20 BUG #2:reset 允许用户从 media_ready 重新提交 ASR -->
            <HButton
              v-if="rowActions(s).reset"
              variant="secondary"
              size="xs"
              :loading="actionLoadingId === `${s.id}:reset`"
              @click="emit('reset', s)"
            >
              重置
            </HButton>
            <!-- 主动作(media_ready→submit_asr / asr_done→generate_recap / uploaded→publish) -->
            <HButton
              v-if="rowActions(s).primary"
              :variant="primaryActionType(rowActions(s).primary!.name) === 'success' ? 'primary' : 'primary'"
              size="xs"
              :disabled="rowActions(s).primary?.disabled"
              :loading="actionLoadingId === `${s.id}:${rowActions(s).primary?.name}`"
              :title="rowActions(s).primary?.disabledReason"
              @click="emit('run-action', s, rowActions(s).primary!)"
            >
              {{ rowActions(s).primary?.label }}
            </HButton>
            <!-- local 不可用 → 独立取回(与其它动作并存) -->
            <HButton
              v-if="canFetch(s)"
              variant="secondary"
              size="xs"
              :loading="actionLoadingId === `${s.id}:fetch`"
              @click="emit('fetch', s)"
            >
              取回
            </HButton>
            <!-- 不可重试的 failed 占位由上方 hint 处理;无任何动作的行留空 -->
          </td>
        </tr>
        <tr v-if="sessions.length === 0">
          <td colspan="6" class="empty-row">没有匹配的场次</td>
        </tr>
      </tbody>
    </table>

    <!-- 翻页栏(壳已分页,这里仅 prev/next + 当前页显示) -->
    <div v-if="totalPages > 1" class="pagination-row">
      <HButton variant="secondary" size="xs" :disabled="currentPage <= 1" @click="onPrev">上一页</HButton>
      <span class="page-info">{{ currentPage }}/{{ totalPages }}</span>
      <HButton variant="secondary" size="xs" :disabled="currentPage >= totalPages" @click="onNext">下一页</HButton>
    </div>
  </div>
</template>

<style scoped>
.table-wrap {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg, 12px);
  overflow: hidden;
  background: var(--bg, #fff);
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
  vertical-align: middle;
}

tr:last-child td {
  border-bottom: none;
}

tbody tr {
  cursor: pointer;
  transition: background 0.1s;
}

tbody tr:hover {
  background: var(--surface);
}

.muted {
  color: var(--text-muted, var(--text-secondary));
}

.no-retry-hint {
  color: var(--text-muted, var(--text-secondary));
  font-size: 12px;
}

.actions-cell {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}

.empty-row {
  text-align: center;
  color: var(--text-muted, var(--text-secondary));
  padding: 32px 0;
}

.pagination-row {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border-top: 1px solid var(--border);
}

.page-info {
  font-size: 13px;
  color: var(--text-secondary);
}
</style>
