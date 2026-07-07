# 前端 V10 全页面重写实施计划 — 审核报告

## 总审核结论：**NEEDS_CHANGES**

计划整体结构清晰、覆盖全面，但存在 **5 个 blocking 问题**必须修改后才能执行，另有 7 个优化建议。

---

## 逐维度审核

### 1. ✅ Spec 忠实度 — **PASS**

计划忠实执行了用户确认的三大决策：

- ✅ **组件策略**：Phase 1 完全移除 Element Plus，Phase 1.2-1.8 用 V10 自定义组件（HButton/HCard/HPill 等）重建
- ✅ **后端缺口处理**：Phase 0 先补字段（Session/Task 加 `channel_name`、listSessions 加过滤参数）再重写
- ✅ **迁移节奏**：Phase 2-5 逐页迁移，每页独立分支/PR/回滚

**与 gap-analysis.md 对照**：
- P0（Session/Task channel_name）→ Phase 0 Task 0.1/0.2 ✅
- P1（listSessions 过滤）→ Phase 0 Task 0.3 ✅
- P2/P3 缺口均在 Phase 2-5 显式处理或合理砍掉 ✅

---

### 2. ⚠️ Task 粒度与可执行性 — **NEEDS_CHANGES**

#### Blocking 问题

**B1. Phase 1 Task 1.9 AppLayout 重写步骤不完整**（行 1602-1640）

```markdown
- [ ] **Step 2:改写 AppLayout.vue 顶栏(用 HButton + HSwitch + 自定义 nav + dot)**
完整 AppLayout 重写在 Task 1.9 Step 2 实现,代码量约 200 行(template + style),此处给出关键结构:
```

**问题**：只给了"关键结构"示例（30 行），缺失完整的 200 行实现。fresh subagent 无法执行。

**修复**：拆成 3 步：
- Step 2a：写完整 `<script setup>`（引用、导航逻辑、connected 状态）
- Step 2b：写完整 `<template>`（topbar 结构 + router-view）
- Step 2c：写完整 `<style scoped>`（.topbar/.topbar-brand/.topbar-nav/.topbar-status 全套）

---

**B2. Phase 2-5 多处"实现 XXX.vue"无具体步骤**

示例：
- Phase 2 Task 2.3："实现 AttentionSection.vue（代码 ~80 行）" — **无步骤拆解**
- Phase 3 Task 3.2："实现 StreamerCard.vue" — **无步骤拆解**
- Phase 5 Task 5.2："实现 DashScopeCardV10.vue" — **无步骤拆解**

这些组件都 80-150 行，应拆成：
1. 写 props/emit 接口
2. 写模板结构（主要 HTML）
3. 写 scoped 样式
4. dev 视觉验证

**当前形态**：一步"实现"= placeholder。

**修复示例**（以 AttentionSection 为例）：
```markdown
- [ ] **Step 1a: 写 AttentionSection props/emit**
- [ ] **Step 1b: 写模板结构（失败场次 alert-card + cookie warning + disk warning）**
- [ ] **Step 1c: 写 scoped 样式（alert-card-danger 等）**
- [ ] **Step 1d: dev 验证**
```

**影响范围**：Phase 2-5 约 **20 个组件**未拆步骤。

---

**B3. Phase 0 Task 0.1 Step 3 实现细节不足**（行 191-212）

```markdown
- [ ] **Step 3:实现 — Session 加 ChannelName 字段 + 改 List 走 JOIN**
把 `listSQL`(行 469)改为 JOIN 版本...
**避免破坏 Get 的最干净方案**:拆两个 scan —— `scanSessionCore(row)`(原始 18 列)+ `scanSessionWithChannel(row)`(19 列)。Get 走 core,List 走 withChannel。
```

**问题**：给了设计思路，但 `scanSessionCore` / `scanSessionWithChannel` 的完整实现代码缺失。fresh subagent 不知道如何 scan 19 列、如何 COALESCE channel_name。

**修复**：在 Step 3 展开完整 SQL + 完整 scan 代码（至少给 signature + 关键 Scan 调用）。

---

#### 非 blocking 但需改进

**建议 S1**：Phase 4 Task 4.5 RecapDrawerV10 内容最复杂（md preview + 动作按钮 + 部分回顾 + 术语候选），应拆成 3 步：
1. 基础 drawer（md preview + 复制）
2. 动作按钮区（表 B 状态机）
3. 部分回顾 + 术语候选

---

### 3. ⚠️ TDD 严格遵守 — **NEEDS_CHANGES**

#### Blocking 问题

**B4. Phase 1 部分组件测试不完整**

- **HCard/HPill**（Task 1.3）：测试只覆盖"renders header + body / applies variant class"，缺 `title` prop 测试。
- **HInput/HSelect/HTextarea**（Task 1.4）：测试文件名为 `HInput.test.ts`，但实际应测 3 个组件，代码只给了 HInput 的测试。
- **HDialog/HProgress/HEmpty/HDescriptions**（Task 1.8）：只测了 HTable，其余 4 个组件**无测试**。

**当前状态**：Task 1.8 Step 1 写 HTable 测试 → Step 10 跑 HTable 测试 → 其余 4 个组件无测试 = **不符合 TDD**。

**修复**：
- Task 1.3：补 HCard `title` prop 测试
- Task 1.4：拆成 3 个测试文件（HInput/HSelect/HTextarea 各一个）
- Task 1.8：拆成 5 个子 task，每个组件独立 TDD（HDialog/HProgress/HEmpty/HDescriptions/HTable）

---

**B5. Phase 2-5 业务组件无测试**

计划中 Phase 2-5 的 **所有业务组件**（LiveSection/AttentionSection/StreamerCard/RecapDrawerV10 等 30+ 个）**均无测试**。

只有 1 个 composable（`useElapsedDuration`）有测试，其余组件靠"dev 视觉验证"。

**问题**：用户要求"TDD 严格遵守：每个新组件先写失败测试 → 跑测确认失败 → 实现 → 跑测确认通过"，但 Phase 2-5 全是 **实现优先 + 视觉验证**，无单测 = **严重违反 TDD**。

**修复建议**：
1. **关键组件补测试**：LiveSection（useElapsedDuration 集成）、SessionTableV10（WS 进度）、RecapDrawerV10（动作按钮状态机）
2. **其余组件放宽**：纯展示组件（AttentionSection/QuickActions 等）可豁免单测，改为"类型检查 + dev 验证"（在 Plan 开头显式声明豁免策略）

**当前形态**：Plan 声称"TDD 严格遵守"但实际只测 H* 组件 = **名实不符**。

---

### 4. ✅ 类型一致性 — **PASS with Minor Issues**

#### 跨 task 类型一致性检查

- ✅ `Session.channel_name`：Phase 0 Task 0.4 加到 OpenAPI → generated.ts 更新 → Phase 2+ 所有引用都用 `session.channel_name`
- ✅ `listSessions(params?)`：Phase 0 Task 0.3 改后端 → Task 0.4 加 OpenAPI parameters → Phase 4 Task 4.1 改前端 wrapper → 旧无参调用兼容
- ✅ H* 组件 props：`modelValue`（v-model）、`visible`（drawer/dialog）、`variant`/`size` 统一
- ✅ `useElapsedDuration(getStartedAt: () => string)`：getter 模式，与现有 composables 一致

#### 小问题

**建议 S2**：Phase 1 Task 1.8 Step 9 删除了 `HTabs.vue`（"为简化，实际用「裸 div + 调用方自己 v-for 按钮」模式"），但 Phase 2-5 多处仍引用 `HTabs`：
- Phase 4 RecapToolbarV10："tab 切换用 .h-tabs-bar" — 这是 CSS class，不是组件，**表述不清**
- ui.css 末尾有 `.h-tabs-bar` / `.h-tab` 样式 — 与"删除 HTabs.vue"不矛盾，但文档应明确"HTabs 不是组件，是 CSS 约定"

**修复**：在 Task 1.8 Step 9 注释中明确"HTabs 改为纯 CSS 模式，调用方自行写 button + .h-tab class"。

---

### 5. ✅ 文件结构合理性 — **PASS**

- ✅ `web/src/components/ui/`：16 个 H* 组件 + `ui.css` + `index.ts` 统一导出，边界清晰
- ✅ `web/src/features/home/components/`：7 个子 section，职责明确
- ✅ `web/src/features/recaps/components/`：工具栏/过滤器/表格/抽屉，符合 FRONTEND_ARCHITECTURE.md 分层
- ✅ `web/src/features/settings/components-v10/`：15 卡 + sidebar + pipeline，按功能分组
- ✅ Phase 6 删除 `types.ts` 后改用 `types-derived.ts`，避免重复

**无过度拆分或职责重叠**。

---

### 6. ⚠️ 风险与回滚 — **NEEDS_CHANGES**

#### 风险评估

**已识别风险**（Plan §4）：
1. ✅ EP 共存期视觉不一致 — 缓解方案：Phase 1 Task 1.1b EP 主题变量映射（**建议补这个 task**）
2. ✅ HTable 性能（>500 场次）— 缓解：客户端分页保留
3. ✅ Phase 6 回滚困难 — 缓解：充分验证 + revert 脚本

#### 新增风险（Plan 未提）

**R1. Phase 0 后端改动破坏性风险**

Phase 0 Task 0.1/0.2 改 `session.go` / `task.go` 的 SQL（JOIN channels），若 `channels` 表有脏数据（孤儿 session/task），`COALESCE(c.name, '')` 兜底逻辑可能不符预期。

**缓解**：Task 0.1 Step 3 补一条："运行 `SELECT COUNT(*) FROM sessions s LEFT JOIN channels c ON s.channel_id = c.id WHERE c.id IS NULL` 检查孤儿数量，若 >0 需先清理或调整 COALESCE"。

---

**R2. Phase 1 design-tokens 与 EP 的 CSS 变量冲突**

Plan 定义 `:root { --accent: #0075de }` 等 V10 变量，但 Element Plus 自带 `--el-color-primary` 等变量，两套并存可能导致：
- EP 组件（GlossaryEditor/QR Dialog）仍用 EP 变量，视觉与 V10 accent 不一致
- 覆盖 EP 变量可能破坏 EP 组件内部样式

Plan §4 提了"Phase 1 Task 1.1b EP 主题变量映射"但未正式加入 Task 清单。

**修复**：在 Phase 1 Task 1.1 之后**显式加 Task 1.1b**：
```markdown
### Task 1.1b: EP 主题变量映射（过渡期视觉统一）

- [ ] **Step 1: 在 main.ts 追加 EP 变量覆盖**
```css
:root {
  --el-color-primary: var(--accent);
  --el-color-success: var(--success);
  --el-color-warning: var(--warning);
  --el-color-danger: var(--danger);
}
```
- [ ] **Step 2: dev 验证 EP 组件（el-button/el-dialog）颜色与 V10 一致**
```

---

#### 回滚策略

**已有策略**（Plan §整体里程碑）：
- ✅ Phase 2-5 任一页可独立 revert（通过 store+router 解耦）
- ✅ Phase 6 需同退所有页面（EP 已移除）

**缺失**：Phase 0 后端改动的回滚策略。

**修复**：在 Phase 0 末尾加：
```markdown
**Phase 0 回滚策略**：
- 若前端重写暂停，Phase 0 的 channel_name 字段不影响旧前端（optional 字段，旧代码不引用）
- 若需完全回退 Phase 0：`git revert <merge>` + 重新 `make api-gen-types`（前端 generated.ts 回退到无 channel_name）
```

---

### 7. ⚠️ Self-Review 质量 — **NEEDS_CHANGES**

#### Coverage 检查

**已覆盖**（Plan §Self-Review）：
- ✅ P0/P1 阻塞点（channel_name / listSessions 过滤 / WS 接入）
- ✅ P2/P3 可选点（导出 Markdown / 片段预览 / 高级参数）
- ✅ 多余端点按需启用
- ✅ 字段映射差异（英文枚举→中文 / duration 计算）
- ✅ 设计 token + 16 H* 组件

#### 遗漏项

**M1. gap-analysis.md 的"⚠️ 多余端点"未全覆盖**

gap-analysis 列出：
- 术语表：batch-delete/toggle、import/export、note、candidates 审批
- 回顾模板：presets 选择器、import/export
- 场次：upload/fetch/archive 按钮
- recap/content 编辑

Plan 提到：
- ✅ Phase 4 RecapDrawerV10 补 upload/fetch/archive/编辑
- ✅ Phase 5 TemplateCardV10 补 presets 选择器、导入导出
- ⚠️ **GlossaryCardV10（Task 5.3 Step 2）提到"批量/导入导出"，但未显式提 `batch-delete` / `toggle` / `note` / `candidates` 审批**

**修复**：Phase 5 Task 5.3 Step 2 改为：
```markdown
- [ ] **Step 2: 实现 GlossaryCardV10.vue**
（全局术语表 CRUD + **候选审批**（GET /api/glossary/candidates）+ **批量删除**（POST batch-delete）+ **导入导出**（POST import/export）+ **备注**（GET/PUT note）；复用 useGlossaryEntries composable scope='global'）
```

---

**M2. Placeholder scan 不彻底**

Self-Review 声称"已检查，无 TBD/TODO"，但实际存在：
- Phase 2 Task 2.3/2.4 多处"实现 XXX.vue（代码 ~80 行）" = **概括性描述 = 变相 placeholder**
- Phase 3/5 同样问题

Self-Review 试图用"模式重复，但都指向具体文件 + 具体行为（GET/PUT/emit），无歧义"来辩护，但这不符合"bite-sized 2-5 分钟单步"标准 — 一个 80 行组件的"实现"步骤至少要 10-15 分钟。

**修复**：Self-Review 改为承认"Phase 2-5 部分组件步骤为概括性描述（实现优先），需执行时展开"。

---

## Blocking 问题汇总（必须修改）

| ID | 问题 | 影响范围 | 修复建议 |
|----|------|---------|---------|
| **B1** | Phase 1 Task 1.9 AppLayout 重写步骤不完整（只给关键结构） | Task 1.9 不可执行 | 拆成 3 步（script/template/style） |
| **B2** | Phase 2-5 约 20 个组件"实现 XXX.vue"无步骤拆解 | Phase 2-5 大量 task 不可执行 | 每个组件拆成 props/template/style/verify 4 步 |
| **B3** | Phase 0 Task 0.1 Step 3 实现细节不足（缺 scan 代码） | Task 0.1 不可执行 | 补完整 scanSessionCore/scanSessionWithChannel 代码 |
| **B4** | Phase 1 部分组件测试不完整（HDialog/HProgress/HEmpty/HDescriptions 无测试） | 违反 TDD，Phase 1 测试覆盖不足 | 每个组件独立 TDD |
| **B5** | Phase 2-5 业务组件全无测试（30+ 组件只靠视觉验证） | **严重违反 TDD 要求** | 关键组件补测试 OR 开头显式声明豁免策略 |

---

## Suggestions（可选优化）

| ID | 建议 | 优先级 |
|----|------|--------|
| **S1** | Phase 4 Task 4.5 RecapDrawerV10 拆成 3 步 | Medium |
| **S2** | Task 1.8 明确 HTabs 改为纯 CSS 模式 | Low |
| **S3** | Phase 0 补孤儿 session/task 检查步骤 | Medium |
| **S4** | Phase 1 显式加 Task 1.1b（EP 主题变量映射） | **High** |
| **S5** | Phase 0 补回滚策略说明 | Low |
| **S6** | Phase 5 Task 5.3 Step 2 显式列出 glossary 的 batch-delete/toggle/note/candidates | Medium |
| **S7** | Self-Review 承认 Phase 2-5 步骤为概括性描述 | Low |

---

## 最终建议

1. **立即修复 5 个 blocking 问题**（B1-B5），否则计划不可执行
2. **强烈建议补 S4**（EP 主题变量映射），避免过渡期视觉混乱
3. **重新审视 TDD 策略**：要么所有组件严格 TDD，要么在 Plan 开头显式声明"H* 组件 TDD，业务组件视觉验证"

修复后重新提交审核。
