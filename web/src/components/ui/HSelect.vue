<!-- web/src/components/ui/HSelect.vue -->
<script setup lang="ts">
defineProps<{
  modelValue: string | number
  options: { label: string; value: string | number }[]
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
function onChange(e: Event) {
  emit('update:modelValue', (e.target as HTMLSelectElement).value)
}
</script>
<template>
  <label class="form-field">
    <span v-if="$slots.label" class="form-label"><slot name="label" /></span>
    <select
      class="select"
      :value="modelValue"
      :disabled="disabled"
      @change="onChange"
    >
      <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
    </select>
  </label>
</template>
