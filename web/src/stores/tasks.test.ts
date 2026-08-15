// web/src/stores/tasks.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'

// Mock the API before importing the store
vi.mock('@/api/tasks', () => ({
  listTasks: vi.fn(),
}))

import { listTasks } from '@/api/tasks'
import { setActivePinia, createPinia } from 'pinia'
import { useTasksStore } from './tasks'

function mockTask(id: string) {
  return { id, status: 'running', progress: 10 } as any
}

describe('tasks store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  // L8:轮询刷新与进度事件触发的 unknown-task 全量刷新并发时只发一次 list 请求
  it('fetchTasks deduplicates concurrent calls', async () => {
    let resolveList: (val: any) => void
    const pending = new Promise((resolve) => { resolveList = resolve })
    vi.mocked(listTasks).mockReturnValue(pending as any)

    const store = useTasksStore()

    const p1 = store.fetchTasks()
    const p2 = store.fetchTasks()

    expect(listTasks).toHaveBeenCalledTimes(1)

    resolveList!({ items: [mockTask('t1')] })
    await Promise.all([p1, p2])

    expect(store.items).toHaveLength(1)
    expect(store.loading).toBe(false)
  })

  it('fetchTasks allows refresh after completion (no dedup across completed calls)', async () => {
    vi.mocked(listTasks).mockResolvedValueOnce({ items: [mockTask('t1')] } as any)
    vi.mocked(listTasks).mockResolvedValueOnce({ items: [mockTask('t1'), mockTask('t2')] } as any)

    const store = useTasksStore()

    await store.fetchTasks()
    expect(store.items).toHaveLength(1)
    expect(listTasks).toHaveBeenCalledTimes(1)

    await store.fetchTasks()
    expect(store.items).toHaveLength(2)
    expect(listTasks).toHaveBeenCalledTimes(2)
  })

  it('fetchTasks recovers from rejection (inflight and loading reset)', async () => {
    vi.mocked(listTasks).mockRejectedValueOnce(new Error('network'))
    vi.mocked(listTasks).mockResolvedValueOnce({ items: [mockTask('t1')] } as any)

    const store = useTasksStore()

    await expect(store.fetchTasks()).rejects.toThrow('network')
    expect(store.loading).toBe(false)

    // Should be able to retry
    await store.fetchTasks()
    expect(store.items).toHaveLength(1)
  })
})
