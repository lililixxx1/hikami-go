// web/src/features/recaps/components/__tests__/SessionTableV10.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import SessionTableV10 from '../SessionTableV10.vue'
import type { Session, Task, Capabilities } from '@/api/types-derived'

const caps: Capabilities = {
  replay_download: true, asr_submit: true, asr_model: '', asr_request_mode: '',
  recap_generate: true, webdav_upload: true, publish_opus: true, reason: '',
}
const sessions: Session[] = [
  { id: 's1', slug: 's1', channel_id: 'c1', channel_name: 'Alice', source_type: 'live_record', source_id: '', title: 'T1', started_at: '', ended_at: '', source_url: '', status: 'asr_done', current_task_id: 't1', last_error: '', local_available: true, uploaded_at: '', published_at: '', archived_at: '', publish_target: '', created_at: '2026-07-07T10:00:00+08:00', updated_at: '' },
]
const tasks: Task[] = [
  { id: 't1', channel_id: 'c1', session_id: 's1', type: 'asr', status: 'running', payload: '{}', progress: 75, message: '', error: '', attempt: 1, created_at: '', updated_at: '' },
]

describe('SessionTableV10', () => {
  it('renders channel_name from session', () => {
    const wrapper = mount(SessionTableV10, {
      props: { sessions, tasks, capabilities: caps, channels: [], actionLoadingId: '', currentPage: 1, pageSize: 20 },
    })
    expect(wrapper.text()).toContain('Alice')
  })
  it('shows progress bar from matching task', () => {
    const wrapper = mount(SessionTableV10, {
      props: { sessions, tasks, capabilities: caps, channels: [], actionLoadingId: '', currentPage: 1, pageSize: 20 },
    })
    const fill = wrapper.find('.progress-bar-fill')
    expect(fill.attributes('style')).toContain('width: 75%')
  })
  it('emits open-recap on row click', async () => {
    const wrapper = mount(SessionTableV10, {
      props: { sessions, tasks, capabilities: caps, channels: [], actionLoadingId: '', currentPage: 1, pageSize: 20 },
    })
    await wrapper.find('tbody tr').trigger('click')
    expect(wrapper.emitted('open-recap')?.[0]).toEqual([sessions[0]])
  })
})
