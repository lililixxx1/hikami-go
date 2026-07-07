<!-- web/src/components/ui/HCheckbox.vue -->
<script setup lang="ts">
defineProps<{ modelValue: boolean; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>
<template>
  <label class="checkbox-row" :class="{ disabled }">
    <button
      type="button"
      class="checkbox"
      :class="{ checked: modelValue }"
      :disabled="disabled"
      role="checkbox"
      :aria-checked="modelValue"
      @click="emit('update:modelValue', !modelValue)"
    />
    <span v-if="$slots.default"><slot /></span>
  </label>
</template>
