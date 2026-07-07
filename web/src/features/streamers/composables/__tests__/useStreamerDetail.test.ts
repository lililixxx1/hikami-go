// web/src/features/streamers/composables/__tests__/useStreamerDetail.test.ts
import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { useStreamerDetail } from '../useStreamerDetail'

// mock api
vi.mock('@/api/channels', () => ({
  updateChannel: vi.fn().mockResolvedValue({}),
  deleteChannel: vi.fn().mockResolvedValue(undefined),
}))

describe('useStreamerDetail', () => {
  it('cookieStatus returns ok when channel has cookie_file', () => {
    const channel = ref({ id: 'c1', cookie_file: '/x.cookie', download_cookie_file: '' })
    const { cookieStatus } = useStreamerDetail(channel)
    expect(cookieStatus.value).toBe('ok')
  })
  it('cookieStatus returns unknown when runtime not loaded', () => {
    const channel = ref({ id: 'c1', cookie_file: '', download_cookie_file: '' })
    const { cookieStatus } = useStreamerDetail(channel, ref(null))
    expect(cookieStatus.value).toBe('unknown')
  })
})
