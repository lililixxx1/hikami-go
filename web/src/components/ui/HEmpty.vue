<!-- web/src/components/ui/HEmpty.vue -->
<script setup lang="ts">
withDefaults(defineProps<{ description?: string }>(), { description: '暂无数据' })
</script>
<template>
  <div class="empty-state">
    <div class="empty-state-icon">
      <svg viewBox="0 0 64 64" width="48" height="48" fill="none" stroke="currentColor" stroke-width="2">
        <rect x="10" y="14" width="44" height="32" rx="4" />
        <path d="M10 24h44" />
        <circle cx="16" cy="19" r="1" fill="currentColor" stroke="none" />
        <circle cx="20" cy="19" r="1" fill="currentColor" stroke="none" />
        <path d="M26 38c2-3 5-4 8-4s6 1 8 4" stroke-linecap="round" />
      </svg>
    </div>
    <p class="empty-state-text">{{ description }}</p>
    <div v-if="$slots.default" class="empty-state-actions"><slot /></div>
  </div>
</template>
