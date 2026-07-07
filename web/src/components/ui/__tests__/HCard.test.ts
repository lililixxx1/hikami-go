// web/src/components/ui/__tests__/HCard.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HCard from '../HCard.vue'

describe('HCard', () => {
  it('renders title in header when title prop given', () => {
    const wrapper = mount(HCard, { props: { title: 'My Card' } })
    expect(wrapper.find('.card-header').exists()).toBe(true)
    expect(wrapper.find('.card-title').text()).toBe('My Card')
  })
  it('renders header slot overriding title prop', () => {
    const wrapper = mount(HCard, { slots: { header: '<span class="custom">X</span>' } })
    expect(wrapper.find('.card-header').exists()).toBe(true)
    expect(wrapper.find('.custom').exists()).toBe(true)
    expect(wrapper.find('.card-title').exists()).toBe(false)
  })
  it('hides header when neither title nor header slot', () => {
    const wrapper = mount(HCard, { slots: { default: 'body' } })
    expect(wrapper.find('.card-header').exists()).toBe(false)
  })
  it('renders default slot in body', () => {
    const wrapper = mount(HCard, { slots: { default: '<p class="b">body</p>' } })
    expect(wrapper.find('.card-body').html()).toContain('class="b"')
  })
})
