/**
 * B站扫码登录状态机(重构方案 §6 阶段5)。
 *
 * 从 BiliQRCodeLoginDialog 抽出的业务逻辑:显式 DialogState 状态机 + 2s 轮询 + 两路保存。
 * 复用 usePolling(消除手写 setInterval/clearInterval)。
 *
 * composable 不关心 canvas(QR 渲染是展示侧,留在组件);创建 session 成功后调 onSessionReady
 * 让组件渲染二维码。emit 也留在组件,composable 通过回调通知保存结果。
 */
import { computed, ref, watch } from 'vue'
import { HMessage } from '@/components/ui/message'
import {
  cancelQRCodeSession,
  createQRCodeSession,
  pollQRCodeSession,
  saveQRCodeSession,
  saveQRCodeToAccount,
} from '@/api/bili'
import { usePolling } from '@/composables/usePolling'
import type {
  BiliCookieAccount,
  Channel,
  QRCodeCookieUsage,
  QRCodeLoginStatus,
  QRCodePollResult,
  QRCodeSession,
} from '@/api/types'

export type DialogState =
  | 'loading'
  | 'showing_qr'
  | 'scanned'
  | 'saving'
  | 'done'
  | 'expired'
  | 'failed'

export interface UseBiliQRCodeLoginOptions {
  // 用 getter 而非 Ref:props 经 toRef 转换较繁琐,getter 更贴合调用方(props.visible 直接读)
  visible: () => boolean
  channelId: () => string
  defaultUsage: () => QRCodeCookieUsage
  mode: () => 'channel' | 'account'
  /** session 创建成功后回调(组件在此渲染二维码到 canvas) */
  onSessionReady: (url: string) => void | Promise<void>
  /** 保存主播成功(主播模式) */
  onSaved?: (channel: Channel) => void
  /** 保存账号成功(账号模式) */
  onSavedAccount?: (account: BiliCookieAccount) => void
  /** 关闭弹窗 */
  onClose?: () => void
}

export function useBiliQRCodeLogin(options: UseBiliQRCodeLoginOptions) {
  const { visible, channelId, defaultUsage, mode, onSessionReady, onSaved, onSavedAccount, onClose } = options
  // mode/defaultUsage 用 getter:同实例动态切换时不会读到过期快照

  const session = ref<QRCodeSession | null>(null)
  const pollResult = ref<QRCodePollResult | null>(null)
  const state = ref<DialogState>('loading')
  const usage = ref<QRCodeCookieUsage>('download')
  const nickname = ref('')
  const creating = ref(false)
  const polling = ref(false)
  const saving = ref(false)

  const isAccountMode = computed(() => mode() === 'account')
  const canSave = computed(() => pollResult.value?.status === 'succeeded' && !saving.value)
  const statusText = computed(() => {
    if (state.value === 'loading') return '正在创建二维码'
    if (state.value === 'saving') return '正在保存 Cookie'
    if (state.value === 'done') return 'Cookie 已保存'
    if (state.value === 'expired') return '二维码已过期'
    if (state.value === 'failed') return pollResult.value?.message || '登录失败'
    return pollResult.value?.message || '等待扫码'
  })

  // 复用 usePolling(2s 间隔,不立即执行——startLogin 已先手动 poll 一次)
  const { start: startPollTimer, stop: stopPollTimer } = usePolling(() => poll(), {
    interval: 2000,
    immediate: false,
  })

  watch(visible, (v) => {
    if (v) void startLogin()
    else void cleanupSession()
  })

  async function poll(): Promise<void> {
    if (!session.value || polling.value || !visible()) return
    if (new Date(session.value.expires_at).getTime() <= Date.now()) {
      state.value = 'expired'
      stopPollTimer()
      return
    }
    polling.value = true
    try {
      const result = await pollQRCodeSession(session.value.session_id)
      if (!visible()) return
      pollResult.value = result
      applyStatus(result.status)
    } catch {
      state.value = 'failed'
      stopPollTimer()
    } finally {
      polling.value = false
    }
  }

  function applyStatus(status: QRCodeLoginStatus): void {
    if (status === 'pending') {
      state.value = 'showing_qr'
      return
    }
    if (status === 'scanned') {
      state.value = 'scanned'
      return
    }
    if (status === 'succeeded') {
      state.value = 'showing_qr'
      stopPollTimer()
      return
    }
    if (status === 'expired') {
      state.value = 'expired'
      stopPollTimer()
      return
    }
    state.value = 'failed'
    stopPollTimer()
  }

  function reset(): void {
    stopPollTimer()
    session.value = null
    pollResult.value = null
    state.value = 'loading'
    creating.value = false
    polling.value = false
    saving.value = false
  }

  async function startLogin(): Promise<void> {
    reset()
    creating.value = true
    state.value = 'loading'
    usage.value = defaultUsage()
    nickname.value = ''
    try {
      session.value = await createQRCodeSession()
      state.value = 'showing_qr'
      await onSessionReady(session.value.url)
      await poll()
      startPollTimer()
    } catch {
      state.value = 'failed'
    } finally {
      creating.value = false
    }
  }

  async function handleSave(): Promise<void> {
    if (!session.value || !canSave.value) return

    // 账号模式
    if (isAccountMode.value) {
      saving.value = true
      state.value = 'saving'
      try {
        const account = await saveQRCodeToAccount(session.value.session_id, nickname.value || undefined)
        session.value = null
        state.value = 'done'
        HMessage.success('账号已保存')
        onSavedAccount?.(account)
        onClose?.()
      } catch {
        state.value = 'failed'
      } finally {
        saving.value = false
      }
      return
    }

    // 主播模式
    if (!channelId()) return
    saving.value = true
    state.value = 'saving'
    try {
      const response = await saveQRCodeSession(session.value.session_id, channelId(), usage.value)
      session.value = null
      state.value = 'done'
      HMessage.success('Cookie 已保存')
      onSaved?.(response.channel)
      onClose?.()
    } catch {
      state.value = 'failed'
    } finally {
      saving.value = false
    }
  }

  async function cleanupSession(): Promise<void> {
    stopPollTimer()
    const current = session.value
    session.value = null
    if (current && state.value !== 'done') {
      try {
        await cancelQRCodeSession(current.session_id)
      } catch {
        // 关闭弹窗时取消会话是尽力而为。
      }
    }
  }

  function close(): void {
    onClose?.()
  }

  return {
    // 状态
    session,
    pollResult,
    state,
    usage,
    nickname,
    creating,
    saving,
    // computed
    isAccountMode,
    canSave,
    statusText,
    // actions
    startLogin,
    reset,
    poll,
    handleSave,
    cleanupSession,
    close,
  }
}
