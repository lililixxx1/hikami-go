// web/src/features/home/components/__tests__/RunningTasksSection.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import RunningTasksSection from '../RunningTasksSection.vue'
import type { Task } from '@/api/types-derived'

const tasks: Task[] = [
  { id: 't1', channel_id: 'c1', channel_name: 'Alice', session_id: 's1', type: 'asr', status: 'running', payload: '{}', progress: 50, message: '处理中', error: '', attempt: 1, created_at: '2026-07-07T10:00:00+08:00', updated_at: '2026-07-07T10:00:00+08:00' },
]
describe('RunningTasksSection', () => {
  it('renders rows from tasks prop', () => {
    const wrapper = mount(RunningTasksSection, { props: { tasks, cancellingId: null } })
    const cells = wrapper.findAll('tbody td')
    expect(cells.some(c => c.text().includes('Alice'))).toBe(true)
    expect(cells.some(c => c.text().includes('50'))).toBe(true) // progress 显示
  })
  it('emits cancel with task id on cancel button click', async () => {
    const wrapper = mount(RunningTasksSection, { props: { tasks, cancellingId: null } })
    await wrapper.find('button.cancel-btn').trigger('click')
    expect(wrapper.emitted('cancel')?.[0]).toEqual(['t1'])
  })
})
