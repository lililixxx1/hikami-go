export const TASK_TYPE_LABELS: Record<string, string> = {
  download: '下载',
  normalize: '标准化',
  live_record: '直播录制',
  import: '导入',
  asr: 'ASR 转写',
  recap: '回顾生成',
  upload: '上传',
  publish: '发布',
}

export const SESSION_STATUS_LABELS: Record<string, string> = {
  discovered: '已发现',
  downloading: '下载中',
  recording: '录制中',
  importing: '导入中',
  media_ready: '媒体就绪',
  asr_submitted: 'ASR 提交中',
  asr_done: 'ASR 完成',
  recap_done: '回顾完成',
  uploaded: '已上传',
  published: '已发布',
  failed: '失败',
}

export const TASK_STATUS_LABELS: Record<string, string> = {
  pending: '等待中',
  running: '运行中',
  succeeded: '成功',
  failed: '失败',
  cancelled: '已取消',
}

export const SOURCE_TYPE_LABELS: Record<string, string> = {
  live_record: '直播录制',
  download: '回放下载',
  import: '手动导入',
}
