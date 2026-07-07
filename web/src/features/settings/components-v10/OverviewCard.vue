<!--
  OverviewCard.vue(Phase 5 Task 5.1)。能力总览卡。
  4 个能力卡(asr/recap/webdav/publish),每个 done/未完成 dot + label + reason + "配置"按钮 → emit navigate。
  复用 useSettingsOverview composable(从旧 SettingsView.vue 行 55-113 抽出)。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { HCard, HButton, HPill } from '@/components/ui'
import { useSettingsOverview } from '@/features/settings/composables/useSettingsOverview'
import type { Capabilities, ConfigStatus } from '@/api/types'

const props = defineProps<{
  capabilities: Capabilities | null
  configStatus: ConfigStatus | null
}>()

const emit = defineEmits<{ navigate: [section: string] }>()

const { overviewItems, overviewDoneCount } = useSettingsOverview(
  () => props.capabilities,
  () => props.configStatus,
)

function onAction(target: string) {
  emit('navigate', target)
}
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">系统能力总览</span>
      <HPill :variant="overviewDoneCount >= overviewItems.length ? 'success' : 'warning'">
        {{ overviewDoneCount }}/{{ overviewItems.length }} 就绪
      </HPill>
    </template>

    <div v-if="overviewItems.length" class="cap-grid">
      <div
        v-for="item in overviewItems"
        :key="item.key"
        class="cap-card"
        :class="{ done: item.done }"
      >
        <div class="cap-head">
          <span class="cap-dot" :class="item.ok ? 'ok' : 'bad'" />
          <span class="cap-title">{{ item.label }}</span>
          <HPill :variant="item.done ? 'success' : 'danger'">
            {{ item.done ? '已配置' : '未配置' }}
          </HPill>
        </div>
        <div class="cap-reason">{{ item.reason || (item.ok ? '运行正常' : '') }}</div>
        <div style="margin-top: 4px;">
          <HButton size="sm" variant="ghost" @click="onAction(item.actionTarget)">
            {{ item.actionLabel }}
          </HButton>
        </div>
      </div>
    </div>
    <div v-else style="color: var(--text-muted); font-size: 13px;">加载中…</div>
  </HCard>
</template>
