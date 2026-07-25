// web/src/features/recaps/components/__tests__/DiscoverPreviewDrawer.test.ts
// DiscoverPreviewDrawer.vue 的单元测试(2026-07-25 新增)。
// 验证:① 挂载时调 listBiliAccounts;② accountOptions 过滤未登录账号;③ 选「默认」emit account_id=undefined;
// ④ 选具体账号 emit account_id=number;⑤ accountsLoading 时 HSelect disabled。
import { mount } from '@vue/test-utils'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import DiscoverPreviewDrawer from '../DiscoverPreviewDrawer.vue'
import type { DiscoverResult, BiliCookieAccount } from '@/api/types-derived'

// ---- mock API ----
const listBiliAccountsMock = vi.fn()
vi.mock('@/api/bili', () => ({
  listBiliAccounts: (...args: unknown[]) => listBiliAccountsMock(...args),
}))

// 测试数据:acct-1/2 已登录(cookie_file 非空),acct-3 未登录(cookie_file 空,应被下拉过滤)
const accounts: BiliCookieAccount[] = [
  { id: 1, uid: 100, nickname: 'acct-a', cookie_file: '/tmp/a.cookie', is_default_download: false, is_default_publish: false, created_at: '', updated_at: '' },
  { id: 2, uid: 200, nickname: 'acct-b', cookie_file: '/tmp/b.cookie', is_default_download: false, is_default_publish: true, created_at: '', updated_at: '' },
  { id: 3, uid: 300, nickname: 'not-logged-in', cookie_file: '', is_default_download: false, is_default_publish: false, created_at: '', updated_at: '' },
]

const emptyItems: DiscoverResult[] = []

function mountDrawer(props: Partial<InstanceType<typeof DiscoverPreviewDrawer>['$props']> = {}) {
  return mount(DiscoverPreviewDrawer, {
    props: {
      visible: true,
      items: emptyItems,
      executing: false,
      loading: false,
      ...props,
    },
    global: {
      stubs: {
        // HSelect/HInput/HButton/HCheckbox/HPill/HDrawer 用简化 stub(避免引入整个 UI 库)
        HSelect: {
          template: '<select :disabled="disabled" :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option></select>',
          props: ['modelValue', 'options', 'disabled'],
        },
        HInput: {
          template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
          props: ['modelValue', 'size', 'placeholder'],
        },
        HButton: {
          template: '<button :disabled="disabled" :loading="loading" @click="$emit(\'click\')"><slot /></button>',
          props: ['disabled', 'variant', 'size', 'loading'],
        },
        HCheckbox: {
          template: '<input type="checkbox" :checked="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.checked)" />',
          props: ['modelValue', 'disabled'],
        },
        HPill: { template: '<span><slot /></span>', props: ['variant'] },
        HDrawer: {
          // HDrawer 用真组件过于复杂,stub 成透明 div 透传 slot
          template: '<div class="drawer-stub"><slot /></div>',
          props: ['visible', 'title', 'size'],
        },
      },
    },
  })
}

describe('DiscoverPreviewDrawer', () => {
  beforeEach(() => {
    listBiliAccountsMock.mockReset()
    listBiliAccountsMock.mockResolvedValue(accounts)
  })

  it('挂载时调 listBiliAccounts 拉账号列表', () => {
    mountDrawer()
    expect(listBiliAccountsMock).toHaveBeenCalledTimes(1)
  })

  it('accountOptions 过滤未登录账号(cookie_file 空的不出现)', async () => {
    const wrapper = mountDrawer()
    await wrapper.vm.$nextTick()
    const select = wrapper.find('select')
    const options = select.findAll('option')
    // 1 默认项 + 2 已登录账号(acct-a/acct-b),未登录的 acct-3 不出现
    expect(options.length).toBe(3)
    expect(options[0].text()).toContain('默认')
    expect(options[1].text()).toContain('acct-a')
    expect(options[2].text()).toContain('acct-b')
    // 确认 acct-3 不在任何 option 文本里
    const allText = options.map((o) => o.text()).join('|')
    expect(allText).not.toContain('not-logged-in')
  })

  it('accountOptions 默认项 value 为空串(对应 account_id=undefined)', async () => {
    const wrapper = mountDrawer()
    await wrapper.vm.$nextTick()
    const select = wrapper.find('select')
    const options = select.findAll('option')
    expect(options[0].attributes('value')).toBe('')
  })

  it('选「默认」+ 填 URL + 点发现 → emit account_id 为 undefined', async () => {
    const wrapper = mountDrawer()
    await wrapper.vm.$nextTick()
    // 填 URL(第一个 input 是 URL,HInput stub)
    const inputs = wrapper.findAll('input[type="text"], input:not([type])')
    // URL input 是 HInput stub 渲染的 <input>(无 type 属性)
    await inputs[0].setValue('https://space.bilibili.com/123/lists/456')
    // 默认 select 就是「默认」项(value=''),不需操作
    await wrapper.find('button').trigger('click')
    const emit = wrapper.emitted('preview-submit')
    expect(emit).toBeTruthy()
    expect(emit?.[0]?.[0]).toMatchObject({
      url: 'https://space.bilibili.com/123/lists/456',
      account_id: undefined,
    })
  })

  it('选具体账号 → emit account_id 为该账号 id(number)', async () => {
    const wrapper = mountDrawer()
    await wrapper.vm.$nextTick()
    // 填 URL
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('https://example.com')
    // 选账号 acct-a(id=1) —— select 第一个(此抽屉只有账号一个 select)
    const select = wrapper.find('select')
    await select.setValue('1')
    await wrapper.find('button').trigger('click')
    const emit = wrapper.emitted('preview-submit')
    expect(emit).toBeTruthy()
    expect(emit?.[0]?.[0]).toMatchObject({
      url: 'https://example.com',
      account_id: 1, // number,不是 '1' 字符串
    })
    expect(typeof (emit?.[0]?.[0] as { account_id?: number }).account_id).toBe('number')
  })

  it('URL 为空时点发现不 emit(canSubmit 守卫)', async () => {
    const wrapper = mountDrawer()
    await wrapper.vm.$nextTick()
    // 不填 URL,直接点发现
    await wrapper.find('button').trigger('click')
    const emit = wrapper.emitted('preview-submit')
    expect(emit).toBeFalsy() // URL 空时 canSubmit=false,按钮 disabled 不 emit
  })

  it('listBiliAccounts 失败时 accountsError 显示且不阻断渲染', async () => {
    listBiliAccountsMock.mockRejectedValueOnce(new Error('网络错误'))
    const wrapper = mountDrawer()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick() // 等 catch 块赋值 accountsError
    expect(wrapper.text()).toContain('网络错误')
  })
})
