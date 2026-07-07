<!-- web/src/features/home/components/RecordingDuration.vue -->
<script setup lang="ts">
import { useElapsedDuration } from '../composables/useElapsedDuration'
const props = defineProps<{ startedAt: string }>()
const { text } = useElapsedDuration(() => props.startedAt)
</script>
<template>
  <span class="live-meta">已录制 {{ text }}</span>
</template>
