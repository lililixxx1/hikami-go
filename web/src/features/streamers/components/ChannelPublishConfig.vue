<!--
  ChannelPublishConfig.vue(2026-07-20 新增)。
  per-channel 发布配置表单:封面 + 文集 + 模式 + 可见范围 + 声明 + 账号。
  -
  - 字段哨兵值(与 publisher.go resolvePublishConfig 一致):
  -   publish_account_id  null = 跟随全局, 正数 = 账号 ID
  -   publish_mode        ''   = 跟随全局, 'draft' / 'publish' = 显式
  -   publish_private_pub 0    = 跟随全局, 1 = 仅自己, 2 = 公开
  -   publish_list_id     -1   = 跟随全局, 0 = 不加入文集, 正数 = 文集 ID
  -   publish_original    -1   = 跟随全局, 0 / 1 = 显式
  -   publish_aigc        -1   = 跟随全局, 0 / 1 = 显式
  -   publish_cover_url   ''   = 跟随全局
  -
  - HSelect 仅 emit string,数字字段用 computed 代理:
  -   - 账号代理特殊:null = 跟随全局,需 v === '' ? null : Number(v)(空串不能 Number 否则变 0)
  -   - 其余数字哨兵字段 option value 直接是合法数字字符串,Number(v) 正确
  -
  - 保存逻辑:本地 draft → 点保存按钮 diff 出变化的字段 emit 给壳(单次 updateChannel 调用,
  - 壳的 handlePublishOverrides 提交 { ...toInput(c), ...changes } 完整基底,因 updateChannel 全量 PUT)。
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Channel, BiliCookieAccount, BiliSeries } from '@/api/types-derived'
import { HButton, HInput, HSelect } from '@/components/ui'
import { listBiliAccounts } from '@/api/bili'
import { listBiliSeries } from '@/api/settings'
import type { PublishOverrideField, PublishOverrideValue } from '../composables/useStreamerDetail'

const props = defineProps<{
  channel: Channel
  updating: boolean
}>()
const emit = defineEmits<{
  'save-publish': [changes: Partial<Record<PublishOverrideField, PublishOverrideValue>>]
}>()

// 本地 draft:打开抽屉/切换 channel 时从 channel 同步,保存后由壳刷新 channel 自动回填。
interface PublishDraft {
  publish_account_id: number | null
  publish_list_id: number
  publish_mode: string
  publish_private_pub: number
  publish_original: number
  publish_aigc: number
  publish_cover_url: string
}
const draft = ref<PublishDraft>(emptyDraft())

function emptyDraft(): PublishDraft {
  return {
    publish_account_id: null,
    publish_list_id: -1,
    publish_mode: '',
    publish_private_pub: 0,
    publish_original: -1,
    publish_aigc: -1,
    publish_cover_url: '',
  }
}

watch(
  () => [props.channel?.id, props.channel?.publish_list_id, props.channel?.publish_account_id, props.channel?.publish_mode, props.channel?.publish_private_pub, props.channel?.publish_original, props.channel?.publish_aigc, props.channel?.publish_cover_url] as const,
  () => {
    syncDraftFromChannel()
  },
  { immediate: true },
)

function syncDraftFromChannel() {
  const c = props.channel
  if (!c) return
  draft.value = {
    publish_account_id: c.publish_account_id ?? null,
    publish_list_id: c.publish_list_id ?? -1,
    publish_mode: c.publish_mode ?? '',
    publish_private_pub: c.publish_private_pub ?? 0,
    publish_original: c.publish_original ?? -1,
    publish_aigc: c.publish_aigc ?? -1,
    publish_cover_url: c.publish_cover_url ?? '',
  }
}

// ---- HSelect 代理(HSelect emit string,需转 number/null)----
// 账号代理特殊:跟随全局 = null,空串 → null,否则 Number(v)。
// codex r18 HIGH:账号变更时清空文集缓存(不同账号有不同文集,旧缓存会让用户误选),
// 下次展开文集下拉会按新 publish_account_id 重新拉取。
const accountIdProxy = computed({
  get: () => (draft.value.publish_account_id == null ? '' : String(draft.value.publish_account_id)),
  set: (v: string) => {
    const next = v === '' ? null : Number(v)
    if (next !== draft.value.publish_account_id) {
      draft.value.publish_account_id = next
      seriesList.value = []
      seriesLoadedForChannelId = null
      seriesError.value = ''
    }
  },
})
const listIdProxy = computed({
  get: () => String(draft.value.publish_list_id),
  set: (v: string) => { draft.value.publish_list_id = Number(v) },
})
const privatePubProxy = computed({
  get: () => String(draft.value.publish_private_pub),
  set: (v: string) => { draft.value.publish_private_pub = Number(v) },
})
const originalProxy = computed({
  get: () => String(draft.value.publish_original),
  set: (v: string) => { draft.value.publish_original = Number(v) },
})
const aigcProxy = computed({
  get: () => String(draft.value.publish_aigc),
  set: (v: string) => { draft.value.publish_aigc = Number(v) },
})

// ---- 静态 options ----
const modeOptions = [
  { label: '跟随全局', value: '' },
  { label: '保存为草稿', value: 'draft' },
  { label: '发布', value: 'publish' },
]
const privatePubOptions = [
  { label: '跟随全局', value: '0' },
  { label: '仅自己可见', value: '1' },
  { label: '公开', value: '2' },
]
const originalOptions = [
  { label: '跟随全局', value: '-1' },
  { label: '非原创', value: '0' },
  { label: '原创', value: '1' },
]
const aigcOptions = [
  { label: '跟随全局', value: '-1' },
  { label: '否', value: '0' },
  { label: '是', value: '1' },
]

// ---- 账号列表(组件挂载时拉一次)----
const accounts = ref<BiliCookieAccount[]>([])
const accountsLoading = ref(false)
const accountsError = ref('')

async function loadAccounts() {
  if (accounts.value.length > 0 || accountsLoading.value) return
  accountsLoading.value = true
  accountsError.value = ''
  try {
    accounts.value = await listBiliAccounts()
  } catch (err) {
    accountsError.value = err instanceof Error ? err.message : '加载账号列表失败'
    accounts.value = []
  } finally {
    accountsLoading.value = false
  }
}

const accountOptions = computed(() => {
  const opts: { label: string; value: string }[] = [{ label: '跟随全局', value: '' }]
  for (const a of accounts.value) {
    const label = a.nickname ? `${a.nickname}(UID ${a.uid})` : `UID ${a.uid}`
    opts.push({ label, value: String(a.id) })
  }
  return opts
})

// ---- 文集列表(按 channel.id 懒加载,用该主播的 publish_account_id 拉取)----
const seriesList = ref<BiliSeries[]>([])
const seriesLoading = ref(false)
const seriesError = ref('')
let seriesLoadedForChannelId: string | null = null

async function loadSeriesList() {
  const cid = props.channel?.id
  if (!cid) return
  // codex r18 HIGH:listBiliSeries(channel_id) 用的是后端已持久化的 publish_account_id,
  // 不是表单 draft。若用户刚改了账号但还没保存,拉到的还是旧账号的文集,容易误选。
  // 此时给出提示让用户先保存账号,而不是错误地展示旧账号的文集。
  const draftAccount = draft.value.publish_account_id
  const persistedAccount = props.channel?.publish_account_id ?? null
  if (draftAccount !== persistedAccount) {
    seriesError.value = '发布账号已修改但未保存,请先点「保存发布设置」让后端用新账号拉取文集列表'
    seriesList.value = []
    seriesLoadedForChannelId = null
    return
  }
  // 同一 channel + 同一账号 只拉一次(切换 channel 或账号变更后重新拉)
  if (seriesLoadedForChannelId === cid && !seriesError.value) return
  seriesLoading.value = true
  seriesError.value = ''
  try {
    const res = await listBiliSeries(cid)
    if (res.error) {
      seriesError.value = res.error
      seriesList.value = []
    } else {
      seriesList.value = res.items ?? []
    }
    seriesLoadedForChannelId = cid
  } catch (err) {
    seriesError.value = err instanceof Error ? err.message : '加载文集列表失败'
    seriesList.value = []
  } finally {
    seriesLoading.value = false
  }
}

const seriesOptions = computed(() => {
  const opts: { label: string; value: string }[] = [
    { label: '跟随全局', value: '-1' },
    { label: '不加入文集', value: '0' },
  ]
  for (const s of seriesList.value) {
    opts.push({ label: `${s.name}(${s.articles_count} 篇)`, value: String(s.id) })
  }
  return opts
})

// ---- 保存:diff 出变化字段,emit 给壳 ----
function buildChanges(): Partial<Record<PublishOverrideField, PublishOverrideValue>> {
  const c = props.channel
  if (!c) return {}
  const changes: Partial<Record<PublishOverrideField, PublishOverrideValue>> = {}
  // publish_account_id: null vs number 都可能是有效值,直接比较
  const curAcct = c.publish_account_id ?? null
  if (draft.value.publish_account_id !== curAcct) {
    changes.publish_account_id = draft.value.publish_account_id
  }
  if (draft.value.publish_list_id !== (c.publish_list_id ?? -1)) {
    changes.publish_list_id = draft.value.publish_list_id
  }
  if (draft.value.publish_mode !== (c.publish_mode ?? '')) {
    changes.publish_mode = draft.value.publish_mode
  }
  if (draft.value.publish_private_pub !== (c.publish_private_pub ?? 0)) {
    changes.publish_private_pub = draft.value.publish_private_pub
  }
  if (draft.value.publish_original !== (c.publish_original ?? -1)) {
    changes.publish_original = draft.value.publish_original
  }
  if (draft.value.publish_aigc !== (c.publish_aigc ?? -1)) {
    changes.publish_aigc = draft.value.publish_aigc
  }
  const curCover = (c.publish_cover_url ?? '').trim()
  const draftCover = draft.value.publish_cover_url.trim()
  if (draftCover !== curCover) {
    changes.publish_cover_url = draftCover
  }
  return changes
}

function onSave() {
  const changes = buildChanges()
  if (Object.keys(changes).length === 0) return
  emit('save-publish', changes)
}

const hasChanges = computed(() => Object.keys(buildChanges()).length > 0)

// 组件挂载即拉账号列表(避免每次展开下拉都触发)
loadAccounts()
</script>

<template>
  <div class="form-stack">
    <!-- 发布账号(per-channel) -->
    <div>
      <HSelect
        :model-value="accountIdProxy"
        :options="accountOptions"
        :disabled="accountsLoading"
        @update:model-value="accountIdProxy = $event"
      >
        <template #label>发布账号</template>
      </HSelect>
      <div v-if="accountsError" class="hint hint-error">{{ accountsError }}</div>
      <div v-else class="hint">跟随全局 = 用全局默认发布账号;指定账号 = 该主播回顾走该账号 cookie 发布</div>
    </div>

    <!-- 文集 -->
    <div>
      <HSelect
        :model-value="listIdProxy"
        :options="seriesOptions"
        :disabled="seriesLoading"
        @click="loadSeriesList"
        @update:model-value="listIdProxy = $event"
      >
        <template #label>文集</template>
      </HSelect>
      <div v-if="seriesError" class="hint hint-error">{{ seriesError }}</div>
      <div v-else class="hint">跟随全局 = 用全局 list_id;-1 之外的值会覆盖全局文集</div>
    </div>

    <!-- 发布模式 -->
    <HSelect
      :model-value="draft.publish_mode"
      :options="modeOptions"
      @update:model-value="draft.publish_mode = $event"
    >
      <template #label>发布模式</template>
    </HSelect>

    <!-- 可见范围 -->
    <HSelect
      :model-value="privatePubProxy"
      :options="privatePubOptions"
      @update:model-value="privatePubProxy = $event"
    >
      <template #label>可见范围</template>
    </HSelect>

    <!-- 原创声明 -->
    <HSelect
      :model-value="originalProxy"
      :options="originalOptions"
      @update:model-value="originalProxy = $event"
    >
      <template #label>原创声明</template>
    </HSelect>

    <!-- AI 声明 -->
    <HSelect
      :model-value="aigcProxy"
      :options="aigcOptions"
      @update:model-value="aigcProxy = $event"
    >
      <template #label>AI 创作声明</template>
    </HSelect>

    <!-- 自定义封面 -->
    <HInput
      :model-value="draft.publish_cover_url"
      size="sm"
      placeholder="自定义封面 URL(留空跟随全局)"
      @update:model-value="draft.publish_cover_url = $event"
    >
      <template #label>自定义封面</template>
    </HInput>
    <div class="hint">留空跟随全局;优先使用回顾目录封面,无回顾封面时才用此 URL 或本地路径(发布时自动上传)</div>

    <HButton
      size="sm"
      variant="secondary"
      :disabled="updating || !hasChanges"
      @click="onSave"
    >
      {{ updating ? '保存中…' : '保存发布设置' }}
    </HButton>
  </div>
</template>

<style scoped>
.form-stack {
  display: grid;
  gap: 12px;
}

.hint {
  font-size: 12px;
  color: var(--text-muted);
  margin-top: 4px;
}

.hint-error {
  color: var(--warning, #d97706);
}
</style>
