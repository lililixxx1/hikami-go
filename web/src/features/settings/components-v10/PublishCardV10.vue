<!--
  PublishCardV10.vue(Phase 5 Task 5.2)。专栏投稿设置卡。
  移植自 PublishSettingsCard.vue(EP)。endpoint /api/config/publish。
  - enabled 开关 + mode(draft/publish)。
  - private_pub(可见范围 radio 用 HSelect 替代)、cover_url、auto_cover、original/aigc 开关。
  - 高级:close_comment/up_choose_comment 开关、timer_pub_time(定时发布开关+数字秒)、
    话题 select(searchBiliTopics 防抖搜索 → topicOptions)、标签、文集 select(listBiliSeries 懒加载)、
    category_id(专家模式)。
  - 话题搜索:HInput 触发 debounce 300ms → searchBiliTopics;选中后用 HSelect 选 topic_id。
    (HSelect 不支持 remote,故拆成"搜索框 + 结果下拉"两控件。)
  - timerEnabled computed 派生(>0 为开);category_id 等数字字段用 computed 代理。
  L3 视觉验证,无单测。
-->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { HCard, HButton, HInput, HSelect, HSwitch } from '@/components/ui'
import { getPublishConfig, updatePublishConfig, searchBiliTopics, listBiliSeries } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { PublishConfig, BiliTopic, BiliSeries } from '@/api/types'

const props = defineProps<{ isExpert?: boolean }>()
const emit = defineEmits<{ saved: [] }>()
const runtimeStore = useRuntimeStore()

const config = ref<PublishConfig>({
  enabled: false, mode: 'draft', category_id: 15, list_id: 0,
  private_pub: 2, summary_len: 100, original: 0, aigc: 0, timer_pub_time: 0,
  cover_url: '', auto_cover: true, topics: '', topic_id: 0, topic_name: '', close_comment: 0, up_choose_comment: 0,
})
const saving = ref(false)
const advancedOpen = ref(false)

const modeOptions = [
  { label: '保存为草稿', value: 'draft' },
  { label: '发布', value: 'publish' },
]
const privateOptions = [
  { label: '所有人可见', value: 2 },
  { label: '仅自己可见', value: 1 },
]

// 定时发布开关(>0 为开;开时默认当前+2h,关时清 0)
const timerEnabled = computed({
  get: () => config.value.timer_pub_time > 0,
  set: (enabled: boolean) => {
    config.value.timer_pub_time = enabled ? Math.floor(Date.now() / 1000) + 7200 : 0
  },
})
// 数字字段代理(HInput 只接受 string)
const categoryId = computed({ get: () => String(config.value.category_id), set: (v: string) => { config.value.category_id = Number(v) || 0 } })
const timerPubTime = computed({ get: () => String(config.value.timer_pub_time), set: (v: string) => { config.value.timer_pub_time = Number(v) || 0 } })

function formatPublishTime(timestamp: number): string {
  if (!timestamp) return ''
  return new Date(timestamp * 1000).toLocaleString('zh-CN', { hour12: false })
}

// original/aigc/close_comment/up_choose_comment 是 0/1 数字,用开关代理
const originalProxy = computed({ get: () => config.value.original === 1, set: (v: boolean) => { config.value.original = v ? 1 : 0 } })
const aigcProxy = computed({ get: () => config.value.aigc === 1, set: (v: boolean) => { config.value.aigc = v ? 1 : 0 } })
// close_comment 反直觉:0=开评论 1=关评论,故 on=0(开评论)
const closeCommentProxy = computed({ get: () => config.value.close_comment === 0, set: (v: boolean) => { config.value.close_comment = v ? 0 : 1 } })
const upChooseCommentProxy = computed({ get: () => config.value.up_choose_comment === 1, set: (v: boolean) => { config.value.up_choose_comment = v ? 1 : 0 } })

async function fetchConfig() {
  try {
    config.value = await getPublishConfig()
  } catch { /* ignore */ }
}

async function save() {
  saving.value = true
  try {
    config.value = await updatePublishConfig(config.value)
    ElMessage.success('发布设置已保存')
    await runtimeStore.fetchRuntime(true)
    emit('saved')
  } finally {
    saving.value = false
  }
}

async function saveDraft() {
  config.value.mode = 'draft'
  await save()
}

// --- Topic 搜索(本地 debounce 300ms) ---
const topicQuery = ref('')
const topicOptions = ref<BiliTopic[]>([])
const topicsLoading = ref(false)
let topicTimer: ReturnType<typeof setTimeout> | null = null

async function onTopicQuery(q: string) {
  topicQuery.value = q
  if (topicTimer) clearTimeout(topicTimer)
  if (!q || q.trim().length < 2) {
    topicOptions.value = []
    return
  }
  topicTimer = setTimeout(async () => {
    topicsLoading.value = true
    try {
      const res = await searchBiliTopics(q.trim())
      topicOptions.value = res.items ?? []
    } catch {
      topicOptions.value = []
    } finally {
      topicsLoading.value = false
    }
  }, 300)
}

// 选择话题后同步 topic_name(后端需要 name 展示)
watch(() => config.value.topic_id, (id) => {
  if (!id) {
    config.value.topic_name = ''
    return
  }
  const found = topicOptions.value.find((t) => t.id === id)
  if (found) config.value.topic_name = found.name
})

// --- 文集列表(首次展开下拉时懒加载) ---
const seriesOptions = ref<BiliSeries[]>([])
const seriesLoading = ref(false)
const seriesError = ref('')

async function loadSeriesList() {
  if (seriesOptions.value.length > 0 || seriesLoading.value) return
  seriesLoading.value = true
  seriesError.value = ''
  try {
    const res = await listBiliSeries()
    if (res.error) {
      seriesError.value = res.error
      seriesOptions.value = []
    } else {
      seriesOptions.value = res.items ?? []
    }
  } catch {
    seriesError.value = '加载文集列表失败'
    seriesOptions.value = []
  } finally {
    seriesLoading.value = false
  }
}

const seriesSelectOptions = computed(() => {
  const opts: { label: string; value: number }[] = [{ label: '不加入文集', value: 0 }]
  for (const s of seriesOptions.value) opts.push({ label: `${s.name}(${s.articles_count} 篇)`, value: s.id })
  return opts
})

const topicSelectOptions = computed(() =>
  topicOptions.value.map(t => ({ label: t.name, value: t.id })),
)

onMounted(fetchConfig)
defineExpose({ reload: fetchConfig })
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">专栏投稿设置</span>
      <div style="display:flex; align-items:center; gap:12px;">
        <HSwitch v-model="config.enabled">{{ config.enabled ? '启用发布' : '关闭发布' }}</HSwitch>
        <HSelect v-model="config.mode" :options="modeOptions" />
      </div>
    </template>

    <div class="form-row-inline">
      <label class="form-label">可见范围</label>
      <div class="form-field">
        <HSelect v-model="config.private_pub" :options="privateOptions" />
        <div class="form-hint">仅自己可见时不支持分享和商业推广。</div>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">自定义封面</label>
      <div class="form-field">
        <HInput v-model="config.cover_url" placeholder="留空时使用 recap/cover.png 或自动抓取官方封面" />
        <div class="form-hint">最高优先级。留空时依次回退到回顾目录封面、官方源封面。</div>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">自动取源封面</label>
      <div class="form-field">
        <HSwitch v-model="config.auto_cover">自定义与回顾目录封面均未命中时,自动下载官方封面作为兜底</HSwitch>
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">创作声明</label>
      <div class="form-field" style="display:flex; flex-direction:column; gap:10px;">
        <HSwitch v-model="originalProxy">声明此文章为原创,未经授权禁止转载</HSwitch>
        <div>
          <HSwitch v-model="aigcProxy">AI 辅助创作声明</HSwitch>
          <div class="form-hint">勾选后,标识该内容使用人工智能合成技术。</div>
        </div>
      </div>
    </div>

    <button class="collapse-trigger" type="button" @click="advancedOpen = !advancedOpen">
      <span class="collapse-arrow" :class="{ open: advancedOpen }">›</span> 详细投稿参数
    </button>
    <div v-show="advancedOpen" class="collapse-content">
      <div class="form-row-inline">
        <label class="form-label">评论开关</label>
        <div class="form-field">
          <HSwitch v-model="closeCommentProxy">{{ closeCommentProxy ? '允许评论' : '关闭评论' }}</HSwitch>
          <div class="form-hint">关闭后,用户无法在你的专栏发表评论。</div>
        </div>
      </div>

      <div class="form-row-inline">
        <label class="form-label">精选评论</label>
        <div class="form-field">
          <HSwitch v-model="upChooseCommentProxy">开启后,经过筛选的评论才会向用户展示</HSwitch>
        </div>
      </div>

      <div class="form-row-inline">
        <label class="form-label">定时发布</label>
        <div class="form-field">
          <HSwitch v-model="timerEnabled" />
          <div v-if="timerEnabled" style="margin-top:8px;">
            <HInput v-model="timerPubTime" placeholder="定时时间戳(秒)" />
            <div class="form-hint">当前时间:{{ formatPublishTime(config.timer_pub_time) }},以北京时间 UTC+8 为准。</div>
          </div>
          <div v-else class="form-hint">可选时间为当前 2 小时到 7 天内。</div>
        </div>
      </div>

      <div class="form-row-inline">
        <label class="form-label">话题</label>
        <div class="form-field">
          <HInput :model-value="topicQuery" placeholder="搜索话题(2 字以上)" @update:model-value="onTopicQuery" />
          <HSelect v-model="config.topic_id" :options="topicSelectOptions" :disabled="topicsLoading" style="margin-top:8px;" />
          <div v-if="topicsLoading" class="form-hint">搜索中…</div>
          <div v-else-if="config.topic_name" class="form-hint">已选:{{ config.topic_name }}</div>
        </div>
      </div>

      <div class="form-row-inline">
        <label class="form-label">标签</label>
        <div class="form-field">
          <HInput v-model="config.topics" placeholder="多个标签用逗号分隔" />
          <div class="form-hint">自由文本标签,不同于话题。</div>
        </div>
      </div>

      <div class="form-row-inline">
        <label class="form-label">文集</label>
        <div class="form-field">
          <HSelect
            v-model="config.list_id"
            :options="seriesSelectOptions"
            :disabled="seriesLoading"
            @click="loadSeriesList"
          />
          <div v-if="seriesError" class="form-hint" style="color: var(--warning);">{{ seriesError }}</div>
        </div>
      </div>

      <div v-if="props.isExpert" class="form-row-inline">
        <label class="form-label">分区</label>
        <div class="form-field">
          <HInput v-model="categoryId" placeholder="15" />
          <div class="form-hint">B 站专栏分区 ID,默认 15。</div>
        </div>
      </div>
    </div>

    <div class="card-actions">
      <HButton :loading="saving" @click="saveDraft">保存为草稿</HButton>
      <HButton variant="primary" :loading="saving" @click="save">保存设置</HButton>
    </div>
  </HCard>
</template>
