<script setup lang="ts">
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAdminToken } from '@/composables/useAdminToken'

const { hasToken: hasAdminToken, setToken: setAdminToken, clearToken: clearAdminTokenStore } = useAdminToken()

// 管理员令牌(前端本地保存,注入 X-Admin-Token;ISS-2 备注2)
async function editAdminToken() {
  try {
    const { value } = await ElMessageBox.prompt('请输入管理员令牌', '管理员令牌', {
      confirmButtonText: '保存',
      cancelButtonText: '取消',
      inputType: 'password',
      inputPlaceholder: 'admin token',
    })
    const trimmed = (value || '').trim()
    if (trimmed) {
      setAdminToken(trimmed)
      ElMessage.success('管理员令牌已保存')
    }
  } catch {
    // 用户取消
  }
}

function clearAdminToken() {
  clearAdminTokenStore()
  ElMessage.success('已清除管理员令牌')
}
</script>

<template>
  <div class="settings-card" data-section="admin">
    <div class="card-header-row">
      <h3>管理员令牌</h3>
      <el-tag :type="hasAdminToken ? 'success' : 'info'" size="small">
        {{ hasAdminToken ? '已配置' : '未配置' }}
      </el-tag>
    </div>
    <div class="column-form">
      <div class="column-note">
        访问敏感 REST API（主播/配置/密钥等）需要管理员令牌。令牌来自后端 config.yaml 的 admin_token（或 admin_token_env 指向的环境变量）。此处仅将令牌保存到当前浏览器本地，用于自动注入 X-Admin-Token。
      </div>
      <div class="column-actions">
        <el-button type="primary" @click="editAdminToken">
          {{ hasAdminToken ? '修改令牌' : '设置令牌' }}
        </el-button>
        <el-button v-if="hasAdminToken" type="danger" plain @click="clearAdminToken">清除</el-button>
      </div>
    </div>
  </div>
</template>
