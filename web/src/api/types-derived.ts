// web/src/api/types-derived.ts
// 从 generated.ts(openapi-typescript 生成)派生命名类型,替代手写 types.ts(Phase 6 删除 types.ts)。
//
// 策略:
//  - 实体类型(Session/Task/Channel/LiveStatus/RuntimeStatus 等)直接取 generated schema,
//    保留其 optional 语义(与真实 API omitempty 一致)。
//  - 配置类型(DashScope/ASRS3/WebDAV/Publish/Archive/Recap)合并 Response(读)+ Request(写)字段,
//    因表单提交时带明文 key/password/clear_* 写字段,而响应永不返回明文。types.ts 历史如此。
//  - 少数 generated schema 与前端历史用法不一致的字段(如 GlossaryEntry 的 source),
//    按前端实际消费形态定义,避免破坏运行时逻辑。
import type { components, paths } from './generated'

type Schema<K extends keyof components['schemas']> = components['schemas'][K]

// ---------- 实体 ----------
export type Session = Schema<'Session'>
export type Task = Schema<'Task'>
export type Channel = Schema<'Channel'>
export type LiveStatus = Schema<'LiveStatus'>
// Capabilities: generated 标 reason 为 optional(omitempty),但后端实际始终返回(空串或原因汇总)。
// 前端 capReason 等消费方按 string 处理,这里收窄为必填以匹配历史 types.ts。
export type Capabilities = Omit<NonNullable<Schema<'RuntimeStatus'>['capabilities']>, 'reason'> & {
  reason: string
}
// ConfigStatus: 后端 probe.go ConfigStatus struct 无 glossary_* 字段(glossary 已迁 DB 按 channel 存储,
// 无全局"已配置"语义)。历史手写 glossary_configured/path 是幽灵字段,SettingsView 清单项已改为无 done 属性。
export type ConfigStatus = Schema<'ConfigStatus'>
// RuntimeStatus: 用上面收窄后的 Capabilities / ConfigStatus 覆盖 generated 的嵌套字段,
// 使 store.status.capabilities.reason 等访问类型正确。
export type RuntimeStatus = Omit<Schema<'RuntimeStatus'>, 'capabilities' | 'config_status'> & {
  capabilities: Capabilities
  config_status: ConfigStatus
}
export type DashboardData = Schema<'DashboardData'>
export type DashboardChannel = Schema<'DashboardChannel'>
export type DashboardMonth = Schema<'DashboardMonth'>
export type DashboardCost = Schema<'DashboardCost'>
export type DashboardDanmaku = Schema<'DashboardDanmaku'>
export type CookieWarning = NonNullable<Schema<'RuntimeStatus'>['cookie_warnings']>[number]
export type DiskInfo = NonNullable<Schema<'RuntimeStatus'>['disk_usage']>[number]
export type ToolStatus = Schema<'ToolStatus'>
export type SessionFile = Schema<'SessionFile'>

export interface SessionDetail {
  session: Session
  files: SessionFile[]
}

// ---------- Task ----------
export type TaskStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'

// TaskProgressEvent(WS 事件):generated.ts 无对应条目(openapi-typescript 仅生成 HTTP schema,
// WS 事件 schema 未纳入 components)。保留手写定义,待 generated 纳入 WS schema 后再迁移。
export interface TaskProgressEvent {
  type: string
  task_id: string
  channel_id: string
  session_id: string
  status: TaskStatus
  progress: number
  message: string
  error: string
}

// ---------- 发现回放 / 回顾相关 ----------
export type DiscoverResult = Schema<'DiscoverResult'>
// 前端从预览结果勾选后回传给 Execute 的单项。schema 中对应 DiscoverExecuteItem
// (openapi 侧名为 DiscoverExecuteItem;前端历史命名为 DiscoverPickItem,保留别名兼容旧用法)。
export type DiscoverPickItem = Schema<'DiscoverExecuteItem'>
// RecapContent: 后端 getRecapContent(server.go)只返回 available/markdown/prompt/raw_response/suggested_terms,
// 无 bilibili 字段(历史手写补丁基于错误前提,已删除)。
export type RecapContent = Schema<'RecapContent'>

// 列表响应
type ListSessionsResp = paths['/api/sessions']['get']['responses'][200]['content']['application/json']
export type SessionList = ListSessionsResp extends { items: infer T } ? T[] : never

export interface ListResponse<T> {
  items: T[]
}

// ---------- Channel / 识别 ----------
export type UpsertChannelInput = Schema<'UpsertChannelInput'>
export type IdentifyInput = Schema<'IdentifyInput'>
export type IdentifyResult = Schema<'IdentifyResult'>
export type IdentifySaveResult = Schema<'IdentifySaveResult'>

// ---------- Bilibili QR 登录 ----------
export type QRCodeLoginStatus = 'pending' | 'scanned' | 'expired' | 'succeeded' | 'failed'
export type QRCodeCookieUsage = 'download' | 'publish'

// QRCodeSession: generated 名为 QRCodeGenerateResult,字段一致;保留历史命名。
export type QRCodeSession = Schema<'QRCodeGenerateResult'>
export type QRCodePollResult = Schema<'QRCodePollResult'>

export interface QRCodeSaveRequest {
  channel_id: string
  usage: QRCodeCookieUsage
}

export interface QRCodeSaveResponse {
  channel: Channel
  usage: QRCodeCookieUsage
  cookie_file: string
  uid: number
  expires_at: string
}

// ---------- Bili Cookie Account ----------
export type BiliCookieAccount = Schema<'BiliCookieAccount'>

// BiliTopic / BiliSeries: generated.ts 无对应 schema(发布相关枚举由前端独立消费)。
// 保留手写定义。
export interface BiliTopic {
  id: number
  name: string
  stat_desc: string
}

export interface BiliSeries {
  id: number
  name: string
  articles_count: number
}

// ---------- Glossary ----------
// GlossaryEntry: generated schema 无 source 字段(merged 视图才填 "global"|"channel"),
// 但前端 useGlossaryEntries 依赖 e.source 区分来源。叠加可选 source。
export type GlossaryEntry = Schema<'GlossaryEntry'> & { source?: string }

export type GlossaryCandidate = Schema<'GlossaryCandidate'>

export interface GlossaryNote {
  note: string
}

// ---------- Recap Template ----------
export type RecapTemplate = Schema<'RecapTemplate'>
export type TemplatePreset = Schema<'TemplatePreset'>

// ResolvedRecapTemplate: 键名与后端 ResolvedTemplate 的 json tag 一致(snake_case)。
export interface ResolvedRecapTemplate {
  system_prompt: string
  user_format: string
  fan_name: string
  extra_vars: Record<string, string>
}

export interface ChannelRecapTemplateResponse {
  global: RecapTemplate | null
  channel: RecapTemplate | null
  resolved: ResolvedRecapTemplate
}

// ---------- Recap ----------
export type RecapModelOption = Schema<'RecapModelOption'>

// ---------- 配置类型(Response 读字段 + Request 写字段合并) ----------
// DashScopeConfig: Response 字段 + 写入字段(api_key/clear_key)
export type DashScopeConfig = Schema<'DashScopeConfigResponse'> & {
  api_key?: string
  clear_key?: boolean
}

// ASRS3Config: Response + access_key_secret/clear_secret
export type ASRS3Config = Schema<'ASRS3ConfigResponse'> & {
  access_key_secret?: string
  clear_secret?: boolean
}

// WebDAVConfig: Response + password/clear_password
export type WebDAVConfig = Schema<'WebDAVConfigResponse'> & {
  password?: string
  clear_password?: boolean
}

// PublishConfig: Response 字段(api_key 无,纯读)
export type PublishConfig = Schema<'PublishConfigResponse'>

// ArchiveConfig
export type ArchiveConfig = Schema<'ArchiveConfigResponse'>

// RecapConfig: Response + api_key/clear_key
export type RecapConfig = Schema<'RecapConfigResponse'> & {
  api_key?: string
  clear_key?: boolean
}

// ---------- Secrets ----------
export interface SecretEntry {
  key: string
  masked_value: string
  set: boolean
  source: string
  updated_at: string
}

export type ConfigImportResult = Schema<'ConfigImportResult'>

export interface ApiError {
  error: string
  reason: string
}
