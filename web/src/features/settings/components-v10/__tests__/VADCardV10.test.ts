// web/src/features/settings/components-v10/__tests__/VADCardV10.test.ts
// 覆盖 VADCardV10 的加载/编辑/保存链路。
// 见 plans/plan-vad-2026-07-27.md Phase 7。
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, it, expect, vi } from 'vitest'

// mock @/api/settings:让 getVADConfig/updateVADConfig 走桩,并暴露 spy 断言保存载荷。
const { updateVADSpy } = vi.hoisted(() => ({ updateVADSpy: vi.fn() }))
vi.mock('@/api/settings', () => ({
  getVADConfig: vi.fn().mockResolvedValue({
    enabled: true,
    threshold_db: -40,
    min_silence_sec: 2,
    padding_sec: 0.2,
    min_output_ratio: 0.3,
  }),
  updateVADConfig: updateVADSpy.mockResolvedValue({
    enabled: false,
    threshold_db: -50,
    min_silence_sec: 3,
    padding_sec: 0.5,
    min_output_ratio: 0.5,
  }),
}))

// mock HMessage(命令式 toast,不影响断言但避免 mount 时找不到组件)
vi.mock('@/components/ui/message', () => ({ HMessage: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }))

import VADCardV10 from '../VADCardV10.vue'
import { getVADConfig, updateVADConfig } from '@/api/settings'

describe('VADCardV10', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getVADConfig).mockResolvedValue({
      enabled: true,
      threshold_db: -40,
      min_silence_sec: 2,
      padding_sec: 0.2,
      min_output_ratio: 0.3,
    } as any)
    updateVADSpy.mockClear()
    updateVADSpy.mockResolvedValue({
      enabled: false,
      threshold_db: -50,
      min_silence_sec: 3,
      padding_sec: 0.5,
      min_output_ratio: 0.5,
    } as any)
  })

  it('挂载时加载 VAD 配置并填充表单', async () => {
    const wrapper = mount(VADCardV10)
    await flushPromises()
    expect(getVADConfig).toHaveBeenCalled()
    // HInput 用 input 元素,HSwitch/HSelect 也渲染
    const inputs = wrapper.findAll('input')
    // 至少应填充 min_silence_sec / padding_sec / min_output_ratio 三个 number input
    expect(inputs.length).toBeGreaterThanOrEqual(3)
  })

  it('点击「保存配置」触发 updateVADConfig,载荷含全部字段', async () => {
    const wrapper = mount(VADCardV10)
    await flushPromises()
    const saveBtn = wrapper.findAll('button').find(b => b.text().includes('保存配置'))!
    await saveBtn.trigger('click')
    await flushPromises()
    expect(updateVADConfig).toHaveBeenCalledTimes(1)
    const payload = updateVADSpy.mock.calls[0][0]
    expect(payload).toMatchObject({
      enabled: true,
      threshold_db: -40,
      min_silence_sec: 2,
      padding_sec: 0.2,
      min_output_ratio: 0.3,
    })
  })

  it('保存成功后 emit saved 事件', async () => {
    const wrapper = mount(VADCardV10, { props: { modelValue: undefined } })
    await flushPromises()
    const saveBtn = wrapper.findAll('button').find(b => b.text().includes('保存配置'))!
    await saveBtn.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('saved')).toBeTruthy()
    expect(wrapper.emitted('saved')!.length).toBe(1)
  })

  it('保存失败时不 emit saved(错误由拦截器处理)', async () => {
    updateVADSpy.mockRejectedValueOnce(new Error('network error'))
    const wrapper = mount(VADCardV10)
    await flushPromises()
    const saveBtn = wrapper.findAll('button').find(b => b.text().includes('保存配置'))!
    await saveBtn.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('saved')).toBeFalsy()
  })

  it('reload() 重新拉取配置(defineExpose)', async () => {
    const wrapper = mount(VADCardV10)
    await flushPromises()
    expect(getVADConfig).toHaveBeenCalledTimes(1)
    // 调 expose 的 reload
    ;(wrapper.vm as any).reload()
    await flushPromises()
    expect(getVADConfig).toHaveBeenCalledTimes(2)
  })
})
