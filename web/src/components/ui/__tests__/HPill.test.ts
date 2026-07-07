// web/src/components/ui/__tests__/HPill.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HPill from '../HPill.vue'

describe('HPill', () => {
  it('applies success variant class', () => {
    const wrapper = mount(HPill, { props: { variant: 'success' }, slots: { default: 'Done' } })
    expect(wrapper.classes()).toContain('pill-success')
    expect(wrapper.text()).toBe('Done')
  })
  it('defaults to neutral when variant omitted', () => {
    const wrapper = mount(HPill, { slots: { default: 'X' } })
    expect(wrapper.classes()).toContain('pill-neutral')
  })
  it('renders pill-dot indicator', () => {
    const wrapper = mount(HPill, { props: { variant: 'warning' } })
    expect(wrapper.find('.pill-dot').exists()).toBe(true)
  })
})
