import { computed, ref } from 'vue'
import { getRecapModels } from '@/api/settings'
import type { RecapModelOption } from '@/api/types'

/**
 * 加载后端推荐的回顾模型列表（GET /api/config/recap/models），并按厂商分组。
 * 供 SettingsView（全局）与 StreamersView（主播级）下拉复用，避免两处硬编码不一致。
 * 模型名称仍支持自由输入，列表仅为常用快捷选项。
 */
export function useRecapModels() {
  const models = ref<RecapModelOption[]>([])
  const loaded = ref(false)

  // 按 group 聚合成 el-option-group 所需结构，保持后端定义的顺序
  const groups = computed(() => {
    const map = new Map<string, RecapModelOption[]>()
    for (const m of models.value) {
      if (!map.has(m.group)) map.set(m.group, [])
      map.get(m.group)!.push(m)
    }
    return Array.from(map, ([name, items]) => ({ name, models: items }))
  })

  async function load() {
    if (loaded.value) return
    try {
      const resp = await getRecapModels()
      models.value = resp.models ?? []
    } catch {
      // 拉取失败时保持空列表，下拉退化为纯自由输入
      models.value = []
    }
    loaded.value = true
  }

  return { models, groups, load }
}
