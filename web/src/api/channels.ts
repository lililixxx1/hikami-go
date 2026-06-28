import { get, post, put, del } from './client'
import type { Channel, UpsertChannelInput, IdentifyInput, IdentifyResult, IdentifySaveResult, ListResponse } from './types'

export function listChannels(): Promise<ListResponse<Channel>> {
  return get('/api/channels')
}

export function identifyChannel(input: IdentifyInput): Promise<IdentifyResult> {
  return post('/api/channels/identify', input)
}

export function identifyAndSave(input: IdentifyInput): Promise<IdentifySaveResult> {
  return post('/api/channels/identify/save', input)
}

export function createChannel(input: UpsertChannelInput): Promise<Channel> {
  return post('/api/channels', input)
}

export function updateChannel(id: string, input: UpsertChannelInput): Promise<Channel> {
  return put(`/api/channels/${encodeURIComponent(id)}`, input)
}

export function deleteChannel(id: string): Promise<void> {
  return del(`/api/channels/${encodeURIComponent(id)}`)
}
