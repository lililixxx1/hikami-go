# 文档地图（Documentation Index）

> 本文件是 Hikami-Go 全部文档的导航索引，按「常青参考 / 活跃计划 / 归档」三类组织。
> 维护原则：顶层 `docs/` 与 `plans/` 只保留常青与活跃文档；历史/已落地/一次性报告归档于对应 `archive/` 子目录（保留 git 历史，可追溯）。
> 最后更新：2026-07-01

---

## 一、入口文档（常青）

| 文档 | 受众 | 内容 |
|------|------|------|
| [README.md](../README.md) | 人类开发者 / 用户 | 项目介绍、功能概览、快速开始、API 速查、项目结构 |
| [CLAUDE.md](../CLAUDE.md) | AI 上下文（主入口） | 架构总览、技术栈、Mermaid 模块图、精简模块索引、编码规范、AI 使用指引、变更记录 |
| [AGENTS.md](../AGENTS.md) | AI / 贡献者 | 仓库规范、构建测试命令、编码风格、Go Skills 索引、构建环境说明 |

## 二、AI 上下文补充文档（CLAUDE-detail/）

> 由根 `CLAUDE.md` 拆分，承载根文件不便展开的深度内容。已精简至 5 个文件（删除了与根重复的 architecture/modules/changelog）。

| 文档 | 内容 |
|------|------|
| [api-routes.md](../CLAUDE-detail/api-routes.md) | 所有 API 端点（~105 条）与通知事件完整清单 |
| [pipelines.md](../CLAUDE-detail/pipelines.md) | 回顾管道、术语发现、模板、续写、来源模式、健康检查、引导 |
| [frontend-types.md](../CLAUDE-detail/frontend-types.md) | TypeScript 类型定义与前端 API 模块说明 |
| [development.md](../CLAUDE-detail/development.md) | 构建、运行、配置（20 项）、完整编码规范、逐模块 AI 使用指引 |
| [testing.md](../CLAUDE-detail/testing.md) | 测试策略和现有测试覆盖 |

## 三、模块级文档（常青，随代码演进）

每个 `internal/*/` 与 `cmd/hikami/`、`web/` 下各有一份 `CLAUDE.md`，记录该模块的职责、对外接口、测试与变更记录。完整清单见根 [CLAUDE.md](../CLAUDE.md) 的「精简模块索引」表与 Mermaid 图（共 26 个内部 Go 包 + `cmd/hikami` + `web` = 28 份，每份含导航面包屑）。

## 四、设计文档（docs/，常青参考）

| 文档 | 内容 |
|------|------|
| [DESIGN.md](DESIGN.md) | 系统设计总览（以源码为准） |
| [data-flow.md](data-flow.md) | 数据流与管道细节（当前最详尽） |
| [BUSINESS_FLOW.md](BUSINESS_FLOW.md) | 业务流程 |
| [FRONTEND_ARCHITECTURE.md](FRONTEND_ARCHITECTURE.md) | 前端架构（重构后权威状态） |
| [BILI_OPUS_CAPTURE_GUIDE.md](BILI_OPUS_CAPTURE_GUIDE.md) | B 站专栏抓包/诊断指南 |

## 五、活跃计划（plans/）

> 当前无活跃计划。所有历史设计计划已随仓库重建清理。

## 六、归档区（历史 / 已落地 / 一次性）

### docs/archive/refactor/ — 前端重构三部曲（已 100% 完成）

| 文档 | 说明 |
|------|------|
| [FRONTEND_REFACTOR_BASELINE.md](archive/refactor/FRONTEND_REFACTOR_BASELINE.md) | 重构前快照 |
| [FRONTEND_REFACTOR_PLAN.md](archive/refactor/FRONTEND_REFACTOR_PLAN.md) | 重构计划 |
| [FRONTEND_REFACTOR_ORPHAN_DECISIONS.md](archive/refactor/FRONTEND_REFACTOR_ORPHAN_DECISIONS.md) | 孤立组件决策 |

### docs/archive/investigations/ — 一次性报告

| 文档 | 说明 |
|------|------|
| [前端兜底页-embedded_web构建标签缺失.md](archive/investigations/前端兜底页-embedded_web构建标签缺失.md) | `embedded_web` build tag 缺失导致前端不嵌入、降级 API-only 兜底页的诊断报告 |
| [recap-compare-26.04.22-优化方案.md](archive/investigations/recap-compare-26.04.22-优化方案.md) | 单次回顾质量对比决策（v4-pro 已采纳为默认） |
| [recap-compare-26.04.22-实施计划.md](archive/investigations/recap-compare-26.04.22-实施计划.md) | 同上实施计划 |
