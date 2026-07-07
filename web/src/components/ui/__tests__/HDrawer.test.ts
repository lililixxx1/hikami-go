// web/src/components/ui/__tests__/HDrawer.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HDrawer from '../HDrawer.vue'

describe('HDrawer', () => {
  it('renders content when visible', () => {
    const wrapper = mount(HDrawer, { props: { visible: true, title: 'Detail' }, slots: { default: 'body' } })
    expect(wrapper.find('.drawer').classes()).toContain('open')
    expect(wrapper.find('.drawer-title').text()).toBe('Detail')
    expect(wrapper.find('.drawer-body').text()).toBe('body')
  })
  it('hides when visible=false', () => {
    const wrapper = mount(HDrawer, { props: { visible: false }, slots: { default: 'x' } })
    expect(wrapper.find('.drawer').exists()).toBe(false)
  })
  it('emits update:visible false on overlay click', async () => {
    const wrapper = mount(HDrawer, { props: { visible: true }, slots: { default: 'x' } })
    await wrapper.find('.drawer-overlay').trigger('click')
    expect(wrapper.emitted('update:visible')?.[0]).toEqual([false])
  })
})
