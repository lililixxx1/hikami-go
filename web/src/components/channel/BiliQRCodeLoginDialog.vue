<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import QRCode from 'qrcode'
import { useBiliQRCodeLogin } from '@/features/channel/useBiliQRCodeLogin'
import { HDialog, HButton, HInput } from '@/components/ui'
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

function selectUsage(value: QRCodeCookieUsage): void {
  usage.value = value
}
</script>

<template>
  <HDialog
    :visible="visible"
    title="B 站扫码登录"
    width="360px"
    @update:visible="handleClose"
  >
    <div class="qr-login-body">
      <!-- 状态驱动展示:loading 骨架 / 二维码 / 状态提示 -->
      <div v-if="creating && !session" class="qr-skeleton" />
      <canvas v-show="session" ref="canvasRef" class="qr-canvas" />

      <div class="status-alert" :class="`alert-${state}`">
        {{ statusText }}
      </div>

      <!-- 扫码成功后的保存区 -->
      <div v-if="pollResult?.status === 'succeeded'" class="save-panel">
        <!-- 账号模式:输入昵称 -->
        <template v-if="isAccountMode">
          <HInput v-model="nickname" placeholder="账号备注名(可选)" />
        </template>
        <!-- 主播模式:选择用途 -->
        <template v-else>
          <div class="radio-group">
            <label class="radio-item">
              <input type="radio" :checked="usage === 'download'" @change="selectUsage('download')">
              <span>下载用</span>
            </label>
            <label class="radio-item">
              <input type="radio" :checked="usage === 'publish'" @change="selectUsage('publish')">
              <span>发布用</span>
            </label>
          </div>
        </template>
        <HButton variant="primary" :loading="saving" :disabled="!canSave" @click="handleSave">
          保存
        </HButton>
      </div>
    </div>

    <template #footer>
      <HButton variant="secondary" @click="handleClose">关闭</HButton>
      <HButton variant="primary" :loading="creating" @click="startLogin">刷新二维码</HButton>
    </template>
  </HDialog>
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

.qr-skeleton {
  background: var(--surface);
  border-radius: var(--radius-md);
  animation: skeleton-pulse 1.5s ease-in-out infinite;
}

@keyframes skeleton-pulse {
  0%, 100% { opacity: 0.6; }
  50% { opacity: 1; }
}

.status-alert {
  width: 100%;
  padding: 10px 14px;
  border-radius: var(--radius-md);
  font-size: 13px;
  text-align: center;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text-secondary);
}

.status-alert.alert-done {
  border-color: var(--success-border, #d1edc4);
  background: var(--success-bg, #f0f9eb);
  color: var(--success, #1aae39);
}

.status-alert.alert-expired,
.status-alert.alert-failed {
  border-color: var(--danger-border, #fcd3d3);
  background: var(--danger-bg, #fef0f0);
  color: var(--danger, #e03e2d);
}

.status-alert.alert-scanned {
  border-color: var(--success-border, #d1edc4);
  background: var(--success-bg, #f0f9eb);
  color: var(--success, #1aae39);
}

.save-panel {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: stretch;
}

.radio-group {
  display: flex;
  justify-content: center;
  gap: 20px;
}

.radio-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  font-size: 13px;
  color: var(--text-secondary);
}

.save-panel :deep(.btn) {
  align-self: center;
}
</style>
