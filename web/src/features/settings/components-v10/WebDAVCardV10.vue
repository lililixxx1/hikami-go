<!--
  WebDAVCardV10.vue(Phase 5 Task 5.2)。WebDAV 上传配置卡。
  移植自 WebDAVSettingsCard.vue(EP)。endpoint /api/config/webdav。
  三态密钥字段:password(password_set 显示"密码已保存",clear_password checkbox)。
  其余:url/username/base_path/rclone(remote/password_env)。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HCard, HButton, HInput, HCheckbox, HPill } from '@/components/ui'
import { getWebDAVConfig, updateWebDAVConfig } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { WebDAVConfig } from '@/api/types'

const emit = defineEmits<{ saved: [] }>()
const runtimeStore = useRuntimeStore()

const config = ref<WebDAVConfig>({
  url: '',
  username: '',
  password: '',
  password_env: '',
  base_path: '',
  remote: '',
  password_set: false,
})
const saving = ref(false)
const advancedOpen = ref(false)
// clear_password 是写-only 一次性标志(WebDAVConfig.clear_password?: boolean)。
// HCheckbox 需严格 boolean,故用本地 ref,save 时写入 payload,保存后重置。
const clearPassword = ref(false)

async function fetchConfig() {
  try {
    // 读取后清空 password/clear_password(一次性标志),与原逻辑一致
    config.value = {
      ...(await getWebDAVConfig()),
      password: '',
      clear_password: false,
    }
    clearPassword.value = false
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    const payload: WebDAVConfig = { ...config.value, clear_password: clearPassword.value }
    config.value = {
      ...(await updateWebDAVConfig(payload)),
      password: '',
      clear_password: false,
    }
    clearPassword.value = false
    HMessage.success('WebDAV 设置已保存')
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
      <span class="card-title">WebDAV 上传</span>
      <HPill v-if="config.password_set" variant="success">密码已保存</HPill>
    </template>

    <div class="form-row-inline">
      <label class="form-label">服务地址</label>
      <div class="form-field">
        <HInput v-model="config.url" placeholder="https://webdav.example.com/dav" />
        <div class="form-hint">原生 WebDAV 客户端使用的服务器地址。</div>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">用户名</label>
      <div class="form-field">
        <HInput v-model="config.username" placeholder="WebDAV 用户名" />
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">密码</label>
      <div class="form-field">
        <HInput v-model="config.password" placeholder="留空则保留已保存密码" />
        <div class="form-hint">读取配置时不会返回明文密码;需要更新密码时重新输入。</div>
        <HCheckbox v-if="config.password_set" v-model="clearPassword">清除已保存密码</HCheckbox>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">基础路径</label>
      <div class="form-field">
        <HInput v-model="config.base_path" placeholder="/hikami" />
        <div class="form-hint">上传文件在 WebDAV 服务器中的根目录。</div>
      </div>
    </div>

    <button class="collapse-trigger" type="button" @click="advancedOpen = !advancedOpen">
      <span class="collapse-arrow" :class="{ open: advancedOpen }">›</span> rclone 兼容配置
    </button>
    <div v-show="advancedOpen" class="collapse-content">
      <div class="form-row-inline">
        <label class="form-label">Remote</label>
        <div class="form-field">
          <HInput v-model="config.remote" placeholder="hikami-webdav:" />
          <div class="form-hint">仅作为原生 WebDAV 不可用时的兼容配置。</div>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">密码环境变量</label>
        <div class="form-field">
          <HInput v-model="config.password_env" placeholder="WEBDAV_PASSWORD" />
          <div class="form-hint">配置后优先从环境变量读取密码。</div>
        </div>
      </div>
    </div>

    <div class="card-actions">
      <HButton variant="primary" :loading="saving" @click="save">保存设置</HButton>
    </div>
  </HCard>
</template>
