import { get } from './client'
import type { DashboardData } from './types-derived'

export function getDashboardStats(): Promise<DashboardData> {
  return get('/api/stats/dashboard')
}
