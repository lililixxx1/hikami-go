import { get, post, put, del, delJson } from './client'
import type { Session, SessionDetail, Task, ListResponse, DiscoverResult, DiscoverPickItem, RecapContent } from './types-derived'

// discoverSessions 是「一键全部下载」入口（旧两步式发现的行为：建场次 + 入队下载）。
// 保留作为抽屉「全部下载」快捷按钮的后端调用。
export function discoverSessions(): Promise<ListResponse<DiscoverResult>> {
  return post('/api/sessions/discover')
}

// previewDiscoverSessions 是两步式发现的「第一步预览」：列出所有频道会发现哪些回放，
// 但不建场次、不入队。返回的每条带 exists 标记（是否已建过 download 场次）。
export function previewDiscoverSessions(): Promise<ListResponse<DiscoverResult>> {
  return post('/api/sessions/discover/preview')
}

// previewDiscoverSessionsByURL 是 2026-07-19 解耦改动新增的「按 URL 发现」入口。
// 用户直接粘贴一个 B 站 URL（收藏夹/合集/UP 主主页），后端调 yt-dlp 列出该 URL 下所有回放，
// 不依赖主播管理页的 channel 配置。返回的每条带 exists 标记；不选主播时 channel_id = '_unassigned'。
export function previewDiscoverSessionsByURL(input: {
  url: string
  cookie_file?: string
  title_prefix?: string
}): Promise<ListResponse<DiscoverResult>> {
  return post('/api/sessions/discover/preview-by-url', input)
}

// executeDiscoverSessions 是两步式发现的「第二步执行」：按前端勾选的 entry 列表建场次 + 入队下载。
// 不重跑 yt-dlp，复用预览阶段已拿到的 entry 信息；CreateDownload 幂等，已存在的不会重复下载。
export function executeDiscoverSessions(items: DiscoverPickItem[]): Promise<ListResponse<DiscoverResult>> {
  return post('/api/sessions/discover/execute', { items })
}

// 可选过滤参数(channel_id/source/search),Phase 0 后端已支持。
// 不传或传空对象时退化为无参 GET /api/sessions(旧调用 listSessions() 仍工作)。
export function listSessions(params?: {
  channel_id?: string
  source?: string
  search?: string
}): Promise<ListResponse<Session>> {
  const query = params && Object.keys(params).length ? (params as Record<string, unknown>) : undefined
  return get('/api/sessions', query)
}

export function getSessionDetail(sid: string): Promise<SessionDetail> {
  return get(`/api/sessions/${encodeURIComponent(sid)}`)
}

export function deleteSession(sid: string): Promise<void> {
  return del(`/api/sessions/${encodeURIComponent(sid)}`)
}

export function deleteFailedSessions(): Promise<{ deleted: number }> {
  return delJson('/api/sessions/failed')
}

export function downloadSession(sessionId: string): Promise<Task> {
  return post('/api/sessions/download', { session_id: sessionId })
}

// downloadSessionByURL 接收视频链接（如 B 站 BV 号）与主播 ID，
// 创建下载场次并入队，复用 download → normalize → asr → recap 管道。
export function downloadSessionByURL(channelId: string, url: string): Promise<Task> {
  return post('/api/sessions/download-by-url', { channel_id: channelId, url })
}

export function importSession(formData: FormData): Promise<Task> {
  return post('/api/sessions/import', formData)
}

export function submitASR(sid: string): Promise<Task> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/asr/submit`)
}

export function generateRecap(sid: string): Promise<Task> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/recap/generate`)
}

// regenerateRecap 重新生成整场回顾（覆盖本地 md，不碰 B站）。
// 仅 recap_done/published 状态允许；任务带 BypassFailState，失败不降级主状态。
export function regenerateRecap(sid: string): Promise<Task> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/recap/regenerate`)
}

export function generateRecapWithRange(sid: string, startTime: number, endTime: number): Promise<Task> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/recap-partial`, {
    start_time: startTime,
    end_time: endTime,
  })
}

export function uploadSession(sid: string): Promise<Task> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/upload`)
}

export function fetchSession(sid: string): Promise<{ session: Session }> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/fetch`)
}

export function publishSession(sid: string): Promise<void> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/publish`)
}

// archiveSession 手动归档已发布场次到 WebDAV（自动归档失败时的手动重试入口）。
export function archiveSession(sid: string): Promise<Task> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/archive`)
}

export function getRecapContent(sid: string): Promise<RecapContent> {
  return get(`/api/sessions/${encodeURIComponent(sid)}/recap`)
}

export function updateRecapContent(sid: string, content: string): Promise<{ message: string }> {
  return put(`/api/sessions/${encodeURIComponent(sid)}/recap/content`, { content })
}
