// web/src/components/ui/__tests__/HCollapse.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HCollapse from '../HCollapse.vue'
import HCollapseItem from '../HCollapseItem.vue'

describe('HCollapse', () => {
  it('toggles item open state on trigger click', async () => {
    const wrapper = mount(HCollapse, {
      props: { modelValue: [] as string[] },
      slots: { default: '<HCollapseItem name="a" title="A">body</HCollapseItem>' },
      global: { components: { HCollapseItem } },
    })
    const trigger = wrapper.find('.collapse-trigger')
    await trigger.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([['a']])
  })
})
