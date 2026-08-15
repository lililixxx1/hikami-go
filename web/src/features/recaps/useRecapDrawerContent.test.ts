// web/src/features/recaps/useRecapDrawerContent.test.ts
// L10:openRecap 竞态——快速切换场次时,旧场次的迟到响应不得覆盖新场次内容。
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'

vi.mock('@/api/sessions', () => ({
  getRecapContent: vi.fn(),
}))

import { getRecapContent } from '@/api/sessions'
import { useRecapDrawerContent } from './useRecapDrawerContent'
import type { Session } from '@/api/types-derived'

function mockSession(id: string): Session {
  return { id } as unknown as Session
}

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => { resolve = r })
  return { promise, resolve }
}

describe('useRecapDrawerContent', () => {
  beforeEach(() => {
    vi.mocked(getRecapContent).mockReset()
  })

  it('open 加载当前场次内容并复位 loading', async () => {
    vi.mocked(getRecapContent).mockResolvedValueOnce({ markdown: 'A' } as any)
    const selectedSession = ref<Session | null>(null)
    const { content, loading, addedTerms, open } = useRecapDrawerContent(selectedSession)

    expect(loading.value).toBe(false)
    await open(mockSession('a'))
    expect((content.value as any)?.markdown).toBe('A')
    expect(loading.value).toBe(false)
    expect(addedTerms.value.size).toBe(0)
  })

  it('旧场次的迟到响应被丢弃,不覆盖新场次内容(L10 竞态)', async () => {
    const first = deferred<any>()
    const second = deferred<any>()
    vi.mocked(getRecapContent)
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)

    const selectedSession = ref<Session | null>(null)
    const { content, loading, open } = useRecapDrawerContent(selectedSession)

    // 连续打开 a → b(a 的响应后到)
    const openA = open(mockSession('a'))
    const openB = open(mockSession('b'))

    second.resolve({ markdown: 'B' })
    await openB
    expect((content.value as any)?.markdown).toBe('B')
    expect(loading.value).toBe(false)

    // a 的响应此时才返回:必须被丢弃
    first.resolve({ markdown: 'A-late' })
    await openA
    expect((content.value as any)?.markdown).toBe('B')
    expect(loading.value).toBe(false)
  })

  it('旧场次的迟到失败不清空新场次内容', async () => {
    const first = deferred<any>()
    vi.mocked(getRecapContent)
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce({ markdown: 'B' } as any)

    const selectedSession = ref<Session | null>(null)
    const { content, open } = useRecapDrawerContent(selectedSession)

    const openA = open(mockSession('a'))
    const openB = open(mockSession('b'))
    await openB

    first.resolve(Promise.reject(new Error('late failure')))
    await openA
    expect((content.value as any)?.markdown).toBe('B')
  })

  it('open 失败时 content 为 null 且 loading 复位', async () => {
    vi.mocked(getRecapContent).mockRejectedValueOnce(new Error('network'))
    const selectedSession = ref<Session | null>(null)
    const { content, loading, open } = useRecapDrawerContent(selectedSession)

    await open(mockSession('a'))
    expect(content.value).toBeNull()
    expect(loading.value).toBe(false)
  })

  it('refresh 仅作用于当前场次(切换后不覆盖)', async () => {
    const first = deferred<any>()
    vi.mocked(getRecapContent)
      .mockResolvedValueOnce({ markdown: 'A' } as any)
      .mockReturnValueOnce(first.promise)

    const selectedSession = ref<Session | null>(null)
    const { content, refresh, open } = useRecapDrawerContent(selectedSession)
    await open(mockSession('a'))

    // a 的 refresh 在飞期间切换到 b
    const refreshing = refresh('a')
    selectedSession.value = mockSession('b')

    first.resolve({ markdown: 'A-fresh' })
    await refreshing
    expect((content.value as any)?.markdown).toBe('A') // 未覆盖
  })

  it('refresh 对非当前场次直接跳过', async () => {
    const selectedSession = ref<Session | null>(mockSession('b'))
    const { refresh } = useRecapDrawerContent(selectedSession)

    await refresh('a')
    expect(getRecapContent).not.toHaveBeenCalled()
  })
})
