// web/src/features/settings/components-v10/__tests__/MCPCardV10.test.ts
import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, vi, beforeEach } from 'vitest'

const getMCPConfig = vi.fn()
const updateMCPConfig = vi.fn()
vi.mock('@/api/settings', () => ({
  getMCPConfig: (...args: unknown[]) => getMCPConfig(...args),
  updateMCPConfig: (...args: unknown[]) => updateMCPConfig(...args),
}))

import MCPCardV10 from '../MCPCardV10.vue'

describe('MCPCardV10', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows Windows console-flash hint for stdio servers (M9)', async () => {
    getMCPConfig.mockResolvedValue({
      enabled: true,
      max_tool_rounds: 5,
      servers: [
        { name: 'local-search', transport: 'stdio', command: 'node', args: ['server.js'], env: [], enabled: true, timeout_sec: 30, headers: {} },
        { name: 'remote', transport: 'http', url: 'http://localhost:9090/mcp', command: '', args: [], env: [], enabled: true, timeout_sec: 30, headers: {} },
      ],
      builtin: { brave_api_key_set: false, brave_api_key_env: 'BRAVE_API_KEY', tavily_api_key_set: false, tavily_api_key_env: 'TAVILY_API_KEY' },
    })
    const wrapper = mount(MCPCardV10)
    await flushPromises()

    const hints = wrapper.findAll('.form-hint').map(h => h.text())
    const flashHint = hints.find(t => t.includes('控制台窗口'))
    expect(flashHint).toBeDefined()
    // http 传输的 server 不展示该 hint(stdio 专属)
    expect(hints.filter(t => t.includes('控制台窗口'))).toHaveLength(1)
  })
})
