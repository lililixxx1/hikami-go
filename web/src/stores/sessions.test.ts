// web/src/stores/sessions.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'

// Mock the API before importing the store
vi.mock('@/api/sessions', () => ({
  listSessions: vi.fn(),
}))

import { listSessions } from '@/api/sessions'
import { setActivePinia, createPinia } from 'pinia'
import { useSessionsStore } from './sessions'

function mockSession(id: string) {
  return { id, title: `Session ${id}`, status: 'media_ready', channel_id: 'test' } as any
}

describe('sessions store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('fetchSessions deduplicates concurrent calls', async () => {
    let resolveList: (val: any) => void
    const pending = new Promise((resolve) => { resolveList = resolve })
    vi.mocked(listSessions).mockReturnValue(pending as any)

    const store = useSessionsStore()

    // Two concurrent calls
    const p1 = store.fetchSessions()
    const p2 = store.fetchSessions()

    // Should only call listSessions once
    expect(listSessions).toHaveBeenCalledTimes(1)

    resolveList!({ items: [mockSession('s1')] })
    await Promise.all([p1, p2])

    expect(store.items).toHaveLength(1)
    expect(store.loaded).toBe(true)
  })

  it('fetchSessions allows refresh after completion (no dedup across completed calls)', async () => {
    vi.mocked(listSessions).mockResolvedValueOnce({ items: [mockSession('s1')] } as any)
    vi.mocked(listSessions).mockResolvedValueOnce({ items: [mockSession('s1'), mockSession('s2')] } as any)

    const store = useSessionsStore()

    await store.fetchSessions()
    expect(store.items).toHaveLength(1)
    expect(listSessions).toHaveBeenCalledTimes(1)

    // Second call should make a new request
    await store.fetchSessions()
    expect(store.items).toHaveLength(2)
    expect(listSessions).toHaveBeenCalledTimes(2)
  })

  it('ensureLoaded returns early if already loaded', async () => {
    vi.mocked(listSessions).mockResolvedValue({ items: [mockSession('s1')] } as any)

    const store = useSessionsStore()

    await store.ensureLoaded()
    expect(listSessions).toHaveBeenCalledTimes(1)

    // Second ensureLoaded should be no-op
    await store.ensureLoaded()
    expect(listSessions).toHaveBeenCalledTimes(1)
  })

  it('ensureLoaded shares inflight with concurrent fetchSessions', async () => {
    let resolveList: (val: any) => void
    const pending = new Promise((resolve) => { resolveList = resolve })
    vi.mocked(listSessions).mockReturnValue(pending as any)

    const store = useSessionsStore()

    // fetchSessions and ensureLoaded called concurrently
    const p1 = store.fetchSessions()
    const p2 = store.ensureLoaded()

    expect(listSessions).toHaveBeenCalledTimes(1)

    resolveList!({ items: [mockSession('s1')] })
    await Promise.all([p1, p2])

    expect(store.loaded).toBe(true)
  })

  it('forceRefresh waits for inflight then makes a new request', async () => {
    let resolveFirst: (val: any) => void
    const firstPending = new Promise((resolve) => { resolveFirst = resolve })
    vi.mocked(listSessions)
      .mockReturnValueOnce(firstPending as any)
      .mockResolvedValueOnce({ items: [mockSession('s2')] } as any)

    const store = useSessionsStore()

    // Start a regular fetch (simulating polling or onMounted)
    const p1 = store.fetchSessions()

    // While inflight, call forceRefresh (simulating post-delete refresh)
    const p2 = store.forceRefresh()

    // forceRefresh should NOT have called listSessions yet (waiting for inflight)
    expect(listSessions).toHaveBeenCalledTimes(1)

    // Resolve the first request (returns stale data)
    resolveFirst!({ items: [mockSession('s1')] })
    await p1

    // Now forceRefresh should make a second request
    await p2
    expect(listSessions).toHaveBeenCalledTimes(2)

    // Final items should be from the second (fresh) request
    expect(store.items).toHaveLength(1)
    expect(store.items[0].id).toBe('s2')
  })

  it('forceRefresh makes a new request when no inflight exists', async () => {
    vi.mocked(listSessions).mockResolvedValue({ items: [mockSession('s1')] } as any)

    const store = useSessionsStore()
    await store.forceRefresh()

    expect(listSessions).toHaveBeenCalledTimes(1)
    expect(store.loaded).toBe(true)
  })

  it('fetchSessions recovers from rejection (inflight and loading reset)', async () => {
    vi.mocked(listSessions).mockRejectedValueOnce(new Error('network'))
    vi.mocked(listSessions).mockResolvedValueOnce({ items: [mockSession('s1')] } as any)

    const store = useSessionsStore()

    await expect(store.fetchSessions()).rejects.toThrow('network')
    expect(store.loading).toBe(false)
    expect(store.loaded).toBe(false)

    // Should be able to retry
    await store.fetchSessions()
    expect(store.loaded).toBe(true)
    expect(store.items).toHaveLength(1)
  })

  it('forceRefresh handles inflight rejection gracefully', async () => {
    vi.mocked(listSessions).mockRejectedValueOnce(new Error('network'))
    vi.mocked(listSessions).mockResolvedValueOnce({ items: [mockSession('s1')] } as any)

    const store = useSessionsStore()

    // Start a fetch that will fail
    const p1 = store.fetchSessions()

    // While inflight, call forceRefresh
    const p2 = store.forceRefresh()

    // First request fails
    await expect(p1).rejects.toThrow('network')

    // forceRefresh should still make a new request and succeed
    await p2
    expect(store.loaded).toBe(true)
    expect(store.items).toHaveLength(1)
  })
})
