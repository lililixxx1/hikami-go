import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
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
    { path: '/tasks', redirect: '/recaps' },
    { path: '/import', redirect: { path: '/recaps', query: { import: '1' } } },
    { path: '/channels', redirect: '/streamers' },
    { path: '/channels/:id', redirect: (to) => ({ path: '/streamers', query: { id: to.params.id as string } }) },
    { path: '/health', redirect: { path: '/settings', query: { section: 'runtime' } } },
  ],
})

export default router
