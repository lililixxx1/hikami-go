import { get } from './client'
import type { DashboardData } from './types'

export function getDashboardStats(): Promise<DashboardData> {
  return get('/api/stats/dashboard')
}
