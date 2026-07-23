<!-- web/src/features/home/components/DashboardSection.vue -->
<script setup lang="ts">
import type { DashboardData } from '@/api/types-derived'
import { HDescriptions, HTable } from '@/components/ui'
import { computed } from 'vue'

const props = defineProps<{
  dashboard: DashboardData | null
  currentMonth: string
}>()

const summaryItems = computed(() => {
  const monthCount = props.dashboard?.sessions_by_month.find((m) => m.month === props.currentMonth)?.session_count ?? 0
  const channelCount = props.dashboard?.sessions_by_channel.length ?? 0
  return [
    { label: '本月场次', value: monthCount },
    { label: '总主播数', value: channelCount },
  ]
})

const monthColumns = [
  { key: 'month', label: '月份' },
  { key: 'session_count', label: '场次数' },
]

const channelColumns = [
  { key: 'channel_name', label: '主播' },
  { key: 'session_count', label: '场次' },
]

const costColumns = [
  { key: 'month', label: '月份' },
  { key: 'asr_hours', label: 'ASR 时长(h)' },
  { key: 'asr_cost', label: 'ASR 成本(¥)' },
]

function fixedNumber(value: number | undefined, digits = 1): string {
  return Number(value || 0).toFixed(digits)
}
</script>

<template>
  <section v-if="dashboard" class="section">
    <div class="section-header">
      <h3 class="section-title">统计仪表板</h3>
    </div>

    <HDescriptions :items="summaryItems" :column="2" class="dashboard-summary" />

    <div class="dashboard-grid">
      <div class="dashboard-block">
        <h4>月度场次</h4>
        <HTable :columns="monthColumns" :data="dashboard.sessions_by_month" />
      </div>
      <div class="dashboard-block">
        <h4>主播场次排名</h4>
        <HTable :columns="channelColumns" :data="dashboard.sessions_by_channel" />
      </div>
      <div class="dashboard-block">
        <h4>费用趋势</h4>
        <HTable :columns="costColumns" :data="dashboard.cost_trend">
          <template #cell-asr_hours="{ row }">{{ fixedNumber(row.asr_hours) }}</template>
          <template #cell-asr_cost="{ row }">¥{{ fixedNumber(row.asr_cost) }}</template>
        </HTable>
      </div>
    </div>
  </section>
</template>

<style scoped>
.section-header {
  margin-bottom: 12px;
}

.section-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}

.dashboard-summary {
  margin-bottom: 16px;
}

.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.dashboard-block h4 {
  margin: 0 0 10px;
  font-size: 14px;
  color: var(--text);
}

/* cost_trend 占整行 */
.dashboard-block:last-child {
  grid-column: 1 / -1;
}

@media (max-width: 900px) {
  .dashboard-grid {
    grid-template-columns: 1fr;
  }
}
</style>
