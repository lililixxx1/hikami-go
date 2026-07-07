<!--
  AccountsCardV10.vue(Phase 5 Task 5.4)。B站账号卡。
  受控展示组件(props 进,emit 出),壳(Task 5.5)持有 QR 状态机 + 账号列表。
  - account-row:avatar(首字)/name/meta(uid + 默认下载/发布 HPill)+ cookie_file 空 → 灰"未登录" tag(opacity 0.6)。
  - 操作:设默认下载/发布(emit set-default: [id, usage])、删除(emit delete: [id])。
  - QR 登录区:canvas(qrSession.url 渲染)+ 状态文本(pollResult)+ 倒计时(session.expires_at)。
    按钮:扫码登录(emit generate-qr)、轮询(emit poll)、保存(emit save-qr)、重载(emit reload)。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import QRCode from 'qrcode'
import { HCard, HButton, HPill, HInput, HEmpty } from '@/components/ui'
import type { BiliCookieAccount, QRCodeSession, QRCodePollResult } from '@/api/types'

const props = defineProps<{
  accounts: BiliCookieAccount[]
  qrSession: QRCodeSession | null
  pollResult: QRCodePollResult | null
  qrLoading?: boolean
  qrSaving?: boolean
}>()

const emit = defineEmits<{
  'generate-qr': []
  poll: []
  'save-qr': [nickname: string]
  'set-default': [id: number, usage: 'download' | 'publish']
  delete: [id: number]
  reload: []
}>()

const canvasRef = ref<HTMLCanvasElement | null>(null)
const nickname = ref('')

function isLoggedIn(account: BiliCookieAccount): boolean {
  return account.cookie_file !== ''
}

function avatarText(account: BiliCookieAccount): string {
  if (account.nickname) return account.nickname.slice(0, 1)
  return String(account.uid).slice(0, 1)
}

// QR canvas 渲染:session.url 变化时重绘
watch(
  () => props.qrSession?.url,
  async (url) => {
    if (!url || !canvasRef.value) return
    await QRCode.toCanvas(canvasRef.value, url, { width: 200, margin: 1, errorCorrectionLevel: 'M' })
  },
)

// 倒计时(秒,从 expires_at 计算)
const countdown = computed(() => {
  if (!props.qrSession?.expires_at) return ''
  const exp = new Date(props.qrSession.expires_at).getTime()
  const remain = Math.max(0, Math.floor((exp - Date.now()) / 1000))
  if (remain <= 0) return '已过期'
  const m = Math.floor(remain / 60)
  const s = remain % 60
  return `${m}:${String(s).padStart(2, '0')}`
})

const pollStatusText = computed(() => props.pollResult?.message || (props.qrSession ? '等待扫码…' : ''))

function handleSave() {
  emit('save-qr', nickname.value.trim())
  nickname.value = ''
}
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">B站账号</span>
      <HButton variant="primary" size="sm" :loading="qrLoading" @click="emit('generate-qr')">扫码登录</HButton>
    </template>

    <HEmpty v-if="!accounts.length" description="暂无账号,点击「扫码登录」添加 B 站账号" />

    <div v-else>
      <div
        v-for="account in accounts"
        :key="account.id"
        class="account-row"
        :style="{ opacity: isLoggedIn(account) ? 1 : 0.6 }"
      >
        <div class="account-avatar" :class="{ muted: !isLoggedIn(account) }">{{ avatarText(account) }}</div>
        <div class="account-info">
          <div class="account-name">{{ account.nickname || `UID ${account.uid}` }}</div>
          <div class="account-meta">
            UID: {{ account.uid }}
            <HPill v-if="account.is_default_download" variant="success">默认下载</HPill>
            <HPill v-if="account.is_default_publish" variant="warning">默认发布</HPill>
            <HPill v-if="!isLoggedIn(account)" variant="neutral">未登录</HPill>
          </div>
        </div>
        <div class="account-ops">
          <HButton
            variant="ghost"
            size="xs"
            @click="emit('set-default', account.id, 'download')"
          >{{ account.is_default_download ? '✓ 默认下载' : '设默认下载' }}</HButton>
          <HButton
            variant="ghost"
            size="xs"
            @click="emit('set-default', account.id, 'publish')"
          >{{ account.is_default_publish ? '✓ 默认发布' : '设默认发布' }}</HButton>
          <HButton variant="danger" size="xs" @click="emit('delete', account.id)">删除</HButton>
        </div>
      </div>
    </div>

    <!-- QR 登录区(仅当有 session 时展示) -->
    <div v-if="qrSession" style="margin-top: 16px; border-top: 1px solid var(--border-light); padding-top: 14px;">
      <div class="form-label" style="margin-bottom: 8px;">扫码登录</div>
      <div style="display: flex; gap: 16px; align-items: flex-start;">
        <canvas ref="canvasRef" style="border: 1px solid var(--border-light); border-radius: var(--radius-md);" />
        <div style="flex: 1;">
          <div class="form-hint">状态:{{ pollStatusText }}</div>
          <div class="form-hint">剩余有效期:{{ countdown }}</div>
          <div style="margin-top: 8px; display: flex; flex-direction: column; gap: 8px;">
            <HButton variant="secondary" size="sm" @click="emit('poll')">刷新状态</HButton>
            <template v-if="pollResult?.status === 'succeeded'">
              <HInput v-model="nickname" placeholder="账号备注名(可选)" />
              <HButton variant="primary" size="sm" :loading="qrSaving" @click="handleSave">保存账号</HButton>
            </template>
          </div>
        </div>
      </div>
    </div>

    <div class="card-actions start" style="margin-top: 12px;">
      <HButton variant="ghost" size="sm" @click="emit('reload')">刷新列表</HButton>
    </div>
  </HCard>
</template>
