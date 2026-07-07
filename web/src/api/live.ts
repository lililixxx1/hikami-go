import { get, post } from './client'
import type { LiveStatus, ListResponse } from './types-derived'

export function checkAllLive(): Promise<ListResponse<LiveStatus>> {
  return post('/api/live/check')
}

export function getAllLiveStatus(): Promise<ListResponse<LiveStatus>> {
  return get('/api/live/status')
}

export function getChannelLiveStatus(channelId: string): Promise<LiveStatus> {
  return get(`/api/live/${encodeURIComponent(channelId)}/status`)
}

export function startRecord(channelId: string): Promise<LiveStatus> {
  return post(`/api/live/${encodeURIComponent(channelId)}/record/start`)
}

export function stopRecord(channelId: string): Promise<void> {
  return post(`/api/live/${encodeURIComponent(channelId)}/record/stop`)
}
