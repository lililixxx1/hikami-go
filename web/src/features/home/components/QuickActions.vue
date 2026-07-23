<!-- web/src/features/home/components/QuickActions.vue -->
<script setup lang="ts">
import { HButton } from '@/components/ui'

const emit = defineEmits<{
  'add-streamer': []
  discover: []
}>()
</script>

<template>
  <div class="quick-actions">
    <HButton variant="primary" @click="emit('add-streamer')">添加主播</HButton>
    <HButton variant="secondary" @click="emit('discover')">发现回放</HButton>
  </div>
</template>

<style scoped>
.quick-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
</style>
