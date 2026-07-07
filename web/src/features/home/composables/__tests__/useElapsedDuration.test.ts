// web/src/features/home/composables/__tests__/useElapsedDuration.test.ts
import { describe, it, expect, vi } from 'vitest'
import { useElapsedDuration } from '../useElapsedDuration'

describe('useElapsedDuration', () => {
  it('returns "-" for empty startedAt', () => {
    const { text } = useElapsedDuration(() => '')
    expect(text.value).toBe('-')
  })
  it('computes HH:MM from startedAt', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-07T10:30:00+08:00'))
    const { text } = useElapsedDuration(() => '2026-07-07T08:00:00+08:00')
    expect(text.value).toBe('02:30')
    vi.useRealTimers()
  })
})
