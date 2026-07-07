// web/src/features/streamers/composables/useStreamerDetail.ts
//
// 抽出 StreamersView 的单个主播详情操作逻辑:cookie 状态判定、自动化开关切换、
// 回顾/封面覆盖、删除。壳(StreamersView)负责 store 刷新;composable 仅负责
// 调用 channels API + 暴露 updating 状态。成功后由壳监听并 emit reload 刷新 store。
//
// channel 以结构化最小类型接收(types.ts / types-derived 的 Channel 都满足),
// 使测试可传入部分对象。runtime 为可选 getter(未加载时 cookieStatus 返回 unknown)。
import { computed, ref } from 'vue'
import type { ComputedRef, Ref } from 'vue'
import { updateChannel, deleteChannel } from '@/api/channels'
import type { UpsertChannelInput, RuntimeStatus } from '@/api/types-derived'

// 主播操作所需的最小字段集合。所有字段可选,使:
// 1) types.ts / types-derived.ts 的 Channel 满足此结构(完整对象)
// 2) 单测可仅传入 { id, cookie_file, download_cookie_file } 等部分对象
// 运行时壳总是传入完整 Channel;toInput 对缺失字段给出安全默认,避免 undefined 写脏后端。
export interface StreamerDetailChannel {
  id?: string
  name?: string
  uid?: number
  live_room_id?: number
  replay_source_url?: string
  space_url?: string
  title_prefix?: string
  cookie_file?: string
  download_cookie_file?: string
  enabled?: boolean
  auto_record?: boolean
  auto_asr?: boolean
  auto_recap?: boolean
  record_danmaku?: boolean
  source_mode?: string
  discover_limit?: number
  publish_enabled?: boolean
  publish_mode?: string
  publish_category_id?: number
  publish_list_id?: number
  publish_private_pub?: number
  publish_original?: number
  auto_publish?: boolean
  publish_aigc?: number
  publish_timer_pub_time?: number
  publish_cover_url?: string
  publish_topics?: string
  recap_model?: string
  max_continuations?: number
  download_account_id?: number | null
}

export type CookieStatus = 'ok' | 'missing' | 'unknown'

export type AutoToggleField = 'auto_record' | 'auto_asr' | 'auto_recap' | 'auto_publish'
export type RecapOverrideField = 'recap_model' | 'max_continuations' | 'publish_cover_url'

export interface UseStreamerDetailReturn {
  updating: Ref<boolean>
  cookieStatus: ComputedRef<CookieStatus>
  handleToggle: (field: AutoToggleField) => Promise<void>
  handleRecapOverride: (field: RecapOverrideField, value: string | number) => Promise<void>
  saveCover: (value: string) => Promise<void>
  handleDelete: () => Promise<void>
}

/**
 * 抽象单个主播详情的写操作。channel 是 Ref,壳在切换选中主播时整体替换;
 * runtime 为可选 Ref,未传入或为 null 时 cookieStatus 返回 'unknown'。
 */
export function useStreamerDetail(
  channel: Ref<StreamerDetailChannel | null>,
  runtime?: Ref<RuntimeStatus | null>,
): UseStreamerDetailReturn {
  const updating = ref(false)

  // cookieStatus 判定主播能否解析到 cookie:主播级文件路径优先,
  // 否则回退到账号池默认账号(下载/发布任一存在即视为已配置,避免误报「未配置」)。
  // runtime 未加载时返回 'unknown'(不武断判 missing),避免首屏/缓存过期期间误报。
  const cookieStatus = computed<CookieStatus>(() => {
    const c = channel.value
    if (!c) return 'unknown'
    if (c.cookie_file || c.download_cookie_file) return 'ok'
    const rt = runtime?.value
    if (!rt) return 'unknown'
    if (rt.has_default_download || rt.has_default_publish) return 'ok'
    return 'missing'
  })

  // 把(部分)主播对象透传为后端 Update 所需的完整 UpsertChannelInput。
  // 缺失字段给出零值默认(string→''/number→0/bool→false),保证返回结构完整。
  // download_account_id 必须显式带:null 显式清空绑定,undefined 视为不绑定(保持现值语义),
  // 防止后端 Update() 对缺失字段写 NULL 静默清空已配置的下载账号。
  // 运行时壳传入完整 Channel,默认值仅在单测的裁剪对象路径上生效。
  function toInput(c: StreamerDetailChannel): UpsertChannelInput {
    return {
      id: c.id ?? '', name: c.name ?? '', uid: c.uid ?? 0, live_room_id: c.live_room_id ?? 0,
      replay_source_url: c.replay_source_url ?? '', space_url: c.space_url ?? '',
      title_prefix: c.title_prefix ?? '', cookie_file: c.cookie_file ?? '',
      download_cookie_file: c.download_cookie_file ?? '', enabled: c.enabled ?? false,
      auto_record: c.auto_record ?? false, auto_asr: c.auto_asr ?? false, auto_recap: c.auto_recap ?? false,
      record_danmaku: c.record_danmaku ?? false, source_mode: c.source_mode ?? '',
      discover_limit: c.discover_limit ?? 0, publish_enabled: c.publish_enabled ?? false,
      publish_mode: c.publish_mode ?? '', publish_category_id: c.publish_category_id ?? 0,
      publish_list_id: c.publish_list_id ?? 0, publish_private_pub: c.publish_private_pub ?? 0,
      publish_original: c.publish_original ?? 0, auto_publish: c.auto_publish ?? false,
      publish_aigc: c.publish_aigc ?? 0, publish_timer_pub_time: c.publish_timer_pub_time ?? 0,
      publish_cover_url: c.publish_cover_url ?? '', publish_topics: c.publish_topics ?? '',
      recap_model: c.recap_model ?? '', max_continuations: c.max_continuations ?? 0,
      download_account_id: c.download_account_id ?? null,
    }
  }

  async function handleToggle(field: AutoToggleField): Promise<void> {
    const c = channel.value
    const id = c?.id
    if (!c || !id) return
    updating.value = true
    try {
      await updateChannel(id, { ...toInput(c), [field]: !c[field] })
    } finally {
      updating.value = false
    }
  }

  async function handleRecapOverride(field: RecapOverrideField, value: string | number): Promise<void> {
    const c = channel.value
    const id = c?.id
    if (!c || !id) return
    updating.value = true
    try {
      await updateChannel(id, { ...toInput(c), [field]: value })
    } finally {
      updating.value = false
    }
  }

  // 封面保存:仅当值变化时提交。由壳传入当前草稿值,composable 不持有 UI 草稿状态。
  async function saveCover(value: string): Promise<void> {
    const c = channel.value
    const id = c?.id
    if (!c || !id) return
    const next = value.trim()
    if (next === (c.publish_cover_url ?? '')) return
    updating.value = true
    try {
      await updateChannel(id, { ...toInput(c), publish_cover_url: next })
    } finally {
      updating.value = false
    }
  }

  async function handleDelete(): Promise<void> {
    const c = channel.value
    const id = c?.id
    if (!c || !id) return
    await deleteChannel(id)
  }

  return { updating, cookieStatus, handleToggle, handleRecapOverride, saveCover, handleDelete }
}
