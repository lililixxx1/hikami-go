// web/src/components/ui/__tests__/HInput.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HInput from '../HInput.vue'

describe('HInput', () => {
  it('binds value via v-model', async () => {
    const wrapper = mount(HInput, { props: { modelValue: 'a' } })
    expect(wrapper.find('input').element.value).toBe('a')
    await wrapper.find('input').setValue('abc')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['abc'])
  })
  it('renders label slot', () => {
    const wrapper = mount(HInput, { slots: { label: 'Name' } })
    expect(wrapper.find('.form-label').text()).toBe('Name')
  })
  it('applies input-sm class when size=sm', () => {
    const wrapper = mount(HInput, { props: { modelValue: '', size: 'sm' } })
    expect(wrapper.find('input').classes()).toContain('input-sm')
  })
  it('disables input when disabled=true', () => {
    const wrapper = mount(HInput, { props: { modelValue: '', disabled: true } })
    expect(wrapper.find('input').attributes('disabled')).toBeDefined()
  })
})
