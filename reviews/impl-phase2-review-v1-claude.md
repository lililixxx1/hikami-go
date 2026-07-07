## 审核结论: APPROVED ✅

### 摘要
Phase 2 首页 V10 重写改动**整体优秀**，严格遵循计划执行，TDD 驱动，类型安全处理得当。HomeView 从 573 行成功精简为 234 行薄壳，7 个子组件职责清晰，测试覆盖完备（140 个测试全过），构建成功。三处计划偏离均**合理且有充分文档说明**。

### 逐项审核结果

#### 1. 类型迁移正确性 (types-derived.ts) ✅
- **派生正确**: 8 个核心类型（Session/Task/Channel/LiveStatus/RuntimeStatus/Capabilities/DashboardData/DashboardChannel）均从 `Schema<K>` 派生，符合计划。
- **SessionList 推导**: 使用 `paths['/api/sessions']['get']` 的 `infer T[]` 推导，实现优雅。
- **TaskProgressEvent 偏离合理**: 
  - **证据**: `grep -n "export.*TaskProgressEvent" web/src/api/generated.ts` 返回空，仅在注释描述文本（L109）提及 `websocket.yaml#/TaskProgressEvent`。
  - **结论**: openapi-typescript 确实不生成 WS 事件 schema（HTTP-only），implementer 删除该行并添加 NOTE 注释（L25-27）解释清楚。**✅ 合理偏离**。

#### 2. useElapsedDuration composable (L1 TDD) ✅
- **TDD 顺序正确**: 测试文件（useElapsedDuration.test.ts:17）先于实现写成。
- **测试覆盖**: 2 个用例覆盖空字符串 → '-' 和时间计算（02:30）。
- **实现质量**:
  - ✅ getter 模式 `getStartedAt: () => string`（useElapsedDuration.ts:4）
  - ✅ `setInterval` + `onUnmounted clearInterval`（L6-7）
  - ✅ 空/NaN 守卫 `if (!start || Number.isNaN(start)) return '-'`（L11）
  - ✅ `Math.max(0, ...)` 防负数（L12）
  - ✅ HH:MM 格式化 `padStart(2, '0')`（L13-14）

#### 3. 7 个 section 组件契约一致性 ✅
逐个核查 props/emit 与计划 Task 2.2-2.4 一致：

| 组件 | Props | Emit | 内嵌组件 | 状态 |
|------|-------|------|----------|------|
| **LiveSection** | recordingItems/liveOnlyItems/checking ✅ | refresh/start-record/stop-record ✅ | RecordingDuration ✅ (L40) | ✅ |
| **AttentionSection** | failedSessions/cookieWarnings/diskWarnings ✅ | open-recap ✅ | - | ✅ 有 v-if 任一非空（L18-19） |
| **RecentRecapsSection** | recaps/capabilities ✅ | open-recap/view-all ✅ | - | ✅ 用 channel_name ?? 兜底（L38）+ formatDateTime（L40）+ friendlyStatus（L43） |
| **QuickActions** | 无 props ✅ | add-streamer/discover ✅ | - | ✅ |
| **RunningTasksSection** | tasks/cancellingId ✅ | cancel: [taskId] ✅ | - | ✅ 集成测试通过（2 cases，L11-21） |
| **CapabilitySection** | capabilities ✅ | go-settings ✅ | - | ✅ 4 cap-item（asr/recap/webdav/publish，L15-22） |
| **DashboardSection** | dashboard/currentMonth ✅ | - | - | ✅ HDescriptions + 3 HTable（L48-64） |

**特别验证**:
- RunningTasksSection.test.ts:14 确认 `tbody td` 包含 'Alice'（channel_name）✅
- RunningTasksSection.test.ts:15 确认显示 '50'（progress）✅
- RunningTasksSection.test.ts:20 确认 emit `['t1']` ✅

#### 4. HomeView 壳重写 ✅
- **行数**: 234 行（`wc -l HomeView.vue`），从原 573 行成功精简 59%。
- **业务逻辑保留**:
  - ✅ onMounted 加载 6 个数据源（L154-165）
  - ✅ 7 个 action handlers（handleCheckLive/StartRecord/StopRecord/CancelTask/OpenRecap/ViewAllRecaps/AddStreamer/GoSettings）
  - ✅ expertMode gating（L200, L206, L212 的 `v-if="isExpert"`）
  - ✅ 5 个 computed（failedSessions L61/recentRecaps L72/runningTasks L80/recordingItems L55/liveOnlyItems L56）
- **section 编排**: 顺序完全一致（L171-216）: QuickActions → AttentionSection → LiveSection → RecentRecapsSection → RunningTasksSection(v-if expert) → CapabilitySection(v-if expert) → DashboardSection(v-if expert)
- **保留组件**: OnboardingWizard（L169）+ DiscoverResultDrawer（L219-222）✅
- **不再引用旧 types**: `grep -rn "from '@/api/types'" web/src/views/HomeView.vue` 返回空 ✅

#### 5. 类型安全 (as unknown as 转换审查) ⚠️ **可接受过渡**
- **转换位置**: 6 处（L46/62/65/68/73/81），均在 HomeView computed 中。
- **源类型 vs 目标类型**: 
  - 源: store 返回旧手写 types.ts 类型（Session/Task/Channel）
  - 目标: types-derived.ts 派生类型（与 generated.ts 同步）
  - **运行时兼容性**: Phase 0 后端已补齐 `channel_name`/`total_gb` 等字段，运行时数据结构完全一致，只是 TS 类型定义来源不同。
- **安全性评估**:
  - ✅ 有注释说明（L59-60："store 仍返回旧手写类型(types.ts),实际运行时数据形态与 generated 派生类型一致"）
  - ✅ 计划 Phase 6 才删 types.ts，本阶段作为过渡可接受
  - ⚠️ **风险**: 若 generated.ts 与 types.ts 类型漂移（如字段改 optional/required），运行时会 undefined 崩溃。但 Phase 0 已保证字段对齐，Phase 6 前应监控。
- **更安全替代**: store 本身迁移到派生类型（计划 Phase 5-6）。本阶段用转换是**合理的分阶段策略**。

#### 6. friendlyStatus.ts 参数放宽 ✅
- **变更**: `getFriendlySessionStatus` 参数从 `Session`（types.ts）改为结构类型 `SessionLike { status: string; local_available: boolean }`（L7-10）。
- **向后兼容**: 旧 Session（types.ts，字段 required）满足 SessionLike ✅
- **向前兼容**: 派生 Session（generated，字段 optional）也满足 SessionLike ✅
- **测试**: vitest run 显示 **140 passed** ✅，包含既有 friendlyStatus 测试（13 个，见 baseline）。
- **合理性**: 只读 2 个字段（status/local_available），用结构类型放宽避免迁移期类型冲突，是**标准的 duck typing 实践** ✅。

#### 7. channel_name 使用 (Phase 0 字段落地) ✅
- **RecentRecapsSection**: `{{ s.channel_name || s.channel_id }}`（L38）✅ 兜底正确
- **AttentionSection**: `{{ s.channel_name || s.channel_id }}`（L35）✅ 兜底正确
- **RunningTasksSection**: `{{ task.channel_name || task.channel_id }}`（L51）✅，测试用 'Alice' 验证（RunningTasksSection.test.ts:8）✅
- **DashboardSection**: `channel_name` 列（L27 columns 定义）✅

#### 8. EP 共存 + 无回归 ✅
- **EP 使用**: HomeView 仍保留 ElMessage/ElMessageBox（L5, L96, L110, L123, L137），有 TODO 注释说明 Phase 6 替换（L4）✅。
- **ep-theme-bridge**: 注释明确"仍经 ep-theme-bridge 工作,本阶段保留以避免范围蔓延"（L4）✅。
- **测试通过**: **140 passed (140)**，包含 baseline 测试（sessionActions 47/lifecycle 19/friendlyStatus 13/format 17 = 96）+ 新增（useElapsedDuration 2 + RunningTasksSection 2）= 100，额外 40 来自其他模块 ✅。

#### 9. 计划偏离汇总 ✅ **全部合理**
已知偏离 3 处，均已确认：
1. **types-derived.ts 删 TaskProgressEvent 行** ✅ — generated.ts 确实无该 schema（仅 HTTP），有 NOTE 注释。
2. **friendlyStatus 参数放宽为 SessionLike** ✅ — duck typing 标准实践，向后/向前双兼容。
3. **HomeView 6 处 as unknown as 转换** ⚠️ **可接受** — 运行时兼容（Phase 0 保证），Phase 6 前作为过渡合理，有注释说明。

**未列出的额外偏离**: 无 ✅

#### 10. 验证命令 ✅
| 命令 | 结果 | 状态 |
|------|------|------|
| `npm run type-check` | ✅ PASS（vue-tsc -b 无输出） | ✅ |
| `npx vitest run` | ✅ **140 passed (140)** <br>(baseline 96 + useElapsedDuration 2 + RunningTasksSection 2 + 其他 40) | ✅ |
| `npm run build` | ✅ PASS（dist 产物生成，8.76s） | ✅ |
| `grep -rn "from '@/api/types'" HomeView.vue` | ✅ 0 匹配（不再引用旧 types） | ✅ |

### Blocking 问题
**无 blocking 问题** ✅

### 建议（非阻塞）

1. **类型转换监控**（优先级：中）
   - 当前 6 处 `as unknown as` 转换依赖 Phase 0 保证的运行时字段对齐。
   - **建议**: Phase 6 前若 generated.ts 大改，添加运行时断言或 Zod 校验守护边界。
   - **触发条件**: 后端 API schema 变更导致 generated.ts 字段改 optional/required。

2. **测试计数文档化**（优先级：低）
   - vitest 显示 140 passed，但 baseline 文档只记录 96（静态 `it` 计数）。
   - **建议**: 文档注明"运行时 140 = 静态 100（含 describe.each 展开）+ 其他模块 40"，避免偏差疑惑。

3. **RecordingDuration 隔离测试**（优先级：低）
   - 当前 RecordingDuration 无独立测试，仅通过 useElapsedDuration 测试覆盖。
   - **建议**: Phase 3-4 可补充 RecordingDuration.test.ts（prop 透传 + computed text 渲染），提升组件级隔离。

### 验证命令结果
```bash
# 类型检查
$ npm run type-check
> vue-tsc -b
✅ PASS (无输出)

# 测试套件
$ npx vitest run
 Test Files  20 passed (20)
      Tests  140 passed (140)  # ✅ baseline 96 + 新增 4 + 其他 40
   Duration  3.88s

# 构建
$ npm run build
✓ built in 8.76s  # ✅ dist 产物生成

# HomeView 不再引用旧 types
$ grep -rn "from '@/api/types'" web/src/views/HomeView.vue
✅ 0 匹配
```

---

## 总结
Phase 2 改动**工程质量极高**：TDD 驱动、类型安全处理透明、组件契约严格、测试覆盖完备（140/140）、构建成功。三处计划偏离均有充分理由和文档说明，属于合理的工程权衡。**推荐合并** ✅
