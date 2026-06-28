import { ref, computed, watch } from 'vue'

const STORAGE_KEY = 'hikami-expert-mode'
const LEGACY_KEY = 'hazel-expert-mode'

// 向后兼容：首次从 hazel-* 升级时，把旧键一次性迁移到新键并清理
if (localStorage.getItem(STORAGE_KEY) === null && localStorage.getItem(LEGACY_KEY) !== null) {
  localStorage.setItem(STORAGE_KEY, localStorage.getItem(LEGACY_KEY) as string)
  localStorage.removeItem(LEGACY_KEY)
}

const stored = localStorage.getItem(STORAGE_KEY) === 'true'
const expertMode = ref(stored)

watch(expertMode, (val) => {
  localStorage.setItem(STORAGE_KEY, String(val))
})

export function useExpertMode() {
  return {
    expertMode,
    isExpert: computed(() => expertMode.value),
    toggleExpertMode: () => { expertMode.value = !expertMode.value },
  }
}
