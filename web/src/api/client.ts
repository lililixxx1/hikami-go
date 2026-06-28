import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAdminToken } from '@/composables/useAdminToken'

const client = axios.create({
  baseURL: '',
  timeout: 30000,
})

const { token: adminToken, setToken: setAdminToken } = useAdminToken()

// 请求拦截器：为每个请求注入管理员令牌（ISS-2 备注2）
client.interceptors.request.use((config) => {
  if (adminToken.value && config.headers) {
    config.headers['X-Admin-Token'] = adminToken.value
  }
  return config
})

// 401（令牌缺失/失效）时弹窗输入；并发请求共享同一个弹窗，避免重复打扰
let tokenPrompt: Promise<string> | null = null
function promptAdminToken(): Promise<string> {
  if (tokenPrompt) return tokenPrompt
  tokenPrompt = (async () => {
    try {
      const { value } = await ElMessageBox.prompt('请输入管理员令牌（admin token）', '需要管理员认证', {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        inputType: 'password',
        inputPlaceholder: 'admin token',
      })
      return (value || '').trim()
    } finally {
      tokenPrompt = null
    }
  })()
  return tokenPrompt
}

// 记录已重放过的请求，避免 401 重放死循环
const retriedConfigs = new WeakSet<object>()

client.interceptors.response.use(
  (response) => response,
  async (error) => {
    const status = error.response?.status
    const original = error.config

    if (status === 401 && original && !retriedConfigs.has(original)) {
      retriedConfigs.add(original)
      try {
        const newToken = await promptAdminToken()
        if (newToken) {
          setAdminToken(newToken)
          if (original.headers) {
            original.headers['X-Admin-Token'] = newToken
          }
          return client.request(original)
        }
      } catch {
        // 用户取消输入
      }
      ElMessage.error('未提供有效管理员令牌，请前往设置页配置')
      return Promise.reject(error)
    }

    if (error.response) {
      const { data, status: s } = error.response
      const message = data?.error || data?.reason || `请求失败 (${s})`
      ElMessage.error(message)
    } else {
      ElMessage.error('网络错误，请检查连接')
    }
    return Promise.reject(error)
  },
)

export async function get<T>(url: string, params?: Record<string, unknown>): Promise<T> {
  const response = await client.get<T>(url, { params })
  return response.data
}

export async function post<T>(url: string, data?: unknown): Promise<T> {
  const response = await client.post<T>(url, data)
  return response.data
}

export async function put<T>(url: string, data?: unknown): Promise<T> {
  const response = await client.put<T>(url, data)
  return response.data
}

export async function del(url: string): Promise<void> {
  await client.delete(url)
}

export async function delJson<T>(url: string): Promise<T> {
  const response = await client.delete<T>(url)
  return response.data
}

export default client
