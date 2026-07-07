import { get, post, put, del } from './client'
import type { GlossaryEntry, GlossaryNote, GlossaryCandidate } from './types'

// Global glossary

export function listGlobalEntries(): Promise<{ items: GlossaryEntry[] }> {
  return get('/api/glossary/entries')
}

export function upsertGlobalEntry(term: string, canonical: string, category: string): Promise<GlossaryEntry> {
  return post('/api/glossary/entries', { term, canonical, category })
}

export function deleteGlobalEntry(id: number): Promise<void> {
  return del(`/api/glossary/entries/${id}`)
}

export function getGlobalNote(): Promise<GlossaryNote> {
  return get('/api/glossary/note')
}

export function updateGlobalNote(note: string): Promise<GlossaryNote> {
  return put('/api/glossary/note', { note })
}

// Global glossary import/export

export function importGlobalMarkdown(content: string): Promise<{ imported: number }> {
  return post('/api/glossary/import/markdown', { content })
}

export function importGlobalJSON(data: string): Promise<{ imported: number }> {
  return post('/api/glossary/import/json', JSON.parse(data))
}

export function exportGlobalJSON(): Promise<unknown> {
  return get('/api/glossary/export/json')
}

// Channel glossary

export function listChannelEntries(channelId: string): Promise<{ items: GlossaryEntry[] }> {
  return get(`/api/channels/${encodeURIComponent(channelId)}/glossary/entries`)
}

export function upsertChannelEntry(channelId: string, term: string, canonical: string, category: string): Promise<GlossaryEntry> {
  return post(`/api/channels/${encodeURIComponent(channelId)}/glossary/entries`, { term, canonical, category })
}

export function deleteChannelEntry(channelId: string, entryId: number): Promise<void> {
  return del(`/api/channels/${encodeURIComponent(channelId)}/glossary/entries/${entryId}`)
}

export function getChannelNote(channelId: string): Promise<GlossaryNote> {
  return get(`/api/channels/${encodeURIComponent(channelId)}/glossary/note`)
}

export function updateChannelNote(channelId: string, note: string): Promise<GlossaryNote> {
  return put(`/api/channels/${encodeURIComponent(channelId)}/glossary/note`, { note })
}

// Channel glossary import/export

export function importChannelMarkdown(channelId: string, content: string): Promise<{ imported: number }> {
  return post(`/api/channels/${encodeURIComponent(channelId)}/glossary/import/markdown`, { content })
}

export function importChannelJSON(channelId: string, data: string): Promise<{ imported: number }> {
  return post(`/api/channels/${encodeURIComponent(channelId)}/glossary/import/json`, JSON.parse(data))
}

export function exportChannelJSON(channelId: string): Promise<unknown> {
  return get(`/api/channels/${encodeURIComponent(channelId)}/glossary/export/json`)
}

// Global glossary batch operations

export function batchDeleteGlobalEntries(ids: number[]): Promise<{ deleted: number }> {
  return post('/api/glossary/entries/batch-delete', { ids })
}

export function batchToggleGlobalEntries(ids: number[], enabled: boolean): Promise<{ updated: number }> {
  return post('/api/glossary/entries/batch-toggle', { ids, enabled })
}

export function toggleGlobalEntry(id: number, enabled: boolean): Promise<void> {
  return post(`/api/glossary/entries/${id}/toggle`, { enabled })
}

// Channel glossary batch operations

export function batchDeleteChannelEntries(channelId: string, ids: number[]): Promise<{ deleted: number }> {
  return post(`/api/channels/${encodeURIComponent(channelId)}/glossary/entries/batch-delete`, { ids })
}

export function batchToggleChannelEntries(channelId: string, ids: number[], enabled: boolean): Promise<{ updated: number }> {
  return post(`/api/channels/${encodeURIComponent(channelId)}/glossary/entries/batch-toggle`, { ids, enabled })
}

export function toggleChannelEntry(channelId: string, id: number, enabled: boolean): Promise<void> {
  return post(`/api/channels/${encodeURIComponent(channelId)}/glossary/entries/${id}/toggle`, { enabled })
}

// ---------- Glossary candidates (V10 候选审批,Phase 5 Task 5.3) ----------

export function listGlobalCandidates(status?: 'pending' | 'approved' | 'rejected' | 'all'): Promise<{ items: GlossaryCandidate[] }> {
  return get('/api/glossary/candidates', status ? { status } : undefined)
}

export function approveGlobalCandidate(cid: number, term?: string): Promise<GlossaryCandidate> {
  // term 可选覆盖候选原值;不传则后端用候选原值。请求体全可选(容 EOF)。
  return post(`/api/glossary/candidates/${cid}/approve`, term ? { term } : {})
}

export function rejectGlobalCandidate(cid: number): Promise<GlossaryCandidate> {
  return post(`/api/glossary/candidates/${cid}/reject`, {})
}
