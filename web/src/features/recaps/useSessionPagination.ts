// web/src/features/recaps/useSessionPagination.ts
import { computed, watch, type Ref } from 'vue'

// L9(2026-08-15):回顾页列表被轮询/WS 推送收缩(如清空失败场次、删除)后,
// totalPages 随之变小,currentPage 停在已消失的页码上会让 pagedSessions 渲染成
// 空表(过滤条件变化有既有 watch 重置,数据收缩没有)。这里在 totalPages 变化时
// 把 currentPage 收敛到新的最后一页——不重置到第 1 页,保留用户当前浏览位置。
export function useSessionPagination(options: {
  currentPage: Ref<number>
  pageSize: Ref<number>
  totalItems: Ref<number>
}) {
  const totalPages = computed(() =>
    Math.max(1, Math.ceil(options.totalItems.value / options.pageSize.value)),
  )
  watch(totalPages, (pages) => {
    if (options.currentPage.value > pages) options.currentPage.value = pages
  })
  return { totalPages }
}
