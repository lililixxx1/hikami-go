<!--
  ToolsCardV10.vue。外部工具路径配置卡(可编辑)。
  - 上半部:yt_dlp / rclone 路径编辑表单(GET/PUT /api/config/tools)。
    保存后 refreshRuntimeStatus 会重新 Probe,emit('saved') 让壳重拉 runtime 刷新探测表。
  - 下半部:只读工具探测结果表(ffmpeg/ffprobe/yt-dlp/rclone + claude/codex 的 available/path/error/install_hint)。
    props.tools 由壳从 runtime status.tools 传入。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HCard, HButton, HInput } from '@/components/ui'
import { getToolsConfig, updateToolsConfig } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { ToolStatus } from '@/api/types-derived'

defineProps<{
  tools: ToolStatus[]
}>()

const emit = defineEmits<{ saved: [] }>()
const runtimeStore = useRuntimeStore()

const ytDlp = ref('')
const rclone = ref('')
const saving = ref(false)

async function fetchConfig() {
  try {
    const cfg = await getToolsConfig()
    ytDlp.value = cfg.yt_dlp
    rclone.value = cfg.rclone
  } catch { /* error shown by interceptor */ }
}

async function save() {
  saving.value = true
  try {
    await updateToolsConfig({ yt_dlp: ytDlp.value, rclone: rclone.value })
    HMessage.success('工具路径已保存,已重新探测')
    await runtimeStore.fetchRuntime(true)
    emit('saved')
  } catch { /* error shown by interceptor */ }
  finally {
    saving.value = false
  }
}

onMounted(fetchConfig)
defineExpose({ reload: fetchConfig })
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">外部工具</span>
    </template>

    <div class="form-hint" style="margin-bottom: 12px;">
      手动指定 yt-dlp 与 rclone 的可执行文件路径(留空则走系统 PATH 探测)。保存后会立即重新探测可用性,下方表格会刷新。ffmpeg/ffprobe 为硬依赖(改错会阻止启动),仍需在 config.yaml 修改。
    </div>

    <div class="form-row-inline">
      <label class="form-label">yt-dlp 路径</label>
      <div class="form-field">
        <HInput v-model="ytDlp" placeholder="留空使用系统 PATH 探测(如 yt-dlp 或 /usr/local/bin/yt-dlp)" />
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">rclone 路径</label>
      <div class="form-field">
        <HInput v-model="rclone" placeholder="留空使用系统 PATH 探测(如 rclone 或 /usr/bin/rclone)" />
      </div>
    </div>

    <div class="card-actions">
      <HButton variant="primary" :loading="saving" @click="save">保存路径</HButton>
    </div>

    <!-- 下方只读探测结果表:由壳从 runtime status.tools 传入,保存后自动刷新 -->
    <div v-if="tools.length" style="margin-top: 20px;">
      <div class="card-title" style="font-size: 13px; color: var(--text-muted); margin-bottom: 8px;">当前探测结果</div>
      <table class="tool-table">
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
    </div>
  </HCard>
</template>
