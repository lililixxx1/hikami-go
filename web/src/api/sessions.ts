import { get, post, put, del, delJson } from './client'
import type { Session, SessionDetail, Task, ListResponse, DiscoverResult, RecapContent } from './types'

export function discoverSessions(): Promise<ListResponse<DiscoverResult>> {
  return post('/api/sessions/discover')
}

export function listSessions(): Promise<ListResponse<Session>> {
  return get('/api/sessions')
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

// editOpus 编辑已发布专栏（删旧 + 用最新 recap 重发），返回新的 publish_target。
export function editOpus(sid: string): Promise<{ publish_target: string }> {
  return post(`/api/sessions/${encodeURIComponent(sid)}/opus/edit`)
}

// removeOpus 删除已发布专栏（删除后状态回退 uploaded，可重新发布）。
export function removeOpus(sid: string): Promise<void> {
  return del(`/api/sessions/${encodeURIComponent(sid)}/opus`)
}

export function getRecapContent(sid: string): Promise<RecapContent> {
  return get(`/api/sessions/${encodeURIComponent(sid)}/recap`)
}

export function updateRecapContent(sid: string, content: string): Promise<{ message: string }> {
  return put(`/api/sessions/${encodeURIComponent(sid)}/recap/content`, { content })
}
