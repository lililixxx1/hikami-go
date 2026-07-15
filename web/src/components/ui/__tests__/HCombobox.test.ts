// web/src/components/ui/__tests__/HCombobox.test.ts
import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { describe, it, expect } from 'vitest'
import HCombobox from '../HCombobox.vue'

describe('HCombobox', () => {
  const opts = [
    { label: 'deepseek-v4-flash（快速）', value: 'deepseek-v4-flash' },
    { label: 'deepseek-v4-pro（默认）', value: 'deepseek-v4-pro' },
  ]

  it('renders an input with datalist suggestions', () => {
    const wrapper = mount(HCombobox, { props: { modelValue: '', options: opts } })
    // 可输入框
    expect(wrapper.find('input').exists()).toBe(true)
    // datalist 提供快捷选项
    const options = wrapper.findAll('datalist option')
    expect(options).toHaveLength(2)
    expect(options[0].attributes('value')).toBe('deepseek-v4-flash')
    expect(options[0].text()).toBe('deepseek-v4-flash（快速）')
  })

  it('binds value via v-model', async () => {
    const wrapper = mount(HCombobox, { props: { modelValue: 'a', options: opts } })
    expect(wrapper.find('input').element.value).toBe('a')
    await wrapper.find('input').setValue('my-custom-model')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['my-custom-model'])
  })

  it('displays a value not present in options (legacy/old config value)', () => {
    // 精简预设后，已保存配置里的旧 model 值（如 gpt-4o）仍能在 input 中正确回显
    const wrapper = mount(HCombobox, { props: { modelValue: 'gpt-4o', options: opts } })
    expect(wrapper.find('input').element.value).toBe('gpt-4o')
  })

  it('renders label slot', () => {
    const wrapper = mount(HCombobox, { props: { modelValue: '', options: opts }, slots: { label: '回顾模型' } })
    expect(wrapper.find('.form-label').text()).toBe('回顾模型')
  })

  it('disables input when disabled=true', () => {
    const wrapper = mount(HCombobox, { props: { modelValue: '', options: opts, disabled: true } })
    expect(wrapper.find('input').attributes('disabled')).toBeDefined()
  })

  it('emits empty string when clear button clicked (clearable)', async () => {
    const wrapper = mount(HCombobox, {
      props: { modelValue: 'deepseek-v4-pro', options: opts, clearable: true },
    })
    // 有值且 clearable 时显示清空按钮
    const clearBtn = wrapper.find('.combobox-clear')
    expect(clearBtn.exists()).toBe(true)
    await clearBtn.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([''])
  })

  it('does not show clear button when value is empty', () => {
    const wrapper = mount(HCombobox, {
      props: { modelValue: '', options: opts, clearable: true },
    })
    expect(wrapper.find('.combobox-clear').exists()).toBe(false)
  })

  it('applies input-sm class when size=sm', () => {
    const wrapper = mount(HCombobox, { props: { modelValue: '', options: opts, size: 'sm' } })
    expect(wrapper.find('input').classes()).toContain('input-sm')
  })

  it('does not show clear button when disabled even with value and clearable', () => {
    const wrapper = mount(HCombobox, {
      props: { modelValue: 'deepseek-v4-pro', options: opts, clearable: true, disabled: true },
    })
    expect(wrapper.find('.combobox-clear').exists()).toBe(false)
  })

  it('binds input[list] to its own datalist[id]', () => {
    const wrapper = mount(HCombobox, { props: { modelValue: '', options: opts } })
    const input = wrapper.find('input')
    const datalist = wrapper.find('datalist')
    const listAttr = input.attributes('list')
    const idAttr = datalist.attributes('id')
    expect(listAttr).toBeTruthy()
    expect(idAttr).toBeTruthy()
    expect(listAttr).toBe(idAttr)
  })

  it('assigns unique datalist ids to two instances in the same app', () => {
    // useId() 在同一 app 内为每个组件实例生成不同 id；同页两个 HCombobox 不会串台。
    // 用一个父组件包裹两个 HCombobox，确保它们共享同一个 app scope。
    const TwoBoxes = defineComponent({
      setup() {
        return () => [
          h(HCombobox, { modelValue: '', options: opts }),
          h(HCombobox, { modelValue: '', options: opts }),
        ]
      },
    })
    const wrapper = mount(TwoBoxes)
    const datalists = wrapper.findAll('datalist')
    expect(datalists).toHaveLength(2)
    const ids = datalists.map((d) => d.attributes('id'))
    expect(ids[0]).toBeTruthy()
    expect(ids[1]).toBeTruthy()
    expect(ids[0]).not.toBe(ids[1])
  })
})
