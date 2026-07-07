import { get } from './client'
import type { RuntimeStatus } from './types-derived'

export function checkHealth(): Promise<{ status: string }> {
  return get('/api/healthz')
}

export function getRuntimeStatus(): Promise<RuntimeStatus> {
  return get('/api/health/runtime')
}
