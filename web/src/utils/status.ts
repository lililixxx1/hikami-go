export const SESSION_STATUS_COLORS: Record<string, string> = {
  discovered: 'info',
  downloading: 'warning',
  recording: 'warning',
  importing: 'warning',
  media_ready: '',
  asr_submitted: 'warning',
  asr_done: 'success',
  recap_done: 'success',
  uploaded: 'success',
  published: 'success',
  failed: 'danger',
}

export const TASK_STATUS_COLORS: Record<string, string> = {
  pending: 'info',
  running: 'warning',
  succeeded: 'success',
  failed: 'danger',
  cancelled: 'info',
}

export function sessionStatusColor(status: string): string {
  return SESSION_STATUS_COLORS[status] || 'info'
}

export function taskStatusColor(status: string): string {
  return TASK_STATUS_COLORS[status] || 'info'
}
