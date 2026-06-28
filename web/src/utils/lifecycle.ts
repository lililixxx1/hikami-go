import type { Capabilities } from '@/api/types'

export type LifecycleStepKey = 'source' | 'media' | 'asr' | 'recap' | 'upload' | 'publish'
export type SessionActionName =
  | 'stop_record'
  | 'submit_asr'
  | 'generate_recap'
  | 'upload'
  | 'fetch'
  | 'publish'

export interface LifecycleStep {
  key: LifecycleStepKey
  label: string
  description: string
  statuses: string[]
}

export interface NextAction {
  name: SessionActionName
  label: string
  endpoint: string
  capability?: keyof Capabilities
  disabled: boolean
  disabledReason: string
  destructive: boolean
  confirmText: string
}

export const LIFECYCLE_STEPS: LifecycleStep[] = [
  {
    key: 'source',
    label: '来源处理',
    description: '下载、录制或导入',
    statuses: ['discovered', 'downloading', 'recording', 'importing'],
  },
  {
    key: 'media',
    label: '媒体就绪',
    description: '本地媒体可处理',
    statuses: ['media_ready'],
  },
  {
    key: 'asr',
    label: 'ASR',
    description: '语音转写',
    statuses: ['asr_submitted', 'asr_done'],
  },
  {
    key: 'recap',
    label: '回顾',
    description: '生成直播回顾',
    statuses: ['recap_done'],
  },
  {
    key: 'upload',
    label: '上传',
    description: '归档到 WebDAV',
    statuses: ['uploaded'],
  },
  {
    key: 'publish',
    label: '发布',
    description: '发布到目标平台',
    statuses: ['published'],
  },
]

export const ACTION_DISABLED_REASON: Record<SessionActionName, string> = {
  stop_record: '',
  submit_asr: 'ASR 能力不可用，请检查 DashScope API Key 与 ASR 配置',
  generate_recap: '回顾生成能力不可用，请检查 AI 回顾配置',
  upload: 'WebDAV 上传能力不可用，请检查 WebDAV 配置',
  fetch: '',
  publish: '发布能力不可用，请检查发布配置与 Cookie',
}

const ACTION_META: Record<SessionActionName, Omit<NextAction, 'disabled' | 'disabledReason'>> = {
  stop_record: {
    name: 'stop_record',
    label: '停止录制',
    endpoint: '/api/live/{channelId}/record/stop',
    destructive: true,
    confirmText: '确定要停止录制吗？停止后会进入后续标准化流程。',
  },
  submit_asr: {
    name: 'submit_asr',
    label: '提交 ASR',
    endpoint: '/api/sessions/{sessionId}/asr/submit',
    capability: 'asr_submit',
    destructive: false,
    confirmText: '确定提交 ASR 转写任务？',
  },
  generate_recap: {
    name: 'generate_recap',
    label: '生成回顾',
    endpoint: '/api/sessions/{sessionId}/recap/generate',
    capability: 'recap_generate',
    destructive: false,
    confirmText: '确定生成 AI 直播回顾？',
  },
  upload: {
    name: 'upload',
    label: '上传归档',
    endpoint: '/api/sessions/{sessionId}/upload',
    capability: 'webdav_upload',
    destructive: false,
    confirmText: '确定上传归档到 WebDAV？',
  },
  fetch: {
    name: 'fetch',
    label: '取回文件',
    endpoint: '/api/sessions/{sessionId}/fetch',
    capability: 'webdav_upload',
    destructive: false,
    confirmText: '确定要从归档取回本场文件？',
  },
  publish: {
    name: 'publish',
    label: '发布',
    endpoint: '/api/sessions/{sessionId}/publish',
    capability: 'publish_opus',
    destructive: false,
    confirmText: '确定发布本场内容？',
  },
}

// 需要读取本地 package/recap 产物的动作：本地已清理时必须先取回才能执行。
// upload 不读本地产物（仅打包上传），fetch 是恢复路径，均不受此限制。
const LOCAL_REQUIRED_ACTIONS: SessionActionName[] = ['generate_recap', 'publish']

export function getNextAction(
  status: string,
  capabilities: Capabilities | null,
  localAvailable: boolean = true,
): NextAction | null {
  const actionName = getActionNameForStatus(status)
  if (!actionName) return null

  const meta = ACTION_META[actionName]
  let disabledReason = getDisabledReason(meta, capabilities)

  // 本地已清理时，读本地文件的动作一律禁用，提示先取回。
  if (!localAvailable && LOCAL_REQUIRED_ACTIONS.includes(actionName)) {
    disabledReason = '本地已清理，请先取回'
  }

  return {
    ...meta,
    disabled: disabledReason !== '',
    disabledReason,
  }
}

export function getActionMeta(name: SessionActionName): Omit<NextAction, 'disabled' | 'disabledReason'> {
  return ACTION_META[name]
}

export function getDisabledReason(
  action: Pick<NextAction, 'name' | 'capability'>,
  capabilities: Capabilities | null,
): string {
  if (!action.capability) return ''
  if (!capabilities) return '运行时能力未加载，请稍后重试'
  if (Boolean(capabilities[action.capability])) return ''
  return capabilities.reason || ACTION_DISABLED_REASON[action.name]
}

function getActionNameForStatus(status: string): SessionActionName | null {
  if (status === 'recording') return 'stop_record'
  if (status === 'media_ready') return 'submit_asr'
  if (status === 'asr_done') return 'generate_recap'
  if (status === 'recap_done') return 'upload'
  if (status === 'uploaded') return 'publish'
  if (status === 'published') return 'fetch'
  return null
}
