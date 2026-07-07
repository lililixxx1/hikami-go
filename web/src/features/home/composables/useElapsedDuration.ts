// web/src/features/home/composables/useElapsedDuration.ts
import { computed, onUnmounted, ref } from 'vue'

export function useElapsedDuration(getStartedAt: () => string, tickMs = 1000) {
  const now = ref(Date.now())
  const timer = setInterval(() => { now.value = Date.now() }, tickMs)
  onUnmounted(() => clearInterval(timer))

  const text = computed(() => {
    const start = new Date(getStartedAt() || '').getTime()
    if (!start || Number.isNaN(start)) return '-'
    const sec = Math.max(0, Math.floor((now.value - start) / 1000))
    const h = String(Math.floor(sec / 3600)).padStart(2, '0')
    const m = String(Math.floor((sec % 3600) / 60)).padStart(2, '0')
    return `${h}:${m}`
  })

  return { text }
}
