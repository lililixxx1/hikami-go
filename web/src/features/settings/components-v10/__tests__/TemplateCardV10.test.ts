// web/src/features/settings/components-v10/__tests__/TemplateCardV10.test.ts
// 覆盖 TemplateCardV10「额外变量(extra_vars)」编辑链路的回归测试。
// 历史 bug:kvRows 是 writable computed，setter 在序列化时 if(k) 丢弃空 key 行，
// 导致点「+ 添加变量」后新行立即被销毁。修复后用独立 ref 管理编辑态，仅在保存时 flush。
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, it, expect, vi } from 'vitest'

// mock HConfirm（applyPreset 用它做覆盖确认）：默认 true（确认），
// 预设取消测试里用 mockResolvedValueOnce(false) 模拟取消。
vi.mock('@/components/ui/HConfirm', async () => {
  const actual = await vi.importActual<typeof import('@/components/ui/HConfirm')>('@/components/ui/HConfirm')
  return { ...actual, HConfirm: vi.fn().mockResolvedValue(true) }
})

// mock @/api/recap-templates：让 composable 的 loadData/save 走桩，
// 并暴露 upsertGlobalRecapTemplate 的 spy 用于断言保存时 flush 出的 extra_vars。
// 用 vi.hoisted 让 spy 与被提升的 vi.mock 同序可见。
const { upsertGlobalSpy } = vi.hoisted(() => ({ upsertGlobalSpy: vi.fn().mockResolvedValue({}) }))
vi.mock('@/api/recap-templates', () => ({
  exportChannelRecapTemplates: vi.fn().mockResolvedValue(new Blob(['{}'])),
  exportGlobalRecapTemplates: vi.fn().mockResolvedValue(new Blob(['{}'])),
  getChannelRecapTemplate: vi.fn().mockResolvedValue({ global: null, channel: null, resolved: { system_prompt: '', user_format: '', fan_name: '', extra_vars: {} } }),
  importChannelRecapTemplates: vi.fn().mockResolvedValue({}),
  importGlobalRecapTemplates: vi.fn().mockResolvedValue({}),
  listGlobalRecapTemplates: vi.fn().mockResolvedValue([]),
  listRecapPresets: vi.fn().mockResolvedValue([]),
  upsertChannelRecapTemplate: vi.fn().mockResolvedValue({}),
  upsertGlobalRecapTemplate: upsertGlobalSpy,
  deleteChannelRecapTemplate: vi.fn().mockResolvedValue({}),
}))

import TemplateCardV10 from '../TemplateCardV10.vue'
import { listGlobalRecapTemplates, importGlobalRecapTemplates, listRecapPresets } from '@/api/recap-templates'
import { HConfirm as HConfirmFn } from '@/components/ui/HConfirm'

describe('TemplateCardV10 extra_vars 编辑', () => {
  beforeEach(() => {
    upsertGlobalSpy.mockClear()
    // 默认：global 无模板（loadData 不写 extraVars）
    vi.mocked(listGlobalRecapTemplates).mockResolvedValue({ items: [] } as any)
    vi.mocked(importGlobalRecapTemplates).mockResolvedValue({} as any)
  })

  it('点击「添加变量」后新增一行空输入框且不消失', async () => {
    const wrapper = mount(TemplateCardV10, { attachTo: document.body })
    await flushPromises() // onMounted: loadData + loadPresets + syncKvRows

    expect(wrapper.findAll('.kv-row')).toHaveLength(0)
    const addBtn = wrapper.findAll('button').find(b => b.text().includes('添加变量'))!
    await addBtn.trigger('click')
    await flushPromises()

    // 核心 bug 回归：修复前这里会是 0（行被 setter 销毁）
    expect(wrapper.findAll('.kv-row')).toHaveLength(1)
    // 再等一个 tick，确认不会因响应式重算而消失
    await flushPromises()
    expect(wrapper.findAll('.kv-row')).toHaveLength(1)

    wrapper.unmount()
  })

  it('空 key 行保存时被丢弃(传给 API 的 extra_vars 为空对象)', async () => {
    const wrapper = mount(TemplateCardV10, { attachTo: document.body })
    await flushPromises()

    const addBtn = wrapper.findAll('button').find(b => b.text().includes('添加变量'))!
    await addBtn.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.kv-row')).toHaveLength(1) // 一行空 key

    const saveBtn = wrapper.findAll('button').find(b => b.text().includes('保存模板'))!
    await saveBtn.trigger('click')
    await flushPromises()

    expect(upsertGlobalSpy).toHaveBeenCalledTimes(1)
    const payload = upsertGlobalSpy.mock.calls[0][0]
    // 空 key 行在 flush 时被丢弃 → extra_vars 序列化为 '{}'
    expect(payload.extra_vars).toBe('{}')
    wrapper.unmount()
  })

  it('删除中间行内容不串行(稳定 id 修复)', async () => {
    const wrapper = mount(TemplateCardV10, { attachTo: document.body })
    await flushPromises()

    // 加 3 行并填入 key=a/b/c（通过 updateKvKey 的 @update:model-value）
    const addBtn = wrapper.findAll('button').find(b => b.text().includes('添加变量'))!
    for (let k = 0; k < 3; k++) {
      await addBtn.trigger('click')
      await flushPromises()
    }
    expect(wrapper.findAll('.kv-row')).toHaveLength(3)

    const rows = wrapper.findAll('.kv-row')
    const keyInputs = rows.map(r => r.find('input'))
    await keyInputs[0].setValue('a')
    await keyInputs[1].setValue('b')
    await keyInputs[2].setValue('c')

    // 删除中间行(索引 1, key=b)
    const delBtnsRow1 = rows[1].findAll('button').find(b => b.text().includes('删除'))!
    await delBtnsRow1.trigger('click')
    await flushPromises()

    // 剩余两行的 key 应仍为 a 和 c（修复 :key="row.id" 后不串行）
    const remaining = wrapper.findAll('.kv-row')
    expect(remaining).toHaveLength(2)
    const remainingKeys = remaining.map(r => (r.find('input').element as HTMLInputElement).value)
    expect(remainingKeys).toEqual(['a', 'c'])

    // 保存验证 flush 出的对象只含 a/c
    const saveBtn = wrapper.findAll('button').find(b => b.text().includes('保存模板'))!
    await saveBtn.trigger('click')
    await flushPromises()
    const payload = upsertGlobalSpy.mock.calls[0][0]
    expect(JSON.parse(payload.extra_vars)).toEqual({ a: '', c: '' })
    wrapper.unmount()
  })

  it('导入模板后 kvRows 同步为新 extra_vars（不静默覆盖）', async () => {
    // 回归场景(B1)：修复前 kvRows 是独立 ref，importTemplateFile 内部 loadData
    // 重写了 extraVars 但没刷新 kvRows → 编辑器显示旧行 → 下次保存覆盖导入数据。
    // 修复后在 import handler 末尾调用 syncKvRowsFromExtraVars。
    //
    // 真实走导入路径：初始有旧行 old_key → 打开导入对话框 → 粘贴 JSON → 点导入
    // → importTemplateFile(调导入 API + loadData 重拉) → sync → kvRows 变成新行。
    vi.mocked(listGlobalRecapTemplates).mockResolvedValueOnce({
      items: [{
        id: 1, channel_id: '', name: 'default', system_prompt: '', user_format: '', fan_name: '',
        extra_vars: '{"old_key":"old_val"}', enabled: true, is_default: true, created_at: '', updated_at: '',
      }],
    } as any)

    const wrapper = mount(TemplateCardV10, { attachTo: document.body })
    await flushPromises()
    // 初始显示旧行
    expect((wrapper.findAll('.kv-row')[0].findAll('input')[0].element as HTMLInputElement).value).toBe('old_key')

    // 配置导入后的 loadData 返回新模板（导入 API 成功 + 服务器已存新值）
    vi.mocked(importGlobalRecapTemplates).mockResolvedValue({ imported: 1 } as any)
    vi.mocked(listGlobalRecapTemplates).mockResolvedValue({
      items: [{
        id: 1, channel_id: '', name: 'default', system_prompt: '', user_format: '', fan_name: '',
        extra_vars: '{"new_key":"new_val"}', enabled: true, is_default: true, created_at: '', updated_at: '',
      }],
    } as any)

    // 打开导入对话框（HDialog Teleport 到 document.body）
    const openImportBtn = wrapper.findAll('button').find(b => b.text().includes('导入模板'))!
    await openImportBtn.trigger('click')
    await flushPromises()

    // 在 body 里找对话框的 textarea 和导入按钮（HDialog Teleport 到 document.body，class 是 .dialog）
    const dialogTextarea = document.body.querySelector('.dialog textarea') as HTMLTextAreaElement
    expect(dialogTextarea).toBeTruthy()
    const dialogImportBtn = Array.from(document.body.querySelectorAll('.dialog button'))
      .find(b => b.textContent?.trim() === '导入') as HTMLButtonElement
    expect(dialogImportBtn).toBeTruthy()

    // 直接设 DOM value 并派发 input 触发 v-model 响应式（teleport 后 wrapper.find 不可靠）
    dialogTextarea.value = '{"system_prompt":"","extra_vars":"{\\"new_key\\":\\"new_val\\"}"}'
    dialogTextarea.dispatchEvent(new Event('input'))
    await flushPromises()

    dialogImportBtn.click()
    await flushPromises()

    // 导入后 kvRows 应刷新为新行（修复前会保持 old_key）
    const rows = wrapper.findAll('.kv-row')
    expect(rows).toHaveLength(1)
    expect((rows[0].findAll('input')[0].element as HTMLInputElement).value).toBe('new_key')
    expect((rows[0].findAll('input')[1].element as HTMLInputElement).value).toBe('new_val')

    // 保存持久化：flush 出的 extra_vars 含新键（旧行未被覆盖回去）
    const saveBtn = wrapper.findAll('button').find(b => b.text().includes('保存模板'))!
    await saveBtn.trigger('click')
    await flushPromises()
    const savePayload = upsertGlobalSpy.mock.calls[0][0]
    expect(JSON.parse(savePayload.extra_vars)).toEqual({ new_key: 'new_val' })
    wrapper.unmount()
  })

  it('应用/取消预设时不丢失正在编辑的变量行', async () => {
    // 回归(codex 审核 BLOCKING 1)：handleApplyPreset 曾无条件 syncKvRowsFromExtraVars，
    // 但 applyPreset 不动 extraVars（取消时连 prompt 都不改），导致用户正在编辑的 kvRows
    // 被未变的 extraVars 重建、丢失未保存修改。修复后 handleApplyPreset 不再 sync。
    vi.mocked(listGlobalRecapTemplates).mockResolvedValue({
      items: [{
        id: 1, channel_id: '', name: 'default', system_prompt: '', user_format: '', fan_name: '',
        extra_vars: '{"existing":"v"}', enabled: true, is_default: true, created_at: '', updated_at: '',
      }],
    } as any)
    vi.mocked(listRecapPresets).mockResolvedValue({
      presets: [{ name: '预设A', description: '', system_prompt: 'SP_A', user_format: 'UF_A' }],
    } as any)

    const wrapper = mount(TemplateCardV10, { attachTo: document.body })
    await flushPromises()
    // 初始有一行 existing；用户点「添加变量」新增一行未保存的空行
    expect(wrapper.findAll('.kv-row')).toHaveLength(1)
    const addBtn = wrapper.findAll('button').find(b => b.text().includes('添加变量'))!
    await addBtn.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.kv-row')).toHaveLength(2) // existing + 新空行

    // 选择预设并「取消」确认
    vi.mocked(HConfirmFn).mockResolvedValueOnce(false)
    const presetSelect = wrapper.findComponent({ name: 'HSelect' })
    await presetSelect.vm.$emit('update:modelValue', '预设A')
    await flushPromises()

    // 取消预设后，用户的新增行必须保留（修复前会被 sync 用 extraVars 重建回 1 行）
    expect(wrapper.findAll('.kv-row')).toHaveLength(2)

    // 再测：选择预设并「确认」覆盖
    vi.mocked(HConfirmFn).mockResolvedValueOnce(true)
    await presetSelect.vm.$emit('update:modelValue', '预设A')
    await flushPromises()
    // 确认覆盖只改 system_prompt/user_format，变量行仍应保留（applyPreset 不碰 extraVars）
    expect(wrapper.findAll('.kv-row')).toHaveLength(2)
    wrapper.unmount()
  })

  it('导入失败时不 sync、不关对话框、保留用户编辑态', async () => {
    // 回归(codex BLOCKING)：composable 内部 try/catch 吞异常，组件 await 仍正常 resolve，
    // 导致失败时仍 sync/关对话框、用旧 extraVars 覆盖未保存编辑。修复后 composable 返回
    // boolean，组件仅在成功时 sync/关对话框。
    vi.mocked(listGlobalRecapTemplates).mockResolvedValue({
      items: [{
        id: 1, channel_id: '', name: 'default', system_prompt: '', user_format: '', fan_name: '',
        extra_vars: '{"existing":"v"}', enabled: true, is_default: true, created_at: '', updated_at: '',
      }],
    } as any)
    // 导入 API reject
    vi.mocked(importGlobalRecapTemplates).mockRejectedValueOnce(new Error('网络错误'))

    const wrapper = mount(TemplateCardV10, { attachTo: document.body })
    await flushPromises()
    expect(wrapper.findAll('.kv-row')).toHaveLength(1) // existing 行

    // 用户新增一行（未保存）
    const addBtn = wrapper.findAll('button').find(b => b.text().includes('添加变量'))!
    await addBtn.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.kv-row')).toHaveLength(2)

    // 打开导入对话框，粘贴内容，点导入（导入 API 将 reject）
    const openImportBtn = wrapper.findAll('button').find(b => b.text().includes('导入模板'))!
    await openImportBtn.trigger('click')
    await flushPromises()
    const dialogTextarea = document.body.querySelector('.dialog textarea') as HTMLTextAreaElement
    dialogTextarea.value = '{"system_prompt":""}'
    dialogTextarea.dispatchEvent(new Event('input'))
    await flushPromises()
    const dialogImportBtn = Array.from(document.body.querySelectorAll('.dialog button'))
      .find(b => b.textContent?.trim() === '导入') as HTMLButtonElement
    dialogImportBtn.click()
    await flushPromises()

    // 失败：kvRows 保持用户的 2 行（未被旧 extraVars 覆盖回 1 行），对话框仍打开
    expect(wrapper.findAll('.kv-row')).toHaveLength(2)
    const dialogStillOpen = document.body.querySelector('.dialog')
    expect(dialogStillOpen).toBeTruthy()
    wrapper.unmount()
  })

  it('保存失败时不 emit saved、保留用户编辑态', async () => {
    // 回归(codex BLOCKING)：保存失败时不应 emit('saved')（否则父组件误以为已持久化），
    // 也不应 sync（保留用户正在编辑的空 key 行）。
    vi.mocked(listGlobalRecapTemplates).mockResolvedValue({
      items: [{
        id: 1, channel_id: '', name: 'default', system_prompt: '', user_format: '', fan_name: '',
        extra_vars: '{}', enabled: true, is_default: true, created_at: '', updated_at: '',
      }],
    } as any)
    // upsert reject
    upsertGlobalSpy.mockRejectedValueOnce(new Error('服务器错误'))

    const wrapper = mount(TemplateCardV10, { attachTo: document.body })
    await flushPromises()
    // 用户新增一行空 key
    const addBtn = wrapper.findAll('button').find(b => b.text().includes('添加变量'))!
    await addBtn.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.kv-row')).toHaveLength(1)

    const saveBtn = wrapper.findAll('button').find(b => b.text().includes('保存模板'))!
    await saveBtn.trigger('click')
    await flushPromises()

    // 失败：不 emit saved，kvRows 保留用户的空 key 行（未被 sync 清空）
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect(wrapper.findAll('.kv-row')).toHaveLength(1)
    wrapper.unmount()
  })

  it('导入API成功但重载失败时，不 sync、不关对话框', async () => {
    // 回归(codex BLOCKING)：导入 API 成功但随后 loadData() 失败时，
    // importTemplateFile 应返回 false（整体失败），组件不 sync/不关对话框，
    // 避免用旧 extraVars 重建 kvRows 并在后续保存覆盖刚导入的数据。
    vi.mocked(listGlobalRecapTemplates).mockResolvedValue({
      items: [{
        id: 1, channel_id: '', name: 'default', system_prompt: '', user_format: '', fan_name: '',
        extra_vars: '{"existing":"v"}', enabled: true, is_default: true, created_at: '', updated_at: '',
      }],
    } as any)
    // 导入 API 成功
    vi.mocked(importGlobalRecapTemplates).mockResolvedValue({ imported: 1 } as any)

    const wrapper = mount(TemplateCardV10, { attachTo: document.body })
    await flushPromises() // onMounted loadData 走默认 mock（成功）
    const addBtn = wrapper.findAll('button').find(b => b.text().includes('添加变量'))!
    await addBtn.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.kv-row')).toHaveLength(2) // existing + 用户新增

    // 在导入操作前设置：导入后的那次 loadData 重拉 reject（once 队列优先于默认）
    vi.mocked(listGlobalRecapTemplates).mockRejectedValueOnce(new Error('重载失败'))

    const openImportBtn = wrapper.findAll('button').find(b => b.text().includes('导入模板'))!
    await openImportBtn.trigger('click')
    await flushPromises()
    const dialogTextarea = document.body.querySelector('.dialog textarea') as HTMLTextAreaElement
    dialogTextarea.value = '{"system_prompt":""}'
    dialogTextarea.dispatchEvent(new Event('input'))
    await flushPromises()
    const dialogImportBtn = Array.from(document.body.querySelectorAll('.dialog button'))
      .find(b => b.textContent?.trim() === '导入') as HTMLButtonElement
    dialogImportBtn.click()
    await flushPromises()

    // 重载失败：整体视为失败，kvRows 保留用户编辑态、对话框仍打开
    expect(wrapper.findAll('.kv-row')).toHaveLength(2)
    expect(document.body.querySelector('.dialog')).toBeTruthy()
    wrapper.unmount()
  })
})
