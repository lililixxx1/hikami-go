<!-- web/src/components/ui/HCollapseItem.vue -->
<script setup lang="ts">
import { inject, computed } from 'vue'
const props = defineProps<{ name: string; title: string }>()
const toggle = inject<(name: string) => void>('h-collapse-toggle')!
const openSet = inject<() => Set<string>>('h-collapse-open-set')!
const isOpen = computed(() => openSet().has(props.name))
</script>
<template>
  <div class="collapse-item" :class="{ open: isOpen }">
    <div class="collapse-trigger" @click="toggle(name)">
      <span class="collapse-arrow" :class="{ open: isOpen }">›</span>
      <span>{{ title }}</span>
    </div>
    <div v-show="isOpen" class="collapse-content"><slot /></div>
  </div>
</template>
