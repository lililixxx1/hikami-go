<!--
  Sidebar.vue(Phase 5 Task 5.1)。V10 设置页左侧导航。
  - props.sections:扁平 section 列表(含 group 分组字段),组件内按 group 聚合成 sidebar-group。
  - 每个 item sb-dot(done/pending/warning 三态)+ label;点击 emit navigate(sectionId)。
  - 壳(SettingsView Task 5.5)负责 scrollIntoView 滚动到对应 section + 标记 active。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed } from 'vue'

interface SidebarSection {
  id: string
  label: string
  group: string
  done?: boolean
  status?: 'done' | 'pending' | 'warning'
}

const props = defineProps<{
  sections: SidebarSection[]
  activeId?: string
}>()

const emit = defineEmits<{ navigate: [sectionId: string] }>()

// 按 group 聚合,保持首次出现顺序(group 顺序 = sections 中该 group 首次出现的顺序)
const groups = computed(() => {
  const order: string[] = []
  const map = new Map<string, SidebarSection[]>()
  for (const s of props.sections) {
    if (!map.has(s.group)) {
      map.set(s.group, [])
      order.push(s.group)
    }
    map.get(s.group)!.push(s)
  }
  return order.map(g => ({ group: g, items: map.get(g)! }))
})

function dotClass(s: SidebarSection): string {
  if (s.status) return s.status
  return s.done ? 'done' : 'pending'
}

function onNavigate(s: SidebarSection) {
  emit('navigate', s.id)
}
</script>

<template>
  <aside class="sidebar">
    <div v-for="g in groups" :key="g.group" class="sidebar-group">
      <div class="sidebar-group-header">{{ g.group }}</div>
      <a
        v-for="s in g.items"
        :key="s.id"
        class="sidebar-item"
        :class="{ active: activeId === s.id }"
        role="button"
        tabindex="0"
        @click="onNavigate(s)"
        @keydown.enter="onNavigate(s)"
      >
        <span class="sb-dot" :class="dotClass(s)" />
        <span>{{ s.label }}</span>
      </a>
    </div>
  </aside>
</template>
