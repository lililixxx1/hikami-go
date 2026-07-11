import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import type { Session } from '@/api/types-derived'
import { listSessions } from '@/api/sessions'

export const useSessionsStore = defineStore('sessions', () => {
  const items = ref<Session[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  // 并发去重:多个调用者(如 query 消费 watch + onMounted)同时进 ensureLoaded 时复用同一个 list 请求
  let inflight: Promise<void> | null = null

  // 按 id 索引(派生自 items,供 query 消费者 O(1) 查找)
  const byId = computed(() => {
    const map = new Map<string, Session>()
    for (const s of items.value) map.set(s.id, s)
    return map
  })

  // 强制刷新(动作后、首页轮询等显式需要最新列表的场景调用)。
  // 支持并发去重:多个调用者(如 onMounted + ?sid watch)同时调用时复用同一 list 请求。
  async function fetchSessions(): Promise<void> {
    if (inflight) return inflight
    return doFetch()
  }

  // forceRefresh 用于写操作（如清空失败场次）后的对账刷新：
  // 如果有在飞请求，先等它完成，再发一次新请求拿写操作后的最新快照。
  // 避免复用写操作前已发出的 inflight 请求（会拿到过期数据）。
  async function forceRefresh(): Promise<void> {
    if (inflight) await inflight.catch(() => {})
    return doFetch()
  }

  async function doFetch(): Promise<void> {
    loading.value = true
    inflight = (async () => {
      try {
        const response = await listSessions()
        items.value = response.items
        loaded.value = true
      } finally {
        loading.value = false
        inflight = null
      }
    })()
    return inflight
  }

  // 按需加载:已加载则直接返回;有在飞请求则复用;否则发起一次。解决 ?sid 竞态 + 防并发重复 list
  async function ensureLoaded(): Promise<void> {
    if (loaded.value) return
    return fetchSessions()
  }

  // query 消费专用:确保列表加载完毕后按 id 取 session(?sid 路由参数消费)
  async function getByIdAfterLoad(id: string): Promise<Session | undefined> {
    await ensureLoaded()
    return byId.value.get(id)
  }

  return { items, loading, loaded, byId, fetchSessions, forceRefresh, ensureLoaded, getByIdAfterLoad }
})
