// PublishCardV10 的保存载荷回归测试。
// HSelect 的 DOM change 值恒为 string；发布 API 的三个下拉字段必须转回 number。
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { getPublishSpy, updatePublishSpy, searchTopicsSpy, listSeriesSpy, fetchRuntimeSpy } = vi.hoisted(() => ({
  getPublishSpy: vi.fn(),
  updatePublishSpy: vi.fn(),
  searchTopicsSpy: vi.fn(),
  listSeriesSpy: vi.fn(),
  fetchRuntimeSpy: vi.fn(),
}))

vi.mock('@/api/settings', () => ({
  getPublishConfig: getPublishSpy,
  updatePublishConfig: updatePublishSpy,
  searchBiliTopics: searchTopicsSpy,
  listBiliSeries: listSeriesSpy,
}))
vi.mock('@/stores/runtime', () => ({
  useRuntimeStore: () => ({ fetchRuntime: fetchRuntimeSpy }),
}))
vi.mock('@/components/ui/message', () => ({
  HMessage: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))

import PublishCardV10 from '../PublishCardV10.vue'

const initialConfig = {
  enabled: true,
  mode: 'draft' as const,
  category_id: 15,
  list_id: 0,
  private_pub: 2 as const,
  summary_len: 100,
  original: 0 as const,
  aigc: 1 as const,
  timer_pub_time: 0,
  cover_url: '',
  auto_cover: true,
  topics: '随一Suiii,虚拟主播,直播回顾',
  topic_id: 0,
  topic_name: '',
  close_comment: 0 as const,
  up_choose_comment: 0 as const,
}

describe('PublishCardV10', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    getPublishSpy.mockResolvedValue({ ...initialConfig })
    updatePublishSpy.mockImplementation(async (payload) => payload)
    searchTopicsSpy.mockResolvedValue({ items: [{ id: 2468, name: '虚拟主播切片' }] })
    listSeriesSpy.mockResolvedValue({ items: [{ id: 42, name: '五月回顾', articles_count: 3 }] })
    fetchRuntimeSpy.mockResolvedValue(undefined)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('操作数字下拉后保存仍发送 JSON number', async () => {
    const wrapper = mount(PublishCardV10)
    await flushPromises()

    // 展开高级设置；v-show 内容始终挂载，但模拟真实用户操作便于回归定位。
    await wrapper.find('.collapse-trigger').trigger('click')

    const topicInput = wrapper.find('input[placeholder="搜索话题(2 字以上)"]')
    await topicInput.setValue('虚拟主播')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()

    let selects = wrapper.findAll('select')
    // 顺序：发布模式、可见范围、话题、文集。
    await selects[1].setValue('1')
    await selects[2].setValue('2468')
    await selects[3].trigger('click')
    await flushPromises()
    selects = wrapper.findAll('select')
    await selects[3].setValue('42')

    const saveButton = wrapper.findAll('button').find(button => button.text().includes('保存设置'))!
    await saveButton.trigger('click')
    await flushPromises()

    expect(updatePublishSpy).toHaveBeenCalledTimes(1)
    const payload = updatePublishSpy.mock.calls[0][0]
    expect(payload).toMatchObject({ private_pub: 1, topic_id: 2468, list_id: 42 })
    expect(typeof payload.private_pub).toBe('number')
    expect(typeof payload.topic_id).toBe('number')
    expect(typeof payload.list_id).toBe('number')
  })
})
