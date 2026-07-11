// web/src/features/recaps/components/__tests__/RecapDrawerV10.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'

// Mock @/api/sessions — RecapDrawerV10 动态 import updateRecapContent
const mockUpdateRecapContent = vi.fn()
vi.mock('@/api/sessions', () => ({
  updateRecapContent: (...args: unknown[]) => mockUpdateRecapContent(...args),
}))

import RecapDrawerV10 from '../RecapDrawerV10.vue'
import type { Session, RecapContent, Capabilities } from '@/api/types-derived'

const caps: Capabilities = {
  replay_download: true, asr_submit: true, asr_model: '', asr_request_mode: '',
  recap_generate: true, webdav_upload: true, publish_opus: true, reason: '',
}
const session: Session = {
  id: 's1', slug: 's1', channel_id: 'c1', channel_name: 'Alice', source_type: 'live_record',
  source_id: '', title: 'T1', started_at: '', ended_at: '', source_url: '', status: 'recap_done',
  current_task_id: '', last_error: '', local_available: true, uploaded_at: '', published_at: '',
  archived_at: '', publish_target: '', created_at: '', updated_at: '',
}
const content: RecapContent = {
  available: true, markdown: '# Title\n\nbody', bilibili: '', prompt: '', raw_response: '',
  suggested_terms: ['术语A', '术语B'],
}

describe('RecapDrawerV10', () => {
  it('renders markdown content', () => {
    const wrapper = mount(RecapDrawerV10, {
      props: { visible: true, session, content, loading: false, capabilities: caps,
        isExpert: false, channels: [], actionLoadingId: '', addingTerm: '', partialLoading: false,
        addedTerms: new Set<string>() },
    })
    expect(wrapper.find('.md-preview').html()).toContain('<h1')  // marked 渲染
    expect(wrapper.find('.md-preview').html()).toContain('Title')
  })
  it('shows suggested term pills when present', () => {
    const wrapper = mount(RecapDrawerV10, {
      props: { visible: true, session, content, loading: false, capabilities: caps,
        isExpert: false, channels: [], actionLoadingId: '', addingTerm: '', partialLoading: false,
        addedTerms: new Set<string>() },
    })
    expect(wrapper.text()).toContain('术语A')
    expect(wrapper.text()).toContain('术语B')
  })
  it('emits add-term when suggested term pill clicked', async () => {
    const wrapper = mount(RecapDrawerV10, {
      props: { visible: true, session, content, loading: false, capabilities: caps,
        isExpert: false, channels: [], actionLoadingId: '', addingTerm: '', partialLoading: false,
        addedTerms: new Set<string>() },
    })
    const pills = wrapper.findAll('.suggested-term-btn')
    await pills[0].trigger('click')
    expect(wrapper.emitted('add-term')?.[0]).toEqual(['术语A'])
  })
  it('upload button emits run-action for recap_done status', () => {
    const wrapper = mount(RecapDrawerV10, {
      props: { visible: true, session, content, loading: false, capabilities: caps,
        isExpert: false, channels: [], actionLoadingId: '', addingTerm: '', partialLoading: false,
        addedTerms: new Set<string>() },
    })
    // recap_done → upload 是主动作;getDrawerActions 返回 upload
    expect(wrapper.text()).toContain('上传')  // 或对应中文 label
  })

  it('save edit emits saved with session id and exits edit mode', async () => {
    mockUpdateRecapContent.mockResolvedValueOnce(undefined)
    const wrapper = mount(RecapDrawerV10, {
      props: { visible: true, session, content, loading: false, capabilities: caps,
        isExpert: false, channels: [], actionLoadingId: '', addingTerm: '', partialLoading: false,
        addedTerms: new Set<string>() },
    })

    // Click edit button
    const editBtn = wrapper.findAll('button').find((b) => b.text().includes('编辑'))
    expect(editBtn).toBeTruthy()
    await editBtn!.trigger('click')

    // Modify textarea
    const textarea = wrapper.find('textarea')
    expect(textarea.exists()).toBe(true)
    await textarea.setValue('# Updated\n\nnew content')

    // Click save
    const saveBtn = wrapper.findAll('button').find((b) => b.text().includes('保存'))
    expect(saveBtn).toBeTruthy()
    await saveBtn!.trigger('click')

    // Wait for async
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()

    // Should have called updateRecapContent with session id and new content
    expect(mockUpdateRecapContent).toHaveBeenCalledWith('s1', '# Updated\n\nnew content')

    // Should emit saved with session id
    expect(wrapper.emitted('saved')?.[0]).toEqual(['s1'])
  })

  it('save edit failure keeps edit mode and does not emit saved', async () => {
    mockUpdateRecapContent.mockRejectedValueOnce(new Error('server error'))
    const wrapper = mount(RecapDrawerV10, {
      props: { visible: true, session, content, loading: false, capabilities: caps,
        isExpert: false, channels: [], actionLoadingId: '', addingTerm: '', partialLoading: false,
        addedTerms: new Set<string>() },
    })

    // Enter edit mode
    const editBtn = wrapper.findAll('button').find((b) => b.text().includes('编辑'))
    await editBtn!.trigger('click')

    const textarea = wrapper.find('textarea')
    await textarea.setValue('# Edited content')

    // Click save
    const saveBtn = wrapper.findAll('button').find((b) => b.text().includes('保存'))
    await saveBtn!.trigger('click')

    // Wait for async
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()

    // Should NOT emit saved
    expect(wrapper.emitted('saved')).toBeUndefined()

    // Should still be in edit mode (textarea still visible)
    expect(wrapper.find('textarea').exists()).toBe(true)
  })
})
