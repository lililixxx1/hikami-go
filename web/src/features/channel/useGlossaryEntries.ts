/**
 * 术语表 CRUD + 列表(重构方案 §6 阶段5)。
 *
 * 从 GlossaryEditor 抽出:global/channel 双 scope 分发的增删改查 + 批量 + 导入导出。
 * 组件保留纯 UI 状态(对话框开关/表单输入/tableRef)。
 *
 * CRUD 函数原本有大量 `scope==='global' ? upsertGlobalEntry : upsertChannelEntry` 重复,
 * 抽到这里集中分发,组件只调用统一入口。
 */
import { computed, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HConfirm } from '@/components/ui/HConfirm'
import {
  batchDeleteChannelEntries,
  batchDeleteGlobalEntries,
  batchToggleChannelEntries,
  batchToggleGlobalEntries,
  deleteChannelEntry,
  deleteGlobalEntry,
  exportChannelJSON,
  exportGlobalJSON,
  importChannelJSON,
  importChannelMarkdown,
  importGlobalJSON,
  importGlobalMarkdown,
  listChannelEntries,
  listGlobalEntries,
  toggleChannelEntry,
  toggleGlobalEntry,
  upsertChannelEntry,
  upsertGlobalEntry,
} from '@/api/glossary'
import type { GlossaryEntry } from '@/api/types-derived'

export interface UseGlossaryEntriesOptions {
  scope: () => 'global' | 'channel'
  channelId: () => string
  channelName: () => string
  showGlobalReadonly: () => boolean
}

/** 识别后端「重复」错误(用于词条已存在的友好提示) */
export function isDuplicateError(error: unknown): boolean {
  const err = error as { response?: { data?: { error?: string; reason?: string } }; message?: string }
  const message = `${err.response?.data?.error || ''} ${err.response?.data?.reason || ''} ${err.message || ''}`.toLowerCase()
  return message.includes('duplicate') || message.includes('already exists')
}

export function useGlossaryEntries(options: UseGlossaryEntriesOptions) {
  const { scope, channelId, channelName, showGlobalReadonly } = options

  const loading = ref(false)
  const editableEntries = ref<GlossaryEntry[]>([])
  const readonlyEntries = ref<GlossaryEntry[]>([])

  const editableTitle = computed(() => (scope() === 'global' ? '全局词条' : '主播词条'))
  const exportFilename = computed(() =>
    scope() === 'global' ? 'glossary.json' : `${channelName() || channelId() || 'channel'}-glossary.json`,
  )
  const categoryOptions = computed(() => {
    const categories = editableEntries.value
      .map((entry) => entry.category?.trim())
      .filter((category): category is string => !!category)
    return Array.from(new Set(categories)).sort()
  })

  async function fetchData(): Promise<void> {
    if (scope() === 'channel' && !channelId()) return
    loading.value = true
    try {
      if (scope() === 'global') {
        const entriesResp = await listGlobalEntries()
        editableEntries.value = entriesResp.items ?? []
        readonlyEntries.value = []
      } else {
        const channelResp = await listChannelEntries(channelId())
        editableEntries.value = (channelResp.items ?? []).filter((e: GlossaryEntry) => e.source !== 'global')

        if (showGlobalReadonly()) {
          const globalResp = await listGlobalEntries()
          readonlyEntries.value = globalResp.items ?? []
        } else {
          readonlyEntries.value = []
        }
      }
    } finally {
      loading.value = false
    }
  }

  // 增(统一入口,内部按 scope 分发)
  async function addEntry(term: string, canonical: string, category: string): Promise<void> {
    if (scope() === 'global') {
      await upsertGlobalEntry(term, canonical, category)
    } else {
      await upsertChannelEntry(channelId(), term, canonical, category)
    }
    await fetchData()
  }

  // 删(单条,带确认)
  async function deleteEntry(entry: GlossaryEntry): Promise<boolean> {
    if (!(await HConfirm(`确定要删除词条「${entry.term}」吗？`, {
      title: '删除确认', confirmText: '删除', cancelText: '取消', type: 'warning',
    }))) return false
    if (scope() === 'global') {
      await deleteGlobalEntry(entry.id)
    } else {
      await deleteChannelEntry(channelId(), entry.id)
    }
    HMessage.success('已删除')
    await fetchData()
    return true
  }

  // 切换启用(失败回滚由调用方处理,因 entry.enabled 是组件列表项的响应式属性)
  async function toggleEntry(entry: GlossaryEntry, enabled: boolean): Promise<void> {
    if (scope() === 'global') {
      await toggleGlobalEntry(entry.id, enabled)
    } else {
      await toggleChannelEntry(channelId(), entry.id, enabled)
    }
  }

  // 批量切换(带确认)
  async function batchToggle(ids: number[], enabled: boolean): Promise<boolean> {
    if (!(await HConfirm(
      `确定要${enabled ? '启用' : '禁用'}选中的 ${ids.length} 条词条吗？`,
      { title: '批量操作确认', confirmText: '确认', cancelText: '取消', type: 'warning' },
    ))) return false
    if (scope() === 'global') {
      await batchToggleGlobalEntries(ids, enabled)
    } else {
      await batchToggleChannelEntries(channelId(), ids, enabled)
    }
    HMessage.success('操作成功')
    await fetchData()
    return true
  }

  // 批量删除(带确认)
  async function batchDelete(ids: number[]): Promise<boolean> {
    if (!(await HConfirm(
      `确定要删除选中的 ${ids.length} 条词条吗？`,
      { title: '批量删除确认', confirmText: '删除', cancelText: '取消', type: 'warning' },
    ))) return false
    if (scope() === 'global') {
      await batchDeleteGlobalEntries(ids)
    } else {
      await batchDeleteChannelEntries(channelId(), ids)
    }
    HMessage.success('删除成功')
    await fetchData()
    return true
  }

  // 导入(markdown/json)
  async function importEntries(content: string, type: 'markdown' | 'json'): Promise<number> {
    const result =
      scope() === 'global'
        ? type === 'markdown'
          ? await importGlobalMarkdown(content)
          : await importGlobalJSON(content)
        : type === 'markdown'
          ? await importChannelMarkdown(channelId(), content)
          : await importChannelJSON(channelId(), content)
    HMessage.success(`成功导入 ${result.imported} 条词条`)
    await fetchData()
    return result.imported
  }

  // 导出
  async function exportJSON(): Promise<void> {
    const data = scope() === 'global' ? await exportGlobalJSON() : await exportChannelJSON(channelId())
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = exportFilename.value
    a.click()
    URL.revokeObjectURL(url)
  }

  return {
    // 状态
    loading,
    editableEntries,
    readonlyEntries,
    // computed
    editableTitle,
    exportFilename,
    categoryOptions,
    // actions
    fetchData,
    addEntry,
    deleteEntry,
    toggleEntry,
    batchToggle,
    batchDelete,
    importEntries,
    exportJSON,
  }
}
