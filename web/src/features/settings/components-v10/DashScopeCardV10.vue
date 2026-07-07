<!--
  DashScopeCardV10.vue(Phase 5 Task 5.2)。ASR 转写(DashScope)配置卡。
  移植自 DashScopeSettingsCard.vue(EP)。业务逻辑全保留:
  - 本地 config ref(GET /api/config/dashscope 填充)、apiKey/clearKey 一次性标志、saving ref。
  - 三态密钥:api_key_set 时显示"已配置"标签 + 输入框(留空保留)+ 清除密钥 checkbox(clear_key=true)。
  - 高级参数折叠(speaker_count/asr_url/tasks_url/vocabulary_id/api_key_env/diarization_enabled)。
  - 保存 PUT → fetchRuntime(true) → emit saved。
  UI 替换:el-card→HCard, el-input→HInput, el-select→HSelect, el-switch→HSwitch,
  el-checkbox→HCheckbox, el-button→HButton, el-tag→HPill。保留 ElMessage toast。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { HCard, HButton, HInput, HSelect, HSwitch, HCheckbox, HPill } from '@/components/ui'
import { getDashScopeConfig, updateDashScopeConfig } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { DashScopeConfig } from '@/api/types'

const emit = defineEmits<{ saved: [] }>()
const runtimeStore = useRuntimeStore()

const config = ref<DashScopeConfig>({
  api_key_env: '',
  api_key_set: false,
  asr_url: '',
  tasks_url: '',
  model: '',
  language: 'zh',
  diarization_enabled: false,
  speaker_count: 0,
  vocabulary_id: '',
})
const apiKey = ref('')
const clearKey = ref(false)
const saving = ref(false)
const advancedOpen = ref(false)

// HInput 只接受 string,数字字段用 computed 代理互转
const speakerCount = computed({
  get: () => String(config.value.speaker_count),
  set: (v: string) => { config.value.speaker_count = Number(v) || 0 },
})

const modelOptions = [
  { label: 'fun-asr(推荐)', value: 'fun-asr' },
  { label: 'paraformer-v2', value: 'paraformer-v2' },
  { label: 'qwen-audio-asr', value: 'qwen-audio-asr' },
]
const languageOptions = [
  { label: '中文', value: 'zh' },
  { label: '英文', value: 'en' },
  { label: '自动检测', value: 'auto' },
]

async function fetchConfig() {
  try {
    config.value = await getDashScopeConfig()
    apiKey.value = ''
    clearKey.value = false
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    const payload: DashScopeConfig = {
      ...config.value,
      api_key: apiKey.value.trim(),
      clear_key: clearKey.value,
    }
    config.value = await updateDashScopeConfig(payload)
    apiKey.value = ''
    clearKey.value = false
    ElMessage.success('ASR 设置已保存')
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
      <span class="card-title">ASR 转写 · DashScope</span>
      <HPill :variant="config.api_key_set ? 'success' : 'danger'">
        {{ config.api_key_set ? '密钥已保存' : '密钥未保存' }}
      </HPill>
    </template>

    <div class="form-hint" style="margin-bottom: 12px;">
      阿里云 DashScope 语音转写。配置后用于把直播录制自动转成文字稿,作为回顾生成的输入。
    </div>

    <div class="form-row-inline">
      <label class="form-label">模型</label>
      <div class="form-field">
        <HSelect v-model="config.model" :options="modelOptions" />
        <div class="form-hint">支持输入任意 DashScope 支持的模型名称(默认 fun-asr)。</div>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">语言</label>
      <div class="form-field">
        <HSelect v-model="config.language" :options="languageOptions" />
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">API 密钥</label>
      <div class="form-field">
        <div class="input-group">
          <HInput
            v-model="apiKey"
            placeholder="留空则保留已保存密钥"
          />
          <HPill :variant="config.api_key_set ? 'success' : 'danger'">
            {{ config.api_key_set ? '已配置' : '未配置' }}
          </HPill>
        </div>
        <div class="form-hint">DashScope API 密钥,读取配置时不会返回明文;需要更新时重新输入。</div>
        <HCheckbox v-if="config.api_key_set" v-model="clearKey">清除已保存密钥</HCheckbox>
      </div>
    </div>

    <button class="collapse-trigger" type="button" @click="advancedOpen = !advancedOpen">
      <span class="collapse-arrow" :class="{ open: advancedOpen }">›</span> 高级参数
    </button>
    <div v-show="advancedOpen" class="collapse-content">
      <div class="form-row-inline">
        <label class="form-label">说话人分离</label>
        <div class="form-field">
          <HSwitch v-model="config.diarization_enabled">开启后识别不同说话人,供回顾 AI 区分发言者</HSwitch>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">说话人数</label>
        <div class="form-field">
          <HInput v-model="speakerCount" placeholder="0" />
          <div class="form-hint">0 = 自动检测(填数字)。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">定制词表</label>
        <div class="form-field">
          <HInput v-model="config.vocabulary_id" placeholder="可选,DashScope 定制词表 ID" />
          <div class="form-hint">用于纠正专有名词/术语的转写错误。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">ASR URL</label>
        <div class="form-field">
          <HInput v-model="config.asr_url" placeholder="留空走 DashScope 官方默认" />
          <div class="form-hint">提交转写任务的接口地址,留空使用官方默认。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">Tasks URL</label>
        <div class="form-field">
          <HInput v-model="config.tasks_url" placeholder="留空走 DashScope 官方默认" />
          <div class="form-hint">查询转写任务结果的接口地址,留空使用官方默认。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">密钥环境变量</label>
        <div class="form-field">
          <HInput v-model="config.api_key_env" placeholder="DASHSCOPE_API_KEY" />
          <div class="form-hint">配置后从该环境变量读取密钥,留空使用默认。改名后已保存的密钥会迁移。</div>
        </div>
      </div>
    </div>

    <div class="card-actions">
      <HButton variant="primary" :loading="saving" @click="save">保存设置</HButton>
    </div>
  </HCard>
</template>
