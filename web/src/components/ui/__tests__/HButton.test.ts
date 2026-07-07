// web/src/components/ui/__tests__/HButton.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HButton from '../HButton.vue'

describe('HButton', () => {
  it('renders default variant', () => {
    const wrapper = mount(HButton, { slots: { default: 'Click' } })
    expect(wrapper.classes()).toContain('btn')
    expect(wrapper.classes()).toContain('btn-primary')
    expect(wrapper.text()).toBe('Click')
  })
  it('applies variant and size classes', () => {
    const wrapper = mount(HButton, { props: { variant: 'ghost', size: 'sm' }, slots: { default: 'X' } })
    expect(wrapper.classes()).toContain('btn-ghost')
    expect(wrapper.classes()).toContain('btn-sm')
  })
  it('emits click', async () => {
    const wrapper = mount(HButton, { slots: { default: 'Go' } })
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toHaveLength(1)
  })
  it('does not emit click when disabled', async () => {
    const wrapper = mount(HButton, { props: { disabled: true }, slots: { default: 'X' } })
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toBeUndefined()
  })
})
