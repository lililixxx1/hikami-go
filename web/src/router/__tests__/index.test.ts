// web/src/router/__tests__/index.test.ts
// L12:老路由 redirect 映射验证——/tasks 带 session_id 要落到 /recaps?sid=,
// 不能丢 query(修复前 redirect: '/recaps' 把 task_id/session_id 全部丢弃)。
import { describe, it, expect } from 'vitest'
import { createRouter, createMemoryHistory, type RouteRecordRaw } from 'vue-router'
import { routes } from '../index'

// 视图懒组件 stub 掉:navigation 会 resolve 懒组件,不 stub 会拉起整个 view 依赖树。
const stubView = { render: () => null }
const stubbedRoutes = routes.map((r) =>
  'redirect' in r ? r : { ...r, component: stubView },
) as RouteRecordRaw[]

async function navigate(to: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: stubbedRoutes })
  await router.push(to)
  return router.currentRoute.value
}

describe('router legacy redirects', () => {
  it('/tasks 带 session_id 映射到 /recaps?sid=(L12)', async () => {
    const route = await navigate('/tasks?task_id=t1&session_id=s1')
    expect(route.path).toBe('/recaps')
    expect(route.query.sid).toBe('s1')
    expect(route.query.task_id).toBeUndefined() // 无消费者,不透传
  })

  it('/tasks 裸路径落到 /recaps 无 query', async () => {
    const route = await navigate('/tasks')
    expect(route.path).toBe('/recaps')
    expect(route.query.sid).toBeUndefined()
  })

  it('/tasks 只有 task_id(无 session_id)落到 /recaps 无 query', async () => {
    const route = await navigate('/tasks?task_id=t1')
    expect(route.path).toBe('/recaps')
    expect(route.query.sid).toBeUndefined()
  })

  it('/sessions/:sid 保留 sid(既有行为回归保护)', async () => {
    const route = await navigate('/sessions/s9')
    expect(route.path).toBe('/recaps')
    expect(route.query.sid).toBe('s9')
  })

  it('/import 映射到 /recaps?import=1(既有行为回归保护)', async () => {
    const route = await navigate('/import')
    expect(route.path).toBe('/recaps')
    expect(route.query.import).toBe('1')
  })
})
