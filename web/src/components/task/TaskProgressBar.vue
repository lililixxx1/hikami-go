<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  progress: number
  status: string
}>()

const percentage = computed(() => {
  const val = Math.max(0, Math.min(100, props.progress))
  return Math.round(val)
})

const progressStatus = computed<'success' | 'warning' | 'exception' | undefined>(() => {
  if (props.status === 'succeeded') return 'success'
  if (props.status === 'running') return undefined
  if (props.status === 'failed') return 'exception'
  if (props.status === 'cancelled') return 'warning'
  return undefined
})
</script>

<template>
  <el-progress
    :percentage="percentage"
    :status="progressStatus"
    :stroke-width="14"
    :show-text="true"
    :text-inside="percentage > 10"
    striped
    :striped-flow="status === 'running'"
  />
</template>
