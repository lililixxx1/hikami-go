import { ref, onUnmounted } from 'vue'
import mitt from 'mitt'
import type { TaskProgressEvent } from '@/api/types-derived'
import { useAdminToken } from '@/composables/useAdminToken'

type Events = {
  task_progress: TaskProgressEvent
  connected: void
  disconnected: void
}

const emitter = mitt<Events>()

export function useEventBus() {
  return emitter
}

export function useWebSocket(url?: string) {
  const connected = ref(false)
  let ws: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let heartbeatTimer: ReturnType<typeof setTimeout> | null = null
  let lastMessageTime = 0
  let reconnectDelay = 1000
  const maxReconnectDelay = 30000
  let disposed = false

  const wsUrl = url || `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/ws`

  // H1(2026-08-15 全项目审核):服务端 /ws 要求 admin token。浏览器 WebSocket API
  // 无法自定义 header → token 经 query 传递;必须在每次 connect() 时重读——
  // AdminTokenCardV10 存在运行期设置/清除 token 的入口,若在初始化时一次算好,
  // 运行期改 token 不会反映到 URL,会导致 401 重连死循环。
  const { token } = useAdminToken()

  function connect(): void {
    if (disposed) return
    if (ws && (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN)) {
      return
    }

    const urlWithToken = token.value
      ? `${wsUrl}?token=${encodeURIComponent(token.value)}`
      : wsUrl
    ws = new WebSocket(urlWithToken)

    ws.onopen = () => {
      connected.value = true
      reconnectDelay = 1000
      lastMessageTime = Date.now()
      startHeartbeatCheck()
      emitter.emit('connected')
    }

    ws.onmessage = (event) => {
      lastMessageTime = Date.now()
      try {
        const data = JSON.parse(event.data) as TaskProgressEvent
        if (data.type === 'task_progress') {
          emitter.emit('task_progress', data)
        }
      } catch {
        // ignore non-JSON messages
      }
    }

    ws.onclose = () => {
      connected.value = false
      stopHeartbeatCheck()
      emitter.emit('disconnected')
      scheduleReconnect()
    }

    ws.onerror = () => {
      ws?.close()
    }
  }

  function scheduleReconnect(): void {
    if (disposed) return
    reconnectTimer = setTimeout(() => {
      connect()
    }, reconnectDelay)
    reconnectDelay = Math.min(reconnectDelay * 1.5, maxReconnectDelay)
  }

  function startHeartbeatCheck(): void {
    stopHeartbeatCheck()
    heartbeatTimer = setInterval(() => {
      if (Date.now() - lastMessageTime > 30000) {
        ws?.close()
      }
    }, 5000)
  }

  function stopHeartbeatCheck(): void {
    if (heartbeatTimer !== null) {
      clearInterval(heartbeatTimer)
      heartbeatTimer = null
    }
  }

  function disconnect(): void {
    disposed = true
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    stopHeartbeatCheck()
    ws?.close()
    ws = null
  }

  onUnmounted(() => {
    disconnect()
  })

  return { connected, connect, disconnect }
}
