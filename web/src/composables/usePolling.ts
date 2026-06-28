import { ref, onUnmounted } from 'vue'

export interface UsePollingOptions {
  interval?: number
  immediate?: boolean
}

export function usePolling(
  callback: () => Promise<void>,
  options: UsePollingOptions = {},
) {
  const { interval = 5000, immediate = true } = options
  const active = ref(false)
  let timer: ReturnType<typeof setInterval> | null = null
  let disposed = false

  async function tick(): Promise<void> {
    if (disposed) return
    try {
      await callback()
    } catch {
      // errors handled by callback / API layer
    }
  }

  function start(): void {
    if (active.value || disposed) return
    active.value = true
    if (immediate) {
      tick()
    }
    timer = setInterval(tick, interval)
  }

  function stop(): void {
    active.value = false
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
  }

  onUnmounted(() => {
    disposed = true
    stop()
  })

  return { active, start, stop }
}
