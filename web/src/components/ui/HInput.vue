<!-- web/src/components/ui/HInput.vue -->
<script setup lang="ts">
withDefaults(defineProps<{
  modelValue?: string
  size?: 'sm' | 'md'
  disabled?: boolean
  placeholder?: string
}>(), { modelValue: '', size: 'md' })
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
      :value="modelValue"
      :placeholder="placeholder"
      :disabled="disabled"
      @input="onInput"
    >
  </label>
</template>
