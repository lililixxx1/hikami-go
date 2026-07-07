<!-- web/src/components/ui/HPill.vue -->
<script setup lang="ts">
defineProps<{
  variant?: 'success' | 'warning' | 'danger' | 'info' | 'neutral'
}>()
</script>
<template>
  <span class="pill" :class="variant ? `pill-${variant}` : 'pill-neutral'">
    <span class="pill-dot" /><slot />
  </span>
</template>
