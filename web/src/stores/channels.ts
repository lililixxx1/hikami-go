import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import type { Channel, UpsertChannelInput } from '@/api/types'
import { listChannels, createChannel, updateChannel, deleteChannel as apiDelete } from '@/api/channels'
import { HMessage } from '@/components/ui/message'

export const useChannelsStore = defineStore('channels', () => {
  const items = ref<Channel[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  // 并发去重:与 sessions 一致,多入口同时进 ensureLoaded 复用同一请求
  let inflight: Promise<void> | null = null

  // 按 id 索引(派生自 items,供 query 消费者 O(1) 查找)
  const byId = computed(() => {
    const map = new Map<string, Channel>()
    for (const c of items.value) map.set(c.id, c)
    return map
  })

  async function fetchChannels(): Promise<void> {
    loading.value = true
    try {
      const response = await listChannels()
      items.value = response.items
      loaded.value = true
    } finally {
      loading.value = false
    }
  }

  // 按需加载:已加载直接返回;有在飞请求复用;否则发一次。解决 ?id 竞态 + 防并发重复 list
  async function ensureLoaded(): Promise<void> {
    if (loaded.value) return
    if (inflight) return inflight
    inflight = fetchChannels().finally(() => {
      inflight = null
    })
    return inflight
  }

  // query 消费专用:确保列表加载完毕后按 id 取 channel(?id 路由参数消费)
  async function getByIdAfterLoad(id: string): Promise<Channel | undefined> {
    await ensureLoaded()
    return byId.value.get(id)
  }

  async function create(input: UpsertChannelInput): Promise<Channel | null> {
    try {
      const channel = await createChannel(input)
      items.value.push(channel)
      HMessage.success('主播创建成功')
      return channel
    } catch {
      return null
    }
  }

  async function update(id: string, input: UpsertChannelInput): Promise<Channel | null> {
    try {
      const channel = await updateChannel(id, input)
      const index = items.value.findIndex((c) => c.id === id)
      if (index !== -1) {
        items.value[index] = channel
      }
      HMessage.success('主播更新成功')
      return channel
    } catch {
      return null
    }
  }

  async function remove(id: string): Promise<boolean> {
    try {
      await apiDelete(id)
      items.value = items.value.filter((c) => c.id !== id)
      HMessage.success('主播删除成功')
      return true
    } catch {
      return false
    }
  }

  return { items, loading, loaded, byId, fetchChannels, ensureLoaded, getByIdAfterLoad, create, update, remove }
})
