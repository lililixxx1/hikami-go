<!--
  AdminTokenCardV10.vue(Phase 5 Task 5.4)。管理员令牌卡。
  移植自 AdminTokenCard.vue(EP)。复用 useAdminToken(hasToken/setToken/clearToken)。
  - 本地 tokenInput ref(HInput password),保存=setToken,清除=clearToken。
  - hasToken 状态用 HPill 显示已配置/未配置。
  EP 原用 ElMessageBox.prompt;V10 改为内联 HInput(更直接)。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { HCard, HButton, HInput, HPill } from '@/components/ui'
import { useAdminToken } from '@/composables/useAdminToken'

const { hasToken, setToken, clearToken } = useAdminToken()

const tokenInput = ref('')

function handleSave() {
  const trimmed = tokenInput.value.trim()
  if (!trimmed) {
    ElMessage.warning('请输入令牌')
    return
  }
  setToken(trimmed)
  tokenInput.value = ''
  ElMessage.success('管理员令牌已保存')
}

function handleClear() {
  clearToken()
  tokenInput.value = ''
  ElMessage.success('已清除管理员令牌')
}
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">管理员令牌</span>
      <HPill :variant="hasToken ? 'success' : 'neutral'">
        {{ hasToken ? '已配置' : '未配置' }}
      </HPill>
    </template>

    <div class="form-hint" style="margin-bottom: 12px;">
      访问敏感 REST API(主播/配置/密钥等)需要管理员令牌。令牌来自后端 config.yaml 的
      admin_token(或 admin_token_env 指向的环境变量)。此处仅将令牌保存到当前浏览器本地,
      用于自动注入 X-Admin-Token。
    </div>

    <div class="form-field">
      <span class="form-label">管理员令牌</span>
      <HInput v-model="tokenInput" placeholder="admin token" />
    </div>

    <div class="card-actions">
      <HButton variant="ghost" v-if="hasToken" @click="handleClear">清除</HButton>
      <HButton variant="primary" @click="handleSave">{{ hasToken ? '修改令牌' : '设置令牌' }}</HButton>
    </div>
  </HCard>
</template>
