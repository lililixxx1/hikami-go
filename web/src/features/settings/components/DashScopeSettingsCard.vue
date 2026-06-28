<script setup lang="ts">
import './settings-cards.css'
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
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

async function fetchConfig() {
  try {
    const data = await getDashScopeConfig()
    config.value = data
    apiKey.value = ''
    clearKey.value = false
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    // 密钥随卡片一起提交:留空保留,clear_key 清除。trim 后纯空白视为未输入。
    const payload: DashScopeConfig = {
      ...config.value,
      api_key: apiKey.value.trim(),
      clear_key: clearKey.value,
    }
    const data = await updateDashScopeConfig(payload)
    config.value = data
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
  <div class="settings-card" data-section="dashscope">
    <div class="card-header-row">
      <h3>ASR 转写(DashScope)</h3>
      <el-tag :type="config.api_key_set ? 'success' : 'info'" size="small">
        {{ config.api_key_set ? '密钥已保存' : '未配置' }}
      </el-tag>
    </div>
    <div class="column-note" style="margin-bottom: 12px;">
      阿里云 DashScope 语音转写。配置后用于把直播录制自动转成文字稿,作为回顾生成的输入。
    </div>
    <div class="column-form">
      <div class="column-row">
        <div class="column-label">模型</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-select v-model="config.model" filterable allow-create size="small" placeholder="fun-asr" style="width: 320px">
              <el-option label="fun-asr(推荐)" value="fun-asr" />
              <el-option label="paraformer-v2" value="paraformer-v2" />
              <el-option label="qwen-audio-asr" value="qwen-audio-asr" />
            </el-select>
          </div>
          <div class="column-note">支持输入任意 DashScope 支持的模型名称。</div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">语言</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-select v-model="config.language" size="small" style="width: 200px">
              <el-option label="中文" value="zh" />
              <el-option label="英文" value="en" />
              <el-option label="自动检测" value="auto" />
            </el-select>
          </div>
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
          <div class="column-note">DashScope API 密钥,读取配置时不会返回明文;需要更新时重新输入。</div>
          <el-checkbox v-if="config.api_key_set" v-model="clearKey">清除已保存密钥</el-checkbox>
        </div>
      </div>

      <el-collapse>
        <el-collapse-item title="高级参数" name="advanced">
          <div class="column-row">
            <div class="column-label">说话人分离</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-switch v-model="config.diarization_enabled" size="small" />
              </div>
              <div class="column-note">开启后识别不同说话人,供回顾 AI 区分发言者。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">说话人数</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input-number v-model="config.speaker_count" size="small" :min="0" style="width: 160px" />
              </div>
              <div class="column-note">0 = 自动检测。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">定制词表</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input v-model="config.vocabulary_id" size="small" clearable placeholder="可选,DashScope 定制词表 ID" style="width: 320px" />
              </div>
              <div class="column-note">用于纠正专有名词/术语的转写错误。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">ASR URL</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input v-model="config.asr_url" size="small" clearable placeholder="留空走 DashScope 官方默认" style="width: 420px" />
              </div>
              <div class="column-note">提交转写任务的接口地址,留空使用官方默认。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">Tasks URL</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input v-model="config.tasks_url" size="small" clearable placeholder="留空走 DashScope 官方默认" style="width: 420px" />
              </div>
              <div class="column-note">查询转写任务结果的接口地址,留空使用官方默认。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">密钥环境变量</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input v-model="config.api_key_env" size="small" placeholder="DASHSCOPE_API_KEY" style="width: 240px" />
              </div>
              <div class="column-note">配置后从该环境变量读取密钥,留空使用默认 DASHSCOPE_API_KEY。改名后已保存的密钥会迁移到新名称。</div>
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
