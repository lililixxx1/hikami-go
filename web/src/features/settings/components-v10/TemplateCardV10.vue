<!--
  TemplateCardV10.vue(Phase 5 Task 5.3)。全局回顾模板卡。
  移植自 RecapTemplateEditor.vue(EP),scope='global'。
  业务逻辑全在 useRecapTemplateEditor composable(四字段表单 + 预设 + 导入导出 + 保存)。
  - 预设 HSelect(options 来自 listRecapPresets),选中 applyPreset 填充 system_prompt/user_format。
  - HTextarea:system_prompt/user_format/fan_name。
  - extra_vars 编辑器:JSON 字符串 ↔ key-value 行列表(增删改)。
  - 导出(GET export → Blob 下载)、导入(HDialog 选文件 → POST import)、保存(PUT upsert)。
  emit saved。L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { HCard, HButton, HTextarea, HSelect, HDialog, HInput, HPill } from '@/components/ui'
import { useRecapTemplateEditor } from '@/features/channel/useRecapTemplateEditor'

const emit = defineEmits<{ saved: [] }>()

const {
  loading, saving, importing, presetLoading,
  systemPrompt, userFormat, fanName, extraVars,
  presets, loadData, loadPresets, applyPreset, save, exportTemplate, importTemplateFile,
} = useRecapTemplateEditor({ scope: () => 'global', channelId: () => '' })

const selectedPresetName = ref('')
const importDialogVisible = ref(false)
const importContent = ref('')

const presetOptions = computed(() =>
  presets.value.map(p => ({ label: `${p.name}${p.description ? ' · ' + p.description : ''}`, value: p.name })),
)

// extra_vars 是 JSON 字符串(composable 内部以 '{}' 初始化)。拆成 key-value 行编辑。
interface KVRow { key: string; value: string }
const kvRows = computed<KVRow[]>({
  get: () => {
    try {
      const obj = JSON.parse(extraVars.value || '{}') as Record<string, string>
      return Object.entries(obj).map(([key, value]) => ({ key, value: String(value ?? '') }))
    } catch {
      return []
    }
  },
  set: (rows: KVRow[]) => {
    const obj: Record<string, string> = {}
    for (const r of rows) {
      const k = r.key.trim()
      if (k) obj[k] = r.value
    }
    extraVars.value = JSON.stringify(obj)
  },
})

function updateKvKey(i: number, key: string) {
  const rows = kvRows.value.map((r, idx) => idx === i ? { ...r, key } : r)
  kvRows.value = rows
}
function updateKvValue(i: number, value: string) {
  const rows = kvRows.value.map((r, idx) => idx === i ? { ...r, value } : r)
  kvRows.value = rows
}
function addKvRow() {
  kvRows.value = [...kvRows.value, { key: '', value: '' }]
}
function removeKvRow(i: number) {
  kvRows.value = kvRows.value.filter((_, idx) => idx !== i)
}

async function handleApplyPreset(name: string) {
  selectedPresetName.value = ''
  if (!name) return
  await applyPreset(name)
}

async function handleSave() {
  await save()
  emit('saved')
}

function showImportDialog() {
  importContent.value = ''
  importDialogVisible.value = true
}

async function handleImportFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  input.value = ''
  try {
    const content = await file.text()
    await importTemplateFile(content)
    importDialogVisible.value = false
  } catch { /* error shown by interceptor */ }
}

async function handleImportText() {
  if (!importContent.value.trim()) {
    ElMessage.warning('请粘贴模板 JSON 内容')
    return
  }
  try {
    await importTemplateFile(importContent.value.trim())
    importDialogVisible.value = false
  } catch { /* error shown by interceptor */ }
}

onMounted(async () => {
  await Promise.all([loadData(), loadPresets()])
})
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">回顾模板</span>
      <HPill variant="neutral">全局模板</HPill>
    </template>

    <div v-if="loading" class="form-hint">加载中…</div>

    <template v-else>
      <div class="glossary-toolbar">
        <HSelect
          :model-value="selectedPresetName"
          :options="presetOptions"
          :disabled="presetLoading"
          style="min-width: 240px;"
          @update:model-value="handleApplyPreset"
        />
        <HButton variant="secondary" size="sm" @click="exportTemplate">导出模板</HButton>
        <HButton variant="secondary" size="sm" :loading="importing" @click="showImportDialog">导入模板</HButton>
      </div>

      <HTextarea v-model="systemPrompt" :rows="8">
        <template #label>System Prompt(系统提示词)</template>
      </HTextarea>
      <div class="form-hint">回顾生成的系统提示词。留空(__builtin__)跟随内置默认。</div>

      <HTextarea v-model="userFormat" :rows="6">
        <template #label>User Format(输出格式)</template>
      </HTextarea>
      <div class="form-hint">控制回顾文章的结构与段落格式。留空跟随内置默认。</div>

      <HInput v-model="fanName" placeholder="如:小伙伴、舰长">
        <template #label>粉丝称呼(fan_name)</template>
      </HInput>

      <div style="margin-top: 14px;">
        <div class="form-label" style="margin-bottom: 6px;">额外变量(extra_vars)</div>
        <div v-for="(row, i) in kvRows" :key="i" class="kv-row">
          <HInput :model-value="row.key" placeholder="变量名" @update:model-value="(v: string) => updateKvKey(i, v)" />
          <HInput :model-value="row.value" placeholder="值" @update:model-value="(v: string) => updateKvValue(i, v)" />
          <HButton variant="ghost" size="xs" @click="removeKvRow(i)">删除</HButton>
        </div>
        <HButton variant="secondary" size="xs" @click="addKvRow">+ 添加变量</HButton>
      </div>

      <div class="card-actions">
        <HButton variant="primary" :loading="saving" @click="handleSave">保存模板</HButton>
      </div>
    </template>

    <HDialog v-model:visible="importDialogVisible" title="导入模板" width="520px">
      <div class="form-hint" style="margin-bottom: 8px;">粘贴模板 JSON 内容,或选择文件导入:</div>
      <input type="file" accept=".json,application/json" @change="handleImportFile" />
      <HTextarea v-model="importContent" :rows="8" placeholder='{"system_prompt":"...","user_format":"..."}' style="margin-top: 10px;" />
      <template #footer>
        <HButton variant="secondary" size="sm" @click="importDialogVisible = false">取消</HButton>
        <HButton variant="primary" size="sm" @click="handleImportText">导入</HButton>
      </template>
    </HDialog>
  </HCard>
</template>
