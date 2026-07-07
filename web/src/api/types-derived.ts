// web/src/api/types-derived.ts
// 从 generated.ts(openapi-typescript 生成)派生命名类型,逐步替代手写 types.ts
import type { components, paths } from './generated'

type Schema<K extends keyof components['schemas']> = components['schemas'][K]

// 实体
// NOTE: source_type 在 generated schema 里被收窄为 "live"|"download"|"import",但后端实际还会发出
// "live_record" 等值(枚举不完整)。这里放宽为 string,与旧手写 types.ts 一致,也使派生 Session
// 与 sessionActions.ts 消费的 types.ts Session 在该字段上兼容。其余字段保留 generated 的 optional 语义
// (与真实 API omitempty 一致;plan 的测试 fixture 按需填充,未填的允许缺席)。
// 与 sessionActions.ts(types.ts 的全必填 Session/Task)互操作时,由 V10 组件在调用边界做窄化转换。
export type Session = Omit<Schema<'Session'>, 'source_type'> & { source_type: string }
export type Task = Schema<'Task'>
export type Channel = Schema<'Channel'>
export type LiveStatus = Schema<'LiveStatus'>
export type RuntimeStatus = Schema<'RuntimeStatus'>
export type Capabilities = NonNullable<RuntimeStatus['capabilities']>
export type DashboardData = Schema<'DashboardData'>
export type DashboardChannel = Schema<'DashboardChannel'>
export type DashboardMonth = Schema<'DashboardMonth'>
export type DashboardCost = Schema<'DashboardCost'>
export type CookieWarning = NonNullable<RuntimeStatus['cookie_warnings']>[number]
export type DiskInfo = NonNullable<RuntimeStatus['disk_usage']>[number]

// 发现回放 / 回顾相关(Phase 4 RecapsView 迁移)
export type DiscoverResult = Schema<'DiscoverResult'>
// 前端从预览结果勾选后回传给 Execute 的单项。schema 中对应 DiscoverExecuteItem
// (openapi 侧名为 DiscoverExecuteItem;前端历史命名为 DiscoverPickItem,这里保留别名兼容旧用法)。
export type DiscoverPickItem = Schema<'DiscoverExecuteItem'>
// RecapContent: generated schema 缺 bilibili 字段,但后端 GET /recap 实际返回 bilibili(旧 EP 类型已含,
// 且 RecapDrawerV10 测试 fixture 带 bilibili:'')。这里以 schema 为基叠加可选 bilibili,兼容真实响应。
export type RecapContent = Schema<'RecapContent'> & { bilibili?: string }

// 列表响应
type ListSessionsResp = paths['/api/sessions']['get']['responses'][200]['content']['application/json']
export type SessionList = ListSessionsResp extends { items: infer T } ? T[] : never

// NOTE: TaskProgressEvent(WS 事件)在 generated.ts 的 components/schemas 中无对应条目
// (openapi-typescript 仅生成 HTTP schema,WS 事件 schema 未纳入 components)。
// 故仍保留在 types.ts;types-derived 待 generated 纳入 WS schema 后再迁移。
