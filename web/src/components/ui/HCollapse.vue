<!-- web/src/components/ui/HCollapse.vue -->
<script setup lang="ts">
import { provide } from 'vue'
const props = defineProps<{ modelValue: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
// provide toggle 函数给 HCollapseItem
provide('h-collapse-toggle', (name: string) => {
  const next = props.modelValue.includes(name)
    ? props.modelValue.filter(n => n !== name)
    : [...props.modelValue, name]
  emit('update:modelValue', next)
})
provide('h-collapse-open-set', () => new Set(props.modelValue))
</script>
<template>
  <div class="collapse"><slot /></div>
</template>
