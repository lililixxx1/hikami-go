<!-- web/src/components/ui/HCard.vue -->
<script setup lang="ts">
defineProps<{ title?: string }>()
</script>
<template>
  <div class="card">
    <div v-if="title || $slots.header" class="card-header">
      <slot name="header"><span class="card-title">{{ title }}</span></slot>
    </div>
    <div class="card-body"><slot /></div>
  </div>
</template>
