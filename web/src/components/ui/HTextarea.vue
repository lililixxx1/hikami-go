<!-- web/src/components/ui/HTextarea.vue -->
<script setup lang="ts">
withDefaults(defineProps<{
  modelValue: string
  rows?: number
  disabled?: boolean
  placeholder?: string
}>(), { rows: 4 })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLTextAreaElement).value)
}
</script>
<template>
  <label class="form-field">
    <span v-if="$slots.label" class="form-label"><slot name="label" /></span>
    <textarea
      class="textarea"
      :value="modelValue"
      :rows="rows"
      :placeholder="placeholder"
      :disabled="disabled"
      @input="onInput"
    />
  </label>
</template>
