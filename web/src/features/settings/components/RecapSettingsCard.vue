<script setup lang="ts">
import './settings-cards.css'
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getRecapConfig, updateRecapConfig } from '@/api/settings'
import { useRecapModels } from '@/composables/useRecapModels'
import { useRuntimeStore } from '@/stores/runtime'
import type { RecapConfig } from '@/api/types'

const emit = defineEmits<{
  saved: []
}>()

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

async function fetchConfig() {
  try {
    const data = await getRecapConfig()
    config.value = data
    apiKey.value = ''
    clearKey.value = false
    await loadRecapModels()
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    // 密钥随卡片一起提交:留空保留,clear_key 清除(后端响应层兜底 base_url/model/provider)
    // trim 后纯空白视为未输入(留空保留语义),与旧行为一致(codex 审核低[7])。
    const payload: RecapConfig = {
      ...config.value,
      api_key: apiKey.value.trim(),
      clear_key: clearKey.value,
    }
    const data = await updateRecapConfig(payload)
    config.value = data
    apiKey.value = ''
    clearKey.value = false
    ElMessage.success('回顾 AI 设置已保存')
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
  <div class="settings-card" data-section="recap">
    <div class="card-header-row">
      <h3>回顾 AI</h3>
      <el-switch v-model="config.enabled" active-text="已启用" inactive-text="已关闭" />
    </div>
    <div class="column-form">
      <div class="column-row">
        <div class="column-label">API 地址</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.base_url" size="small" placeholder="https://api.deepseek.com" style="width: 320px" />
          </div>
          <div class="column-note">OpenAI 兼容格式的 API 地址,留空跟随 DeepSeek 默认。</div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">模型版本</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-select v-model="config.model" size="small" filterable allow-create placeholder="选择或输入模型名称" style="width: 320px">
              <el-option-group v-for="grp in recapModelGroups" :key="grp.name" :label="grp.name">
                <el-option v-for="m in grp.models" :key="m.value" :label="m.label" :value="m.value" />
              </el-option-group>
            </el-select>
          </div>
          <div class="column-note">支持输入任意 OpenAI 兼容模型名称,留空跟随 DeepSeek 默认。</div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">API 密钥</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="apiKey" type="password" show-password size="small" placeholder="留空则保留已保存密钥" style="width: 320px" />
            <el-tag v-if="config.api_key_set" type="success" size="small">已配置</el-tag>
            <el-tag v-else type="danger" size="small">未配置</el-tag>
          </div>
          <div class="column-note">读取配置时不会返回明文;需要更新时重新输入。</div>
          <el-checkbox v-if="config.api_key_set" v-model="clearKey">清除已保存密钥</el-checkbox>
        </div>
      </div>

      <el-collapse>
        <el-collapse-item title="高级参数" name="advanced">
          <div class="column-row">
            <div class="column-label">Provider</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-select v-model="config.provider" size="small" style="width: 240px">
                  <el-option label="OpenAI 兼容(DeepSeek 等)" value="openai_compatible" />
                  <el-option label="Anthropic" value="anthropic" />
                  <el-option label="Claude CLI" value="claude_cli" />
                  <el-option label="Codex CLI" value="codex_cli" />
                </el-select>
              </div>
              <div class="column-note">回顾生成后端实现,留空跟随 openai_compatible。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">密钥环境变量</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input v-model="config.api_key_env" size="small" placeholder="AI_API_KEY" style="width: 240px" />
              </div>
              <div class="column-note">配置后从该环境变量读取密钥,留空使用默认 AI_API_KEY。改名后已保存的密钥会迁移到新名称。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">说话人统计</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-switch v-model="config.include_speaker_info" size="small" />
              </div>
              <div class="column-note">检测到多个有效说话人时，将 ASR 说话人统计注入回顾提示词。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">最大 Token</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input-number v-model="config.max_tokens" size="small" :min="0" :step="1024" style="width: 160px" />
              </div>
              <div class="column-note">控制单次生成的最大输出长度。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">最大续写次数</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input-number v-model="config.max_continuations" size="small" :min="0" :max="10" style="width: 160px" />
              </div>
              <div class="column-note">输出被截断时自动续写的轮数，0 表示不续写。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">超时时间(秒)</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input-number v-model="config.timeout_seconds" size="small" :min="30" :step="30" style="width: 160px" />
              </div>
              <div class="column-note">单次请求超时时间。</div>
            </div>
          </div>
        </el-collapse-item>
      </el-collapse>

      <div class="column-actions">
        <el-button size="default" :loading="saving" @click="save">保存设置</el-button>
      </div>
    </div>
  </div>
</template>
