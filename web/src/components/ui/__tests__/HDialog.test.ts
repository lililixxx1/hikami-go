// web/src/components/ui/__tests__/HDialog.test.ts
// HDialog 使用 <Teleport to="body">(production 需逃出父级 overflow/z-index 上下文覆盖顶栏)。
// 测试用 attachTo: document.body 让 Teleport 目标存在,并直接查 document.body。
import { mount } from '@vue/test-utils'
import { describe, it, expect, afterEach } from 'vitest'
import HDialog from '../HDialog.vue'

describe('HDialog', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('renders title + body when visible', () => {
    mount(HDialog, {
      props: { visible: true, title: 'T' },
      slots: { default: 'body' },
      attachTo: document.body,
    })
    expect(document.body.querySelector('.dialog-title')?.textContent).toBe('T')
    expect(document.body.querySelector('.dialog-body')?.textContent).toBe('body')
  })
  it('does not render when visible=false', () => {
    mount(HDialog, {
      props: { visible: false },
      slots: { default: 'x' },
      attachTo: document.body,
    })
    expect(document.body.querySelector('.dialog')).toBeNull()
  })
  it('emits update:visible=false on overlay click', async () => {
    const wrapper = mount(HDialog, {
      props: { visible: true },
      slots: { default: 'x' },
      attachTo: document.body,
    })
    const overlay = document.body.querySelector('.dialog-overlay') as HTMLElement
    await overlay.click()
    expect(wrapper.emitted('update:visible')?.[0]).toEqual([false])
  })
  it('does not close when clicking dialog content (@click.self on overlay)', async () => {
    // 嵌套结构 + @click.self:点 dialog 内部(body/footer)不应冒泡触发关闭。
    // .self 修饰符只在 event.target === overlay 自身时触发,点击子元素不触发。
    const wrapper = mount(HDialog, {
      props: { visible: true },
      slots: { default: 'content' },
      attachTo: document.body,
    })
    const body = document.body.querySelector('.dialog-body') as HTMLElement
    await body.click()
    expect(wrapper.emitted('update:visible')).toBeUndefined()
  })
  it('dialog is a child of overlay (flex centering requires nesting)', () => {
    // 验证 DOM 嵌套结构:dialog 必须是 overlay 的子元素,
    // overlay 的 display:flex;align-items:center;justify-content:center 才能居中 dialog。
    mount(HDialog, {
      props: { visible: true },
      slots: { default: 'x' },
      attachTo: document.body,
    })
    const overlay = document.body.querySelector('.dialog-overlay') as HTMLElement
    expect(overlay.querySelector('.dialog')).not.toBeNull()
  })
  it('renders footer slot when provided', () => {
    mount(HDialog, {
      props: { visible: true },
      slots: { default: 'b', footer: '<button>F</button>' },
      attachTo: document.body,
    })
    expect(document.body.querySelector('.dialog-footer')).not.toBeNull()
  })
})
