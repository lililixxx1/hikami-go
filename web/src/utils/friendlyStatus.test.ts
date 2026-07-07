import { describe, it, expect } from 'vitest'
import { getFriendlySessionStatus, statusGroupMap } from './friendlyStatus'
import type { Session } from '@/api/types-derived'

function makeSession(status: string): Session {
  return {
    id: 'test',
    channel_id: 'ch1',
    slug: 'test-slug',
    source_id: 'BV1test',
    source_type: 'replay_download',
    status,
    title: 'Test',
    started_at: '2026-06-04T00:00:00Z',
    created_at: '2026-06-04T00:00:00Z',
    updated_at: '2026-06-04T00:00:00Z',
  } as Session
}

describe('getFriendlySessionStatus', () => {
  it('returns warning for discovered', () => {
    const result = getFriendlySessionStatus(makeSession('discovered'))
    expect(result.color).toBe('warning')
    expect(result.progress).toBe(15)
  })

  it('returns warning with action for media_ready', () => {
    const result = getFriendlySessionStatus(makeSession('media_ready'))
    expect(result.color).toBe('warning')
    expect(result.progress).toBe(30)
    expect(result.action).toBe('开始转写')
  })

  it('returns warning for asr_submitted', () => {
    const result = getFriendlySessionStatus(makeSession('asr_submitted'))
    expect(result.progress).toBe(45)
  })

  it('returns warning with action for asr_done', () => {
    const result = getFriendlySessionStatus(makeSession('asr_done'))
    expect(result.action).toBe('生成回顾')
    expect(result.progress).toBe(60)
  })

  it('returns success for recap_done', () => {
    const result = getFriendlySessionStatus(makeSession('recap_done'))
    expect(result.color).toBe('success')
    expect(result.progress).toBe(75)
    expect(result.action).toBe('阅读回顾')
  })

  it('returns success for uploaded', () => {
    const result = getFriendlySessionStatus(makeSession('uploaded'))
    expect(result.color).toBe('success')
    expect(result.progress).toBe(90)
  })

  it('returns success with progress 100 for published', () => {
    const result = getFriendlySessionStatus(makeSession('published'))
    expect(result.color).toBe('success')
    expect(result.progress).toBe(100)
  })

  it('returns danger for failed', () => {
    const result = getFriendlySessionStatus(makeSession('failed'))
    expect(result.color).toBe('danger')
    expect(result.label).toBe('处理失败')
    expect(result.action).toBe('重试')
  })

  it('returns info for unknown status', () => {
    const result = getFriendlySessionStatus(makeSession('unknown_xyz'))
    expect(result.color).toBe('info')
    expect(result.label).toBe('未知状态')
  })
})

describe('statusGroupMap', () => {
  it('processing group includes source-to-asr statuses', () => {
    expect(statusGroupMap.processing).toContain('discovered')
    expect(statusGroupMap.processing).toContain('media_ready')
    expect(statusGroupMap.processing).toContain('asr_done')
  })

  it('recap group includes recap_done', () => {
    expect(statusGroupMap.recap).toEqual(['recap_done'])
  })

  it('published group includes uploaded and published', () => {
    expect(statusGroupMap.published).toContain('uploaded')
    expect(statusGroupMap.published).toContain('published')
  })

  it('failed group includes failed', () => {
    expect(statusGroupMap.failed).toEqual(['failed'])
  })
})
