# 孤儿 Wrapper 登记决策(阶段 1 交付)

> **来源**:docs/FRONTEND_REFACTOR_BASELINE.md §5.2-B(15 个孤儿 wrapper)+ §7 必修坑
> **依据**:docs/FRONTEND_REFACTOR_PLAN.md §6 阶段 1 / §7
> **核对方法**:在 `web/src` 下 grep 每个 wrapper 名,排除 `api/*.ts`(定义处)与 `types.ts`,统计消费者文件数
> **日期**:2026-06-20

---

## 判定标准

- **保留**:后端能力有计划 UI 入口,wrapper 作为契约层资产留下,后续阶段接 UI。
- **本次接通**:阶段 1 已给它补 UI 调用(从孤儿变已用)。
- **待删**:确认废弃(被新接口取代 / 死路由 / 重复语义),在后续阶段删除 wrapper 定义。
- **待定**:需后端确认是否保留 endpoint,前端暂不动 wrapper。

> ⚠️ 阶段 1 只做「**登记决策**」,不批量删 wrapper——删 wrapper 属于契约层改动,放到对应功能阶段一并处理,避免阶段 1 范围膨胀。

---

## 决策表

| # | wrapper | 定义 | 本次核对消费者数 | 决策 | 理由 / 后续动作 |
|---|---------|------|------------------|------|-----------------|
| 1 | `getSessionDetail` | api/sessions.ts | 0 | **保留** | 列表已含详情,但单场缓存(plan §5)会用到;阶段 3/5 接入 |
| 2 | `deleteSession` | api/sessions.ts | 0 | **保留** | 场次删除能力,RecapsView 拆分后(阶段 3)可在「清理失败」旁补「删除单场」 |
| 3 | `downloadSession` | api/sessions.ts | 0 | **待删** | UI 用的是 `download-by-url`(DownloadByURLDrawer);语义重叠,留 wrapper 易误用。阶段 3 删 |
| 4 | `getTask` | api/tasks.ts | 0 | **保留** | 单任务查询,刷新协调器(阶段 6)在 WS 推送缺失任务时可用作兜底 |
| 5 | `retryTask` | api/tasks.ts | **1**(本次接通) | **本次接通** | ✅ 阶段 1 子任务 2 已接通:RecapsView `handleRetry` 按 §7.1 五边界调用 |
| 6 | `deleteTask` | api/tasks.ts | 0 | **保留** | 任务删除能力,阶段 3 RecapsView 拆分后可在任务行补删除入口 |
| 7 | `deleteFailedTasks` | api/tasks.ts | 0 | **待删** | RecapsView 已用 sessions 级 `deleteFailedSessions`;tasks 级重复,阶段 3 删 |
| 8 | `batchRetryTasks` | api/tasks.ts | 0 | **保留** | 批量重试,阶段 3 RecapsView 失败行可补「批量重试」 |
| 9 | `updateRecapContent` | api/sessions.ts | 0 | **保留**(不混入架构阶段) | 回顾编辑能力(plan §8 不在重构中实现大功能);作为后续独立功能项,本次不补 UI |
| 10 | `getChannelLiveStatus` | api/live.ts | 0 | **保留** | 单点查;首页用批量 getAllLiveStatus。阶段 6 刷新协调器可能用到 |
| 11 | `getGlobalNote` | api/glossary.ts | 0 | **保留** | 术语表备注(plan §3 阶段 5 GlossaryEditor 迁移时补 note UI) |
| 12 | `updateGlobalNote` | api/glossary.ts | 0 | **保留** | 同上 |
| 13 | `getChannelNote` | api/glossary.ts | 0 | **保留** | 主播级术语表备注,同上 |
| 14 | `updateChannelNote` | api/glossary.ts | 0 | **保留** | 同上 |
| 15 | `checkHealth` | api/health.ts | 0 | **待定** | `/api/healthz`;onboarding 间接用 `/api/health/runtime`。需确认 healthz 是否保留(可能仅探活) |

---

## 阶段 1 实际动作汇总

- **接通(1 个)**:`retryTask` → RecapsView `handleRetry`(§7.1)
- **本次不删任何 wrapper**:删除动作收敛到对应功能阶段(`downloadSession`/`deleteFailedTasks` → 阶段 3;`checkHealth` 待后端确认)
- **登记归档**:本表作为 14 个仍孤儿 wrapper 的处置依据,后续阶段按此表推进,避免重复判断

## 复核命令(供 codex 核对)

```bash
cd web/src
for w in getSessionDetail deleteSession downloadSession getTask retryTask deleteTask deleteFailedTasks batchRetryTasks updateRecapContent getChannelLiveStatus getGlobalNote updateGlobalNote getChannelNote updateChannelNote checkHealth; do
  n=$(grep -rl "\b${w}\b" --include=*.vue --include=*.ts . | grep -v "^./api/" | grep -v "/types.ts" | wc -l)
  echo "$w: $n"
done
```

预期:`retryTask: 1`,其余 `: 0`。
