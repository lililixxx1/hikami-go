/**
 * 回顾模板编辑器状态(重构方案 §6 阶段5)。
 *
 * 从 RecapTemplateEditor 抽出:四字段表单 + global/channel 双 scope 分发 + 预设覆盖逻辑。
 * 组件保留纯 UI 状态(selectedPresetName/defaultPreviewVisible)和常量(templateVariables)。
 */
import { computed, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HConfirm } from '@/components/ui/HConfirm'
import {
  exportChannelRecapTemplates,
  exportGlobalRecapTemplates,
  getChannelRecapTemplate,
  importChannelRecapTemplates,
  importGlobalRecapTemplates,
  listGlobalRecapTemplates,
  listRecapPresets,
  upsertChannelRecapTemplate,
  upsertGlobalRecapTemplate,
  deleteChannelRecapTemplate,
} from '@/api/recap-templates'
import type { RecapTemplate, ResolvedRecapTemplate, TemplatePreset } from '@/api/types'

export interface UseRecapTemplateEditorOptions {
  scope: () => 'global' | 'channel'
  channelId: () => string
}

export function useRecapTemplateEditor(options: UseRecapTemplateEditorOptions) {
  const { scope, channelId } = options

  const loading = ref(false)
  const saving = ref(false)
  const importing = ref(false)
  const presetLoading = ref(false)
  const systemPrompt = ref('')
  const userFormat = ref('')
  const fanName = ref('')
  const extraVars = ref('{}')
  const globalTemplate = ref<RecapTemplate | null>(null)
  const resolvedTemplate = ref<ResolvedRecapTemplate | null>(null)
  const useCustom = ref(false)
  const presets = ref<TemplatePreset[]>([])

  const builtinDefaultPreset = computed(
    () => presets.value.find((preset) => preset.name === '内置默认') || null,
  )
  const usingBuiltinSystemPrompt = computed(
    () => !systemPrompt.value.trim() || systemPrompt.value.trim() === '__builtin__',
  )
  const usingBuiltinUserFormat = computed(
    () => !userFormat.value.trim() || userFormat.value.trim() === '__builtin__',
  )

  async function loadData() {
    loading.value = true
    try {
      if (scope() === 'global') {
        const res = await listGlobalRecapTemplates()
        if (res.items && res.items.length > 0) {
          const t = res.items[0]
          systemPrompt.value = t.system_prompt === '__builtin__' ? '' : t.system_prompt
          userFormat.value = t.user_format === '__builtin__' ? '' : t.user_format
          fanName.value = t.fan_name || ''
          extraVars.value = t.extra_vars || '{}'
        }
      } else if (channelId()) {
        const res = await getChannelRecapTemplate(channelId())
        globalTemplate.value = res.global
        resolvedTemplate.value = res.resolved
        if (res.channel) {
          useCustom.value = true
          systemPrompt.value = res.channel.system_prompt === '__builtin__' ? '' : res.channel.system_prompt
          userFormat.value = res.channel.user_format === '__builtin__' ? '' : res.channel.user_format
          fanName.value = res.channel.fan_name || ''
          extraVars.value = res.channel.extra_vars || '{}'
        } else {
          useCustom.value = false
          systemPrompt.value = ''
          userFormat.value = ''
          fanName.value = ''
          extraVars.value = '{}'
        }
      }
    } catch (e: any) {
      HMessage.error('加载模板失败: ' + (e.message || e))
    } finally {
      loading.value = false
    }
  }

  async function loadPresets() {
    presetLoading.value = true
    try {
      const res = await listRecapPresets()
      presets.value = res.presets ?? []
    } catch (e: any) {
      HMessage.error('加载模板预设失败: ' + (e.message || e))
    } finally {
      presetLoading.value = false
    }
  }

  async function applyPreset(name: string) {
    if (!name) return
    const preset = presets.value.find((item) => item.name === name)
    if (!preset) return
    const isBuiltinDefault = preset.name === '内置默认'

    if (!(await HConfirm(
      isBuiltinDefault
        ? '将清空当前自定义内容，恢复为内置默认（留空自动生效）'
        : '将覆盖当前编辑内容，是否继续？',
      { title: '应用模板预设', confirmText: '覆盖', cancelText: '取消', type: 'warning' },
    ))) {
      // 组件负责清空 selectedPresetName(由 onPresetCancelled 回调通知)
      return false
    }

    if (isBuiltinDefault) {
      systemPrompt.value = ''
      userFormat.value = ''
    } else {
      systemPrompt.value = preset.system_prompt
      userFormat.value = preset.user_format
    }
    if (scope() === 'channel') {
      useCustom.value = true
    }
    HMessage.success(isBuiltinDefault ? '已恢复为内置默认' : `已应用预设：${preset.name}`)
    return true
  }

  async function save() {
    saving.value = true
    try {
      if (scope() === 'global') {
        await upsertGlobalRecapTemplate({
          name: 'default',
          system_prompt: systemPrompt.value,
          user_format: userFormat.value,
          fan_name: fanName.value,
          extra_vars: extraVars.value,
          enabled: true,
        })
      } else if (channelId()) {
        await upsertChannelRecapTemplate(channelId(), {
          name: 'default',
          system_prompt: systemPrompt.value,
          user_format: userFormat.value,
          fan_name: fanName.value,
          extra_vars: extraVars.value,
          enabled: true,
        })
      }
      HMessage.success('模板已保存')
      loadData()
    } catch (e: any) {
      HMessage.error('保存失败: ' + (e.message || e))
    } finally {
      saving.value = false
    }
  }

  async function resetToDefault() {
    if (!(await HConfirm('确定要重置为内置默认模板吗？当前自定义内容将丢失。', {
      title: '重置确认',
      confirmText: '确定',
      cancelText: '取消',
      type: 'warning',
    }))) {
      // 取消
      return
    }
    systemPrompt.value = ''
    userFormat.value = ''
    fanName.value = ''
    extraVars.value = '{}'
    // 保存空值表示使用内置默认
    await save()
  }

  async function removeChannelTemplate() {
    if (!channelId()) return
    if (!(await HConfirm('确定要删除主播自定义模板吗？将回退到全局模板。', {
      title: '删除确认',
      confirmText: '确定',
      cancelText: '取消',
      type: 'warning',
    }))) return
    try {
      await deleteChannelRecapTemplate(channelId())
      HMessage.success('已删除，回退到全局模板')
      loadData()
    } catch (e: any) {
      HMessage.error('删除失败: ' + (e.message || e))
    }
  }

  async function exportTemplate() {
    const data =
      scope() === 'global'
        ? await exportGlobalRecapTemplates()
        : await exportChannelRecapTemplates(channelId())
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download =
      scope() === 'global' ? 'recap-templates.json' : `${channelId()}-recap-templates.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  async function importTemplateFile(content: string) {
    importing.value = true
    try {
      const result =
        scope() === 'global'
          ? await importGlobalRecapTemplates(content)
          : await importChannelRecapTemplates(channelId(), content)
      HMessage.success(`成功导入 ${result.imported} 个模板`)
      if (scope() === 'channel') {
        useCustom.value = true
      }
      await loadData()
    } catch (e: any) {
      HMessage.error('导入失败: ' + (e.message || e))
    } finally {
      importing.value = false
    }
  }

  return {
    // 状态
    loading,
    saving,
    importing,
    presetLoading,
    systemPrompt,
    userFormat,
    fanName,
    extraVars,
    globalTemplate,
    resolvedTemplate,
    useCustom,
    presets,
    // computed
    builtinDefaultPreset,
    usingBuiltinSystemPrompt,
    usingBuiltinUserFormat,
    // actions
    loadData,
    loadPresets,
    applyPreset,
    save,
    resetToDefault,
    removeChannelTemplate,
    exportTemplate,
    importTemplateFile,
  }
}
