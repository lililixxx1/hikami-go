import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { RuntimeStatus } from '@/api/types-derived'
import { getRuntimeStatus } from '@/api/health'

export const useRuntimeStore = defineStore('runtime', () => {
  const status = ref<RuntimeStatus | null>(null)
  const loading = ref(false)
  let lastFetchAt = 0

  async function fetchRuntime(force = false): Promise<void> {
    if (force) {
      lastFetchAt = 0
    }
    if (Date.now() - lastFetchAt < 30000) return
    loading.value = true
    try {
      status.value = await getRuntimeStatus()
      lastFetchAt = Date.now()
    } finally {
      loading.value = false
    }
  }

  return { status, loading, fetchRuntime }
})
