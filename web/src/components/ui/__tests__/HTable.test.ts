// web/src/components/ui/__tests__/HTable.test.ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import HTable from '../HTable.vue'

interface Row {
  id: number
  name: string
}

describe('HTable', () => {
  const columns = [
    { key: 'id', label: 'ID' },
    { key: 'name', label: '名称' },
  ]
  const data: Row[] = [
    { id: 1, name: 'Alice' },
    { id: 2, name: 'Bob' },
  ]
  it('renders column headers and row cells', () => {
    const wrapper = mount(HTable, { props: { columns, data } })
    const headers = wrapper.findAll('thead th').map(th => th.text())
    expect(headers).toEqual(['ID', '名称'])
    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    // 第一行的单元格文本
    expect(rows[0].findAll('td').map(td => td.text())).toEqual(['1', 'Alice'])
  })
  it('emits row-click with the row payload on tbody row click', async () => {
    const wrapper = mount(HTable, { props: { columns, data } })
    await wrapper.findAll('tbody tr')[1].trigger('click')
    expect(wrapper.emitted('row-click')?.[0]).toEqual([data[1]])
  })
})
