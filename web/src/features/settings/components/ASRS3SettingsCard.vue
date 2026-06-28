<script setup lang="ts">
import './settings-cards.css'
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getASRS3Config, updateASRS3Config } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { ASRS3Config } from '@/api/types'

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

async function fetchConfig() {
  try {
    const data = await getASRS3Config()
    config.value = data
    accessKeySecret.value = ''
    clearSecret.value = false
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    // 密钥随卡片一起提交:留空保留,clear_secret 清除。trim 后纯空白视为未输入。
    const payload: ASRS3Config = {
      ...config.value,
      access_key_secret: accessKeySecret.value.trim(),
      clear_secret: clearSecret.value,
    }
    const data = await updateASRS3Config(payload)
    config.value = data
    accessKeySecret.value = ''
    clearSecret.value = false
    ElMessage.success('对象存储设置已保存')
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
  <div class="settings-card" data-section="asr-s3">
    <div class="card-header-row">
      <h3>ASR 对象存储</h3>
      <el-tag :type="config.access_key_set ? 'success' : 'info'" size="small">
        {{ config.access_key_set ? '密钥已保存' : '未配置' }}
      </el-tag>
    </div>
    <div class="column-note" style="margin-bottom: 12px;">
      S3 兼容对象存储(阿里云 OSS / MinIO 推荐),用于发布临时音频供 DashScope 转写拉取。
      配置后可替代本地 HTTP 服务,适合无公网 IP 的部署环境。
    </div>
    <div class="column-form">
      <div class="column-row">
        <div class="column-label">Endpoint</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.endpoint" size="small" clearable placeholder="https://oss-cn-hangzhou.aliyuncs.com" style="width: 420px" />
          </div>
          <div class="column-note">S3 兼容服务的接入地址,必须为 http(s) 且包含 host。</div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">Bucket</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.bucket" size="small" clearable placeholder="my-bucket" style="width: 320px" />
          </div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">Region</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.region" size="small" clearable placeholder="oss-cn-hangzhou(阿里云可留空)" style="width: 240px" />
          </div>
          <div class="column-note">部分 S3 实现必填(MinIO/AWS),阿里云 OSS 可留空。</div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">AccessKey ID</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.access_key_id" size="small" clearable placeholder="LTAI..." style="width: 320px" />
          </div>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">AccessKey Secret</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="accessKeySecret" type="password" show-password size="small" placeholder="留空则保留已保存密钥" style="width: 320px" />
            <el-tag v-if="config.access_key_set" type="success" size="small">已配置</el-tag>
            <el-tag v-else type="danger" size="small">未配置</el-tag>
          </div>
          <div class="column-note">对象存储访问密钥,读取配置时不会返回明文;需要更新时重新输入。</div>
          <el-checkbox v-if="config.access_key_set" v-model="clearSecret">清除已保存密钥</el-checkbox>
        </div>
      </div>

      <div class="column-row">
        <div class="column-label">公网 URL 前缀</div>
        <div class="column-main">
          <div class="column-control compact-control">
            <el-input v-model="config.public_url_prefix" size="small" clearable placeholder="https://my-bucket.oss-cn-hangzhou.aliyuncs.com/asr" style="width: 420px" />
          </div>
          <div class="column-note">DashScope 用此前缀 + 对象 key 拼出音频公网 URL,必须为 http(s) 且可公网访问。</div>
        </div>
      </div>

      <el-collapse>
        <el-collapse-item title="高级参数" name="advanced">
          <div class="column-row">
            <div class="column-label">Path Style</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-switch v-model="config.use_path_style" size="small" />
              </div>
              <div class="column-note">MinIO 等自建服务通常需要开启;阿里云 OSS / AWS 关闭。</div>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">密钥环境变量</div>
            <div class="column-main">
              <div class="column-control compact-control">
                <el-input v-model="config.access_key_env" size="small" placeholder="ASR_S3_ACCESS_KEY_SECRET" style="width: 280px" />
              </div>
              <div class="column-note">配置后从该环境变量读取密钥,留空使用默认 ASR_S3_ACCESS_KEY_SECRET。改名后已保存的密钥会迁移到新名称。</div>
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
