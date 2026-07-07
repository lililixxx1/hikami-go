// web/src/api/types-derived.ts
// 从 generated.ts(openapi-typescript 生成)派生命名类型,逐步替代手写 types.ts
import type { components, paths } from './generated'

type Schema<K extends keyof components['schemas']> = components['schemas'][K]

// 实体
export type Session = Schema<'Session'>
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

// 列表响应
type ListSessionsResp = paths['/api/sessions']['get']['responses'][200]['content']['application/json']
export type SessionList = ListSessionsResp extends { items: infer T } ? T[] : never

// NOTE: TaskProgressEvent(WS 事件)在 generated.ts 的 components/schemas 中无对应条目
// (openapi-typescript 仅生成 HTTP schema,WS 事件 schema 未纳入 components)。
// 故仍保留在 types.ts;types-derived 待 generated 纳入 WS schema 后再迁移。
