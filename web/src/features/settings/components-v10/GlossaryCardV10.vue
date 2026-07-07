<!--
  GlossaryCardV10.vue(Phase 5 Task 5.3)。全局术语表卡。
  移植自 GlossaryEditor.vue(EP),scope='global'。
  业务逻辑(CRUD/列表/批量/导入导出)复用 useGlossaryEntries composable。
  组件保留纯 UI:表格 + 增删改 HDialog + 候选审批区 + 批量多选 + 启用/禁用 + 导入导出 + 备注编辑。
  - 候选审批:listGlobalCandidates(pending)→ 列表 + "加入术语表"(approveGlobalCandidate)。
  - 批量:checkbox 多选 + 批量删除/启用/禁用(composable.batchDelete/batchToggle)。
  - 启用/禁用:每行 checkbox toggle(composable.toggleEntry)。
  - 导入导出:composable.exportJSON / importEntries(json)。
  - 备注编辑:GET note → 双击进入编辑 → PUT updateGlobalNote。
  emit saved。L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HCard, HButton, HInput, HCheckbox, HPill, HDialog, HEmpty } from '@/components/ui'
import { useGlossaryEntries, isDuplicateError } from '@/features/channel/useGlossaryEntries'
import { listGlobalCandidates, approveGlobalCandidate, rejectGlobalCandidate } from '@/api/glossary'
import { getGlobalNote, updateGlobalNote } from '@/api/glossary'
import type { GlossaryEntry, GlossaryCandidate } from '@/api/types'

const emit = defineEmits<{ saved: [] }>()

const {
  loading, editableEntries,
  fetchData, addEntry, deleteEntry, toggleEntry,
  batchToggle, batchDelete, importEntries, exportJSON,
} = useGlossaryEntries({
  scope: () => 'global',
  channelId: () => '',
  channelName: () => '',
  showGlobalReadonly: () => false,
})

// --- 选中态(批量) ---
const selectedIds = ref<Set<number>>(new Set())
function toggleSelect(id: number, checked: boolean) {
  const next = new Set(selectedIds.value)
  if (checked) {
    next.add(id)
  } else {
    next.delete(id)
  }
  selectedIds.value = next
}
const allSelected = computed(() =>
  editableEntries.value.length > 0 && editableEntries.value.every(e => selectedIds.value.has(e.id)),
)
const someSelected = computed(() => selectedIds.value.size > 0 && !allSelected.value)
function toggleSelectAll(checked: boolean) {
  selectedIds.value = checked ? new Set(editableEntries.value.map(e => e.id)) : new Set()
}

// --- 增(对话框) ---
const addDialogVisible = ref(false)
const addForm = ref({ term: '', canonical: '', category: '' })
const addSaving = ref(false)
function showAddDialog() {
  addForm.value = { term: '', canonical: '', category: '' }
  addDialogVisible.value = true
}
async function handleAdd() {
  const term = addForm.value.term.trim()
  if (!term) { HMessage.warning('请填写词条'); return }
  const canonical = addForm.value.canonical.trim() || term
  addSaving.value = true
  try {
    await addEntry(term, canonical, addForm.value.category.trim())
    HMessage.success('词条已添加')
    addDialogVisible.value = false
    emit('saved')
  } catch (error) {
    if (isDuplicateError(error)) { HMessage.warning('词条已存在'); return }
    throw error
  } finally {
    addSaving.value = false
  }
}

// --- 删(单条) ---
async function handleDelete(entry: GlossaryEntry) {
  const ok = await deleteEntry(entry)
  if (ok) emit('saved')
}

// --- 切换启用(失败回滚) ---
async function handleToggle(entry: GlossaryEntry, enabled: boolean) {
  const prev = entry.enabled
  try {
    await toggleEntry(entry, enabled)
  } catch {
    entry.enabled = prev
  }
}

// --- 批量 ---
async function handleBatchDelete() {
  if (selectedIds.value.size === 0) return
  const ok = await batchDelete([...selectedIds.value])
  if (ok) { selectedIds.value = new Set(); emit('saved') }
}
async function handleBatchToggle(enabled: boolean) {
  if (selectedIds.value.size === 0) return
  const ok = await batchToggle([...selectedIds.value], enabled)
  if (ok) { selectedIds.value = new Set(); emit('saved') }
}

// --- 导入/导出 ---
const importDialogVisible = ref(false)
const importContent = ref('')
const importing = ref(false)
function showImportDialog() {
  importContent.value = ''
  importDialogVisible.value = true
}
async function handleImport() {
  if (!importContent.value.trim()) { HMessage.warning('请输入 JSON 内容'); return }
  importing.value = true
  try {
    await importEntries(importContent.value.trim(), 'json')
    importDialogVisible.value = false
    emit('saved')
  } finally {
    importing.value = false
  }
}
async function handleImportFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  input.value = ''
  importing.value = true
  try {
    const content = await file.text()
    await importEntries(content, 'json')
    importDialogVisible.value = false
    emit('saved')
  } finally {
    importing.value = false
  }
}

// --- 候选审批 ---
const candidates = ref<GlossaryCandidate[]>([])
const candidatesLoading = ref(false)
async function loadCandidates() {
  candidatesLoading.value = true
  try {
    const res = await listGlobalCandidates('pending')
    candidates.value = res.items ?? []
  } catch { /* ignore */ }
  finally { candidatesLoading.value = false }
}
async function handleApprove(c: GlossaryCandidate) {
  try {
    await approveGlobalCandidate(c.id)
    HMessage.success(`「${c.term}」已加入术语表`)
    await Promise.all([fetchData(), loadCandidates()])
    emit('saved')
  } catch { /* error shown by interceptor */ }
}
async function handleReject(c: GlossaryCandidate) {
  try {
    await rejectGlobalCandidate(c.id)
    HMessage.success('已拒绝候选词')
    await loadCandidates()
  } catch { /* error shown by interceptor */ }
}

// --- 备注(note) ---
const note = ref('')
const noteLoading = ref(false)
const noteEditing = ref(false)
const noteDraft = ref('')
async function loadNote() {
  noteLoading.value = true
  try {
    const res = await getGlobalNote()
    note.value = res.note
    noteDraft.value = res.note
  } catch { /* ignore */ }
  finally { noteLoading.value = false }
}
function startEditNote() {
  noteDraft.value = note.value
  noteEditing.value = true
}
async function saveNote() {
  try {
    const res = await updateGlobalNote(noteDraft.value)
    note.value = res.note
    noteEditing.value = false
    HMessage.success('备注已保存')
  } catch { /* error shown by interceptor */ }
}

onMounted(async () => {
  await Promise.all([fetchData(), loadCandidates(), loadNote()])
})
defineExpose({ reload: fetchData })
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">全局术语表</span>
      <HPill variant="neutral">{{ editableEntries.length }} 个词条</HPill>
    </template>

    <div class="glossary-toolbar">
      <HButton variant="primary" size="sm" @click="showAddDialog">+ 添加词条</HButton>
      <HButton variant="secondary" size="sm" @click="exportJSON">导出</HButton>
      <HButton variant="secondary" size="sm" :loading="importing" @click="showImportDialog">导入</HButton>
      <HButton v-if="someSelected || allSelected" variant="danger" size="sm" @click="handleBatchDelete">
        批量删除({{ selectedIds.size }})
      </HButton>
      <HButton v-if="someSelected || allSelected" variant="secondary" size="sm" @click="handleBatchToggle(true)">
        批量启用
      </HButton>
      <HButton v-if="someSelected || allSelected" variant="secondary" size="sm" @click="handleBatchToggle(false)">
        批量禁用
      </HButton>
      <HButton variant="ghost" size="sm" @click="loadCandidates">刷新候选</HButton>
    </div>

    <!-- 词条表 -->
    <div v-if="loading" class="form-hint">加载中…</div>
    <HEmpty v-else-if="!editableEntries.length" description="暂无词条,点击「添加词条」或审批下方候选词" />
    <table v-else class="tool-table">
      <thead>
        <tr>
          <th style="width: 32px;">
            <HCheckbox :model-value="allSelected" @update:model-value="(v: boolean) => toggleSelectAll(v)" />
          </th>
          <th>词条</th>
          <th>正确写法</th>
          <th>分类</th>
          <th style="width: 60px;">启用</th>
          <th style="width: 80px;">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="entry in editableEntries" :key="entry.id">
          <td>
            <HCheckbox
              :model-value="selectedIds.has(entry.id)"
              @update:model-value="(v: boolean) => toggleSelect(entry.id, v)"
            />
          </td>
          <td>{{ entry.term }}</td>
          <td>{{ entry.canonical }}</td>
          <td>{{ entry.category || '-' }}</td>
          <td>
            <HCheckbox
              :model-value="entry.enabled"
              @update:model-value="(v: boolean) => handleToggle(entry, v)"
            />
          </td>
          <td>
            <HButton variant="danger" size="xs" @click="handleDelete(entry)">删除</HButton>
          </td>
        </tr>
      </tbody>
    </table>

    <!-- 备注编辑(双击进入) -->
    <div class="note-block">
      <template v-if="!noteEditing">
        <div @dblclick="startEditNote" :title="'双击编辑备注'">
          <strong>备注:</strong>{{ noteLoading ? '加载中…' : (note || '(空,双击编辑)') }}
        </div>
      </template>
      <template v-else>
        <HInput v-model="noteDraft" placeholder="备注(可选)" />
        <div style="margin-top: 6px; display: flex; gap: 8px;">
          <HButton variant="primary" size="sm" @click="saveNote">保存备注</HButton>
          <HButton variant="secondary" size="sm" @click="noteEditing = false">取消</HButton>
        </div>
      </template>
    </div>

    <!-- 候选审批区 -->
    <div style="margin-top: 16px; border-top: 1px solid var(--border-light); padding-top: 14px;">
      <div class="form-label" style="margin-bottom: 8px;">候选术语(待审批)</div>
      <div v-if="candidatesLoading" class="form-hint">加载中…</div>
      <div v-else-if="!candidates.length" class="form-hint">暂无候选术语。</div>
      <div v-for="c in candidates" :key="c.id" class="candidate-row">
        <span class="term">{{ c.term }}</span>
        <span class="count">出现 {{ c.occurrence_count }} 次 · {{ c.session_count }} 场</span>
        <span v-if="c.reason" class="count">{{ c.reason }}</span>
        <div style="margin-left: auto; display: flex; gap: 6px;">
          <HButton variant="primary" size="xs" @click="handleApprove(c)">加入术语表</HButton>
          <HButton variant="ghost" size="xs" @click="handleReject(c)">拒绝</HButton>
        </div>
      </div>
    </div>

    <!-- 增加词条对话框 -->
    <HDialog v-model:visible="addDialogVisible" title="添加词条">
      <div class="form-field" style="margin-bottom: 12px;">
        <span class="form-label">错误写法(必填)</span>
        <HInput v-model="addForm.term" placeholder="如:蜜瓜" />
      </div>
      <div class="form-field" style="margin-bottom: 12px;">
        <span class="form-label">正确写法(留空则同词条)</span>
        <HInput v-model="addForm.canonical" placeholder="如:蜜瓜熊" />
      </div>
      <div class="form-field">
        <span class="form-label">分类(可选)</span>
        <HInput v-model="addForm.category" placeholder="如:昵称" />
      </div>
      <template #footer>
        <HButton variant="secondary" size="sm" @click="addDialogVisible = false">取消</HButton>
        <HButton variant="primary" size="sm" :loading="addSaving" @click="handleAdd">添加</HButton>
      </template>
    </HDialog>

    <!-- 导入对话框 -->
    <HDialog v-model:visible="importDialogVisible" title="导入术语表" width="520px">
      <div class="form-hint" style="margin-bottom: 8px;">粘贴 JSON 或选择文件:</div>
      <input type="file" accept=".json,application/json" @change="handleImportFile" />
      <textarea
        v-model="importContent"
        class="textarea"
        :rows="8"
        placeholder='[{"term":"...","canonical":"..."}]'
        style="margin-top: 10px; width: 100%;"
      />
      <template #footer>
        <HButton variant="secondary" size="sm" @click="importDialogVisible = false">取消</HButton>
        <HButton variant="primary" size="sm" :loading="importing" @click="handleImport">导入</HButton>
      </template>
    </HDialog>
  </HCard>
</template>
