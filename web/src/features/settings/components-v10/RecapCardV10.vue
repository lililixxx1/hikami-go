<!--
  RecapCardV10.vue(Phase 5 Task 5.2)。回顾 AI 配置卡。
  移植自 RecapSettingsCard.vue(EP)。
  - enabled 开关 + provider(base_url/model/api_key/api_key_env)。
  - 模型下拉用 useRecapModels(GET /api/config/recap/models,扁平化为 HCombobox options,可手动输入)。
  - 三态密钥:api_key_set + clear_key checkbox(gap-analysis 补的 UI,与 DashScope 同模式)。
  - 高级参数:max_tokens/max_continuations/timeout_seconds(include_speaker_info)。
  - 数字字段用 computed 代理(HInput 只接受 string)。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HCard, HButton, HInput, HSelect, HSwitch, HCheckbox, HPill, HCombobox } from '@/components/ui'
import { getRecapConfig, updateRecapConfig } from '@/api/settings'
import { useRecapModels } from '@/composables/useRecapModels'
import { useRuntimeStore } from '@/stores/runtime'
import type { RecapConfig } from '@/api/types-derived'

const emit = defineEmits<{ saved: [] }>()
const runtimeStore = useRuntimeStore()
const { groups: recapModelGroups, load: loadRecapModels } = useRecapModels()

const config = ref<RecapConfig>({
  enabled: true,
  provider: 'openai_compatible',
  api_key_env: 'AI_API_KEY',
  api_key_set: false,
  base_url: 'https://api.deepseek.com',
  model: 'deepseek-v4-pro',
  max_tokens: 16384,
  max_continuations: 2,
  timeout_seconds: 180,
  include_speaker_info: true,
})
const apiKey = ref('')
const clearKey = ref(false)
const saving = ref(false)
const advancedOpen = ref(false)

// HInput 只接受 string,数字字段用 computed 代理互转
const maxTokens = computed({ get: () => String(config.value.max_tokens), set: (v: string) => { config.value.max_tokens = Number(v) || 0 } })
const maxContinuations = computed({ get: () => String(config.value.max_continuations), set: (v: string) => { config.value.max_continuations = Number(v) || 0 } })
const timeoutSeconds = computed({ get: () => String(config.value.timeout_seconds), set: (v: string) => { config.value.timeout_seconds = Number(v) || 0 } })

// useRecapModels 按 group 聚合,扁平化为 HCombobox options(可输入,无需 group 前缀)
const modelOptions = computed(() => {
  const opts: { label: string; value: string }[] = []
  for (const g of recapModelGroups.value) {
    for (const m of g.models) opts.push({ label: m.label, value: m.value })
  }
  return opts
})

const providerOptions = [
  { label: 'OpenAI 兼容(DeepSeek 等)', value: 'openai_compatible' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Claude CLI', value: 'claude_cli' },
  { label: 'Codex CLI', value: 'codex_cli' },
]

async function fetchConfig() {
  try {
    config.value = await getRecapConfig()
    apiKey.value = ''
    clearKey.value = false
    await loadRecapModels()
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    const payload: RecapConfig = {
      ...config.value,
      api_key: apiKey.value.trim(),
      clear_key: clearKey.value,
    }
    config.value = await updateRecapConfig(payload)
    apiKey.value = ''
    clearKey.value = false
    HMessage.success('回顾 AI 设置已保存')
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
      <span class="card-title">回顾 AI 模型</span>
      <HSwitch v-model="config.enabled">{{ config.enabled ? '已启用' : '已关闭' }}</HSwitch>
    </template>

    <div class="form-row-inline">
      <label class="form-label">API 地址</label>
      <div class="form-field">
        <HInput v-model="config.base_url" placeholder="https://api.deepseek.com" />
        <div class="form-hint">OpenAI 兼容格式的 API 地址,留空跟随 DeepSeek 默认。</div>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">模型版本</label>
      <div class="form-field">
        <HCombobox v-model="config.model" :options="modelOptions" placeholder="deepseek-v4-pro" clearable />
        <div class="form-hint">支持输入任意 OpenAI 兼容模型名称,清空跟随 DeepSeek 默认。</div>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">API 密钥</label>
      <div class="form-field">
        <div class="input-group">
          <HInput v-model="apiKey" placeholder="留空则保留已保存密钥" />
          <HPill :variant="config.api_key_set ? 'success' : 'danger'">
            {{ config.api_key_set ? '已配置' : '未配置' }}
          </HPill>
        </div>
        <div class="form-hint">读取配置时不会返回明文;需要更新时重新输入。</div>
        <HCheckbox v-if="config.api_key_set" v-model="clearKey">清除已保存密钥</HCheckbox>
      </div>
    </div>

    <button class="collapse-trigger" type="button" @click="advancedOpen = !advancedOpen">
      <span class="collapse-arrow" :class="{ open: advancedOpen }">›</span> 高级参数
    </button>
    <div v-show="advancedOpen" class="collapse-content">
      <div class="form-row-inline">
        <label class="form-label">Provider</label>
        <div class="form-field">
          <HSelect v-model="config.provider" :options="providerOptions" />
          <div class="form-hint">回顾生成后端实现,留空跟随 openai_compatible。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">密钥环境变量</label>
        <div class="form-field">
          <HInput v-model="config.api_key_env" placeholder="AI_API_KEY" />
          <div class="form-hint">配置后从该环境变量读取密钥,留空使用默认 AI_API_KEY。改名后密钥会迁移。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">说话人统计</label>
        <div class="form-field">
          <HSwitch v-model="config.include_speaker_info">检测到多个有效说话人时,将 ASR 说话人统计注入回顾提示词</HSwitch>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">最大 Token</label>
        <div class="form-field">
          <HInput v-model="maxTokens" placeholder="16384" />
          <div class="form-hint">控制单次生成的最大输出长度。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">最大续写次数</label>
        <div class="form-field">
          <HInput v-model="maxContinuations" placeholder="2" />
          <div class="form-hint">输出被截断时自动续写的轮数,0 表示不续写。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">超时时间(秒)</label>
        <div class="form-field">
          <HInput v-model="timeoutSeconds" placeholder="180" />
          <div class="form-hint">单次请求超时时间。</div>
        </div>
      </div>
    </div>

    <div class="card-actions">
      <HButton variant="primary" :loading="saving" @click="save">保存设置</HButton>
    </div>
  </HCard>
</template>
