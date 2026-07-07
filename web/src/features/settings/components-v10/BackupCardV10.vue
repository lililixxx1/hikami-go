<!--
  BackupCardV10.vue(Phase 5 Task 5.4)。配置备份卡。
  移植自 ConfigBackupCard.vue(EP)。
  - 导出配置:GET export → Blob 下载 json。
  - 导入配置:HDialog(strategy HSelect merge/overwrite + 文件 input + 确认)
    → POST import?strategy= + json 文本 → emit imported(壳重拉各卡)。
  EP 原用 ElMessageBox 二次确认(合并/覆盖两按钮);V10 改为 HDialog 显式选 strategy。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { HCard, HButton, HSelect, HDialog } from '@/components/ui'
import { exportConfig, importConfig } from '@/api/settings'

const emit = defineEmits<{ imported: [] }>()

const exportLoading = ref(false)
const importDialogVisible = ref(false)
const strategy = ref<'merge' | 'overwrite'>('merge')
const importing = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)
const selectedFile = ref<File | null>(null)

const strategyOptions = [
  { label: '合并(保留现有,更新导入项)', value: 'merge' },
  { label: '覆盖(清除现有后导入)', value: 'overwrite' },
]

async function handleExport() {
  exportLoading.value = true
  try {
    const blob = await exportConfig()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `hikami-config-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
    ElMessage.success('配置已导出')
  } catch { /* error shown by interceptor */ }
  finally { exportLoading.value = false }
}

function showImportDialog() {
  strategy.value = 'merge'
  selectedFile.value = null
  if (fileInput.value) fileInput.value.value = ''
  importDialogVisible.value = true
}

function handleFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  selectedFile.value = input.files?.[0] ?? null
}

async function handleImport() {
  if (!selectedFile.value) {
    ElMessage.warning('请选择配置文件')
    return
  }
  importing.value = true
  try {
    const text = await selectedFile.value.text()
    const result = await importConfig(text, strategy.value)
    ElMessage.success(`配置导入成功(策略:${strategy.value === 'merge' ? '合并' : '覆盖'})`)
    if (result.warnings?.length) {
      result.warnings.forEach((w) => ElMessage.warning({ message: w, duration: 5000 }))
    }
    importDialogVisible.value = false
    emit('imported')
  } catch { /* error shown by interceptor */ }
  finally { importing.value = false }
}
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">配置备份</span>
    </template>

    <div class="form-hint" style="margin-bottom: 12px; color: var(--warning);">
      导出文件包含 API 密钥、密码等敏感信息,请妥善保管,切勿公开分享。
    </div>

    <div class="card-actions start">
      <HButton variant="secondary" :loading="exportLoading" @click="handleExport">导出配置</HButton>
      <HButton variant="primary" @click="showImportDialog">导入配置</HButton>
    </div>

    <HDialog v-model:visible="importDialogVisible" title="导入配置" width="480px">
      <div class="form-hint" style="margin-bottom: 8px;">
        导入将修改当前配置(API 密钥、发布设置、术语表等)。确认文件来源可信。
      </div>
      <div class="form-field" style="margin-bottom: 12px;">
        <span class="form-label">导入策略</span>
        <HSelect v-model="strategy" :options="strategyOptions" />
      </div>
      <div class="form-field">
        <span class="form-label">配置文件</span>
        <input ref="fileInput" type="file" accept=".json,application/json" @change="handleFileChange" />
      </div>
      <template #footer>
        <HButton variant="secondary" size="sm" @click="importDialogVisible = false">取消</HButton>
        <HButton variant="primary" size="sm" :loading="importing" @click="handleImport">确认导入</HButton>
      </template>
    </HDialog>
  </HCard>
</template>
