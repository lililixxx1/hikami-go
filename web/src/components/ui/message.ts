// web/src/components/ui/message.ts
// 轻量 toast 消息队列 + ElMessage 兼容 API(替代 element-plus ElMessage)。
// 由 HToast.vue 挂载消费;HMessage.* 在任意处命令式调用即可入队。
import { ref } from 'vue'

export type MessageType = 'success' | 'warning' | 'error' | 'info'

export interface Toast {
  id: number
  type: MessageType
  message: string
}

// 全局响应式队列:HToast.vue 订阅渲染
export const toasts = ref<Toast[]>([])
let nextId = 0

function push(type: MessageType, message: string, duration = 3000): number {
  const id = ++nextId
  toasts.value.push({ id, type, message })
  if (duration > 0) {
    setTimeout(() => dismissToast(id), duration)
  }
  return id
}

export function dismissToast(id: number): void {
  const idx = toasts.value.findIndex((t) => t.id === id)
  if (idx >= 0) toasts.value.splice(idx, 1)
}

// ElMessage 兼容 API:ElMessage.success(x) → HMessage.success(x)
export const HMessage = {
  success: (msg: string, d?: number) => push('success', msg, d),
  warning: (msg: string, d?: number) => push('warning', msg, d),
  error: (msg: string, d?: number) => push('error', msg, d),
  info: (msg: string, d?: number) => push('info', msg, d),
}

// useMessage 组合式(组合式风格调用方使用;与 HMessage 等价)
export function useMessage() {
  return {
    success: (msg: string, d?: number) => HMessage.success(msg, d),
    warning: (msg: string, d?: number) => HMessage.warning(msg, d),
    error: (msg: string, d?: number) => HMessage.error(msg, d),
    info: (msg: string, d?: number) => HMessage.info(msg, d),
  }
}
