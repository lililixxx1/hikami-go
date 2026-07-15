<!-- web/src/features/recaps/components/RecapDrawerV10.vue -->
<!--
  回顾抽屉(Phase 4,L2 测试)。最复杂的组件:md 渲染 + 动作矩阵(表B)+ 部分回顾 + 术语候选 + 编辑 + 导出。
  动作矩阵复用 sessionActions.ts 的 getDrawerActions / primaryActionType(不改状态机)。
  - .md-preview:marked + DOMPurify.sanitize(L2 测试断言渲染出 <h1>)。
  - 术语候选 pills:.suggested-term-btn 按钮,过滤 addedTerms,点击 emit add-term(L2 测试)。
  - recap_done → getDrawerActions 返回 upload(label 含「上传」;L2 测试断言)。
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { HMessage } from '@/components/ui/message'
import { HButton, HTextarea } from '@/components/ui'
import { getFriendlySessionStatus } from '@/utils/friendlyStatus'
import { formatDateTime } from '@/utils/format'
import {
  getDrawerActions,
  type PrimaryAction,
} from '@/features/recaps/sessionActions'
import type { Session, RecapContent, Capabilities, Channel } from '@/api/types-derived'
// sessionActions.ts 消费旧手写 types.ts 的 Session/Capabilities(全必填),与 generated 派生类型在
// TS 层不完全兼容(Phase 6 统一迁移)。状态机仅读 status/local_available,运行时安全;调用边界窄化。
import type { Session as LooseSession, Capabilities as LooseCapabilities } from '@/api/types-derived'

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
  saved: [sessionId: string]
}>()

// ---------- md 渲染(marked + DOMPurify) ----------
const renderedMarkdown = computed(() => {
  if (!props.content?.markdown) return ''
  return DOMPurify.sanitize(marked.parse(props.content.markdown) as string)
})

// ---------- 术语候选(过滤空 + 排除已添加) ----------
const suggestedTerms = computed(
  () => props.content?.suggested_terms?.filter((t) => t.trim()) ?? [],
)

function termAdded(term: string): boolean {
  return props.addedTerms.has(term.trim())
}

function handleAddTerm(term: string): void {
  const t = term.trim()
  if (!t || termAdded(t)) return
  // 不在此处标记「已添加」:壳在 upsertChannelEntry 成功后写入 addedTerms,避免失败误显示。
  emit('add-term', t)
}

// ---------- 抽屉主动作(表B): recap_done→upload; uploaded→publish ----------
function drawerPrimary(s: Session | null): PrimaryAction | undefined {
  if (!s) return undefined
  return getDrawerActions(s as unknown as LooseSession, props.capabilities as LooseCapabilities | null).primary
}

// ---------- 部分回顾(时间段,秒) ----------
const rangeStart = ref<string>('')
const rangeEnd = ref<string>('')

function toSeconds(hms: string): number | null {
  const m = hms.trim().match(/^(\d{1,2}):(\d{1,2}):(\d{1,2})$/)
  if (!m) return null
  const [, h, mm, s] = m
  return Number(h) * 3600 + Number(mm) * 60 + Number(s)
}

function handlePartial(): void {
  const start = toSeconds(rangeStart.value)
  const end = toSeconds(rangeEnd.value)
  if (start === null || end === null || end <= start) {
    HMessage.warning('请输入有效的开始/结束时间(HH:MM:SS)')
    return
  }
  emit('partial-range', start, end)
}

// ---------- 编辑模式 ----------
const editing = ref(false)
const draft = ref('')
const saving = ref(false)

function startEdit(): void {
  draft.value = props.content?.markdown ?? ''
  editing.value = true
}

async function saveEdit(): Promise<void> {
  if (!props.session) return
  // 在 await 前捕获稳定的 session ID，防止保存期间切换场次导致 emit 错误 ID
  const sessionId = props.session.id
  saving.value = true
  try {
    // 直接调 PUT recap/content(就近复用),保存后退出编辑态。
    const { updateRecapContent } = await import('@/api/sessions')
    await updateRecapContent(sessionId, draft.value)
    HMessage.success('回顾内容已保存')
    editing.value = false
    emit('saved', sessionId)
  } catch {
    // 错误 toast 由 client.ts 拦截器统一处理；保持编辑态让用户可重试
  } finally {
    saving.value = false
  }
}

// ---------- 导出 md ----------
function exportMarkdown(): void {
  const md = props.content?.markdown ?? ''
  if (!md) return
  const blob = new Blob([md], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${props.session?.slug || props.session?.id || 'recap'}.md`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

// ---------- 辅助 ----------
function channelName(cid: string): string {
  return props.channels.find((c) => c.id === cid)?.name || cid
}

function statusLabel(s: Session): string {
  return getFriendlySessionStatus(s).label
}

// session 变化时重置局部状态(addedTerms 由壳在 openRecap 时重置)
watch(
  () => props.session?.id,
  () => {
    rangeStart.value = ''
    rangeEnd.value = ''
    editing.value = false
  },
)
</script>

<template>
  <!--
    内联抽屉面板(非 Teleport):HDrawer 用 <Teleport to="body"> 会把内容移出 wrapper 根,
    导致集成测试的 wrapper.find('.md-preview')/wrapper.text() 查不到(L2 测试用 mount 不带 attachTo)。
    故 RecapDrawerV10 自管面板 DOM(overlay + header + body),保证内容在组件树内可被测试查询。
    生产视觉与 HDrawer 一致(右侧滑出 + 遮罩点击关闭)。
  -->
  <template v-if="visible">
    <div class="drawer-overlay" @click="emit('update:visible', false)" />
    <div class="drawer rtl open recap-drawer-panel" style="width: 600px;">
      <div class="drawer-header">
        <span class="drawer-title">{{ session?.title || '回顾' }}</span>
        <button type="button" class="drawer-close" aria-label="关闭" @click="emit('update:visible', false)">
          <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M4 4l8 8M12 4l-8 8" />
          </svg>
        </button>
      </div>
      <div class="drawer-body">
        <div v-if="loading" class="loading-block">加载中…</div>
        <template v-else-if="session">
          <!-- 状态行 -->
          <div class="drawer-status">
            <span class="status-label">{{ statusLabel(session) }}</span>
            <span class="drawer-date">{{ formatDateTime(session.created_at) }}</span>
            <span>{{ channelName(session.channel_id) }}</span>
          </div>

          <!-- 自定义时间段回顾(emit partial-range) -->
          <details class="range-recap">
            <summary>自定义时间段回顾</summary>
            <div class="range-form">
              <input
                v-model="rangeStart"
                class="time-input"
                type="text"
                placeholder="开始 HH:MM:SS"
              >
              <input
                v-model="rangeEnd"
                class="time-input"
                type="text"
                placeholder="结束 HH:MM:SS"
              >
              <HButton variant="primary" size="sm" :loading="partialLoading" @click="handlePartial">
                生成区间回顾
              </HButton>
            </div>
          </details>

          <template v-if="content?.available">
            <!-- 动作栏:复制 / 重新生成 / 抽屉主动作(表B) / 编辑 / 导出 -->
            <div class="drawer-actions">
              <HButton variant="secondary" size="sm" @click="emit('copy')">复制</HButton>
              <!-- 重新生成:覆盖本地 md,不碰 B站。仅 recap_done/published 有意义。非状态推进型,硬编码。 -->
              <HButton
                v-if="session.status === 'recap_done' || session.status === 'published'"
                variant="primary"
                size="sm"
                :loading="actionLoadingId === `${session.id}:regenerate`"
                @click="emit('regenerate')"
              >
                重新生成
              </HButton>
              <!-- 抽屉主动作(表B): recap_done→上传归档; uploaded→发布 -->
              <HButton
                v-if="drawerPrimary(session)"
                :variant="drawerPrimary(session)!.name === 'publish' ? 'primary' : 'primary'"
                size="sm"
                :disabled="drawerPrimary(session)?.disabled"
                :loading="actionLoadingId === `${session.id}:${drawerPrimary(session)?.name}`"
                :title="drawerPrimary(session)?.disabledReason"
                @click="session && drawerPrimary(session) && emit('run-action', session, drawerPrimary(session)!)"
              >
                {{ drawerPrimary(session)?.label }}
              </HButton>
              <HButton variant="secondary" size="sm" :disabled="editing" @click="startEdit">编辑</HButton>
              <HButton variant="secondary" size="sm" @click="exportMarkdown">导出</HButton>
            </div>

            <!-- 术语候选 pills(content.suggested_terms 过滤 addedTerms) -->
            <div v-if="suggestedTerms.length > 0" class="suggested-terms">
              <div class="suggested-title">建议加入术语表</div>
              <div class="suggested-term-list">
                <button
                  v-for="term in suggestedTerms"
                  :key="term"
                  type="button"
                  class="suggested-term-btn"
                  :class="{ added: termAdded(term) }"
                  :disabled="termAdded(term) || addingTerm === term.trim()"
                  @click="handleAddTerm(term)"
                >
                  {{ term }}<span v-if="termAdded(term)" class="term-suffix">已添加</span>
                  <span v-else-if="addingTerm === term.trim()" class="term-suffix">…</span>
                  <span v-else class="term-suffix">+</span>
                </button>
              </div>
            </div>

            <!-- 编辑态:HTextarea 本地 draft + 保存 -->
            <div v-if="editing" class="edit-block">
              <HTextarea v-model="draft" :rows="14" placeholder="编辑回顾 markdown" />
              <div class="edit-actions">
                <HButton variant="secondary" size="sm" @click="editing = false">取消</HButton>
                <HButton variant="primary" size="sm" :loading="saving" @click="saveEdit">保存</HButton>
              </div>
            </div>

            <!-- md 渲染(DOMPurify 净化) -->
            <div v-if="!editing" class="md-preview" v-html="renderedMarkdown" />
          </template>
          <div v-else class="empty-recap">回顾内容尚未生成</div>

          <!-- 技术信息(专家模式) -->
          <details v-if="isExpert" class="expert-info">
            <summary>技术信息</summary>
            <dl class="tech-list">
              <dt>Session ID</dt><dd>{{ session.id }}</dd>
              <dt>Status</dt><dd>{{ session.status }}</dd>
              <dt>Source</dt><dd>{{ session.source_type }}</dd>
              <dt v-if="session.last_error">Error</dt>
              <dd v-if="session.last_error" class="error-text">{{ session.last_error }}</dd>
              <dt v-if="session.publish_target">Publish</dt>
              <dd v-if="session.publish_target">{{ session.publish_target }}</dd>
            </dl>
          </details>
        </template>
      </div>
    </div>
  </template>
</template>

<style scoped>
.loading-block {
  min-height: 200px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary);
}

.drawer-status {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  font-size: 14px;
  color: var(--text-secondary);
}

.status-label {
  font-weight: 600;
  color: var(--text);
}

.drawer-date {
  color: var(--text-muted, var(--text-secondary));
  font-size: 13px;
}

.range-recap {
  margin-bottom: 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px 12px;
}

.range-recap summary {
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  color: var(--text);
}

.range-form {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
  margin-top: 8px;
}

.time-input {
  width: 130px;
  padding: 5px 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 13px;
  font-family: inherit;
}

.drawer-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}

.suggested-terms {
  margin-bottom: 16px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
}

.suggested-title {
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 8px;
  color: var(--text);
}

.suggested-term-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.suggested-term-btn {
  appearance: none;
  border: 1px solid var(--border);
  background: var(--bg, #fff);
  color: var(--text);
  padding: 4px 10px;
  border-radius: 14px;
  font-size: 13px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  transition: border-color 0.15s, background 0.15s;
}

.suggested-term-btn:hover:not(:disabled) {
  border-color: var(--accent, var(--primary, #409eff));
}

.suggested-term-btn.added {
  opacity: 0.55;
  cursor: default;
}

.suggested-term-btn:disabled {
  cursor: default;
}

.term-suffix {
  font-size: 11px;
  color: var(--text-secondary);
}

.edit-block {
  margin-bottom: 16px;
}

.edit-block :deep(.textarea) {
  width: 100%;
  font-family: var(--font-mono, monospace);
  font-size: 12.5px;
}

.edit-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 8px;
}

/* md-preview:marked 渲染容器(L2 测试断言 .md-preview 内含 <h1>) */
.md-preview {
  font-size: 14px;
  line-height: 1.8;
  color: var(--text);
  word-break: break-word;
}

.md-preview :deep(h1) { font-size: 20px; margin: 16px 0 10px; font-weight: 700; }
.md-preview :deep(h2) { font-size: 17px; margin: 14px 0 8px; font-weight: 700; }
.md-preview :deep(h3) { font-size: 15px; margin: 12px 0 6px; font-weight: 600; }
.md-preview :deep(p) { margin-bottom: 8px; }
.md-preview :deep(ul),
.md-preview :deep(ol) { padding-left: 20px; margin-bottom: 8px; }
.md-preview :deep(li) { margin-bottom: 4px; }
.md-preview :deep(strong) { font-weight: 600; }
.md-preview :deep(code) { font-family: var(--font-mono, monospace); font-size: 12.5px; }

.empty-recap {
  text-align: center;
  color: var(--text-secondary);
  padding: 48px 0;
}

.expert-info {
  margin-top: 16px;
  border-top: 1px solid var(--border);
  padding-top: 12px;
}

.expert-info summary {
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
}

.tech-list {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 4px 12px;
  margin: 8px 0 0;
  font-size: 12.5px;
}

.tech-list dt {
  color: var(--text-secondary);
}

.tech-list dd {
  margin: 0;
  word-break: break-all;
}

.error-text {
  color: var(--danger, #f56c6c);
}
</style>
