// web/src/components/ui/__tests__/HDrawer.test.ts
// HDrawer 使用 <Teleport to="body"> 渲染(production 需要逃出父级 overflow/z-index 上下文覆盖顶栏)。
// 测试用 attachTo: document.body 让 Teleport 目标存在,并直接查 document.body 而非 wrapper。
import { mount } from '@vue/test-utils'
import { describe, it, expect, afterEach } from 'vitest'
import HDrawer from '../HDrawer.vue'

describe('HDrawer', () => {
  afterEach(() => {
    // 清理 Teleport 到 body 的残留
    document.body.innerHTML = ''
  })

  it('renders content when visible', () => {
    mount(HDrawer, {
      props: { visible: true, title: 'Detail' },
      slots: { default: 'body' },
      attachTo: document.body,
    })
    const drawer = document.body.querySelector('.drawer')
    expect(drawer?.classList.contains('open')).toBe(true)
    expect(document.body.querySelector('.drawer-title')?.textContent).toBe('Detail')
    expect(document.body.querySelector('.drawer-body')?.textContent).toBe('body')
  })
  it('hides when visible=false', () => {
    mount(HDrawer, {
      props: { visible: false },
      slots: { default: 'x' },
      attachTo: document.body,
    })
    expect(document.body.querySelector('.drawer')).toBeNull()
  })
  it('emits update:visible false on overlay click', async () => {
    const wrapper = mount(HDrawer, {
      props: { visible: true },
      slots: { default: 'x' },
      attachTo: document.body,
    })
    const overlay = document.body.querySelector('.drawer-overlay') as HTMLElement
    await overlay.click()
    expect(wrapper.emitted('update:visible')?.[0]).toEqual([false])
  })
})
