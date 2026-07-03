import { describe, it, expect } from 'vitest'
import {
  getRowActions,
  getDrawerActions,
  canFetchLocal,
  decideRetry,
  isRetryable,
  retryHint,
  primaryActionType,
  UI_ACTION_REASON,
  type UIActionName,
} from './sessionActions'
import type { Session, Task, Capabilities } from '@/api/types'

// ---------- fixtures ----------

const allCaps: Capabilities = {
  replay_download: true,
  asr_submit: true,
  asr_model: 'sensevoice',
  asr_request_mode: 'json',
  recap_generate: true,
  webdav_upload: true,
  publish_opus: true,
  reason: '',
}

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 's1',
    slug: 's1',
    channel_id: 'c1',
    source_type: 'replay',
    source_id: 'x',
    title: 't',
    started_at: '',
    ended_at: '',
    source_url: '',
    status: 'media_ready',
    current_task_id: '',
    last_error: '',
    local_available: true,
    uploaded_at: '',
    published_at: '',
    archived_at: '',
    publish_target: '',
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

function makeFailedTask(id = 't1'): Task {
  return {
    id,
    channel_id: 'c1',
    session_id: 's1',
    type: 'asr',
    status: 'failed',
    payload: '',
    progress: 0,
    message: '',
    error: 'boom',
    attempt: 1,
    started_at: '',
    finished_at: '',
    created_at: '',
    updated_at: '',
  }
}

// ---------- 列表行(表A): recap_done 只读阅读,不显示 upload ----------

describe('getRowActions 表A — recap_done', () => {
  it('recap_done → read=true,无 primary/upload(与抽屉差异点①)', () => {
    const a = getRowActions(makeSession({ status: 'recap_done' }), allCaps)
    expect(a.read).toBe(true)
    expect(a.primary).toBeUndefined()
    expect(a.retry).toBeUndefined()
  })

  it('recap_done 且本地已清理 → read + fetch 并存', () => {
    const a = getRowActions(makeSession({ status: 'recap_done', local_available: false }), allCaps)
    expect(a.read).toBe(true)
    expect(a.fetch).toBeDefined()
    expect(a.fetch?.name).toBe('fetch')
  })
})

// ---------- 列表行(表A): published 无状态推进型动作(B站专栏只能手动管理) ----------

describe('getRowActions 表A — published', () => {
  it('published → 无状态推进主动作(专栏删除能力已移除,重新生成在抽屉硬编码按钮)', () => {
    const a = getRowActions(makeSession({ status: 'published', publish_target: '{"dyn_id":"d1"}' }), allCaps)
    expect(a.primary).toBeUndefined()
  })

  it('published 且本地已清理 → 仅 fetch(无 edit/remove)', () => {
    const a = getRowActions(
      makeSession({ status: 'published', publish_target: 'd1', local_available: false }),
      allCaps,
    )
    expect(a.fetch).toBeDefined()
  })
})

// ---------- 列表行(表A): failed 的 retry(§7.1 五边界) ----------

describe('getRowActions 表A — failed retry 边界', () => {
  it('failed + 有 failed 任务 → retry', () => {
    const task = makeFailedTask()
    const a = getRowActions(makeSession({ status: 'failed', current_task_id: 't1' }), allCaps, task)
    expect(a.retry).toBeDefined()
    expect(a.retry?.name).toBe('retry')
  })

  it('failed + 无 current_task_id → 无 retry(无任务可重试)', () => {
    const a = getRowActions(makeSession({ status: 'failed', current_task_id: '' }), allCaps, null)
    expect(a.retry).toBeUndefined()
  })

  it('failed + 任务不在 store(task_missing) → 无 retry', () => {
    const a = getRowActions(makeSession({ status: 'failed', current_task_id: 't1' }), allCaps, null)
    expect(a.retry).toBeUndefined()
  })

  it('failed + 任务非 failed(已成功) → 无 retry', () => {
    const task = { ...makeFailedTask(), status: 'succeeded' as const }
    const a = getRowActions(makeSession({ status: 'failed', current_task_id: 't1' }), allCaps, task)
    expect(a.retry).toBeUndefined()
  })

  it('failed + currentTask 未传(undefined) → 无 retry', () => {
    const a = getRowActions(makeSession({ status: 'failed', current_task_id: 't1' }), allCaps)
    expect(a.retry).toBeUndefined()
  })
})

// ---------- 列表行(表A): 主动作 ----------

describe('getRowActions 表A — 主动作', () => {
  it('media_ready → submit_asr', () => {
    const a = getRowActions(makeSession({ status: 'media_ready' }), allCaps)
    expect(a.primary?.name).toBe('submit_asr')
  })

  it('asr_done → generate_recap', () => {
    const a = getRowActions(makeSession({ status: 'asr_done' }), allCaps)
    expect(a.primary?.name).toBe('generate_recap')
  })

  it('uploaded → publish', () => {
    const a = getRowActions(makeSession({ status: 'uploaded' }), allCaps)
    expect(a.primary?.name).toBe('publish')
  })

  it('generate_recap 本地已清理 → 主动作禁用,reason「本地已清理」(走 lifecycle LOCAL_REQUIRED)', () => {
    const a = getRowActions(makeSession({ status: 'asr_done', local_available: false }), allCaps)
    expect(a.primary?.disabled).toBe(true)
    expect(a.primary?.disabledReason).toContain('本地已清理')
    // 本地已清理同时应给 fetch 独立按钮
    expect(a.fetch).toBeDefined()
  })

  it('能力为 false → 主动作禁用 + reason', () => {
    const caps = { ...allCaps, asr_submit: false }
    const a = getRowActions(makeSession({ status: 'media_ready' }), caps)
    expect(a.primary?.disabled).toBe(true)
    expect(a.primary?.disabledReason).toContain('ASR')
  })

  it('capabilities=null → 主动作禁用(运行时能力未加载)', () => {
    const a = getRowActions(makeSession({ status: 'media_ready' }), null)
    expect(a.primary?.disabled).toBe(true)
    expect(a.primary?.disabledReason).toContain('运行时能力')
  })

  it('discovered 等处理中状态 → 无主动作', () => {
    const a = getRowActions(makeSession({ status: 'discovered' }), allCaps)
    expect(a.primary).toBeUndefined()
  })
})

// ---------- 抽屉(表B): 仅主动作,recap_done→upload ----------

describe('getDrawerActions 表B', () => {
  it('recap_done → upload(与列表行 read 的差异点①)', () => {
    const a = getDrawerActions(makeSession({ status: 'recap_done' }), allCaps)
    expect(a.primary?.name).toBe('upload')
  })

  it('uploaded → publish', () => {
    const a = getDrawerActions(makeSession({ status: 'uploaded' }), allCaps)
    expect(a.primary?.name).toBe('publish')
  })

  it('published → 无主动作(专栏只能手动去 B站管理;重新生成在抽屉硬编码按钮)', () => {
    const a = getDrawerActions(makeSession({ status: 'published', publish_target: 'd1' }), allCaps)
    expect(a.primary).toBeUndefined()
  })

  it('failed → 无动作(列表行有 retry,抽屉无)', () => {
    const a = getDrawerActions(makeSession({ status: 'failed' }), allCaps)
    expect(a.primary).toBeUndefined()
  })

  it('media_ready → submit_asr(与列表行一致)', () => {
    const a = getDrawerActions(makeSession({ status: 'media_ready' }), allCaps)
    expect(a.primary?.name).toBe('submit_asr')
  })

  it('本地已清理的 generate_recap → 禁用但不附加 fetch(抽屉无独立取回)', () => {
    const a = getDrawerActions(makeSession({ status: 'asr_done', local_available: false }), allCaps)
    expect(a.primary?.disabled).toBe(true)
    expect((a as unknown as { fetch?: unknown }).fetch).toBeUndefined()
  })
})

// ---------- retry 决策与文案细化(吸收 codex 建议) ----------

describe('decideRetry / retryHint 文案细化', () => {
  it('no_task_id', () => {
    expect(decideRetry(makeSession({ current_task_id: '' }), null)).toBe('no_task_id')
    expect(retryHint('no_task_id')).toBe('无任务可重试')
  })

  it('task_missing', () => {
    expect(decideRetry(makeSession({ current_task_id: 't1' }), null)).toBe('task_missing')
    expect(retryHint('task_missing')).toBe('任务已过期')
  })

  it('task_not_failed', () => {
    const t = { ...makeFailedTask(), status: 'succeeded' as const }
    expect(decideRetry(makeSession({ current_task_id: 't1' }), t)).toBe('task_not_failed')
    expect(retryHint('task_not_failed')).toBe('')
  })

  it('retryable', () => {
    expect(decideRetry(makeSession({ current_task_id: 't1' }), makeFailedTask())).toBe('retryable')
    expect(isRetryable(makeSession({ current_task_id: 't1' }), makeFailedTask())).toBe(true)
    expect(retryHint('retryable')).toBe('')
  })
})

// ---------- canFetchLocal + 配色 + 文案表 ----------

describe('canFetchLocal / primaryActionType / UI_ACTION_REASON', () => {
  it('canFetchLocal: 仅 local_available=false 为 true', () => {
    expect(canFetchLocal(makeSession({ local_available: false }))).toBe(true)
    expect(canFetchLocal(makeSession({ local_available: true }))).toBe(false)
  })

  it('primaryActionType 配色', () => {
    expect(primaryActionType('upload')).toBe('success')
    expect(primaryActionType('publish')).toBe('warning')
    expect(primaryActionType('submit_asr')).toBe('primary')
    expect(primaryActionType('generate_recap')).toBe('primary')
  })

  it('UI_ACTION_REASON 覆盖全部 6 个 UIActionName', () => {
    const all: UIActionName[] = [
      'submit_asr',
      'generate_recap',
      'upload',
      'publish',
      'fetch',
      'retry',
    ]
    for (const name of all) {
      expect(typeof UI_ACTION_REASON[name]).toBe('string')
    }
  })
})

// ---------- 矩阵补充(回应 codex 阶段2 审核:补齐 status × local × caps × target 组合) ----------

describe('getRowActions 表A — 处理中状态无按钮', () => {
  it('recording → 无任何动作(直播中,属首页)', () => {
    const a = getRowActions(makeSession({ status: 'recording' }), allCaps)
    expect(a.read).toBeUndefined()
    expect(a.primary).toBeUndefined()
    expect(a.retry).toBeUndefined()
  })

  it('discovered/downloading/importing/asr_submitted → 无主动作', () => {
    for (const status of ['discovered', 'downloading', 'importing', 'asr_submitted']) {
      const a = getRowActions(makeSession({ status }), allCaps)
      expect(a.primary).toBeUndefined()
    }
  })
})

describe('getRowActions 表A — 叠加规则(local_available 与各动作并存)', () => {
  it('uploaded + 本地已清理 → publish 主动作禁用 + fetch 独立按钮', () => {
    const a = getRowActions(makeSession({ status: 'uploaded', local_available: false }), allCaps)
    expect(a.primary?.name).toBe('publish')
    expect(a.primary?.disabled).toBe(true)
    expect(a.primary?.disabledReason).toContain('本地已清理')
    expect(a.fetch).toBeDefined()
  })

  it('published + 本地已清理 → 仅 fetch(published 无状态推进型动作)', () => {
    const a = getRowActions(
      makeSession({ status: 'published', publish_target: '', local_available: false }),
      allCaps,
    )
    expect(a.fetch).toBeDefined()
  })

  it('media_ready + 本地已清理 → submit_asr 不受影响(非 LOCAL_REQUIRED) + fetch', () => {
    const a = getRowActions(makeSession({ status: 'media_ready', local_available: false }), allCaps)
    expect(a.primary?.name).toBe('submit_asr')
    expect(a.primary?.disabled).toBe(false)
    expect(a.fetch).toBeDefined()
  })
})

describe('getDrawerActions 表B — 能力缺失', () => {
  it('asr_done 能力为 false → generate_recap 禁用', () => {
    const caps = { ...allCaps, recap_generate: false }
    const a = getDrawerActions(makeSession({ status: 'asr_done' }), caps)
    expect(a.primary?.name).toBe('generate_recap')
    expect(a.primary?.disabled).toBe(true)
    expect(a.primary?.disabledReason).toContain('回顾')
  })

  it('media_ready capabilities=null → submit_asr 禁用(运行时能力未加载)', () => {
    const a = getDrawerActions(makeSession({ status: 'media_ready' }), null)
    expect(a.primary?.name).toBe('submit_asr')
    expect(a.primary?.disabled).toBe(true)
    expect(a.primary?.disabledReason).toContain('运行时能力')
  })

  it('discovered 等处理中状态 → 抽屉无主动作', () => {
    for (const status of ['discovered', 'recording', 'asr_submitted']) {
      const a = getDrawerActions(makeSession({ status }), allCaps)
      expect(a.primary).toBeUndefined()
    }
  })
})

describe('buildPrimaryAction 仅返回主动作', () => {
  it('uploaded → publish,name 属 4 个主动作之一(非 fetch/retry)', () => {
    const a = getRowActions(makeSession({ status: 'uploaded', source_type: 'live_record' }), allCaps)
    expect(a.primary?.name).toBe('publish')
    expect(['submit_asr', 'generate_recap', 'upload', 'publish']).toContain(a.primary?.name)
  })
})

// ---------- 回放类(download/import)不发布B站:动作守卫 ----------

describe('回放类来源隐藏B站发布相关动作', () => {
  // 回放类两种 source_type(download=回放发现/链接下载,import=手动上传)
  const replayTypes: Session['source_type'][] = ['download', 'import']

  describe.each(replayTypes)('source_type=%s 行动作(表A)', (st) => {
    it('uploaded → 不显示 publish 主动作', () => {
      const a = getRowActions(makeSession({ status: 'uploaded', source_type: st }), allCaps)
      expect(a.primary).toBeUndefined()
    })

    it('published + publish_target(历史已发布) → 不显示 edit/remove', () => {
      const a = getRowActions(
        makeSession({ status: 'published', source_type: st, publish_target: 'opus-123' }),
        allCaps,
      )
      expect(a.primary).toBeUndefined()
    })

    it('recap_done → 仍有 upload 归档(归档不受影响)', () => {
      // 列表行 recap_done 走 read=true(打开抽屉),upload 在抽屉入口;这里验证抽屉
      const d = getDrawerActions(makeSession({ status: 'recap_done', source_type: st }), allCaps)
      expect(d.primary?.name).toBe('upload')
    })

    it('uploaded 抽屉(表B) → 不显示 publish', () => {
      const d = getDrawerActions(makeSession({ status: 'uploaded', source_type: st }), allCaps)
      expect(d.primary).toBeUndefined()
    })
  })

  // 回归:录播(live_record)动作矩阵不受影响
  describe('录播 live_record 动作不变(回归)', () => {
    it('uploaded → publish 主动作保留', () => {
      const a = getRowActions(makeSession({ status: 'uploaded', source_type: 'live_record' }), allCaps)
      expect(a.primary?.name).toBe('publish')
    })

    it('published + publish_target → 无 edit/remove(专栏能力已移除,两类一致)', () => {
      const a = getRowActions(
        makeSession({ status: 'published', source_type: 'live_record', publish_target: 'opus-456' }),
        allCaps,
      )
      expect(a.primary).toBeUndefined()
    })
  })
})

