<!--
  ASRS3CardV10.vue(Phase 5 Task 5.2)。ASR 对象存储配置卡。
  移植自 ASRS3SettingsCard.vue(EP)。endpoint /api/config/asr-s3。
  三态密钥字段:access_key_secret(access_key_set 显示"已配置",clear_secret checkbox)。
  其余字段:endpoint/bucket/region/access_key_id/public_url_prefix/use_path_style/access_key_env。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HCard, HButton, HInput, HSwitch, HCheckbox, HPill } from '@/components/ui'
import { getASRS3Config, updateASRS3Config } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { ASRS3Config } from '@/api/types-derived'

const emit = defineEmits<{ saved: [] }>()
const runtimeStore = useRuntimeStore()

const config = ref<ASRS3Config>({
  endpoint: '',
  bucket: '',
  access_key_id: '',
  access_key_env: '',
  region: '',
  public_url_prefix: '',
  use_path_style: false,
  access_key_set: false,
})
const accessKeySecret = ref('')
const clearSecret = ref(false)
const saving = ref(false)
const advancedOpen = ref(false)

async function fetchConfig() {
  try {
    config.value = await getASRS3Config()
    accessKeySecret.value = ''
    clearSecret.value = false
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    const payload: ASRS3Config = {
      ...config.value,
      access_key_secret: accessKeySecret.value.trim(),
      clear_secret: clearSecret.value,
    }
    config.value = await updateASRS3Config(payload)
    accessKeySecret.value = ''
    clearSecret.value = false
    HMessage.success('对象存储设置已保存')
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
      <span class="card-title">ASR 对象存储</span>
      <HPill :variant="config.access_key_set ? 'success' : 'warning'">
        {{ config.access_key_set ? '密钥已保存' : '未配置' }}
      </HPill>
    </template>

    <div class="form-hint" style="margin-bottom: 12px;">
      S3 兼容对象存储(阿里云 OSS / MinIO 推荐),用于发布临时音频供 DashScope 转写拉取。
      配置后可替代本地 HTTP 服务,适合无公网 IP 的部署环境。
    </div>

    <div class="form-row-inline">
      <label class="form-label">Endpoint</label>
      <div class="form-field">
        <HInput v-model="config.endpoint" placeholder="https://oss-cn-hangzhou.aliyuncs.com" />
        <div class="form-hint">S3 兼容服务的接入地址,必须为 http(s) 且包含 host。</div>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">Bucket</label>
      <div class="form-field">
        <HInput v-model="config.bucket" placeholder="my-bucket" />
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">Region</label>
      <div class="form-field">
        <HInput v-model="config.region" placeholder="oss-cn-hangzhou(阿里云可留空)" />
        <div class="form-hint">部分 S3 实现必填(MinIO/AWS),阿里云 OSS 可留空。</div>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">AccessKey ID</label>
      <div class="form-field">
        <HInput v-model="config.access_key_id" placeholder="LTAI..." />
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">AccessKey Secret</label>
      <div class="form-field">
        <div class="input-group">
          <HInput v-model="accessKeySecret" placeholder="留空则保留已保存密钥" />
          <HPill :variant="config.access_key_set ? 'success' : 'danger'">
            {{ config.access_key_set ? '已配置' : '未配置' }}
          </HPill>
        </div>
        <div class="form-hint">对象存储访问密钥,读取配置时不会返回明文;需要更新时重新输入。</div>
        <HCheckbox v-if="config.access_key_set" v-model="clearSecret">清除已保存密钥</HCheckbox>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">公网 URL 前缀</label>
      <div class="form-field">
        <HInput v-model="config.public_url_prefix" placeholder="https://my-bucket.oss-cn-hangzhou.aliyuncs.com/asr" />
        <div class="form-hint">DashScope 用此前缀 + 对象 key 拼出音频公网 URL,必须可公网访问。</div>
      </div>
    </div>

    <button class="collapse-trigger" type="button" @click="advancedOpen = !advancedOpen">
      <span class="collapse-arrow" :class="{ open: advancedOpen }">›</span> 高级参数
    </button>
    <div v-show="advancedOpen" class="collapse-content">
      <div class="form-row-inline">
        <label class="form-label">Path Style</label>
        <div class="form-field">
          <HSwitch v-model="config.use_path_style">MinIO 等自建服务通常需要开启;阿里云 OSS / AWS 关闭</HSwitch>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">密钥环境变量</label>
        <div class="form-field">
          <HInput v-model="config.access_key_env" placeholder="ASR_S3_ACCESS_KEY_SECRET" />
          <div class="form-hint">配置后从该环境变量读取密钥,留空使用默认。改名后密钥会迁移。</div>
        </div>
      </div>
    </div>

    <div class="card-actions">
      <HButton variant="primary" :loading="saving" @click="save">保存设置</HButton>
    </div>
  </HCard>
</template>
