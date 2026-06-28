import { get, post, put, del } from './client'
import type { RecapTemplate, ChannelRecapTemplateResponse, TemplatePreset } from './types'

export function listGlobalRecapTemplates(): Promise<{ items: RecapTemplate[] }> {
  return get('/api/recap/templates')
}

export function upsertGlobalRecapTemplate(data: Partial<RecapTemplate>): Promise<RecapTemplate> {
  return put('/api/recap/templates', data)
}

export function exportGlobalRecapTemplates(): Promise<unknown> {
  return get('/api/recap/templates/export')
}

export function importGlobalRecapTemplates(data: string): Promise<{ imported: number }> {
  return post('/api/recap/templates/import', JSON.parse(data))
}

export function getChannelRecapTemplate(channelId: string): Promise<ChannelRecapTemplateResponse> {
  return get(`/api/channels/${encodeURIComponent(channelId)}/recap-template`)
}

export function upsertChannelRecapTemplate(channelId: string, data: Partial<RecapTemplate>): Promise<RecapTemplate> {
  return put(`/api/channels/${encodeURIComponent(channelId)}/recap-template`, data)
}

export function deleteChannelRecapTemplate(channelId: string): Promise<void> {
  return del(`/api/channels/${encodeURIComponent(channelId)}/recap-template`)
}

export function exportChannelRecapTemplates(channelId: string): Promise<unknown> {
  return get(`/api/channels/${encodeURIComponent(channelId)}/recap-template/export`)
}

export function importChannelRecapTemplates(channelId: string, data: string): Promise<{ imported: number }> {
  return post(`/api/channels/${encodeURIComponent(channelId)}/recap-template/import`, JSON.parse(data))
}

export function listRecapPresets(): Promise<{ presets: TemplatePreset[] }> {
  return get('/api/recap/presets')
}
