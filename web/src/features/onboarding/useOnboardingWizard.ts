/**
 * 引导向导状态机(重构方案 §6 阶段5)。
 *
 * 从 OnboardingWizard 抽出:step 状态机 + 三步 API(密钥保存/主播识别/完成)。
 * router 副作用留在 composable(finish 时跳首页),组件只做展示。
 *
 * 注:onboarding 端点暂走 client 直连(pre-existing,无封装 wrapper),不在本阶段新建 api 层。
 */
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { get, post, put } from '@/api/client'

export function useOnboardingWizard() {
  const router = useRouter()

  const needed = ref(false)
  const loading = ref(false)
  const step = ref(0)

  const runtimeData = ref<any>(null)
  const dashScopeKey = ref('')
  const aiKey = ref('')
  const channelInput = ref('')

  async function init() {
    try {
      const res: any = await get('/api/onboarding/status')
      needed.value = res.needed
      if (needed.value) {
        const health: any = await get('/api/health/runtime')
        runtimeData.value = health
      }
    } catch {
      needed.value = false
    }
  }

  function prevStep() {
    if (step.value > 0) step.value--
  }

  async function nextStep() {
    if (step.value === 1) {
      loading.value = true
      try {
        if (dashScopeKey.value) {
          await put('/api/secrets/DASHSCOPE_API_KEY', { value: dashScopeKey.value })
        }
        if (aiKey.value) {
          await put('/api/secrets/AI_API_KEY', { value: aiKey.value })
        }
        if (dashScopeKey.value || aiKey.value) {
          ElMessage.success('API 密钥已保存')
        }
      } catch { /* error handled by client */ }
      loading.value = false
    }
    if (step.value === 2 && channelInput.value.trim()) {
      loading.value = true
      try {
        const identifyResult: any = await post('/api/channels/identify/save', {
          input: channelInput.value.trim(),
          enabled: true,
          auto_record: true,
        })
        if (identifyResult?.channel) {
          ElMessage.success(`主播 "${identifyResult.channel.name}" 已添加`)
        }
      } catch { /* error handled by client */ }
      loading.value = false
    }
    step.value++
  }

  async function finish() {
    try {
      await post('/api/onboarding/dismiss')
    } catch { /* ignore */ }
    needed.value = false
    router.push('/')
  }

  async function dismiss() {
    try {
      await post('/api/onboarding/dismiss')
    } catch { /* ignore */ }
    needed.value = false
  }

  return {
    needed,
    loading,
    step,
    runtimeData,
    dashScopeKey,
    aiKey,
    channelInput,
    init,
    prevStep,
    nextStep,
    finish,
    dismiss,
  }
}
