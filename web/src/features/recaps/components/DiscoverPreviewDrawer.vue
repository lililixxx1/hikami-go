<!-- web/src/features/recaps/components/DiscoverPreviewDrawer.vue -->
<!--
  按 URL 发现回放预览抽屉(2026-07-19 解耦重写,2026-07-25 改 Cookie 选择为账号下拉)。
  纯展示型:壳(RecapsView)负责 preview-by-url/execute API 调用,本组件接收预览结果 items +
  执行态,本地维护勾选索引集合,提交时把勾选项映射成 DiscoverPickItem 回传。
  - 顶部 URL 输入区:用户粘贴 B 站收藏夹/合集/UP 主主页 URL + 选 B 站账号(默认=默认下载账号)+
    可选 title_prefix,点「发现」emit('preview-submit', input) 让壳调 preview-by-url。
  - exists=true 的项灰显 + 标「已存在」+ 禁止勾选(幂等,已建过 download 场次)。
  - footer:「执行勾选」(空选禁用)。原「全部下载」按钮已删除(URL 模式下用「全选新回放」+「执行勾选」替代)。
  - 2026-07-25 改造:Cooke 选择从手填「Cookie 文件路径」HInput 改为「选择已登录 B 站账号」HSelect,
    后端自动解密 HIKAMI_V1 加密格式 + 写明文临时文件给 yt-dlp。
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { HDrawer, HButton, HCheckbox, HPill, HInput, HSelect } from '@/components/ui'
import type { DiscoverResult, DiscoverPickItem, BiliCookieAccount } from '@/api/types-derived'
import { listBiliAccounts } from '@/api/bili'

const props = defineProps<{
  visible: boolean
  items: DiscoverResult[]
  executing: boolean
  loading: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  'preview-submit': [input: { url: string; account_id?: number; title_prefix?: string }]
  execute: [picked: DiscoverPickItem[]]
}>()

// URL 输入区状态(2026-07-19 新增,替代旧「打开即自动遍历主播表」行为)
const previewUrl = ref('')
const previewAccountId = ref<number | null>(null) // 2026-07-25:替代 previewCookieFile,null = 默认账号
const previewTitlePrefix = ref('')

// 账号列表加载(2026-07-25 新增,复用 ChannelPublishConfig.vue 范式)
const accounts = ref<BiliCookieAccount[]>([])
const accountsLoading = ref(false)
const accountsError = ref('')
async function loadAccounts() {
  if (accounts.value.length > 0 || accountsLoading.value) return // 幂等
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
loadAccounts() // setup 顶层同步触发

// accountIdProxy:HSelect emit string,需转 number|null(复用 ChannelPublishConfig accountIdProxy 范式)
const accountIdProxy = computed({
  get: () => (previewAccountId.value == null ? '' : String(previewAccountId.value)),
  set: (v: string) => {
    previewAccountId.value = v === '' ? null : Number(v)
  },
})

// 下拉选项:默认项 + 已登录账号(cookie_file !== '' 才算已登录)
const accountOptions = computed(() => {
  const opts: { label: string; value: string }[] = [
    { label: '(默认) 使用默认登录账号', value: '' },
  ]
  for (const a of accounts.value) {
    if (!a.cookie_file) continue // 仅已登录账号
    const label = a.nickname ? `${a.nickname}(UID ${a.uid})` : `UID ${a.uid}`
    opts.push({ label, value: String(a.id) })
  }
  return opts
})

// 勾选索引集合(以 items 数组下标为 key;Set 不响应式,变更时整体替换)
const picked = ref<Set<number>>(new Set())

// 可勾选项:排除 error 项与空 source_id(无法建场次)。exists 项允许显示但禁止勾选。
const pickableIndices = computed(() =>
  props.items
    .map((it, idx) => ({ it, idx }))
    .filter(({ it }) => !it.error && it.channel_id && it.source_id),
)

const pickableCount = computed(() => pickableIndices.value.filter(({ it }) => !it.exists).length)
const pickedCount = computed(() => picked.value.size)

const allNewPicked = computed(() => {
  if (pickableCount.value === 0) return false
  return pickableIndices.value.every(({ it, idx }) => it.exists || picked.value.has(idx))
})

// URL 必填校验
const urlError = computed(() => {
  if (!previewUrl.value.trim()) return ''
  try {
    // 简单 URL 格式校验,不强制 bilibili 域(用户可能粘别的源)
    // eslint-disable-next-line no-new
    new URL(previewUrl.value.trim())
    return ''
  } catch {
    return 'URL 格式不正确'
  }
})

const canSubmit = computed(() => Boolean(previewUrl.value.trim()) && !urlError.value && !props.loading)

function isPicked(idx: number): boolean {
  return picked.value.has(idx)
}

function toggle(idx: number, checked: boolean): void {
  const next = new Set(picked.value)
  if (checked) next.add(idx)
  else next.delete(idx)
  picked.value = next
}

function toggleAll(checked: boolean): void {
  const next = new Set<number>()
  if (checked) {
    for (const { it, idx } of pickableIndices.value) {
      if (!it.exists) next.add(idx)
    }
  }
  picked.value = next
}

// 「发现」按钮:把输入区状态打包 emit 给壳,壳调 previewDiscoverSessionsByURL。
// 2026-07-25:emit account_id(替代 cookie_file),null → undefined(不传字段,后端走默认账号)。
function handlePreviewSubmit(): void {
  if (!canSubmit.value) return
  emit('preview-submit', {
    url: previewUrl.value.trim(),
    account_id: previewAccountId.value ?? undefined,
    title_prefix: previewTitlePrefix.value.trim() || undefined,
  })
}

// 提交:把勾选索引映射成 DiscoverPickItem(channel_id/source_id/title/source_url)。
function handleExecute(): void {
  const picks: DiscoverPickItem[] = []
  for (const idx of picked.value) {
    const it = props.items[idx]
    if (!it) continue
    picks.push({
      channel_id: it.channel_id,
      source_id: it.source_id,
      title: it.title,
      source_url: it.source_url ?? '',
    })
  }
  if (picks.length === 0) return
  emit('execute', picks)
}

// 抽屉打开/关闭时重置(URL 输入区不重置,方便用户重复发现;勾选重置)
watch(
  () => props.visible,
  (v) => {
    if (v) picked.value = new Set()
  },
)
</script>

<template>
  <HDrawer
    :visible="visible"
    title="发现回放"
    size="560px"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="discover-preview">
      <!-- URL 输入区(2026-07-19 新增:回放页独立入口,不依赖主播管理页配置) -->
      <div class="url-input-block">
        <div class="form-row">
          <label class="form-label">B 站链接 <span class="required">*</span></label>
          <HInput
            v-model="previewUrl"
            placeholder="收藏夹/合集/UP 主主页 URL,如 https://space.bilibili.com/123/lists/456"
          />
          <div v-if="urlError" class="form-error">{{ urlError }}</div>
        </div>
        <!-- 2026-07-25:B 站账号选择(主表单,默认可见;qoder M-4 移出 details)。
             替代原「Cookie 文件路径」HInput —— 改为选已登录账号,后端自动解密加密 cookie。 -->
        <div class="form-row">
          <label class="form-label">B 站账号</label>
          <HSelect
            :model-value="accountIdProxy"
            :options="accountOptions"
            :disabled="accountsLoading"
            @update:model-value="accountIdProxy = $event"
          />
          <div v-if="accountsError" class="form-error">{{ accountsError }}</div>
          <div v-else class="form-hint">默认 = 设置中的默认下载账号;指定账号 = 用该账号 cookie 发现(支持私密收藏夹)</div>
        </div>
        <details class="advanced">
          <summary>高级(可选)</summary>
          <div class="form-row">
            <label class="form-label">标题前缀过滤</label>
            <HInput v-model="previewTitlePrefix" placeholder="可选,逗号分隔,如 【直播回放】" />
          </div>
        </details>
        <HButton
          variant="primary"
          size="sm"
          :disabled="!canSubmit"
          :loading="loading"
          @click="handlePreviewSubmit"
        >
          发现
        </HButton>
        <div class="hint">不选主播时,所有结果归到「未分类」。</div>
      </div>

      <!-- 错误项(频道级 yt-dlp 失败等),单独展示、不可勾选 -->
      <div v-if="items.some((i) => i.error)" class="error-block">
        <div class="error-block-title">发现失败</div>
        <div v-for="(item, idx) in items.filter((i) => i.error)" :key="`err-${idx}`" class="error-row">
          <span class="row-title">{{ item.channel_id || '-' }}</span>
          <span class="row-error">{{ item.error }}</span>
        </div>
      </div>

      <!-- 空态:仅在已经发起过预览(loading=false 且 url 已提交)且 items 为空时显示 -->
      <div v-if="!loading && pickableIndices.length === 0 && items.length === 0" class="empty">
        请输入 B 站链接后点「发现」
      </div>
      <div v-else-if="!loading && pickableIndices.length === 0" class="empty">
        未发现任何回放
      </div>

      <!-- 全选栏(仅对「新」项有效) -->
      <div v-if="pickableCount > 0" class="select-all-row">
        <HCheckbox
          :model-value="allNewPicked"
          @update:model-value="toggleAll($event)"
        >
          全选新回放
        </HCheckbox>
        <span class="count-text">共 {{ pickableCount }} 条新</span>
      </div>

      <div class="item-list">
        <template v-for="{ it, idx } in pickableIndices" :key="`${it.channel_id}-${it.source_id}`">
          <div class="preview-row" :class="{ 'is-exists': it.exists }">
            <HCheckbox
              :model-value="it.exists ? false : isPicked(idx)"
              :disabled="it.exists"
              @update:model-value="toggle(idx, $event)"
            />
            <div class="row-body">
              <div class="row-title-line">
                <span class="row-title">{{ it.title || it.source_id }}</span>
                <HPill v-if="it.exists" variant="neutral">已存在</HPill>
                <HPill v-else variant="success">新</HPill>
              </div>
              <div class="row-sub">
                <span v-if="it.channel_id && it.channel_id !== '_unassigned'">{{ it.channel_id }}</span>
                <span v-else class="unassigned-tag">未分类</span>
                <span v-if="it.source_url" class="row-url">{{ it.source_url }}</span>
              </div>
            </div>
          </div>
        </template>
      </div>

      <div class="drawer-footer">
        <HButton
          variant="primary"
          size="sm"
          :disabled="pickedCount === 0"
          :loading="executing"
          @click="handleExecute"
        >
          执行勾选{{ pickedCount > 0 ? ` (${pickedCount})` : '' }}
        </HButton>
      </div>
    </div>
  </HDrawer>
</template>

<style scoped>
.discover-preview {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* URL 输入区(2026-07-19 新增) */
.url-input-block {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
}

.form-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.form-label {
  font-size: 12px;
  color: var(--text-secondary);
  font-weight: 500;
}

.required {
  color: var(--danger, #f56c6c);
}

.form-error {
  font-size: 12px;
  color: var(--danger, #f56c6c);
}

.form-hint {
  font-size: 11px;
  color: var(--text-muted, var(--text-secondary));
}

.advanced {
  font-size: 12px;
  color: var(--text-secondary);
}

.advanced summary {
  cursor: pointer;
  user-select: none;
  padding: 2px 0;
}

.advanced[open] {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.hint {
  font-size: 11px;
  color: var(--text-muted, var(--text-secondary));
}

.unassigned-tag {
  color: var(--text-muted, var(--text-secondary));
  font-style: italic;
}

.error-block {
  border: 1px solid var(--danger-border, #fcd3d3);
  border-radius: 8px;
  padding: 10px 12px;
  background: var(--danger-bg, #fef0f0);
}

.error-block-title {
  color: var(--danger, #f56c6c);
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 8px;
}

.error-row {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px 0;
  border-top: 1px solid var(--danger-border-light, #fde2e2);
}

.error-row:first-of-type {
  border-top: none;
}

.empty {
  text-align: center;
  color: var(--text-secondary);
  padding: 32px 0;
}

.select-all-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 10px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
}

.count-text {
  font-size: 12px;
  color: var(--text-secondary);
}

.item-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.preview-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg, #fff);
}

.preview-row.is-exists {
  opacity: 0.6;
}

.row-body {
  flex: 1;
  min-width: 0;
}

.row-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.row-title {
  font-weight: 600;
  font-size: 13px;
  color: var(--text);
  word-break: break-word;
}

.row-sub {
  display: flex;
  gap: 12px;
  margin-top: 4px;
  font-size: 12px;
  color: var(--text-secondary);
  flex-wrap: wrap;
}

.row-url {
  color: var(--text-muted, var(--text-secondary));
  word-break: break-all;
}

.row-error {
  color: var(--danger, #f56c6c);
  font-size: 12px;
}

.drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
  position: sticky;
  bottom: 0;
  background: var(--bg, #fff);
}
</style>
