<!-- web/src/components/ui/HProgress.vue -->
<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{
  progress: number
  status?: 'active' | 'success' | 'failed'
}>()
const clamped = computed(() => Math.max(0, Math.min(100, props.progress)))
</script>
<template>
  <div class="progress-bar">
    <div
      class="progress-bar-fill"
      :class="status"
      :style="{ width: `${clamped}%` }"
    />
  </div>
</template>
