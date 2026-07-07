// web/src/components/ui/__tests__/HSwitch.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HSwitch from '../HSwitch.vue'

describe('HSwitch', () => {
  it('reflects on state', () => {
    const wrapper = mount(HSwitch, { props: { modelValue: true } })
    expect(wrapper.find('.toggle').classes()).toContain('on')
  })
  it('toggles on click', async () => {
    const wrapper = mount(HSwitch, { props: { modelValue: false } })
    await wrapper.find('.toggle').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([true])
  })
})
