<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Delete, Download, Plus, Upload } from '@element-plus/icons-vue'
import {
  useGlossaryEntries,
  isDuplicateError,
} from '@/features/channel/useGlossaryEntries'
import type { UploadFile } from 'element-plus'
import type { GlossaryEntry } from '@/api/types'

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
const tableRef = ref()
const selectedEntries = ref<GlossaryEntry[]>([])
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

watch(() => [props.scope, props.channelId], fetchData)
onMounted(fetchData)

// --- 包装:把 composable 的统一接口适配成模板事件 handler(UI 状态管理在此) ---

function handleSelectionChange(rows: GlossaryEntry[]): void {
  selectedEntries.value = rows
}

async function handleAddEntry(): Promise<void> {
  if (!newTerm.value.trim() || !newCanonical.value.trim()) {
    ElMessage.warning('请填写错误写法和正确写法')
    return
  }
  adding.value = true
  try {
    await addEntry(newTerm.value.trim(), newCanonical.value.trim(), newCategory.value.trim())
    ElMessage.success('词条已添加')
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
    ElMessage.warning('请填写词条')
    return
  }
  const canonical = addForm.value.canonical.trim() || term
  addDialogSaving.value = true
  try {
    await addEntry(term, canonical, addForm.value.category.trim())
    ElMessage.success('热词已添加')
    addDialogVisible.value = false
  } catch (error) {
    if (isDuplicateError(error)) {
      ElMessage.warning('词条已存在')
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
  if (selectedEntries.value.length === 0) return
  const ok = await batchToggle(
    selectedEntries.value.map((e) => e.id),
    enabled,
  )
  if (ok) tableRef.value?.clearSelection()
}

async function handleBatchDelete(): Promise<void> {
  if (selectedEntries.value.length === 0) return
  const ok = await batchDelete(selectedEntries.value.map((e) => e.id))
  if (ok) tableRef.value?.clearSelection()
}

function showImportDialog(type: 'markdown' | 'json'): void {
  importType.value = type
  importContent.value = ''
  importDialogVisible.value = true
}

async function handleImport(): Promise<void> {
  if (!importContent.value.trim()) {
    ElMessage.warning('请输入内容')
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

async function handleImportFile(file: UploadFile): Promise<void> {
  const raw = file.raw
  if (!raw) return
  importContent.value = await raw.text()
  await handleImport()
}

async function handleExportJSON(): Promise<void> {
  await exportJSON()
}
</script>

<template>
  <div v-loading="loading" class="glossary-editor">
    <div class="editor-toolbar">
      <div>
        <h3>{{ editableTitle }}</h3>
        <p>用于修正常见误写。启用的词条在生成直播回顾时作为上下文注入，并在使用 Fun-ASR 模型转写时作为热词，提升人名、专有名词的识别准确率。</p>
      </div>
      <div class="toolbar-actions">
        <el-button type="primary" @click="showAddDialog">
          <el-icon><Plus /></el-icon>
          新增热词
        </el-button>
        <el-button @click="showImportDialog('markdown')">
          <el-icon><Upload /></el-icon>
          导入 Markdown
        </el-button>
        <el-button @click="showImportDialog('json')">
          <el-icon><Upload /></el-icon>
          导入 JSON
        </el-button>
        <el-upload
          :auto-upload="false"
          :show-file-list="false"
          accept=".json,application/json"
          :on-change="handleImportFile"
        >
          <el-button>
            <el-icon><Upload /></el-icon>
            从文件导入
          </el-button>
        </el-upload>
        <el-button type="success" plain @click="handleExportJSON">
          <el-icon><Download /></el-icon>
          导出 JSON
        </el-button>
      </div>
    </div>

    <section v-if="readonlyEntries.length > 0" class="readonly-block">
      <h4>全局词条（只读）</h4>
      <el-table :data="readonlyEntries" stripe size="small" class="dense-table">
        <el-table-column prop="term" label="错误写法" min-width="160" show-overflow-tooltip />
        <el-table-column prop="canonical" label="正确写法" min-width="160" show-overflow-tooltip />
        <el-table-column prop="category" label="分类" min-width="120" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.category || '-' }}
          </template>
        </el-table-column>
        <el-table-column label="启用" width="90" align="center">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
              {{ row.enabled ? '启用' : '停用' }}
            </el-tag>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <div v-if="selectedEntries.length > 0" class="batch-bar">
      <span>已选 {{ selectedEntries.length }} 项</span>
      <el-button size="small" type="success" @click="handleBatchToggle(true)">批量启用</el-button>
      <el-button size="small" type="warning" @click="handleBatchToggle(false)">批量禁用</el-button>
      <el-button size="small" type="danger" @click="handleBatchDelete">批量删除</el-button>
    </div>

    <el-table
      ref="tableRef"
      :data="editableEntries"
      stripe
      class="dense-table"
      @selection-change="handleSelectionChange"
    >
      <el-table-column type="selection" width="48" />
      <el-table-column prop="term" label="错误写法" min-width="170" show-overflow-tooltip />
      <el-table-column prop="canonical" label="正确写法" min-width="170" show-overflow-tooltip />
      <el-table-column prop="category" label="分类" min-width="130" show-overflow-tooltip>
        <template #default="{ row }">
          {{ row.category || '-' }}
        </template>
      </el-table-column>
      <el-table-column label="启用" width="90" align="center">
        <template #default="{ row }">
          <el-switch v-model="row.enabled" @change="(val: boolean) => handleToggleEntry(row, val)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="90" align="center">
        <template #default="{ row }">
          <el-button type="danger" link size="small" @click="handleDeleteEntry(row)">
            <el-icon><Delete /></el-icon>
            删除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-empty v-if="!loading && editableEntries.length === 0" description="暂无词条" class="empty-state" />

    <div class="add-row">
      <el-input v-model="newTerm" placeholder="错误写法" @keyup.enter="handleAddEntry" />
      <el-input v-model="newCanonical" placeholder="正确写法" @keyup.enter="handleAddEntry" />
      <el-input v-model="newCategory" placeholder="分类（可选）" @keyup.enter="handleAddEntry" />
      <el-button type="primary" :loading="adding" @click="handleAddEntry">添加词条</el-button>
    </div>

    <el-dialog
      v-model="importDialogVisible"
      :title="importType === 'markdown' ? '导入 Markdown 术语表' : '导入 JSON 术语表'"
      width="640px"
    >
      <el-input
        v-model="importContent"
        type="textarea"
        :rows="15"
        :placeholder="importType === 'markdown' ? '粘贴 Markdown 格式的术语表' : '粘贴 JSON 格式的术语表'"
        class="note-input"
      />
      <template #footer>
        <el-button @click="importDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="importing" @click="handleImport">导入</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="addDialogVisible" title="新增热词" width="480px">
      <el-form label-position="top" @submit.prevent>
        <el-form-item label="词条" required>
          <el-input v-model="addForm.term" placeholder="请输入词条" @keyup.enter="handleDialogAddEntry" />
        </el-form-item>
        <el-form-item label="释义">
          <el-input v-model="addForm.canonical" placeholder="留空则使用词条" @keyup.enter="handleDialogAddEntry" />
        </el-form-item>
        <el-form-item label="分类">
          <el-select
            v-model="addForm.category"
            filterable
            allow-create
            default-first-option
            clearable
            placeholder="选择或输入分类"
            style="width: 100%"
          >
            <el-option v-for="category in categoryOptions" :key="category" :label="category" :value="category" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="addDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="addDialogSaving" @click="handleDialogAddEntry">添加</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.glossary-editor {
  min-height: 240px;
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
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.readonly-block {
  margin-bottom: 18px;
}

.readonly-block h4 {
  margin: 0 0 8px;
  color: var(--el-text-color-secondary);
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
