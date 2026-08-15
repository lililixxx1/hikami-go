import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

// 路由表单独导出:测试用 memory history 构建同表路由验证 redirect 映射(L12),
// 不经 createWebHistory 的单例。
export const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'home',
    component: () => import('@/views/HomeView.vue'),
    meta: { title: '首页' },
  },
  {
    path: '/streamers',
    name: 'streamers',
    component: () => import('@/views/StreamersView.vue'),
    meta: { title: '我的主播' },
  },
  {
    path: '/recaps',
    name: 'recaps',
    component: () => import('@/views/RecapsView.vue'),
    meta: { title: '回顾' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { title: '设置' },
  },
  // Old routes → 301 redirects
  { path: '/live', redirect: '/' },
  { path: '/dashboard', redirect: '/' },
  { path: '/sessions', redirect: '/recaps' },
  { path: '/sessions/:sid', redirect: (to) => ({ path: '/recaps', query: { sid: to.params.sid as string } }) },
  // L12(2026-08-15):/tasks 已并入回顾页,redirect 曾直接丢 query——老链接/书签
  // 带 session_id 的映射成 ?sid=(RecapsView 抽屉消费);task_id 无消费者,丢弃。
  {
    path: '/tasks',
    redirect: (to) => {
      if (to.query.session_id) {
        return { path: '/recaps', query: { sid: String(to.query.session_id) } }
      }
      return { path: '/recaps' }
    },
  },
  { path: '/import', redirect: { path: '/recaps', query: { import: '1' } } },
  { path: '/channels', redirect: '/streamers' },
  { path: '/channels/:id', redirect: (to) => ({ path: '/streamers', query: { id: to.params.id as string } }) },
  { path: '/health', redirect: { path: '/settings', query: { section: 'runtime' } } },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

export default router
