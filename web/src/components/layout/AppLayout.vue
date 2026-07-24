<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAppRefreshCoordinator } from '@/composables/useAppRefreshCoordinator'
import { useTasksStore } from '@/stores/tasks'
import { useRuntimeStore } from '@/stores/runtime'
import { useExpertMode } from '@/composables/useExpertMode'
import { HSwitch } from '@/components/ui'

const route = useRoute()
const router = useRouter()
const tasksStore = useTasksStore()
const runtimeStore = useRuntimeStore()
const { expertMode } = useExpertMode()

// WS + task_progress + 降级轮询统一由 coordinator 管理(§7.2 ownership 唯一)
const { connected, connect, disconnect, refreshTasks } = useAppRefreshCoordinator()

const navItems = [
  { path: '/', label: '首页' },
  { path: '/streamers', label: '主播管理' },
  { path: '/recaps', label: '回顾管理' },
  { path: '/settings', label: '设置' },
]

const activeNav = computed(() => {
  if (route.path === '/' || route.path === '/live') return '/'
  if (route.path.startsWith('/streamers') || route.path.startsWith('/channels')) return '/streamers'
  if (route.path.startsWith('/recaps') || route.path.startsWith('/sessions') || route.path.startsWith('/tasks')) return '/recaps'
  if (route.path.startsWith('/settings') || route.path === '/health') return '/settings'
  return route.path
})

const runningTaskCount = computed(() =>
  tasksStore.items.filter((t) => t.status === 'running' || t.status === 'pending').length,
)

function handleNav(path: string): void {
  if (path !== route.path) router.push(path)
}

onMounted(async () => {
  connect()
  await Promise.all([refreshTasks(), runtimeStore.fetchRuntime()])
})

onUnmounted(() => {
  disconnect()
})
</script>

<template>
  <a class="skip-link" href="#main-content">跳到主内容</a>
  <div class="app-shell">
    <header class="topbar">
      <div class="topbar-brand">
        <div class="brand-icon">H</div>
        Hikami-Go
      </div>
      <nav class="topbar-nav" aria-label="主导航">
        <button
          v-for="item in navItems"
          :key="item.path"
          type="button"
          class="topbar-nav-item"
          :class="{ active: activeNav === item.path }"
          role="tab"
          :aria-selected="activeNav === item.path"
          @click="handleNav(item.path)"
        >{{ item.label }}</button>
      </nav>
      <div class="topbar-spacer" />
      <div class="topbar-status">
        <span class="topbar-status-dot" :class="{ connected }" :title="connected ? 'WebSocket 已连接' : '离线(降级轮询)'" />
        <span class="status-text">{{ connected ? '已连接' : '离线' }}</span>
        <span v-if="runningTaskCount > 0" class="task-count" :title="`${runningTaskCount} 个任务运行中`">
          {{ runningTaskCount }}
        </span>
        <HSwitch v-model="expertMode" />
        <span class="expert-label">专家</span>
      </div>
    </header>
    <main id="main-content" class="main-content">
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.app-shell {
  height: 100vh;
  display: flex;
  flex-direction: column;
}

.topbar {
  height: var(--topbar-h);
  background: var(--canvas);
  border-bottom: 1px solid var(--border-light);
  display: flex;
  align-items: center;
  padding: 0 16px;
  gap: 16px;
  flex-shrink: 0;
  z-index: 10;
}

.topbar-brand {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
  flex-shrink: 0;
  font-family: var(--font-display);
  letter-spacing: -0.02em;
}

.brand-icon {
  width: 28px;
  height: 28px;
  border-radius: 9px;
  background: linear-gradient(135deg, var(--accent) 0%, #338ae6 100%);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
  font-size: 14px;
  box-shadow: 0 2px 6px rgba(0, 102, 204, 0.25);
}

.topbar-nav {
  display: flex;
  gap: 4px;
  flex: 1;
}

.topbar-nav-item {
  padding: 6px 12px;
  border: none;
  background: none;
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: 13.5px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
  font-family: var(--font);
}

.topbar-nav-item:hover {
  background: var(--surface-warm);
  color: var(--text);
}

.topbar-nav-item.active {
  color: var(--accent);
  background: var(--accent-bg);
  font-weight: 600;
}

.topbar-spacer { flex: 1; }

.topbar-status {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  font-size: 12.5px;
  color: var(--text-muted);
}

.topbar-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--danger);
}

.topbar-status-dot.connected {
  background: var(--success);
}

.status-text { color: var(--text-secondary); }

.task-count {
  background: var(--accent);
  color: #fff;
  font-size: 11px;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: var(--radius-full);
  min-width: 16px;
  text-align: center;
}

.expert-label { font-size: 12.5px; color: var(--text-muted); }

.main-content {
  flex: 1;
  overflow-y: auto;
  background: var(--surface);
}

@media (max-width: 768px) {
  .topbar { padding: 0 12px; gap: 4px; overflow-x: auto; }
  .topbar-nav-item { padding: 6px 10px; font-size: 12.5px; }
  .topbar-brand { margin-right: 8px; font-size: 14px; }
  .expert-label { display: none; }
}
</style>
