<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { House, User, Reading, Setting } from '@element-plus/icons-vue'
import { useAppRefreshCoordinator } from '@/composables/useAppRefreshCoordinator'
import { useTasksStore } from '@/stores/tasks'
import { useRuntimeStore } from '@/stores/runtime'
import { useExpertMode } from '@/composables/useExpertMode'

const route = useRoute()
const router = useRouter()
const tasksStore = useTasksStore()
const runtimeStore = useRuntimeStore()
const { expertMode } = useExpertMode()

// WS + task_progress + 降级轮询统一由 coordinator 管理(§7.2 ownership 唯一)
const { connected, connect, disconnect, refreshTasks } = useAppRefreshCoordinator()

const navItems = [
  { path: '/', label: '首页', icon: House },
  { path: '/streamers', label: '我的主播', icon: User },
  { path: '/recaps', label: '回顾', icon: Reading },
  { path: '/settings', label: '设置', icon: Setting },
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
  <div class="app-shell">
    <header class="top-bar">
      <div class="top-bar-inner">
        <div class="brand">Hikami-Go</div>
        <nav class="tab-nav">
          <button
            v-for="item in navItems"
            :key="item.path"
            class="tab-item"
            :class="{ active: activeNav === item.path }"
            @click="handleNav(item.path)"
          >
            <el-icon :size="16"><component :is="item.icon" /></el-icon>
            <span>{{ item.label }}</span>
          </button>
        </nav>
        <div class="top-bar-right">
          <span class="task-badge" :title="`${runningTaskCount} 个任务运行中`">
            <span class="dot" :class="{ connected }" />
          </span>
          <el-switch
            v-model="expertMode"
            active-text="专家"
            inactive-text=""
            size="small"
          />
        </div>
      </div>
    </header>
    <main class="main-content">
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

.top-bar {
  height: 56px;
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
  flex-shrink: 0;
  z-index: 10;
}

.top-bar-inner {
  display: flex;
  align-items: center;
  height: 100%;
  max-width: 1400px;
  margin: 0 auto;
  padding: 0 24px;
  gap: 24px;
}

.brand {
  font-size: 20px;
  font-weight: 700;
  color: #303133;
  letter-spacing: -0.5px;
  flex-shrink: 0;
}

.tab-nav {
  display: flex;
  gap: 4px;
  flex: 1;
}

.tab-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  border: none;
  background: none;
  border-radius: 8px;
  color: #606266;
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.tab-item:hover {
  background: #f5f7fa;
  color: #303133;
}

.tab-item.active {
  background: #ecf5ff;
  color: #409eff;
}

.top-bar-right {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}

.task-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  cursor: default;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #f56c6c;
}

.dot.connected {
  background: #67c23a;
}

.main-content {
  flex: 1;
  overflow-y: auto;
  background: #f5f7fa;
}
</style>
