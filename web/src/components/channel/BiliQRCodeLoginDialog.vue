<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import QRCode from 'qrcode'
import { useBiliQRCodeLogin } from '@/features/channel/useBiliQRCodeLogin'
import type {
  BiliCookieAccount,
  Channel,
  QRCodeCookieUsage,
} from '@/api/types'

const props = withDefaults(defineProps<{
  visible: boolean
  channelId: string
  defaultUsage?: QRCodeCookieUsage
  mode?: 'channel' | 'account'
}>(), {
  defaultUsage: 'download',
  mode: 'channel',
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
  saved: [channel: Channel]
  'saved-account': [account: BiliCookieAccount]
}>()

// canvas 留组件(QR 渲染是展示侧,操作 DOM)
const canvasRef = ref<HTMLCanvasElement | null>(null)

async function renderQRCode(text: string): Promise<void> {
  if (!canvasRef.value) return
  await QRCode.toCanvas(canvasRef.value, text, {
    width: 220,
    margin: 1,
    errorCorrectionLevel: 'M',
  })
}

// 业务逻辑全在 composable(状态机 + 轮询 + 两路保存);组件只桥接 emit
const {
  session,
  pollResult,
  state,
  usage,
  nickname,
  creating,
  saving,
  isAccountMode,
  canSave,
  statusText,
  startLogin,
  handleSave,
  cleanupSession,
  close,
} = useBiliQRCodeLogin({
  visible: () => props.visible,
  channelId: () => props.channelId,
  defaultUsage: () => props.defaultUsage,
  mode: () => props.mode,
  onSessionReady: (url) => renderQRCode(url),
  onSaved: (channel) => emit('saved', channel),
  onSavedAccount: (account) => emit('saved-account', account),
  onClose: () => emit('update:visible', false),
})

function handleClose(): void {
  close()
}

// 组件卸载(如切路由)时清理未完成的扫码会话,防 session 泄漏;
// usePolling 的 onUnmounted 已负责停止轮询定时器。
onBeforeUnmount(() => {
  void cleanupSession()
})
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="B 站扫码登录"
    width="360px"
    @update:model-value="handleClose"
  >
    <div class="qr-login-body">
      <!-- 状态驱动展示:loading 骨架 / 二维码 / 状态提示 -->
      <div v-if="creating && !session" class="qr-skeleton">
        <el-skeleton animated>
          <template #template>
            <el-skeleton-item variant="image" style="width: 220px; height: 220px;" />
          </template>
        </el-skeleton>
      </div>
      <canvas v-show="session" ref="canvasRef" class="qr-canvas" />

      <el-alert
        :type="state === 'done' ? 'success' : state === 'expired' || state === 'failed' ? 'error' : state === 'scanned' ? 'success' : 'info'"
        :title="statusText"
        :closable="false"
        show-icon
        class="status-alert"
      />

      <!-- 扫码成功后的保存区 -->
      <div v-if="pollResult?.status === 'succeeded'" class="save-panel">
        <!-- 账号模式:输入昵称 -->
        <template v-if="isAccountMode">
          <el-input v-model="nickname" placeholder="账号备注名(可选)" clearable />
        </template>
        <!-- 主播模式:选择用途 -->
        <template v-else>
          <el-radio-group v-model="usage">
            <el-radio value="download">下载用</el-radio>
            <el-radio value="publish">发布用</el-radio>
          </el-radio-group>
        </template>
        <el-button type="primary" :loading="saving" :disabled="!canSave" @click="handleSave">
          保存
        </el-button>
      </div>
    </div>

    <template #footer>
      <el-button @click="handleClose">关闭</el-button>
      <el-button type="primary" plain :loading="creating" @click="startLogin">刷新二维码</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.qr-login-body {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  padding: 8px 0;
}

.qr-skeleton,
.qr-canvas {
  width: 220px;
  height: 220px;
}

.status-alert {
  width: 100%;
}

.save-panel {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: stretch;
}

.save-panel :deep(.el-radio-group) {
  justify-content: center;
}

.save-panel .el-button {
  align-self: center;
}
</style>
