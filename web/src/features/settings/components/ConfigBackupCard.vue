<script setup lang="ts">
import { h, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { exportConfig, importConfig } from '@/api/settings'

const emit = defineEmits<{
  // 导入成功后通知壳重拉 secrets/runtime + 各配置卡 reload
  imported: []
}>()

const exportLoading = ref(false)
const importLoading = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

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

function triggerImport() {
  fileInput.value?.click()
}

async function handleImport(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  input.value = ''

  try {
    await ElMessageBox.confirm(
      '导入将修改当前配置（API 密钥、发布设置、术语表等）。导入文件包含敏感数据，确认来源可信？',
      '导入配置',
      { confirmButtonText: '继续', cancelButtonText: '取消', type: 'warning' },
    )
  } catch { return }

  try {
    // ElMessageBox Promise 模式: confirm 会 resolve('confirm');
    // cancel/close 会 reject('cancel'/'close')。故用 .catch 区分:
    //   confirm(合并) → resolve 走 merge
    //   cancel(覆盖) → reject 'cancel' 走 overwrite
    //   close(关闭)  → reject 'close' 不导入
    let chosenStrategy: 'merge' | 'overwrite' | null = null
    try {
      await ElMessageBox({
        title: '选择导入策略',
        message: h('div', null, [
          h('p', { style: 'margin-bottom: 8px' }, '合并：保留现有配置，更新导入中的项'),
          h('p', null, '覆盖：清除现有配置后导入'),
        ]),
        showCancelButton: true,
        confirmButtonText: '合并（推荐）',
        cancelButtonText: '覆盖',
        distinguishCancelAndClose: true,
      })
      chosenStrategy = 'merge'
    } catch (action) {
      if (action === 'cancel') chosenStrategy = 'overwrite'
      else return // 'close' 或其它:不导入
    }

    importLoading.value = true
    const text = await file.text()
    const result = await importConfig(text, chosenStrategy)
    ElMessage.success(`配置导入成功 (策略: ${chosenStrategy === 'merge' ? '合并' : '覆盖'})`)
    if (result.warnings?.length) {
      result.warnings.forEach((w) => ElMessage.warning({ message: w, duration: 5000 }))
    }
    // 通知壳重拉所有配置(secrets/runtime/各配置卡)
    emit('imported')
  } catch { /* error shown by interceptor or user cancel */ }
  finally { importLoading.value = false }
}
</script>

<template>
  <div class="settings-card">
    <div class="card-header-row">
      <h3>配置备份</h3>
    </div>
    <el-alert type="warning" :closable="false" style="margin-bottom: 16px">
      导出文件包含 API 密钥、密码等敏感信息，请妥善保管，切勿公开分享。
    </el-alert>
    <div class="backup-actions">
      <el-button :loading="exportLoading" @click="handleExport">导出配置</el-button>
      <el-button :loading="importLoading" @click="triggerImport">导入配置</el-button>
      <input ref="fileInput" type="file" accept=".json" style="display: none" @change="handleImport" />
    </div>
  </div>
</template>

<style scoped>
.backup-actions {
  display: flex;
  gap: 12px;
}
</style>
