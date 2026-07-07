// web/src/components/ui/__tests__/HEmpty.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HEmpty from '../HEmpty.vue'

describe('HEmpty', () => {
  it('shows default description', () => {
    const wrapper = mount(HEmpty)
    expect(wrapper.find('p').text()).toBe('暂无数据')
  })
  it('shows custom description', () => {
    const wrapper = mount(HEmpty, { props: { description: '无直播' } })
    expect(wrapper.find('p').text()).toBe('无直播')
  })
  it('renders svg icon', () => {
    const wrapper = mount(HEmpty)
    expect(wrapper.find('svg').exists()).toBe(true)
  })
})
