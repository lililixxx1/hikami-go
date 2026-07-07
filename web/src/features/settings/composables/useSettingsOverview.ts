/**
 * 设置总览项派生逻辑(Phase 5 Task 5.1)。
 *
 * 从旧 SettingsView.vue 行 55-113 抽出:合并 capabilities + config_status,
 * 产出 4 个能力卡(asr/recap/webdav/publish)的 done 状态 + 原因 + 跳转目标。
 * OverviewCard.vue 与后续 SettingsView 壳(Task 5.5)共用,避免两处硬编码不一致。
 *
 * actionType='section' → 滚动到对应配置卡;actionType='hint' → 密钥已配但能力仍红,
 * 根因通常是缺临时音频后端/yt-dlp,仅提示不跳转(避免误导用户去配密钥)。
 */
import { computed, type MaybeRefOrGetter } from 'vue'
import { toValue } from 'vue'
import type { Capabilities, ConfigStatus } from '@/api/types'

type CapActionType = 'section' | 'hint'

export interface OverviewItem {
  key: 'asr' | 'recap' | 'webdav' | 'publish'
  label: string
  done: boolean
  ok: boolean
  reason: string
  actionLabel: string
  actionType: CapActionType
  actionTarget: string
}

/**
 * @param capabilities 响应式 Capabilities(getter / ref / 原值)
 * @param configStatus 响应式 ConfigStatus(getter / ref / 原值)
 */
export function useSettingsOverview(
  capabilities: MaybeRefOrGetter<Capabilities | null>,
  configStatus: MaybeRefOrGetter<ConfigStatus | null>,
) {
  function capReason(key: keyof Capabilities, caps: Capabilities, cs: ConfigStatus | null): string {
    if (caps[key]) return ''
    if (key === 'asr_submit') return cs?.dashscope_key_set ? caps.reason : 'ASR 密钥未配置'
    if (key === 'recap_generate') return cs?.recap_key_set ? caps.reason : 'AI 密钥未配置'
    if (key === 'webdav_upload') return cs?.webdav_configured ? caps.reason : 'WebDAV 未配置'
    if (key === 'publish_opus') return cs?.publish_enabled ? caps.reason : '发布未启用'
    return caps.reason
  }

  const overviewItems = computed<OverviewItem[]>(() => {
    const caps = toValue(capabilities)
    const cs = toValue(configStatus)
    if (!caps) return []
    return [
      {
        key: 'asr',
        label: 'ASR 转写',
        done: !!cs?.dashscope_key_set,
        ok: caps.asr_submit,
        reason: capReason('asr_submit', caps, cs),
        // 密钥已配但能力仍红,根因通常是缺临时音频后端/yt-dlp,指向 hint 而非跳卡片
        ...(cs?.dashscope_key_set
          ? { actionLabel: '配置 ASR 后端', actionType: 'hint' as CapActionType, actionTarget: 'asr_backend' }
          : { actionLabel: '配置', actionType: 'section' as CapActionType, actionTarget: 'dashscope' }),
      },
      {
        key: 'recap',
        label: '回顾 AI',
        done: !!caps.recap_generate,
        ok: caps.recap_generate,
        reason: capReason('recap_generate', caps, cs),
        actionLabel: '配置',
        actionType: 'section' as CapActionType,
        actionTarget: 'recap',
      },
      {
        key: 'webdav',
        label: 'WebDAV 上传',
        done: !!cs?.webdav_configured,
        ok: caps.webdav_upload,
        reason: capReason('webdav_upload', caps, cs),
        actionLabel: '配置',
        actionType: 'section' as CapActionType,
        actionTarget: 'webdav',
      },
      {
        key: 'publish',
        label: 'B站发布',
        done: !!cs?.publish_enabled,
        ok: caps.publish_opus,
        reason: capReason('publish_opus', caps, cs),
        actionLabel: '配置',
        actionType: 'section' as CapActionType,
        actionTarget: 'publish',
      },
    ]
  })

  const overviewDoneCount = computed(() => overviewItems.value.filter(i => i.done).length)

  return { overviewItems, overviewDoneCount, capReason: (key: keyof Capabilities) => {
    const caps = toValue(capabilities)
    return caps ? capReason(key, caps, toValue(configStatus)) : ''
  } }
}
