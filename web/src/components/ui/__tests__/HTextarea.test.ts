// web/src/components/ui/__tests__/HTextarea.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HTextarea from '../HTextarea.vue'

describe('HTextarea', () => {
  it('binds value via v-model', async () => {
    const wrapper = mount(HTextarea, { props: { modelValue: 'x' } })
    expect(wrapper.find('textarea').element.value).toBe('x')
    await wrapper.find('textarea').setValue('hello')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['hello'])
  })
  it('sets rows attribute', () => {
    const wrapper = mount(HTextarea, { props: { modelValue: '', rows: 8 } })
    expect(wrapper.find('textarea').attributes('rows')).toBe('8')
  })
})
