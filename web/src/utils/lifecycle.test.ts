import { describe, it, expect } from 'vitest'
import {
  getNextAction,
  getActionMeta,
  getDisabledReason,
  LIFECYCLE_STEPS,
  type SessionActionName,
} from './lifecycle'
import type { Capabilities } from '@/api/types-derived'

const allCapabilities: Capabilities = {
  replay_download: true,
  asr_submit: true,
  asr_model: 'sensevoice',
  asr_request_mode: 'json',
  recap_generate: true,
  webdav_upload: true,
  publish_opus: true,
  reason: '',
}

describe('getNextAction', () => {
  it('maps recording to stop_record', () => {
    const action = getNextAction('recording', allCapabilities)
    expect(action?.name).toBe('stop_record')
    expect(action?.label).toBe('停止录制')
    expect(action?.destructive).toBe(true)
  })

  it('maps media_ready to submit_asr', () => {
    const action = getNextAction('media_ready', allCapabilities)
    expect(action?.name).toBe('submit_asr')
    expect(action?.disabled).toBe(false)
  })

  it('maps asr_done to generate_recap', () => {
    const action = getNextAction('asr_done', allCapabilities)
    expect(action?.name).toBe('generate_recap')
  })

  it('maps recap_done to upload', () => {
    const action = getNextAction('recap_done', allCapabilities)
    expect(action?.name).toBe('upload')
  })

  it('maps uploaded to publish', () => {
    const action = getNextAction('uploaded', allCapabilities)
    expect(action?.name).toBe('publish')
  })

  it('maps published to fetch', () => {
    const action = getNextAction('published', allCapabilities)
    expect(action?.name).toBe('fetch')
    // fetch(取回)需要 WebDAV 能力,故 capability 为 webdav_upload(非 undefined)
    expect(action?.capability).toBe('webdav_upload')
  })

  it('returns null for unknown status', () => {
    expect(getNextAction('unknown_status', allCapabilities)).toBeNull()
  })

  it('returns null for discovered status', () => {
    expect(getNextAction('discovered', allCapabilities)).toBeNull()
  })

  it('disables action when capability is false', () => {
    const caps: Capabilities = { ...allCapabilities, asr_submit: false }
    const action = getNextAction('media_ready', caps)
    expect(action?.name).toBe('submit_asr')
    expect(action?.disabled).toBe(true)
    expect(action?.disabledReason).toContain('ASR')
  })

  it('disables action when capabilities is null', () => {
    const action = getNextAction('media_ready', null)
    expect(action?.name).toBe('submit_asr')
    expect(action?.disabled).toBe(true)
    expect(action?.disabledReason).toContain('运行时能力')
  })

  it('disables fetch when capabilities is null (fetch needs webdav_upload)', () => {
    const action = getNextAction('published', null)
    expect(action?.name).toBe('fetch')
    // fetch 需要 webdav_upload 能力, capabilities=null 时禁用
    expect(action?.disabled).toBe(true)
    expect(action?.disabledReason).toContain('运行时能力')
  })
})

describe('getDisabledReason', () => {
  it('returns empty string for action without capability', () => {
    expect(getDisabledReason({ name: 'fetch', capability: undefined }, allCapabilities)).toBe('')
  })

  it('returns empty string when capability is true', () => {
    expect(getDisabledReason({ name: 'submit_asr', capability: 'asr_submit' }, allCapabilities)).toBe('')
  })

  it('returns reason when capability is false', () => {
    const caps: Capabilities = { ...allCapabilities, recap_generate: false }
    const reason = getDisabledReason({ name: 'generate_recap', capability: 'recap_generate' }, caps)
    expect(reason).toContain('回顾')
  })

  it('returns fallback reason when capabilities is null', () => {
    const reason = getDisabledReason({ name: 'submit_asr', capability: 'asr_submit' }, null)
    expect(reason).toContain('运行时能力')
  })
})

describe('getActionMeta', () => {
  it('returns meta for submit_asr', () => {
    const meta = getActionMeta('submit_asr' as SessionActionName)
    expect(meta.label).toBe('提交 ASR')
    expect(meta.endpoint).toContain('asr')
  })

  it('returns meta for publish', () => {
    const meta = getActionMeta('publish' as SessionActionName)
    expect(meta.label).toBe('发布')
    expect(meta.capability).toBe('publish_opus')
  })
})

describe('LIFECYCLE_STEPS', () => {
  it('has 6 steps', () => {
    expect(LIFECYCLE_STEPS).toHaveLength(6)
  })

  it('has correct step keys in order', () => {
    const keys = LIFECYCLE_STEPS.map((s) => s.key)
    expect(keys).toEqual(['source', 'media', 'asr', 'recap', 'upload', 'publish'])
  })
})
