// web/src/features/recaps/useSessionPagination.test.ts
// L9:列表数据收缩后 currentPage 收敛到新的最后一页,不渲染空表。
import { describe, it, expect } from 'vitest'
import { nextTick, ref } from 'vue'
import { useSessionPagination } from './useSessionPagination'

describe('useSessionPagination', () => {
  it('列表收缩后 currentPage 回落到新的最后一页', async () => {
    const currentPage = ref(3)
    const pageSize = ref(20)
    const totalItems = ref(50) // 3 页
    useSessionPagination({ currentPage, pageSize, totalItems })

    totalItems.value = 30 // → 2 页,第 3 页已消失
    await nextTick()
    expect(currentPage.value).toBe(2)
  })

  it('页码仍有效时保持不变(不强制重置到第 1 页)', async () => {
    const currentPage = ref(2)
    const pageSize = ref(20)
    const totalItems = ref(50) // 3 页
    useSessionPagination({ currentPage, pageSize, totalItems })

    totalItems.value = 40 // 仍是 2 页
    await nextTick()
    expect(currentPage.value).toBe(2)
  })

  it('列表清空后收敛到第 1 页', async () => {
    const currentPage = ref(4)
    const pageSize = ref(20)
    const totalItems = ref(70)
    useSessionPagination({ currentPage, pageSize, totalItems })

    totalItems.value = 0
    await nextTick()
    expect(currentPage.value).toBe(1)
  })

  it('pageSize 增大导致总页数缩小时同样收敛', async () => {
    const currentPage = ref(3)
    const pageSize = ref(20)
    const totalItems = ref(60) // 3 页
    useSessionPagination({ currentPage, pageSize, totalItems })

    pageSize.value = 50 // → 2 页
    await nextTick()
    expect(currentPage.value).toBe(2)
  })

  it('总页数变大时不改动 currentPage', async () => {
    const currentPage = ref(1)
    const pageSize = ref(20)
    const totalItems = ref(10)
    useSessionPagination({ currentPage, pageSize, totalItems })

    totalItems.value = 100 // → 5 页
    await nextTick()
    expect(currentPage.value).toBe(1)
  })
})
