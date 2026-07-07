// web/src/components/ui/__tests__/HProgress.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HProgress from '../HProgress.vue'

describe('HProgress', () => {
  it('sets fill width from progress', () => {
    const wrapper = mount(HProgress, { props: { progress: 50 } })
    expect(wrapper.find('.progress-bar-fill').attributes('style')).toContain('width: 50%')
  })
  it('clamps progress to 0-100', () => {
    const wrapper = mount(HProgress, { props: { progress: 150 } })
    expect(wrapper.find('.progress-bar-fill').attributes('style')).toContain('width: 100%')
  })
  it('applies status class', () => {
    const wrapper = mount(HProgress, { props: { progress: 50, status: 'failed' } })
    expect(wrapper.find('.progress-bar-fill').classes()).toContain('failed')
  })
})
