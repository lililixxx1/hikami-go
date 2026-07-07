/**
 * 友好状态计算所需的最小结构。同时兼容:
 *  - 旧手写类型 {@link Session}(started_at 等字段为 required)
 *  - generated.ts 派生类型(started_at 等字段为 optional)
 * 只读取 status 与 local_available,故用结构类型放宽,避免迁移期两种 Session 互相不可赋值。
 */
interface SessionLike {
  status: string
  local_available: boolean
}

export interface FriendlyStatus {
  label: string
  color: 'success' | 'warning' | 'danger' | 'info' | 'primary'
  progress: number
  action?: string
}

const statusGroups: { states: string[]; label: string; color: FriendlyStatus['color']; progress: number; action?: string }[] = [
  { states: ['discovered', 'downloading', 'recording', 'importing'], label: '处理音频中...', color: 'warning', progress: 15 },
  { states: ['media_ready'], label: '音频已就绪', color: 'warning', progress: 30, action: '开始转写' },
  { states: ['asr_submitted'], label: '转写中...', color: 'warning', progress: 45 },
  { states: ['asr_done'], label: '转写完成', color: 'warning', progress: 60, action: '生成回顾' },
  { states: ['recap_done'], label: '回顾已生成', color: 'success', progress: 75, action: '阅读回顾' },
  { states: ['uploaded'], label: '已上传', color: 'success', progress: 90 },
  { states: ['published'], label: '已发布', color: 'success', progress: 100 },
]

// Status group mapping for filter UI (friendly group name -> technical status list)
export const statusGroupMap: Record<string, string[]> = {
  processing: ['discovered', 'downloading', 'recording', 'importing', 'media_ready', 'asr_submitted', 'asr_done'],
  recap: ['recap_done'],
  published: ['uploaded', 'published'],
  failed: ['failed'],
}

export function getFriendlySessionStatus(session: SessionLike): FriendlyStatus {
  if (session.status === 'failed') {
    return { label: '处理失败', color: 'danger', progress: 0, action: '重试' }
  }
  // 本地目录已被上传清理策略删除：覆盖标签为「本地已清理」，提示用户需先取回。
  // 仅替换 label/color，保留原 progress（上传/发布的 90/100）。
  if (session.local_available === false) {
    const base = lookupStatusGroup(session.status)
    return { label: '本地已清理', color: 'info', progress: base.progress, action: '取回' }
  }
  for (const group of statusGroups) {
    if (group.states.includes(session.status)) {
      return { label: group.label, color: group.color, progress: group.progress, action: group.action }
    }
  }
  return { label: '未知状态', color: 'info', progress: 0 }
}

function lookupStatusGroup(status: string): { progress: number } {
  for (const group of statusGroups) {
    if (group.states.includes(status)) {
      return { progress: group.progress }
    }
  }
  return { progress: 0 }
}

