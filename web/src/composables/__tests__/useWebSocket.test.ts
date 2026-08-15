// web/src/composables/__tests__/useWebSocket.test.ts
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useWebSocket } from '@/composables/useWebSocket'
import { useAdminToken } from '@/composables/useAdminToken'

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  url: string
  readyState = FakeWebSocket.CONNECTING
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }
  close() {
    this.readyState = FakeWebSocket.CLOSED
  }
}

function mountHost(connectRef: { connect: (() => void) | null }) {
  const Host = defineComponent({
    setup() {
      const { connect } = useWebSocket('ws://127.0.0.1:6334/ws')
      connectRef.connect = connect
      return { connect }
    },
    template: '<div />',
  })
  return mount(Host)
}

describe('useWebSocket', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('appends ?token= to URL when admin token is set (H1)', () => {
    const { setToken } = useAdminToken()
    setToken('sec-token-1')
    const connectRef: { connect: (() => void) | null } = { connect: null }
    const wrapper = mountHost(connectRef)
    connectRef.connect!()
    const last = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
    expect(last.url).toBe('ws://127.0.0.1:6334/ws?token=sec-token-1')
    wrapper.unmount()
  })

  it('omits query when no token is set', () => {
    const { clearToken } = useAdminToken()
    clearToken()
    const connectRef: { connect: (() => void) | null } = { connect: null }
    const wrapper = mountHost(connectRef)
    connectRef.connect!()
    const last = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
    expect(last.url).toBe('ws://127.0.0.1:6334/ws')
    wrapper.unmount()
  })

  it('re-reads token on every connect — runtime token change reflects in next URL', () => {
    const { setToken } = useAdminToken()
    setToken('old-token')
    const connectRef: { connect: (() => void) | null } = { connect: null }
    const wrapper = mountHost(connectRef)

    connectRef.connect!()
    expect(FakeWebSocket.instances[FakeWebSocket.instances.length - 1].url).toContain('token=old-token')

    // 模拟连接已断开后 token 被更新(AdminTokenCardV10 运行期设置),重连必须用新 token
    FakeWebSocket.instances[FakeWebSocket.instances.length - 1].readyState = FakeWebSocket.CLOSED
    setToken('new-token')
    connectRef.connect!()
    expect(FakeWebSocket.instances[FakeWebSocket.instances.length - 1].url).toContain('token=new-token')
    wrapper.unmount()
  })
})
