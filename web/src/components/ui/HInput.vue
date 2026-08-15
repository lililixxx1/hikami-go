<!-- web/src/components/ui/HInput.vue -->
<script setup lang="ts">
withDefaults(defineProps<{
  modelValue?: string
  size?: 'sm' | 'md'
  disabled?: boolean
  placeholder?: string
  type?: string
  list?: string
}>(), { modelValue: '', size: 'md', type: 'text' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLInputElement).value)
}
</script>
<template>
  <label class="form-field">
    <span v-if="$slots.label" class="form-label"><slot name="label" /></span>
    <input
      class="input"
      :class="size ? `input-${size}` : ''"
      :type="type"
      :list="list || undefined"
      :value="modelValue"
      :placeholder="placeholder"
      :disabled="disabled"
      @input="onInput"
    >
  </label>
</template>
