<script setup lang="ts">
import './settings-cards.css'
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getWebDAVConfig, updateWebDAVConfig } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { WebDAVConfig } from '@/api/types'

const emit = defineEmits<{
  saved: []
}>()

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

async function fetchConfig() {
  try {
    // 读取后清空 password/clear_password(一次性标志),与原逻辑一致
    config.value = {
      ...(await getWebDAVConfig()),
      password: '',
      clear_password: false,
    }
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    config.value = {
      ...(await updateWebDAVConfig(config.value)),
      password: '',
      clear_password: false,
    }
    ElMessage.success('WebDAV 设置已保存')
    await runtimeStore.fetchRuntime(true)
    emit('saved')
  } finally {
    saving.value = false
  }
}

onMounted(fetchConfig)

// 供外部(壳)在配置导入后触发重新加载
defineExpose({ reload: fetchConfig })
</script>

<template>
  <div class="settings-card" data-section="webdav">
    <div class="card-header-row">
      <h3>WebDAV 上传</h3>
      <el-tag v-if="config.password_set" type="success" size="small">密码已保存</el-tag>
    </div>
    <div class="column-form">
      <div class="column-row">
        <div class="column-label">服务地址</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.url" clearable placeholder="https://webdav.example.com/dav" />
          </div>
          <div class="column-note">原生 WebDAV 客户端使用的服务器地址。</div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">用户名</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.username" clearable placeholder="WebDAV 用户名" />
          </div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">密码</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.password" show-password clearable placeholder="留空则保留已保存密码" />
          </div>
          <div class="column-note">读取配置时不会返回明文密码；需要更新密码时重新输入。</div>
          <el-checkbox v-if="config.password_set" v-model="config.clear_password">清除已保存密码</el-checkbox>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">基础路径</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.base_path" clearable placeholder="/hikami" />
          </div>
          <div class="column-note">上传文件在 WebDAV 服务器中的根目录。</div>
        </div>
      </div>

      <el-collapse>
        <el-collapse-item title="rclone 兼容配置" name="rclone">
          <div class="column-row">
            <div class="column-label">Remote</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input v-model="config.remote" clearable placeholder="hikami-webdav:" />
              </div>
              <div class="column-note">仅作为原生 WebDAV 不可用时的兼容配置。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">密码环境变量</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input v-model="config.password_env" clearable placeholder="WEBDAV_PASSWORD" />
              </div>
              <div class="column-note">配置后优先从环境变量读取密码。</div>
            </div>
          </div>
        </el-collapse-item>
      </el-collapse>

      <div class="column-actions">
        <el-button type="primary" :loading="saving" @click="save">保存设置</el-button>
      </div>
    </div>
  </div>
</template>

