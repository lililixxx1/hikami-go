// web/src/features/streamers/components/__tests__/ChannelPublishConfig.test.ts
// ChannelPublishConfig.vue 的单元测试(2026-07-20 新增)。
import { mount } from '@vue/test-utils'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import ChannelPublishConfig from '../ChannelPublishConfig.vue'
import type { Channel, BiliCookieAccount, BiliSeries } from '@/api/types-derived'

// ---- mock API ----
const listBiliAccountsMock = vi.fn()
const listBiliSeriesMock = vi.fn()
vi.mock('@/api/bili', () => ({
  listBiliAccounts: (...args: unknown[]) => listBiliAccountsMock(...args),
}))
vi.mock('@/api/settings', () => ({
  listBiliSeries: (...args: unknown[]) => listBiliSeriesMock(...args),
}))

const accounts: BiliCookieAccount[] = [
  { id: 1, uid: 100, nickname: 'acct-a', cookie_file: '/tmp/a.cookie', is_default_download: false, is_default_publish: false, created_at: '', updated_at: '' },
  { id: 2, uid: 200, nickname: 'acct-b', cookie_file: '/tmp/b.cookie', is_default_download: false, is_default_publish: true, created_at: '', updated_at: '' },
]
const seriesList: BiliSeries[] = [
  { id: 10, name: '文集1', articles_count: 3 },
  { id: 11, name: '文集2', articles_count: 7 },
]

function makeChannel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: 'ch1', name: 'test', uid: 1, live_room_id: 0,
    replay_source_url: '', space_url: '', title_prefix: '',
    cookie_file: '', download_cookie_file: '',
    enabled: true, auto_record: false, auto_asr: false, auto_recap: false,
    record_danmaku: false, source_mode: 'both', discover_limit: 0,
    publish_enabled: false, publish_mode: '', publish_category_id: 0,
    publish_list_id: -1, publish_private_pub: 0, publish_original: -1,
    auto_publish: false, publish_aigc: -1, publish_timer_pub_time: 0,
    publish_cover_url: '', publish_topics: '', recap_model: '',
    max_continuations: -1, created_at: '', updated_at: '',
    ...overrides,
  } as Channel
}

function mountConfig(channel: Channel = makeChannel(), updating = false) {
  return mount(ChannelPublishConfig, {
    props: { channel, updating },
    global: {
      stubs: {
        // HSelect/HInput/HButton 用真组件但简化(避免引入整个 UI 库的副作用)
        // 注意:Vue runtime template 编译不支持 TS `as` 语法,这里用 JS 强转
        HSelect: { template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option></select>', props: ['modelValue', 'options', 'disabled'] },
        HInput: { template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />', props: ['modelValue', 'size', 'placeholder'] },
        HButton: { template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>', props: ['disabled', 'variant', 'size', 'loading'] },
      },
    },
  })
}

describe('ChannelPublishConfig', () => {
  beforeEach(() => {
    listBiliAccountsMock.mockReset()
    listBiliSeriesMock.mockReset()
    listBiliAccountsMock.mockResolvedValue(accounts)
    listBiliSeriesMock.mockResolvedValue({ items: seriesList })
  })

  it('renders 6 字段控件 + 保存按钮', () => {
    const wrapper = mountConfig()
    // 6 个 select(账号/文集/模式/可见范围/原创/AIGC)+ 1 个 input(封面)+ 1 个按钮
    const selects = wrapper.findAll('select')
    const inputs = wrapper.findAll('input')
    const buttons = wrapper.findAll('button')
    expect(selects.length).toBe(6)
    expect(inputs.length).toBe(1)
    expect(buttons.length).toBe(1)
  })

  it('打开组件时同步 channel 字段到 draft', async () => {
    // 注:list_id 的 select.value 受 HSelect options 限制(series 未加载时
    // seriesOptions 仅含 -1/0),无法直接断言 select.value='11'。改用「保存」
    // 后 emit 的 diff 验证 draft 已正确同步(见保存测试)。
    // 本测试验证除 list_id 外的字段都被 channel 同步。
    const ch = makeChannel({
      publish_account_id: 2,
      publish_mode: 'publish',
      publish_private_pub: 1,
      publish_original: 1,
      publish_aigc: 1,
      publish_cover_url: 'https://example.com/cover.png',
    })
    const wrapper = mountConfig(ch)
    // 等待 watch immediate + accounts 加载完
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    const selects = wrapper.findAll('select')
    expect(selects.length).toBe(6)
    // account(0)=2 / mode(2)=publish / private_pub(3)=1 / original(4)=1 / aigc(5)=1
    expect((selects[0].element as HTMLSelectElement).value).toBe('2')
    expect((selects[2].element as HTMLSelectElement).value).toBe('publish')
    expect((selects[3].element as HTMLSelectElement).value).toBe('1')
    expect((selects[4].element as HTMLSelectElement).value).toBe('1')
    expect((selects[5].element as HTMLSelectElement).value).toBe('1')
    // 封面 input
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('https://example.com/cover.png')
  })

  it('挂载时调 listBiliAccounts 拉账号列表', () => {
    mountConfig()
    expect(listBiliAccountsMock).toHaveBeenCalledTimes(1)
  })

  it('展开文集下拉(loadSeriesList)时按 channel.id 调 listBiliSeries', async () => {
    const wrapper = mountConfig(makeChannel({ id: 'ch1' }))
    // 点第一个 select(文集)触发 click → loadSeriesList
    const selects = wrapper.findAll('select')
    await selects[1].trigger('click')
    expect(listBiliSeriesMock).toHaveBeenCalledWith('ch1')
  })

  it('listBiliSeries 返回 error 时 seriesError 反映', async () => {
    listBiliSeriesMock.mockResolvedValue({ items: [], error: '未设置默认发布账号' })
    const wrapper = mountConfig(makeChannel({ id: 'ch1' }))
    await wrapper.findAll('select')[1].trigger('click')
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('未设置默认发布账号')
  })

  it('保存按钮仅 emit 变化字段(单字段变化)', async () => {
    const ch = makeChannel({ publish_list_id: -1, publish_mode: '', publish_private_pub: 0, publish_original: -1, publish_aigc: -1, publish_cover_url: '' })
    const wrapper = mountConfig(ch)
    await wrapper.vm.$nextTick()
    // 先触发文集 select click 让 series 加载,options 含 '10' 才能 setValue
    const selects = wrapper.findAll('select')
    await selects[1].trigger('click')
    await wrapper.vm.$nextTick()
    // 改 list_id 从 -1 → 10
    await selects[1].setValue('10')
    await wrapper.vm.$nextTick()
    // 保存按钮启用 + emit 仅含 publish_list_id
    const saveBtn = wrapper.find('button')
    expect((saveBtn.element as HTMLButtonElement).disabled).toBe(false)
    await saveBtn.trigger('click')
    const emit = wrapper.emitted('save-publish')
    expect(emit).toBeTruthy()
    expect(emit?.[0]?.[0]).toEqual({ publish_list_id: 10 })
  })

  it('无变化时不 emit', async () => {
    const wrapper = mountConfig()
    await wrapper.vm.$nextTick()
    const saveBtn = wrapper.find('button')
    expect((saveBtn.element as HTMLButtonElement).disabled).toBe(true)
    await saveBtn.trigger('click')
    expect(wrapper.emitted('save-publish')).toBeFalsy()
  })

  it('全字段混合保存:多字段变化一次性 emit', async () => {
    const ch = makeChannel()
    const wrapper = mountConfig(ch)
    await wrapper.vm.$nextTick()
    const selects = wrapper.findAll('select')
    await selects[0].setValue('1')   // account = 1(null → 1)
    await selects[2].setValue('draft') // mode = draft
    await selects[5].setValue('1')    // aigc = 1
    await wrapper.find('input').setValue('https://x.com/cover.png')
    await wrapper.vm.$nextTick()
    await wrapper.find('button').trigger('click')
    const emit = wrapper.emitted('save-publish')
    expect(emit).toBeTruthy()
    expect(emit?.[0]?.[0]).toEqual({
      publish_account_id: 1,
      publish_mode: 'draft',
      publish_aigc: 1,
      publish_cover_url: 'https://x.com/cover.png',
    })
  })

  it('HSelect emit string → draft number 转换正确(关键回归保护)', async () => {
    const ch = makeChannel({ publish_original: -1 })
    const wrapper = mountConfig(ch)
    await wrapper.vm.$nextTick()
    const selects = wrapper.findAll('select')
    // original 字段位置 4,HSelect emit string '1'
    await selects[4].setValue('1')
    await wrapper.vm.$nextTick()
    await wrapper.find('button').trigger('click')
    const emit = wrapper.emitted('save-publish')
    expect(emit?.[0]?.[0]).toEqual({ publish_original: 1 }) // 不是字符串 '1'
  })

  it('账号代理空串 → null(不是 0)', async () => {
    const ch = makeChannel({ publish_account_id: 2 }) // 当前有账号
    const wrapper = mountConfig(ch)
    await wrapper.vm.$nextTick()
    const selects = wrapper.findAll('select')
    // 改回 '' = 跟随全局(null)
    await selects[0].setValue('')
    await wrapper.vm.$nextTick()
    await wrapper.find('button').trigger('click')
    const emit = wrapper.emitted('save-publish')
    expect(emit?.[0]?.[0]).toEqual({ publish_account_id: null }) // 不是 0
  })

  it('updating=true 时保存按钮禁用', () => {
    const wrapper = mountConfig(makeChannel(), true)
    const saveBtn = wrapper.find('button')
    expect((saveBtn.element as HTMLButtonElement).disabled).toBe(true)
  })

  it('codex r18 HIGH:draft 账号 ≠ channel 持久化账号时,展开文集提示先保存', async () => {
    // channel 持久化的 publish_account_id = 1,但 draft 改为 2(未保存)
    const ch = makeChannel({ id: 'ch1', publish_account_id: 1 })
    const wrapper = mountConfig(ch)
    await wrapper.vm.$nextTick()
    const selects = wrapper.findAll('select')
    // 改账号 1 → 2(应该触发清空 seriesLoadedForChannelId)
    await selects[0].setValue('2')
    await wrapper.vm.$nextTick()
    // 触发文集 click
    await selects[1].trigger('click')
    await wrapper.vm.$nextTick()
    // 不调 listBiliSeries(因 draft≠持久化)
    expect(listBiliSeriesMock).not.toHaveBeenCalled()
    // 用户看到提示
    expect(wrapper.text()).toContain('未保存')
  })
})
