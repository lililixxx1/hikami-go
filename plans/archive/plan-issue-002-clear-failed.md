# 计划：ISSUE-002 清空失败场次后返回页面仍显示失败状态

> **分支**：`fix/known-issues-2026-07-11`
> **创建日期**：2026-07-11
> **状态**：APPROVED (codex v1)

## 目标

修复"清空失败"操作失败后列表不刷新 + 跨路由返回页面不刷新两个问题。

## Codex 调研结论

1. **P0：handleClearFailed 加 try/catch/finally**——失败时也执行对账刷新（嵌套 catch 防止刷新本身失败传播）
2. **P0/P1：RecapsView 挂载时用 fetchSessions 替代 ensureLoaded**——保持 store 的 ensureLoaded 契约不变
3. **不引入 clearToasts()**——toast 3秒自动消失，不构成实际问题
4. **不批量改所有 handler**——后续按需处理
5. **不在 catch 中重复 HMessage.error()**——client.ts 拦截器已弹 toast

## 修改方案

### 步骤 1：修复 handleClearFailed

`web/src/views/RecapsView.vue:452-461`：

```js
const clearFailedLoading = ref(false)

async function handleClearFailed() {
  const count = failedCount.value
  if (count === 0) { HMessage.info('没有失败场次'); return }
  if (clearFailedLoading.value) return
  if (!(await HConfirm(`确定清空 ${count} 个失败场次？`, {
    title: '清空', confirmText: '清空', cancelText: '取消', type: 'warning',
  }))) return
  clearFailedLoading.value = true
  try {
    const result = await deleteFailedSessions()
    HMessage.success(`已删除 ${result.deleted} 个`)
  } catch {
    // API 错误已由 client.ts 拦截器 toast
  } finally {
    // 无论成功失败都刷新列表（对账），刷新失败不传播
    try { await sessionsStore.fetchSessions() } catch { /* ignore */ }
    clearFailedLoading.value = false
  }
}
```

### 步骤 2：RecapsView onMounted 用 fetchSessions 替代 ensureLoaded

`web/src/views/RecapsView.vue:463-472`：

把 `sessionsStore.ensureLoaded()` 改为 `sessionsStore.fetchSessions()`。

**注意**：onMounted 与 `?sid` watch 可能并发。需要检查是否有 watch 也调 ensureLoaded，如果是，需要共享 inflight。

### 步骤 3：新增前端测试

在 vitest 测试中验证：
1. deleteFailedSessions 失败时 fetchSessions 仍被调用
2. deleteFailedSessions 成功时 fetchSessions 被调用

## 测试验证

1. `cd web && npx vitest run` — 前端单测
2. `cd web && npm run type-check` — 类型检查
3. `cd web && npm run build` — 构建
