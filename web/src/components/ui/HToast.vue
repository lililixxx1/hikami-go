<!-- web/src/components/ui/HToast.vue -->
<!-- 轻量 toast 容器:固定居顶,消费 message.ts 全局队列,支持点击关闭。
     在 App.vue/AppLayout 挂载一次即可全局可用(替代 ElMessage 渲染层)。 -->
<script setup lang="ts">
import { toasts, dismissToast } from './message'
</script>

<template>
  <Teleport to="body">
    <div class="h-toast-container" role="region" aria-label="通知">
      <Transition-group name="h-toast" tag="div">
        <div
          v-for="t in toasts"
          :key="t.id"
          class="h-toast"
          :class="`h-toast-${t.type}`"
          role="alert"
          @click="dismissToast(t.id)"
        >
          <span v-if="t.type === 'success'" class="h-toast-icon">✓</span>
          <span v-else-if="t.type === 'error'" class="h-toast-icon">✕</span>
          <span v-else-if="t.type === 'warning'" class="h-toast-icon">!</span>
          <span v-else class="h-toast-icon">i</span>
          <span class="h-toast-text">{{ t.message }}</span>
        </div>
      </Transition-group>
    </div>
  </Teleport>
</template>

<style scoped>
.h-toast-container {
  position: fixed;
  top: 16px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 9999;
  display: flex;
  flex-direction: column;
  gap: 8px;
  pointer-events: none; /* let individual toasts capture clicks */
}

.h-toast {
  pointer-events: auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 16px;
  border-radius: var(--radius-md);
  font-size: 14px;
  color: var(--text);
  background: var(--canvas);
  box-shadow: var(--shadow-lg);
  border: 1px solid var(--border-light);
  cursor: pointer;
  min-width: 240px;
  max-width: 480px;
}

.h-toast-success { border-left: 3px solid var(--success); }
.h-toast-error { border-left: 3px solid var(--danger); }
.h-toast-warning { border-left: 3px solid var(--warning); }
.h-toast-info { border-left: 3px solid var(--accent); }

.h-toast-icon {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 700;
  color: #fff;
  flex-shrink: 0;
}

.h-toast-success .h-toast-icon { background: var(--success); }
.h-toast-error .h-toast-icon { background: var(--danger); }
.h-toast-warning .h-toast-icon { background: var(--warning); }
.h-toast-info .h-toast-icon { background: var(--accent); }

.h-toast-enter-active,
.h-toast-leave-active {
  transition: all 0.25s ease;
}
.h-toast-enter-from {
  opacity: 0;
  transform: translateY(-12px);
}
.h-toast-leave-to {
  opacity: 0;
  transform: translateX(20px);
}
</style>
