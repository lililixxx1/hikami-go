/**
 * 应用刷新协调器(重构方案 §7.2)。
 *
 * 单一 owner 收口三件事(消除 WS 与轮询各刷各的双刷问题):
 *  1. WS 连接 + task_progress 订阅 → tasksStore 增量更新 + 终态刷 sessions
 *  2. WS 断线降级轮询(tasks + sessions),WS 重连后停轮询并立即全量拉回
 *  3. refreshTasks() 统一入口(供动作后/初载调用)
 *
 * 注意:liveStatus 轮询由 HomeView 自行管理(后端无 live WS 事件),coordinator 不接管。
 *
 * 必须在组件 setup 内调用(内部 useWebSocket/usePolling 依赖 onUnmounted)。
 * 在 AppLayout 挂载,生命周期等同于 app。
 */
import { ref } from 'vue'
import { useWebSocket, useEventBus } from '@/composables/useWebSocket'
import { usePolling } from '@/composables/usePolling'
import { useTasksStore } from '@/stores/tasks'
import { useSessionsStore } from '@/stores/sessions'
import type { TaskProgressEvent } from '@/api/types'

// 任务终态(到达后需刷新 sessions,因为 session 状态随任务推进变化)
const TERMINAL_STATUSES = ['succeeded', 'failed', 'cancelled']

export function useAppRefreshCoordinator() {
  const tasksStore = useTasksStore()
  const sessionsStore = useSessionsStore()
  const eventBus = useEventBus()

  const { connected, connect: wsConnect, disconnect: wsDisconnect } = useWebSocket()
  const degraded = ref(false)

  // 降级轮询(WS 断线时兜底):刷 tasks + sessions。10s 间隔(比 Home 30s 密,因 WS 断需更快感知恢复)。
  const { start: startFallbackPolling, stop: stopFallbackPolling } = usePolling(
    async () => {
      await Promise.all([tasksStore.fetchTasks(), sessionsStore.fetchSessions()])
    },
    { interval: 10000, immediate: true },
  )

  // task_progress 处理:增量更新 tasks + 终态刷 sessions
  function handleTaskProgress(event: TaskProgressEvent): void {
    tasksStore.handleTaskProgress(event)
    // 终态时 session 状态已变化(如 asr→asr_done),需刷 sessions 让列表同步
    if (TERMINAL_STATUSES.includes(event.status)) {
      void sessionsStore.fetchSessions()
    }
  }

  // WS 连接/断开 → 降级轮询切换
  function onConnected(): void {
    degraded.value = false
    stopFallbackPolling()
    // 重连后立即全量拉回断线期间丢失的 task 状态
    void tasksStore.fetchTasks()
  }

  function onDisconnected(): void {
    degraded.value = true
    startFallbackPolling()
  }

  function connect(): void {
    eventBus.on('task_progress', handleTaskProgress)
    eventBus.on('connected', onConnected)
    eventBus.on('disconnected', onDisconnected)
    wsConnect()
  }

  function disconnect(): void {
    eventBus.off('task_progress', handleTaskProgress)
    eventBus.off('connected', onConnected)
    eventBus.off('disconnected', onDisconnected)
    stopFallbackPolling()
    wsDisconnect()
  }

  // 显式全量刷新 tasks(供动作后/初载调用,替代散落的 fetchTasks)
  async function refreshTasks(): Promise<void> {
    await tasksStore.fetchTasks()
  }

  return {
    connected,
    degraded,
    connect,
    disconnect,
    refreshTasks,
  }
}
