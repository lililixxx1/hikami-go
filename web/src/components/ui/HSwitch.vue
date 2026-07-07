<!-- web/src/components/ui/HSwitch.vue -->
<script setup lang="ts">
defineProps<{ modelValue: boolean; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>
<template>
  <div class="toggle-row">
    <label v-if="$slots.default" class="toggle-label"><slot /></label>
    <button
      type="button"
      class="toggle"
      :class="{ on: modelValue }"
      :disabled="disabled"
      role="switch"
      :aria-checked="modelValue"
      @click="emit('update:modelValue', !modelValue)"
    />
  </div>
</template>
