// web/src/features/recaps/useRecapDrawerContent.ts
import { ref, type Ref } from 'vue'
import { getRecapContent } from '@/api/sessions'
import type { RecapContent, Session } from '@/api/types-derived'

// L10(2026-08-15):openRecap 拉取回顾内容是异步的,快速连续打开两个场次时,
// 先发出的旧响应可能后到,覆盖新场次的内容/loading/onRecapSaved 早已有的
// id 双重守卫在 openRecap 缺失。统一收敛到这里:每次请求返回后先确认
// selectedSession 仍是发请求时的场次,不匹配则丢弃响应。
// getRecapContent 返回旧手写类型,边界处窄化为派生类型(与 RecapsView 口径一致)。
export function useRecapDrawerContent(selectedSession: Ref<Session | null>) {
  const content = ref<RecapContent | null>(null)
  const loading = ref(false)
  // 抽屉内术语「已添加」标记:API 成功后才写入(避免失败时按钮误显示已添加)
  const addedTerms = ref<Set<string>>(new Set())

  function isCurrent(id: string): boolean {
    return selectedSession.value?.id === id
  }

  // 打开场次抽屉:重置展示态并拉取回顾内容(带竞态守卫)。
  async function open(session: Session): Promise<void> {
    selectedSession.value = session
    content.value = null
    addedTerms.value = new Set()
    loading.value = true
    try {
      const fresh = (await getRecapContent(session.id)) as unknown as RecapContent
      if (isCurrent(session.id)) content.value = fresh
    } catch {
      if (isCurrent(session.id)) content.value = null
    } finally {
      if (isCurrent(session.id)) loading.value = false
    }
  }

  // 编辑保存后刷新预览(原 RecapsView.onRecapSaved 迁移):同一套 id 双重守卫。
  async function refresh(sessionId: string): Promise<void> {
    if (!selectedSession.value || selectedSession.value.id !== sessionId) return
    loading.value = true
    try {
      const fresh = (await getRecapContent(sessionId)) as unknown as RecapContent
      // 请求返回后再次检查：若期间用户切换了 session，不覆盖新 session 的内容
      if (isCurrent(sessionId)) content.value = fresh
    } catch {
      // 刷新失败由 client.ts 拦截器提示；保持旧内容
    } finally {
      // 只在仍是同一 session 时清除 loading（切换后的 session 有自己的 loading 生命周期）
      if (isCurrent(sessionId)) loading.value = false
    }
  }

  return { content, loading, addedTerms, open, refresh }
}
