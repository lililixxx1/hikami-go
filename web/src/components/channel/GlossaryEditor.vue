<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HButton, HInput, HSwitch, HCheckbox, HPill, HDialog, HEmpty } from '@/components/ui'
import {
  useGlossaryEntries,
  isDuplicateError,
} from '@/features/channel/useGlossaryEntries'
import type { GlossaryEntry } from '@/api/types-derived'

const props = withDefaults(defineProps<{
  scope: 'global' | 'channel'
  channelId?: string
  channelName?: string
  showGlobalReadonly?: boolean
}>(), {
  channelId: '',
  channelName: '',
  showGlobalReadonly: false,
})

// 业务逻辑(CRUD/列表/批量/导入导出)全在 composable,global/channel 双 scope 分发集中在此
const {
  loading,
  editableEntries,
  readonlyEntries,
  editableTitle,
  categoryOptions,
  fetchData,
  addEntry,
  deleteEntry,
  toggleEntry,
  batchToggle,
  batchDelete,
  importEntries,
  exportJSON,
} = useGlossaryEntries({
  scope: () => props.scope,
  channelId: () => props.channelId,
  channelName: () => props.channelName,
  showGlobalReadonly: () => props.showGlobalReadonly,
})

// 纯 UI 状态(留组件:表格选择/底部快速添加/对话框开关)
const selectedIds = ref<Set<number>>(new Set())
const newTerm = ref('')
const newCanonical = ref('')
const newCategory = ref('')
const adding = ref(false)
const addDialogVisible = ref(false)
const addForm = ref({ term: '', canonical: '', category: '' })
const addDialogSaving = ref(false)
const importDialogVisible = ref(false)
const importType = ref<'markdown' | 'json'>('markdown')
const importContent = ref('')
const importing = ref(false)

// 分类下拉需要把 categoryOptions 转成 HSelect 选项；但 HSelect 不支持 allow-create。
// 这里改用原生 input(自由输入) + datalist 提供建议,保留「选择已有或输入新值」的交互。
const categoryList = categoryOptions

watch(() => [props.scope, props.channelId], fetchData)
onMounted(fetchData)

// --- 包装:把 composable 的统一接口适配成模板事件 handler(UI 状态管理在此) ---

function toggleSelect(id: number, checked: boolean): void {
  const next = new Set(selectedIds.value)
  if (checked) next.add(id)
  else next.delete(id)
  selectedIds.value = next
}

const allSelected = computed(() =>
  editableEntries.value.length > 0 && editableEntries.value.every((e) => selectedIds.value.has(e.id)),
)
function toggleSelectAll(checked: boolean): void {
  selectedIds.value = checked ? new Set(editableEntries.value.map((e) => e.id)) : new Set()
}

async function handleAddEntry(): Promise<void> {
  if (!newTerm.value.trim() || !newCanonical.value.trim()) {
    HMessage.warning('请填写错误写法和正确写法')
    return
  }
  adding.value = true
  try {
    await addEntry(newTerm.value.trim(), newCanonical.value.trim(), newCategory.value.trim())
    HMessage.success('词条已添加')
    newTerm.value = ''
    newCanonical.value = ''
    newCategory.value = ''
  } finally {
    adding.value = false
  }
}

function showAddDialog(): void {
  addForm.value = { term: '', canonical: '', category: '' }
  addDialogVisible.value = true
}

async function handleDialogAddEntry(): Promise<void> {
  const term = addForm.value.term.trim()
  if (!term) {
    HMessage.warning('请填写词条')
    return
  }
  const canonical = addForm.value.canonical.trim() || term
  addDialogSaving.value = true
  try {
    await addEntry(term, canonical, addForm.value.category.trim())
    HMessage.success('热词已添加')
    addDialogVisible.value = false
  } catch (error) {
    if (isDuplicateError(error)) {
      HMessage.warning('词条已存在')
      return
    }
    throw error
  } finally {
    addDialogSaving.value = false
  }
}

async function handleDeleteEntry(entry: GlossaryEntry): Promise<void> {
  await deleteEntry(entry)
}

async function handleToggleEntry(entry: GlossaryEntry, enabled: boolean): Promise<void> {
  const prev = !enabled
  try {
    await toggleEntry(entry, enabled)
  } catch {
    entry.enabled = prev
  }
}

async function handleBatchToggle(enabled: boolean): Promise<void> {
  if (selectedIds.value.size === 0) return
  const ok = await batchToggle([...selectedIds.value], enabled)
  if (ok) selectedIds.value = new Set()
}

async function handleBatchDelete(): Promise<void> {
  if (selectedIds.value.size === 0) return
  const ok = await batchDelete([...selectedIds.value])
  if (ok) selectedIds.value = new Set()
}

function showImportDialog(type: 'markdown' | 'json'): void {
  importType.value = type
  importContent.value = ''
  importDialogVisible.value = true
}

async function handleImport(): Promise<void> {
  if (!importContent.value.trim()) {
    HMessage.warning('请输入内容')
    return
  }
  importing.value = true
  try {
    await importEntries(importContent.value, importType.value)
    importDialogVisible.value = false
  } finally {
    importing.value = false
  }
}

async function handleImportFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  input.value = ''
  importContent.value = await file.text()
  await handleImport()
}

async function handleExportJSON(): Promise<void> {
  await exportJSON()
}
</script>

<template>
  <div class="glossary-editor">
    <div v-if="loading" class="form-hint">加载中…</div>

    <div class="editor-toolbar">
      <div>
        <h3>{{ editableTitle }}</h3>
        <p>用于修正常见误写。启用的词条在生成直播回顾时作为上下文注入，并在使用 Fun-ASR 模型转写时作为热词，提升人名、专有名词的识别准确率。</p>
      </div>
      <div class="toolbar-actions">
        <HButton variant="primary" size="sm" @click="showAddDialog">+ 新增热词</HButton>
        <HButton variant="secondary" size="sm" @click="showImportDialog('markdown')">导入 Markdown</HButton>
        <HButton variant="secondary" size="sm" @click="showImportDialog('json')">导入 JSON</HButton>
        <label class="upload-btn btn btn-secondary btn-sm">
          <input type="file" accept=".json,application/json" @change="handleImportFile">
          从文件导入
        </label>
        <HButton variant="ghost" size="sm" @click="handleExportJSON">导出 JSON</HButton>
      </div>
    </div>

    <section v-if="readonlyEntries.length > 0" class="readonly-block">
      <h4>全局词条（只读）</h4>
      <table class="dense-table">
        <thead>
          <tr>
            <th>错误写法</th>
            <th>正确写法</th>
            <th>分类</th>
            <th style="width: 80px;">启用</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in readonlyEntries" :key="entry.id">
            <td>{{ entry.term }}</td>
            <td>{{ entry.canonical }}</td>
            <td>{{ entry.category || '-' }}</td>
            <td>
              <HPill :variant="entry.enabled ? 'success' : 'neutral'">
                {{ entry.enabled ? '启用' : '停用' }}
              </HPill>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <div v-if="selectedIds.size > 0" class="batch-bar">
      <span>已选 {{ selectedIds.size }} 项</span>
      <HButton variant="secondary" size="sm" @click="handleBatchToggle(true)">批量启用</HButton>
      <HButton variant="secondary" size="sm" @click="handleBatchToggle(false)">批量禁用</HButton>
      <HButton variant="danger" size="sm" @click="handleBatchDelete">批量删除</HButton>
    </div>

    <HEmpty v-if="!loading && editableEntries.length === 0" description="暂无词条" class="empty-state" />

    <table v-if="editableEntries.length > 0" class="dense-table">
      <thead>
        <tr>
          <th style="width: 40px;">
            <HCheckbox :model-value="allSelected" @update:model-value="(v: boolean) => toggleSelectAll(v)" />
          </th>
          <th>错误写法</th>
          <th>正确写法</th>
          <th>分类</th>
          <th style="width: 70px;">启用</th>
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
            <HSwitch :model-value="entry.enabled" @update:model-value="(v: boolean) => handleToggleEntry(entry, v)" />
          </td>
          <td>
            <HButton variant="danger" size="xs" @click="handleDeleteEntry(entry)">删除</HButton>
          </td>
        </tr>
      </tbody>
    </table>

    <div class="add-row">
      <HInput v-model="newTerm" placeholder="错误写法" @keyup.enter="handleAddEntry" />
      <HInput v-model="newCanonical" placeholder="正确写法" @keyup.enter="handleAddEntry" />
      <HInput v-model="newCategory" placeholder="分类（可选）" @keyup.enter="handleAddEntry" />
      <HButton variant="primary" :loading="adding" @click="handleAddEntry">添加词条</HButton>
    </div>

    <HDialog
      v-model:visible="importDialogVisible"
      :title="importType === 'markdown' ? '导入 Markdown 术语表' : '导入 JSON 术语表'"
      width="640px"
    >
      <textarea
        v-model="importContent"
        class="textarea"
        :rows="15"
        :placeholder="importType === 'markdown' ? '粘贴 Markdown 格式的术语表' : '粘贴 JSON 格式的术语表'"
      />
      <template #footer>
        <HButton variant="secondary" size="sm" @click="importDialogVisible = false">取消</HButton>
        <HButton variant="primary" size="sm" :loading="importing" @click="handleImport">导入</HButton>
      </template>
    </HDialog>

    <HDialog v-model:visible="addDialogVisible" title="新增热词" width="480px">
      <div class="form-field" style="margin-bottom: 12px;">
        <span class="form-label">词条（必填）</span>
        <HInput v-model="addForm.term" placeholder="请输入词条" @keyup.enter="handleDialogAddEntry" />
      </div>
      <div class="form-field" style="margin-bottom: 12px;">
        <span class="form-label">释义</span>
        <HInput v-model="addForm.canonical" placeholder="留空则使用词条" @keyup.enter="handleDialogAddEntry" />
      </div>
      <div class="form-field">
        <span class="form-label">分类</span>
        <HInput v-model="addForm.category" placeholder="选择或输入分类" list="glossary-categories" />
        <datalist id="glossary-categories">
          <option v-for="category in categoryList" :key="category" :value="category" />
        </datalist>
      </div>
      <template #footer>
        <HButton variant="secondary" size="sm" @click="addDialogVisible = false">取消</HButton>
        <HButton variant="primary" size="sm" :loading="addDialogSaving" @click="handleDialogAddEntry">添加</HButton>
      </template>
    </HDialog>
  </div>
</template>

<style scoped>
.glossary-editor {
  min-height: 240px;
}

.form-hint {
  color: var(--text-secondary);
  font-size: 13px;
}

.editor-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 16px;
}

.editor-toolbar h3 {
  margin: 0;
  font-size: 16px;
}

.editor-toolbar p {
  margin: 6px 0 0;
  color: var(--text-secondary);
  font-size: 13px;
}

.toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.upload-btn {
  display: inline-flex;
  cursor: pointer;
}

.upload-btn input[type="file"] {
  position: absolute;
  width: 0;
  height: 0;
  opacity: 0;
  pointer-events: none;
}

.readonly-block {
  margin-bottom: 18px;
}

.readonly-block h4 {
  margin: 0 0 8px;
  color: var(--text-secondary);
  font-size: 13px;
}

.batch-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.dense-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.dense-table th {
  padding: 8px 10px;
  text-align: left;
  font-weight: 600;
  font-size: 11.5px;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.03em;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
}

.dense-table td {
  padding: 8px 10px;
  border-bottom: 1px solid var(--border-light);
  color: var(--text);
}

.dense-table tbody tr:last-child td {
  border-bottom: none;
}

.empty-state {
  margin: 12px 0;
}

.add-row {
  display: grid;
  grid-template-columns: minmax(160px, 1fr) minmax(160px, 1fr) minmax(140px, 0.8fr) auto;
  gap: 8px;
  margin-top: 16px;
  align-items: center;
}

@media (max-width: 900px) {
  .editor-toolbar {
    flex-direction: column;
  }

  .toolbar-actions {
    justify-content: flex-start;
  }

  .add-row {
    grid-template-columns: 1fr;
  }
}
</style>
