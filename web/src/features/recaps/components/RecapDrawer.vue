<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { ElMessage } from 'element-plus'
import { getFriendlySessionStatus } from '@/utils/friendlyStatus'
import { formatDateTime } from '@/utils/format'
import {
  getDrawerActions,
  primaryActionType,
  type PrimaryAction,
} from '@/features/recaps/sessionActions'
import type { Session, RecapContent, Capabilities, Channel } from '@/api/types'

const props = defineProps<{
  visible: boolean
  session: Session | null
  content: RecapContent | null
  loading: boolean
  capabilities: Capabilities | null
  isExpert: boolean
  channels: Channel[]
  actionLoadingId: string
  addingTerm: string
  partialLoading: boolean
  addedTerms: Set<string>
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  copy: []
  'run-action': [session: Session, action: PrimaryAction]
  regenerate: []
  'partial-range': [startSeconds: number, endSeconds: number]
  'add-term': [term: string]
}>()

// 抽屉内局部 UI 状态(时间段选择器);术语「已添加」集合由 content 派生 + 本地标记
const rangeStart = ref<Date | null>(null)
const rangeEnd = ref<Date | null>(null)

const renderedMarkdown = computed(() => {
  if (!props.content?.markdown) return ''
  return DOMPurify.sanitize(marked.parse(props.content.markdown) as string)
})

const suggestedTerms = computed(
  () => props.content?.suggested_terms?.filter((term) => term.trim()) ?? [],
)

// session 变化时重置时间段(addedTerms 由壳持有,壳在 openRecap 时重置)
watch(
  () => props.session?.id,
  () => {
    rangeStart.value = null
    rangeEnd.value = null
  },
)

function channelName(cid: string): string {
  return props.channels.find((c) => c.id === cid)?.name || cid
}

function drawerActions(s: Session | null) {
  // session 为 null 时抽屉不显示动作(模板用 v-else-if="session" 守卫,不会走到此分支)
  if (!s) return { primary: undefined }
  return getDrawerActions(s, props.capabilities)
}

function secondsOfDay(value: Date | null): number | null {
  if (!value) return null
  return value.getHours() * 3600 + value.getMinutes() * 60 + value.getSeconds()
}

function handlePartialRecap() {
  if (!props.session) return
  const start = secondsOfDay(rangeStart.value)
  const end = secondsOfDay(rangeEnd.value)
  if (start === null || end === null || end <= start) {
    ElMessage.warning('请选择有效的开始和结束时间')
    return
  }
  emit('partial-range', start, end)
}

function handleAddTerm(term: string) {
  const t = term.trim()
  // 不在此处标记「已添加」:由壳在 upsertChannelEntry 成功后写入 addedTerms prop,
  // 避免 API 失败时按钮误显示「已添加」。
  emit('add-term', t)
}

// 抽屉主动作(表B): recap_done→upload; uploaded→publish
function runPrimary() {
  if (!props.session) return
  const primary = drawerActions(props.session).primary
  if (primary) emit('run-action', props.session, primary)
}

function formatPublishTarget(raw: string): string {
  if (!raw) return ''
  if (raw.startsWith('{')) {
    try {
      const t = JSON.parse(raw) as { dyn_id?: string; draft_id?: string }
      if (t.dyn_id) return `动态 ${t.dyn_id}`
      if (t.draft_id) return `草稿 ${t.draft_id}`
    } catch {
      // fallthrough
    }
  }
  return raw.startsWith('draft:') ? `草稿 ${raw.slice(6)}` : `动态 ${raw}`
}
</script>

<template>
  <el-drawer
    :model-value="visible"
    :title="session?.title || '回顾'"
    direction="rtl"
    size="640px"
    @update:model-value="emit('update:visible', $event)"
  >
    <div v-if="loading" v-loading="true" style="min-height: 200px;" />
    <template v-else-if="session">
      <!-- Status -->
      <div class="drawer-status">
        <el-tag
          :type="getFriendlySessionStatus(session).color === 'success' ? 'success' : getFriendlySessionStatus(session).color === 'danger' ? 'danger' : 'warning'"
        >
          {{ getFriendlySessionStatus(session).label }}
        </el-tag>
        <span class="drawer-date">{{ formatDateTime(session.created_at) }}</span>
        <span>{{ channelName(session.channel_id) }}</span>
      </div>

      <!-- Recap content: 自定义时间段回顾 -->
      <el-collapse class="range-recap">
        <el-collapse-item title="自定义时间段回顾" name="range">
          <div class="range-form">
            <el-time-picker v-model="rangeStart" placeholder="开始" format="HH:mm:ss" />
            <el-time-picker v-model="rangeEnd" placeholder="结束" format="HH:mm:ss" />
            <el-button type="primary" :loading="partialLoading" @click="handlePartialRecap">生成</el-button>
          </div>
        </el-collapse-item>
      </el-collapse>

      <template v-if="content?.available">
        <div class="drawer-actions">
          <el-button size="small" @click="emit('copy')">复制 Markdown</el-button>
          <!-- 重新生成回顾:覆盖本地 md,不碰 B站。仅 recap_done/published 有意义(其它状态走主动作生成)。
               非状态推进型动作,故不进 getDrawerActions,在此硬编码。 -->
          <el-button
            v-if="session.status === 'recap_done' || session.status === 'published'"
            size="small"
            type="warning"
            plain
            :loading="actionLoadingId === `${session.id}:regenerate`"
            @click="emit('regenerate')"
          >
            重新生成
          </el-button>
          <!-- 抽屉主动作(表B): recap_done→upload; uploaded→publish; published/failed 无 -->
          <el-button
            v-if="drawerActions(session).primary"
            :type="primaryActionType(drawerActions(session).primary!.name)"
            size="small"
            :disabled="drawerActions(session).primary?.disabled"
            :loading="actionLoadingId === `${session.id}:${drawerActions(session).primary?.name}`"
            @click="runPrimary"
          >
            {{ drawerActions(session).primary?.label }}
          </el-button>
        </div>
        <el-alert
          v-if="suggestedTerms.length > 0"
          class="suggested-terms"
          title="建议加入术语表"
          type="info"
          :closable="false"
          show-icon
        >
          <div class="suggested-term-list">
            <div
              v-for="term in suggestedTerms"
              :key="term"
              class="suggested-term-item"
              :class="{ added: addedTerms.has(term.trim()) }"
            >
              <el-tag :type="addedTerms.has(term.trim()) ? 'info' : 'primary'" effect="plain">
                {{ term }}
              </el-tag>
              <el-button
                size="small"
                type="primary"
                link
                :disabled="addedTerms.has(term.trim())"
                :loading="addingTerm === term.trim()"
                @click="handleAddTerm(term)"
              >
                {{ addedTerms.has(term.trim()) ? '已添加' : '添加' }}
              </el-button>
            </div>
          </div>
        </el-alert>
        <el-divider />
        <div class="markdown-body" v-html="renderedMarkdown" />
      </template>
      <el-empty v-else description="回顾内容尚未生成" />

      <!-- Expert: technical info -->
      <template v-if="isExpert">
        <el-divider>技术信息</el-divider>
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="Session ID">{{ session.id }}</el-descriptions-item>
          <el-descriptions-item label="Status">{{ session.status }}</el-descriptions-item>
          <el-descriptions-item label="Source">{{ session.source_type }}</el-descriptions-item>
          <el-descriptions-item v-if="session.last_error" label="Error">
            <span style="color: #f56c6c;">{{ session.last_error }}</span>
          </el-descriptions-item>
          <el-descriptions-item v-if="session.publish_target" label="Publish">
            {{ formatPublishTarget(session.publish_target) }}
          </el-descriptions-item>
        </el-descriptions>
      </template>
    </template>
  </el-drawer>
</template>

<style scoped>
/* 对齐原 RecapsView 抽屉样式(阶段3 拆分时简化了,现按 codex 残留 improvement 补全) */
.drawer-status {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  font-size: 14px;
  color: #606266;
}

.drawer-date {
  color: #909399;
  font-size: 13px;
}

.range-recap {
  margin-bottom: 16px;
}

.range-form {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}

.drawer-actions {
  display: flex;
  gap: 8px;
}

.suggested-terms {
  margin-top: 12px;
}

.suggested-term-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 8px;
}

.suggested-term-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.suggested-term-item.added {
  opacity: 0.55;
}

.markdown-body {
  font-size: 14px;
  line-height: 1.8;
  color: #303133;
  word-break: break-word;
}
</style>
