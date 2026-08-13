<!--
  VADCardV10.vue。ASR 前置 VAD 静音裁剪配置卡(可编辑)。
  - GET/PUT /api/config/vad。
  - 启用后:asr.HandleTask 在调 DashScope 前裁掉 >min_silence_sec 的静音段,
    产出 audio.asr.trimmed.mp3 + silence_map.json,ASR 返回后用 silence_map 反向映射回原始时间线。
  - 目的:减少 DashScope ASR 计费时长(实测 -40dB/2s 节省 3-10%)。
  - 零回归:失败/裁剪比过低/ffmpeg 缺 filter → 用原始音频,行为与关闭一致。
  - 详见 plans/plan-vad-2026-07-27.md。
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HCard, HButton, HInput, HSelect, HSwitch } from '@/components/ui'
import { getVADConfig, updateVADConfig } from '@/api/settings'

const emit = defineEmits<{ saved: [] }>()

const enabled = ref(true)
const engine = ref<'silence' | 'ina'>('silence')
const thresholdDb = ref(-40)
const minSilenceSec = ref(2)
const paddingSec = ref(0.2)
const minOutputRatio = ref(0.3)
const inaPython = ref('python3')
const inaScript = ref('scripts/ina_segment.py')
const inaBatchSize = ref(256)
const inaMinSpeechSec = ref(0.6)
const inaMergeGapSec = ref(0.4)
const saving = ref(false)

// HSelect 用 options 数组(threshold 预设五档)
const thresholdOptions = [
  { label: '-30 dB(保守,只裁极静音)', value: -30 },
  { label: '-35 dB', value: -35 },
  { label: '-40 dB(推荐)', value: -40 },
  { label: '-45 dB', value: -45 },
  { label: '-50 dB(激进,可能裁掉低音量说话)', value: -50 },
]
const engineOptions = [
  { label: 'inaSpeechSegmenter（语音 / 音乐 / 噪声）', value: 'ina' },
  { label: 'ffmpeg 静音检测（兼容模式）', value: 'silence' },
]

// HInput 的 modelValue 是 string,用 computed 做 string↔number 转换
const minSilenceStr = computed({
  get: () => String(minSilenceSec.value),
  set: (v: string) => { minSilenceSec.value = Number(v) || 0 },
})
const paddingStr = computed({
  get: () => String(paddingSec.value),
  set: (v: string) => { paddingSec.value = Number(v) || 0 },
})
const ratioStr = computed({
  get: () => String(minOutputRatio.value),
  set: (v: string) => { minOutputRatio.value = Number(v) || 0 },
})
const inaBatchSizeStr = computed({
  get: () => String(inaBatchSize.value),
  set: (v: string) => { inaBatchSize.value = Number(v) || 0 },
})
const inaMinSpeechStr = computed({
  get: () => String(inaMinSpeechSec.value),
  set: (v: string) => { inaMinSpeechSec.value = Number(v) || 0 },
})
const inaMergeGapStr = computed({
  get: () => String(inaMergeGapSec.value),
  set: (v: string) => { inaMergeGapSec.value = Number(v) || 0 },
})

async function fetchConfig() {
  try {
    const cfg = await getVADConfig()
    enabled.value = cfg.enabled
    engine.value = cfg.engine ?? 'silence'
    thresholdDb.value = cfg.threshold_db
    minSilenceSec.value = cfg.min_silence_sec
    paddingSec.value = cfg.padding_sec
    minOutputRatio.value = cfg.min_output_ratio
    // 滚动升级期间兼容尚未返回 ina 新字段的旧后端。
    inaPython.value = cfg.ina_python ?? 'python3'
    inaScript.value = cfg.ina_script ?? 'scripts/ina_segment.py'
    inaBatchSize.value = cfg.ina_batch_size ?? 256
    inaMinSpeechSec.value = cfg.ina_min_speech_sec ?? 0.6
    inaMergeGapSec.value = cfg.ina_merge_gap_sec ?? 0.4
  } catch { /* error shown by interceptor */ }
}

async function save() {
  saving.value = true
  try {
    await updateVADConfig({
      enabled: enabled.value,
      engine: engine.value,
      threshold_db: thresholdDb.value,
      min_silence_sec: minSilenceSec.value,
      padding_sec: paddingSec.value,
      min_output_ratio: minOutputRatio.value,
      ina_python: inaPython.value,
      ina_script: inaScript.value,
      ina_batch_size: inaBatchSize.value,
      ina_min_speech_sec: inaMinSpeechSec.value,
      ina_merge_gap_sec: inaMergeGapSec.value,
    })
    HMessage.success('VAD 配置已保存')
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
      <span class="card-title">ASR 前置分段</span>
    </template>

    <div class="form-hint" style="margin-bottom: 12px;">
      启用后,上传 ASR 前先在本地分段。inaSpeechSegmenter 模式只保留 speech，裁掉 music、noise
      和无人声片段；兼容模式只裁长静音。裁剪后音频与时间映射表
      (<code>silence_map.json</code>)会反向映射回原始时间线,所有下游(回顾/术语/弹幕)零改动。
      分段器故障会自动回退原始音频；明确检测为全程无说话时会跳过付费 ASR。
    </div>

    <div class="form-row-inline">
      <label class="form-label">启用分段</label>
      <div class="form-field">
        <HSwitch v-model="enabled" />
        <span class="field-hint">{{ enabled ? '已启用' : '已禁用（上传原始音频）' }}</span>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">分段引擎</label>
      <div class="form-field">
        <HSelect v-model="engine" :options="engineOptions" />
      </div>
    </div>

    <div v-if="engine === 'silence'" class="form-row-inline">
      <label class="form-label">静音阈值</label>
      <div class="form-field">
        <HSelect v-model="thresholdDb" :options="thresholdOptions" />
      </div>
    </div>

    <div v-if="engine === 'silence'" class="form-row-inline">
      <label class="form-label">最小静音时长(秒)</label>
      <div class="form-field">
        <HInput v-model="minSilenceStr" type="number" placeholder="2" />
        <span class="field-hint">超过此时长的静音才会被裁掉(推荐 2 秒)</span>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">缓冲时长(秒)</label>
      <div class="form-field">
        <HInput v-model="paddingStr" type="number" placeholder="0.2" />
        <span class="field-hint">说话段两端各保留的静音缓冲(避免裁得太硬,推荐 0.2 秒)</span>
      </div>
    </div>

    <template v-if="engine === 'ina'">
      <div class="form-row-inline">
        <label class="form-label">Python 环境</label>
        <div class="form-field">
          <HInput v-model="inaPython" placeholder="python3" />
          <span class="field-hint">建议使用已安装 inaSpeechSegmenter 与 TensorFlow 的独立虚拟环境</span>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">分段脚本</label>
        <div class="form-field">
          <HInput v-model="inaScript" placeholder="scripts/ina_segment.py" />
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">GPU batch</label>
        <div class="form-field">
          <HInput v-model="inaBatchSizeStr" type="number" placeholder="256" />
          <span class="field-hint">高显存 GPU 可使用 256；显存不足时调低</span>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">最小说话段(秒)</label>
        <div class="form-field">
          <HInput v-model="inaMinSpeechStr" type="number" placeholder="0.6" />
          <span class="field-hint">忽略更短的 speech 毛刺</span>
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">合并间隔(秒)</label>
        <div class="form-field">
          <HInput v-model="inaMergeGapStr" type="number" placeholder="0.4" />
          <span class="field-hint">合并相邻说话片段，减少 ffmpeg 切片数量</span>
        </div>
      </div>
    </template>

    <div v-if="engine === 'silence'" class="form-row-inline">
      <label class="form-label">安全比例</label>
      <div class="form-field">
        <HInput v-model="ratioStr" type="number" placeholder="0.3" />
        <span class="field-hint">裁剪后 / 原始 低于此值视为异常,自动回退原始音频(防 ffmpeg bug 裁过头,推荐 0.3)</span>
      </div>
    </div>

    <div class="card-actions">
      <HButton variant="primary" :loading="saving" @click="save">保存配置</HButton>
    </div>
  </HCard>
</template>
