// web/src/components/ui/__tests__/HSelect.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HSelect from '../HSelect.vue'

describe('HSelect', () => {
  const opts = [
    { label: 'A', value: 'a' },
    { label: 'B', value: 'b' },
  ]
  it('renders options from prop', () => {
    const wrapper = mount(HSelect, { props: { modelValue: 'a', options: opts } })
    const options = wrapper.findAll('option')
    expect(options).toHaveLength(2)
    expect(options[0].text()).toBe('A')
  })
  it('emits update:modelValue on change', async () => {
    const wrapper = mount(HSelect, { props: { modelValue: 'a', options: opts } })
    await wrapper.find('select').setValue('b')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['b'])
  })
})
