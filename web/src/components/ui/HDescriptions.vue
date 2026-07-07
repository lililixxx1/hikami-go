<!-- web/src/components/ui/HDescriptions.vue -->
<script setup lang="ts">
import { computed } from 'vue'
interface DescItem {
  label: string
  value: string | number | null | undefined
}
const props = withDefaults(defineProps<{
  items: DescItem[]
  column?: number
}>(), { column: 2 })

function display(v: DescItem['value']): string {
  return v === null || v === undefined || v === '' ? '-' : String(v)
}

const gridStyle = computed(() => ({
  'grid-template-columns': `repeat(${props.column}, 1fr)`,
}))
</script>
<template>
  <div class="h-descriptions" :style="gridStyle">
    <div v-for="(item, i) in items" :key="i" class="desc-cell">
      <div class="desc-label">{{ item.label }}</div>
      <div class="desc-value">{{ display(item.value) }}</div>
    </div>
  </div>
</template>
