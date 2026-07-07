<!--
  ArchiveCardV10.vue(Phase 5 Task 5.2)。发布后归档配置卡。
  移植自 ArchiveSettingsCard.vue(EP)。endpoint /api/config/archive。
  - auto_after_publish 开关 + cleanup_policy select(none/temp/generated/all)。
  - cleanupHint 根据策略给出说明(all 红色提示)。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HCard, HButton, HSwitch, HSelect, HPill } from '@/components/ui'
import { getArchiveConfig, updateArchiveConfig } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { ArchiveConfig } from '@/api/types'

const emit = defineEmits<{ saved: [] }>()
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
    case 'all': return '删除整个本地目录后,再次编辑专栏需先 Fetch 取回。'
    case 'generated': return '删除 asr/ 中间产物,保留 raw/、package/、recap/。'
    case 'temp': return '仅删除 asr/audio.public.json 与远端临时对象。'
    default: return '归档成功后保留全部本地文件。'
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
    HMessage.success('归档设置已保存')
    await runtimeStore.fetchRuntime(true)
    emit('saved')
  } finally {
    saving.value = false
  }
}

onMounted(fetchConfig)
defineExpose({ reload: fetchConfig })
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">发布后归档</span>
      <HPill :variant="config.auto_after_publish ? 'success' : 'neutral'">
        {{ config.auto_after_publish ? '已启用' : '未启用' }}
      </HPill>
    </template>

    <div class="form-hint" style="margin-bottom: 12px;">
      发布专栏成功后自动把场次目录归档到 WebDAV。归档不改变场次状态(始终保持已发布)。
    </div>

    <div class="form-row-inline">
      <label class="form-label">自动归档</label>
      <div class="form-field">
        <HSwitch v-model="config.auto_after_publish">开启后,每场专栏发布成功即自动上传到 WebDAV</HSwitch>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">删除范围</label>
      <div class="form-field">
        <HSelect v-model="config.cleanup_policy" :options="cleanupOptions" :disabled="!config.auto_after_publish" />
        <div class="form-hint" :style="{ color: config.cleanup_policy === 'all' ? 'var(--danger)' : '' }">
          {{ cleanupHint }}
        </div>
      </div>
    </div>

    <div class="card-actions">
      <HButton variant="primary" :loading="saving" @click="save">保存设置</HButton>
    </div>
  </HCard>
</template>
