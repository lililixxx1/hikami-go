<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { deleteBiliAccount, listBiliAccounts, updateBiliAccount } from '@/api/bili'
import BiliQRCodeLoginDialog from '@/components/channel/BiliQRCodeLoginDialog.vue'
import type { BiliCookieAccount } from '@/api/types'

const accounts = ref<BiliCookieAccount[]>([])
const loading = ref(false)
const showQRDialog = ref(false)

async function fetchAccounts() {
  loading.value = true
  try {
    accounts.value = await listBiliAccounts()
  } catch { /* ignore */ }
  finally {
    loading.value = false
  }
}

// account 模式扫码保存成功后刷新列表
async function handleAccountSaved() {
  await fetchAccounts()
}

async function handleSetDefaultDownload(account: BiliCookieAccount, val: boolean) {
  try {
    await updateBiliAccount(account.id, { is_default_download: val })
    await fetchAccounts()
  } catch { /* error shown by interceptor */ }
}

async function handleSetDefaultPublish(account: BiliCookieAccount, val: boolean) {
  try {
    await updateBiliAccount(account.id, { is_default_publish: val })
    await fetchAccounts()
  } catch { /* error shown by interceptor */ }
}

async function handleDeleteAccount(account: BiliCookieAccount) {
  try {
    await ElMessageBox.confirm(
      `确认删除账号「${account.nickname || account.uid}」？`,
      '删除账号',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' },
    )
  } catch { return }
  try {
    await deleteBiliAccount(account.id)
    ElMessage.success('已删除')
    await fetchAccounts()
  } catch { /* error shown by interceptor */ }
}

onMounted(fetchAccounts)
defineExpose({ reload: fetchAccounts })
</script>

<template>
  <div class="settings-card">
    <div class="card-header-row">
      <h3>B站账号</h3>
      <el-button type="primary" size="small" @click="showQRDialog = true">扫码登录</el-button>
    </div>
    <div v-loading="loading">
      <div v-if="!accounts.length" class="empty-hint">
        暂无账号，点击「扫码登录」添加 B 站账号
      </div>
      <div v-else class="account-list">
        <div v-for="account in accounts" :key="account.id" class="account-card">
          <div class="account-info">
            <div class="account-name">
              {{ account.nickname || `UID ${account.uid}` }}
              <el-tag v-if="account.is_default_download" type="success" size="small" style="margin-left: 6px">默认下载</el-tag>
              <el-tag v-if="account.is_default_publish" type="warning" size="small" style="margin-left: 6px">默认发布</el-tag>
            </div>
            <div class="account-uid">UID: {{ account.uid }}</div>
          </div>
          <div class="account-actions">
            <el-switch
              :model-value="account.is_default_download"
              size="small"
              active-text="默认下载"
              @change="(val: boolean) => handleSetDefaultDownload(account, val)"
            />
            <el-switch
              :model-value="account.is_default_publish"
              size="small"
              active-text="默认发布"
              @change="(val: boolean) => handleSetDefaultPublish(account, val)"
            />
            <el-button size="small" type="danger" plain @click="handleDeleteAccount(account)">删除</el-button>
          </div>
        </div>
      </div>
    </div>

    <!-- QR code login dialog (account mode) -->
    <BiliQRCodeLoginDialog
      v-model:visible="showQRDialog"
      channel-id=""
      mode="account"
      @saved-account="handleAccountSaved"
    />
  </div>
</template>

<style scoped>
.empty-hint {
  padding: 16px 0;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  text-align: center;
}

.account-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.account-card {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px;
  background: var(--el-fill-color-light);
  border-radius: 6px;
}

.account-info {
  min-width: 0;
}

.account-name {
  font-weight: 500;
  font-size: 14px;
}

.account-uid {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 2px;
}

.account-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
  flex-wrap: wrap;
  justify-content: flex-end;
}
</style>
