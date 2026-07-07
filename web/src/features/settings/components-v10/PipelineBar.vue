<!--
  PipelineBar.vue(Phase 5 Task 5.1)。V10 设置页顶部能力流水线。
  6 段 pipeline-stage:source(下载源)/ media(临时音频)/ asr(转写)/ recap(回顾)/ upload(WebDAV)/ publish(发布)。
  每段 pipeline-dot 状态(done/partial/pending)派生自 capabilities + configStatus。
  点击 emit navigate(target)(壳滚动到对应配置卡)。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { Capabilities, ConfigStatus } from '@/api/types'

type DotStatus = 'done' | 'partial' | 'pending'

interface Stage {
  index: number
  name: string
  status: string
  dot: DotStatus
  target: string
}

const props = defineProps<{
  capabilities: Capabilities | null
  configStatus: ConfigStatus | null
}>()

const emit = defineEmits<{ navigate: [target: string] }>()

const stages = computed<Stage[]>(() => {
  const caps = props.capabilities
  const cs = props.configStatus
  if (!caps || !cs) return []

  // source:有任意下载/回放能力即视为就绪(回放源 + 账号)
  const sourceReady = caps.replay_download
  const sourceStatus = sourceReady ? '回放源就绪' : '未配置下载源'

  // media:临时音频后端。asr_temp_configured=true 用对象存储,否则走本地 HTTP(默认可用)
  const mediaReady = cs.asr_temp_configured || true
  const mediaStatus = cs.asr_temp_configured ? '对象存储已配置' : '本地 HTTP 默认'

  // asr:DashScope
  const asrReady = caps.asr_submit
  const asrStatus = cs.dashscope_key_set
    ? (asrReady ? 'DashScope 就绪' : '转写能力异常')
    : 'DashScope 未配置'

  // recap:回顾 AI
  const recapReady = caps.recap_generate
  const recapStatus = cs.recap_key_set
    ? (recapReady ? '回顾模型就绪' : '回顾能力异常')
    : '回顾密钥未配置'

  // upload:WebDAV
  const uploadReady = caps.webdav_upload
  const uploadStatus = cs.webdav_configured
    ? (uploadReady ? 'WebDAV 就绪' : '上传能力异常')
    : 'WebDAV 未配置'

  // publish:B站专栏
  const publishReady = caps.publish_opus
  const publishStatus = cs.publish_enabled
    ? (publishReady ? '发布就绪' : '发布能力异常')
    : '发布未启用'

  return [
    { index: 1, name: '录制', status: sourceStatus, dot: sourceReady ? 'done' : 'pending', target: 'accounts' },
    { index: 2, name: '临时音频', status: mediaStatus, dot: mediaReady ? 'done' : 'pending', target: 'asr-s3' },
    { index: 3, name: '转写', status: asrStatus, dot: asrReady ? 'done' : (cs.dashscope_key_set ? 'partial' : 'pending'), target: 'dashscope' },
    { index: 4, name: '回顾', status: recapStatus, dot: recapReady ? 'done' : (cs.recap_key_set ? 'partial' : 'pending'), target: 'recap' },
    { index: 5, name: '上传', status: uploadStatus, dot: uploadReady ? 'done' : (cs.webdav_configured ? 'partial' : 'pending'), target: 'webdav' },
    { index: 6, name: '发布', status: publishStatus, dot: publishReady ? 'done' : (cs.publish_enabled ? 'partial' : 'pending'), target: 'publish' },
  ]
})

// 连线状态:前置阶段就绪则连线 done
function connectorDone(i: number): boolean {
  return stages.value.slice(0, i).every(s => s.dot === 'done')
}

function onNavigate(target: string) {
  emit('navigate', target)
}
</script>

<template>
  <div v-if="stages.length" class="pipeline">
    <template v-for="(s, i) in stages" :key="s.index">
      <div class="pipeline-stage pipeline-clickable" @click="onNavigate(s.target)">
        <div class="pipeline-dot" :class="s.dot">{{ s.index }}</div>
        <div class="pipeline-info">
          <div class="pipeline-name">{{ s.name }}</div>
          <div class="pipeline-status">{{ s.status }}</div>
        </div>
      </div>
      <div
        v-if="i < stages.length - 1"
        class="pipeline-connector"
        :class="{ done: connectorDone(i + 1) }"
      />
    </template>
  </div>
</template>
