<!--
  ToolsCardV10.vue(Phase 5 Task 5.4)。外部工具检测卡。
  props.tools: ToolStatus[](来自 runtime status.tools)。
  Template:HCard + 表格(name/path/available/error/install_hint)。
  available 列用彩色 ✓/× 标记。纯展示,无 API 调用。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { HCard } from '@/components/ui'
import type { ToolStatus } from '@/api/types'

defineProps<{
  tools: ToolStatus[]
}>()
</script>

<template>
  <HCard title="外部工具">
    <table v-if="tools.length" class="tool-table">
      <thead>
        <tr>
          <th>工具</th>
          <th>路径</th>
          <th style="width: 80px;">状态</th>
          <th>错误 / 安装提示</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="tool in tools" :key="tool.name">
          <td style="font-weight: 550; color: var(--text);">{{ tool.name }}</td>
          <td><code style="font-size: 12px; color: var(--text-secondary);">{{ tool.path || '-' }}</code></td>
          <td>
            <span v-if="tool.available" style="color: var(--success); font-weight: 600;">✓ 可用</span>
            <span v-else style="color: var(--danger); font-weight: 600;">✗ 缺失</span>
          </td>
          <td>
            <div v-if="tool.error" style="color: var(--danger); font-size: 12px;">{{ tool.error }}</div>
            <div v-if="tool.install_hint" style="color: var(--text-muted); font-size: 12px;">
              安装:{{ tool.install_hint }}
            </div>
            <span v-if="!tool.error && !tool.install_hint">-</span>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="form-hint">暂无工具检测结果。</div>
  </HCard>
</template>
