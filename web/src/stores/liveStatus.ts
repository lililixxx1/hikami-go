import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { LiveStatus } from '@/api/types'
import { getAllLiveStatus } from '@/api/live'

export const useLiveStatusStore = defineStore('liveStatus', () => {
  const statusMap = ref<Record<string, LiveStatus>>({})
  const loading = ref(false)

  async function fetchAll(): Promise<void> {
    loading.value = true
    try {
      const response = await getAllLiveStatus()
      const map: Record<string, LiveStatus> = {}
      for (const status of response.items) {
        map[status.channel_id] = status
      }
      statusMap.value = map
    } finally {
      loading.value = false
    }
  }

  function getStatus(channelId: string): LiveStatus | undefined {
    return statusMap.value[channelId]
  }

  return { statusMap, loading, fetchAll, getStatus }
})
