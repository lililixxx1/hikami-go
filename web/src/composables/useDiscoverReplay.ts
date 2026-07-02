/**
 * useDiscoverReplay — 「发现回放」抽屉的可复用状态。
 *
 * 抽屉自管理 preview/execute 调用，所以这里只保留最小状态：
 *  - drawerVisible：抽屉开关（view 的按钮控制打开）
 *  - openDiscover()：打开抽屉（按钮 @click 绑定）
 *  - onExecuted()：抽屉执行完成后回调，刷新 sessions 列表
 *
 * RecapsView 和 HomeView 原各自重复实现 handleDiscover（含 loading/result 状态），
 * 改版后抽屉接管了那些职责，两个 view 只需用本 composable 统一管理可见性 + 刷新。
 */
import { ref } from 'vue'
import { useSessionsStore } from '@/stores/sessions'
import { useTasksStore } from '@/stores/tasks'

export function useDiscoverReplay() {
  const drawerVisible = ref(false)

  function openDiscover(): void {
    drawerVisible.value = true
  }

  async function onExecuted(): Promise<void> {
    const sessionsStore = useSessionsStore()
    const tasksStore = useTasksStore()
    await Promise.all([sessionsStore.fetchSessions(), tasksStore.fetchTasks()])
  }

  return { drawerVisible, openDiscover, onExecuted }
}
