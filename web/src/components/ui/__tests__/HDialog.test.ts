// web/src/components/ui/__tests__/HDialog.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HDialog from '../HDialog.vue'

describe('HDialog', () => {
  it('renders title + body when visible', () => {
    const wrapper = mount(HDialog, { props: { visible: true, title: 'T' }, slots: { default: 'body' } })
    expect(wrapper.find('.dialog-title').text()).toBe('T')
    expect(wrapper.find('.dialog-body').text()).toBe('body')
  })
  it('does not render when visible=false', () => {
    const wrapper = mount(HDialog, { props: { visible: false }, slots: { default: 'x' } })
    expect(wrapper.find('.dialog').exists()).toBe(false)
  })
  it('emits update:visible=false on overlay click', async () => {
    const wrapper = mount(HDialog, { props: { visible: true }, slots: { default: 'x' } })
    await wrapper.find('.dialog-overlay').trigger('click')
    expect(wrapper.emitted('update:visible')?.[0]).toEqual([false])
  })
  it('renders footer slot when provided', () => {
    const wrapper = mount(HDialog, {
      props: { visible: true },
      slots: { default: 'b', footer: '<button>F</button>' },
    })
    expect(wrapper.find('.dialog-footer').exists()).toBe(true)
  })
})
