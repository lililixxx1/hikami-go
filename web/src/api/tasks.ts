import { get, post, del, delJson } from './client'
import type { Task, ListResponse } from './types'

export function listTasks(): Promise<ListResponse<Task>> {
  return get('/api/tasks')
}

export function getTask(id: string): Promise<Task> {
  return get(`/api/tasks/${encodeURIComponent(id)}`)
}

export function retryTask(id: string): Promise<Task> {
  return post(`/api/tasks/${encodeURIComponent(id)}/retry`)
}

export function cancelTask(id: string): Promise<Task> {
  return post(`/api/tasks/${encodeURIComponent(id)}/cancel`)
}

export function deleteTask(id: string): Promise<void> {
  return del(`/api/tasks/${encodeURIComponent(id)}`)
}

export function deleteFailedTasks(): Promise<{ deleted: number }> {
  return delJson('/api/tasks/failed')
}

export function batchRetryTasks(taskIds: string[]): Promise<{ retried: number }> {
  return post('/api/tasks/batch-retry', { task_ids: taskIds })
}
