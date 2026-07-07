// web/src/components/ui/__tests__/HDescriptions.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HDescriptions from '../HDescriptions.vue'

describe('HDescriptions', () => {
  const items = [
    { label: '名称', value: 'Alice' },
    { label: 'ID', value: 123 },
    { label: '空', value: '' },
  ]
  it('renders label + value for each item', () => {
    const wrapper = mount(HDescriptions, { props: { items } })
    const labels = wrapper.findAll('.desc-label').map(e => e.text())
    const values = wrapper.findAll('.desc-value').map(e => e.text())
    expect(labels).toEqual(['名称', 'ID', '空'])
    expect(values).toEqual(['Alice', '123', '-'])  // 空值显示 '-'
  })
  it('applies column count to grid template', () => {
    const wrapper = mount(HDescriptions, { props: { items: [], column: 3 } })
    expect(wrapper.find('.h-descriptions').attributes('style')).toContain('grid-template-columns: repeat(3, 1fr)')
  })
})
