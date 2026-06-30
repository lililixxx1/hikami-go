<script setup lang="ts">
import './settings-cards.css'
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { getPublishConfig, updatePublishConfig, searchBiliTopics, listBiliSeries } from '@/api/settings'
import { useRuntimeStore } from '@/stores/runtime'
import type { PublishConfig, BiliTopic, BiliSeries } from '@/api/types'

defineProps<{
  isExpert: boolean
}>()

const emit = defineEmits<{
  saved: []
}>()

const runtimeStore = useRuntimeStore()

const config = ref<PublishConfig>({
  enabled: false, mode: 'draft', category_id: 15, list_id: 0,
  private_pub: 2, summary_len: 100, original: 0, aigc: 0, timer_pub_time: 0,
  cover_url: '', auto_cover: true, topics: '', topic_id: 0, topic_name: '', close_comment: 0, up_choose_comment: 0,
})
const saving = ref(false)

// 定时发布开关(>0 为开;开时默认当前+2h,关时清 0)
const timerEnabled = computed({
  get: () => config.value.timer_pub_time > 0,
  set: (enabled: boolean) => {
    config.value.timer_pub_time = enabled ? Math.floor(Date.now() / 1000) + 7200 : 0
  },
})

function formatPublishTime(timestamp: number) {
  if (!timestamp) return ''
  return new Date(timestamp * 1000).toLocaleString('zh-CN', { hour12: false })
}

function minPublishTime() {
  return Math.floor(Date.now() / 1000) + 7200
}

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
const topicOptions = ref<BiliTopic[]>([])
const topicsLoading = ref(false)
let topicTimer: ReturnType<typeof setTimeout> | null = null

async function searchTopics(query: string) {
  if (topicTimer) clearTimeout(topicTimer)
  if (!query || query.trim().length < 2) {
    topicOptions.value = []
    return
  }
  topicTimer = setTimeout(async () => {
    topicsLoading.value = true
    try {
      const res = await searchBiliTopics(query.trim())
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

function handleSeriesDropdownOpen() {
  loadSeriesList()
}

onMounted(fetchConfig)
defineExpose({ reload: fetchConfig })
</script>

<template>
  <div class="settings-card" data-section="publish">
    <div class="card-header-row">
      <h3>专栏投稿设置</h3>
      <div class="publish-mode-control">
        <el-switch v-model="config.enabled" active-text="启用发布" inactive-text="关闭发布" />
        <el-radio-group v-model="config.mode" size="small">
          <el-radio-button value="draft">保存为草稿</el-radio-button>
          <el-radio-button value="publish">发布</el-radio-button>
        </el-radio-group>
      </div>
    </div>
    <div class="column-form">
      <div class="column-row">
        <div class="column-label">可见范围</div>
        <div class="column-control">
          <el-radio-group v-model="config.private_pub" size="small">
            <el-radio :value="2">所有人可见</el-radio>
            <el-radio :value="1">仅自己可见</el-radio>
          </el-radio-group>
        </div>
        <div class="column-note">仅自己可见时不支持分享和商业推广。</div>
      </div>

      <div class="column-row">
        <div class="column-label">自定义封面</div>
        <div class="column-control">
          <el-input v-model="config.cover_url" size="small" clearable placeholder="未设置时自动使用 recap/cover.png" style="width: 320px" />
        </div>
        <div class="column-note">可填写封面图片 URL 或本地路径；留空时自动抓取回顾目录封面。</div>
      </div>

      <div class="column-row">
        <div class="column-label">自动取源封面</div>
        <div class="column-control">
          <el-switch v-model="config.auto_cover" />
        </div>
        <div class="column-note">开启后自动下载视频/直播官方封面作为回顾封面；关闭则仅使用上方自定义封面。</div>
      </div>

      <div class="column-row">
        <div class="column-label">创作声明</div>
        <div class="column-control declaration-list">
          <label class="declaration-item">
            <el-switch v-model="config.original" :active-value="1" :inactive-value="0" size="small" />
            <span>声明此文章为原创，未经授权禁止转载</span>
          </label>
          <label class="declaration-item two-line">
            <el-switch v-model="config.aigc" :active-value="1" :inactive-value="0" size="small" />
            <span>
              <strong>AI 辅助创作声明</strong>
              <small>勾选后，图片指标识该内容使用人工智能合成技术</small>
            </span>
          </label>
        </div>
      </div>

      <el-collapse>
        <el-collapse-item title="详细投稿参数" name="advanced">
          <div class="column-row">
            <div class="column-label">评论开关</div>
            <div class="column-control">
              <!-- close_comment 反直觉:0=开评论 1=关评论,故 active-value=0 -->
              <el-switch v-model="config.close_comment" :active-value="0" :inactive-value="1" size="small" />
            </div>
            <div class="column-note">关闭后，用户无法在你的专栏发表评论。</div>
          </div>

          <div class="column-row">
            <div class="column-label">精选评论</div>
            <div class="column-control">
              <el-switch v-model="config.up_choose_comment" :active-value="1" :inactive-value="0" size="small" />
            </div>
            <div class="column-note">开启后，经过筛选的评论才会向用户展示。</div>
          </div>

          <div class="column-row">
            <div class="column-label">定时发布</div>
            <div class="column-control timer-control">
              <el-switch v-model="timerEnabled" size="small" />
              <el-input-number
                v-if="timerEnabled"
                v-model="config.timer_pub_time"
                size="small"
                :min="minPublishTime()"
                :step="600"
                controls-position="right"
                style="width: 220px"
              />
            </div>
            <div class="column-note" v-if="timerEnabled">
              当前时间：{{ formatPublishTime(config.timer_pub_time) }}，以北京时间 UTC+8 为准。
            </div>
            <div class="column-note" v-else>可选时间为当前 2 小时到 7 天内。</div>
          </div>

          <div class="column-row">
            <div class="column-label">话题</div>
            <div class="column-control">
              <el-select
                v-model="config.topic_id"
                size="small"
                filterable
                remote
                reserve-keyword
                clearable
                placeholder="搜索话题"
                :remote-method="searchTopics"
                :loading="topicsLoading"
                value-key="id"
                style="width: 320px"
              >
                <el-option v-for="t in topicOptions" :key="t.id" :label="t.name" :value="t.id">
                  <span>{{ t.name }}</span>
                  <small style="color: var(--el-text-color-secondary); margin-left: 8px;">{{ t.stat_desc }}</small>
                </el-option>
              </el-select>
            </div>
          </div>

          <div class="column-row">
            <div class="column-label">标签</div>
            <div class="column-control">
              <el-input v-model="config.topics" size="small" clearable placeholder="多个标签用逗号分隔" style="width: 320px" />
            </div>
            <div class="column-note">自由文本标签，不同于话题。</div>
          </div>

          <div class="column-row">
            <div class="column-label">文集</div>
            <div class="column-control">
              <el-select
                v-model="config.list_id"
                size="small"
                clearable
                placeholder="选择文集"
                :loading="seriesLoading"
                @visible-change="(v: boolean) => v && handleSeriesDropdownOpen()"
                style="width: 320px"
              >
                <el-option :value="0" label="不加入文集" />
                <el-option v-for="s in seriesOptions" :key="s.id" :label="`${s.name}（${s.articles_count} 篇）`" :value="s.id" />
              </el-select>
              <div v-if="seriesError" class="column-note" style="color: var(--el-color-warning);">{{ seriesError }}</div>
            </div>
          </div>

          <div class="column-row expert-row" v-if="isExpert">
            <div class="column-label">分区</div>
            <div class="column-control">
              <el-input-number v-model="config.category_id" size="small" :min="0" controls-position="right" style="width: 160px" />
            </div>
            <div class="column-note">B 站专栏分区 ID，默认 15。</div>
          </div>
        </el-collapse-item>
      </el-collapse>

      <div class="column-actions">
        <el-button size="default" :loading="saving" @click="saveDraft">保存为草稿</el-button>
        <el-button size="default" type="primary" :loading="saving" @click="save">保存设置</el-button>
      </div>
    </div>
  </div>
</template>
