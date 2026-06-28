import { ref, computed, watch } from 'vue'

// 管理员令牌：持久化到 localStorage，供 axios 请求拦截器注入 X-Admin-Token（ISS-2 备注2）。
const STORAGE_KEY = 'hikami-admin-token'
const LEGACY_KEY = 'hazel-admin-token'

// 向后兼容：首次从 hazel-* 升级时迁移令牌（避免现有用户被登出）
if (!localStorage.getItem(STORAGE_KEY) && localStorage.getItem(LEGACY_KEY)) {
  localStorage.setItem(STORAGE_KEY, localStorage.getItem(LEGACY_KEY) as string)
  localStorage.removeItem(LEGACY_KEY)
}

const token = ref(localStorage.getItem(STORAGE_KEY) || '')

watch(token, (val) => {
  const trimmed = (val || '').trim()
  if (trimmed) {
    localStorage.setItem(STORAGE_KEY, trimmed)
  } else {
    localStorage.removeItem(STORAGE_KEY)
  }
})

export function useAdminToken() {
  return {
    token,
    hasToken: computed(() => Boolean(token.value)),
    setToken: (val: string) => {
      token.value = (val || '').trim()
    },
    clearToken: () => {
      token.value = ''
    },
  }
}
