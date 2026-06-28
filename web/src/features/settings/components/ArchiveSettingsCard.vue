<script setup lang="ts">
import './settings-cards.css'
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getArchiveConfig, updateArchiveConfig } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { ArchiveConfig } from '@/api/types'

defineProps<{
  isExpert: boolean
}>()

const emit = defineEmits<{
  saved: []
}>()

const runtimeStore = useRuntimeStore()

const config = ref<ArchiveConfig>({
  auto_after_publish: false,
  cleanup_policy: 'none',
})
const saving = ref(false)

const cleanupOptions = [
  { value: 'none', label: '保留本地' },
  { value: 'temp', label: '删除 ASR 公开音频' },
  { value: 'generated', label: '删除 ASR 中间产物' },
  { value: 'all', label: '删除整个本地目录' },
]

const cleanupHint = computed(() => {
  switch (config.value.cleanup_policy) {
    case 'all':
      return '删除整个本地目录后，再次编辑专栏需先 Fetch 取回。'
    case 'generated':
      return '删除 asr/ 中间产物，保留 raw/、package/、recap/。'
    case 'temp':
      return '仅删除 asr/audio.public.json 与远端临时对象。'
    default:
      return '归档成功后保留全部本地文件。'
  }
})

async function fetchConfig() {
  try {
    config.value = await getArchiveConfig()
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    config.value = await updateArchiveConfig(config.value)
    ElMessage.success('归档设置已保存')
    await runtimeStore.fetchRuntime(true)
    emit('saved')
  } finally {
    saving.value = false
  }
}

onMounted(fetchConfig)

// 供外部（壳）在配置导入后触发重新加载
defineExpose({ reload: fetchConfig })
</script>

<template>
  <div class="settings-card" data-section="archive">
    <div class="card-header-row">
      <h3>发布后归档</h3>
      <el-tag v-if="config.auto_after_publish" type="success" size="small">已启用</el-tag>
      <el-tag v-else type="info" size="small">未启用</el-tag>
    </div>
    <div class="column-note" style="margin-bottom: 12px;">
      发布专栏成功后自动把场次目录归档到 WebDAV。归档不改变场次状态（始终保持已发布）。
    </div>
    <div class="column-form">
      <div class="column-row">
        <div class="column-label">自动归档</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-switch v-model="config.auto_after_publish" />
          </div>
          <div class="column-note">开启后，每场专栏发布成功即自动上传到 WebDAV。</div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">删除范围</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-select
              v-model="config.cleanup_policy"
              :disabled="!config.auto_after_publish"
              style="width: 100%;"
            >
              <el-option
                v-for="opt in cleanupOptions"
                :key="opt.value"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>
          </div>
          <div
            class="column-note"
            :style="{ color: config.cleanup_policy === 'all' ? 'var(--el-color-danger)' : '' }"
          >
            {{ cleanupHint }}
          </div>
        </div>
      </div>

      <div class="column-actions">
        <el-button type="primary" :loading="saving" @click="save">保存设置</el-button>
      </div>
    </div>
  </div>
</template>
