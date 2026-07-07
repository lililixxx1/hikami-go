<!-- web/src/features/streamers/components/CookieStatus.vue -->
<script setup lang="ts">
import { HButton } from '@/components/ui'
import type { CookieStatus } from '../composables/useStreamerDetail'

// Cookie 状态行:状态点 + 文案 + 扫码登录(emit qr-login)+ 删除主播(emit delete)。
// 不直接调 store/API;删除确认由壳(ElMessageBox,Phase 6 换)处理。
defineProps<{
  status: CookieStatus
}>()

const emit = defineEmits<{
  'qr-login': []
  delete: []
}>()

// 状态文案:{ ok:'已配置', missing:'无Cookie', unknown:'加载中' }
function statusText(s: CookieStatus): string {
  if (s === 'ok') return 'Cookie 已配置'
  if (s === 'missing') return '未配置 Cookie'
  return '加载中…'
}

// 状态点颜色类
function dotClass(s: CookieStatus): string {
  if (s === 'ok') return 'dot-ok'
  if (s === 'missing') return 'dot-missing'
  return 'dot-unknown'
}
</script>

<template>
  <div class="cookie-row">
    <span class="cookie-status">
      <span class="cookie-dot" :class="dotClass(status)" />
      <span>{{ statusText(status) }}</span>
    </span>
    <span class="cookie-actions">
      <HButton size="sm" variant="secondary" @click="emit('qr-login')">扫码登录</HButton>
      <HButton size="sm" variant="danger" @click="emit('delete')">删除主播</HButton>
    </span>
  </div>
</template>

<style scoped>
.cookie-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.cookie-status {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text);
}

.cookie-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.cookie-dot.dot-ok { background: var(--success); }
.cookie-dot.dot-missing { background: var(--danger); }
.cookie-dot.dot-unknown { background: var(--text-muted); }

.cookie-actions {
  display: flex;
  gap: 8px;
}
</style>
