import client, { get, post, put } from './client'
import type { ASRS3Config, ArchiveConfig, BiliSeries, BiliTopic, ConfigImportResult, SecretEntry, DashScopeConfig, PublishConfig, RecapConfig, RecapModelOption, WebDAVConfig } from './types-derived'

export interface SecretsResponse {
  items: SecretEntry[]
}

export function listSecrets(): Promise<SecretsResponse> {
  return get('/api/secrets')
}

export function updateSecret(key: string, value: string): Promise<SecretEntry> {
  return put(`/api/secrets/${encodeURIComponent(key)}`, { value })
}

export function getPublishConfig(): Promise<PublishConfig> {
  return get('/api/config/publish')
}

export function updatePublishConfig(config: PublishConfig): Promise<PublishConfig> {
  return put('/api/config/publish', config)
}

export function getRecapConfig(): Promise<RecapConfig> {
  return get('/api/config/recap')
}

export function updateRecapConfig(config: RecapConfig): Promise<RecapConfig> {
  return put('/api/config/recap', config)
}

export function getDashScopeConfig(): Promise<DashScopeConfig> {
  return get('/api/config/dashscope')
}

export function updateDashScopeConfig(config: DashScopeConfig): Promise<DashScopeConfig> {
  return put('/api/config/dashscope', config)
}

export function getASRS3Config(): Promise<ASRS3Config> {
  return get('/api/config/asr-s3')
}

export function updateASRS3Config(config: ASRS3Config): Promise<ASRS3Config> {
  return put('/api/config/asr-s3', config)
}

export function getRecapModels(): Promise<{ models: RecapModelOption[] }> {
  return get('/api/config/recap/models')
}

export function getWebDAVConfig(): Promise<WebDAVConfig> {
  return get('/api/config/webdav')
}

export function updateWebDAVConfig(config: WebDAVConfig): Promise<WebDAVConfig> {
  return put('/api/config/webdav', config)
}

export function getArchiveConfig(): Promise<ArchiveConfig> {
  return get('/api/config/archive')
}

export function updateArchiveConfig(config: ArchiveConfig): Promise<ArchiveConfig> {
  return put('/api/config/archive', config)
}

// tools 段(yt_dlp/rclone 路径)。generated.ts 尚未含此端点(OpenAPI spec 待同步生成),
// 过渡期手写内联类型;openapi-typescript 重新生成后改走 types-derived 派生类型。
export interface ToolsConfig {
  yt_dlp: string
  rclone: string
}

export function getToolsConfig(): Promise<ToolsConfig> {
  return get('/api/config/tools')
}

export function updateToolsConfig(config: Partial<ToolsConfig>): Promise<ToolsConfig> {
  return put('/api/config/tools', config)
}

// MCP 搜索工具配置段(MCP 搜索集成)。密钥字段只返回是否已设置(只写)。
export interface MCPServerConfig {
  name: string
  transport: 'http' | 'sse' | 'stdio'
  url: string
  command: string
  args: string[]
  env: string[]
  enabled: boolean
  timeout_sec: number
  headers?: Record<string, string>
}

export interface MCPBuiltinConfig {
  brave_api_key_set: boolean
  brave_api_key_env: string
  tavily_api_key_set: boolean
  tavily_api_key_env: string
}

export interface MCPConfig {
  enabled: boolean
  servers: MCPServerConfig[]
  builtin: MCPBuiltinConfig
  max_tool_rounds: number
}

// MCPConfig 的 PUT 请求(partial,密钥字段在 builtin 内)。
export interface MCPConfigUpdate {
  enabled?: boolean
  servers?: MCPServerConfig[]
  builtin?: {
    brave_api_key?: string
    brave_api_key_env?: string
    tavily_api_key?: string
    tavily_api_key_env?: string
  }
  max_tool_rounds?: number
}

export function getMCPConfig(): Promise<MCPConfig> {
  return get('/api/config/mcp')
}

export function updateMCPConfig(config: MCPConfigUpdate): Promise<MCPConfig> {
  return put('/api/config/mcp', config)
}

// 触发 AI 批量复核 pending 候选词(异步,返回 202)。
export function reviewGlossaryCandidates(channelId?: string): Promise<{ ok: boolean; message: string }> {
  return post('/api/glossary/candidates/review', channelId ? { channel_id: channelId } : {})
}

export function getOnboardingStatus(): Promise<{
  needed: boolean
  has_tools: boolean
  has_keys: boolean
  has_channels: boolean
}> {
  return get('/api/onboarding/status')
}

export function searchBiliTopics(keywords: string, pageSize = 20, pageNum = 1): Promise<{ items: BiliTopic[] }> {
  return get('/api/bili/topics/search', { keywords, page_size: pageSize, page_num: pageNum })
}

export function listBiliSeries(channelId?: string): Promise<{ items: BiliSeries[]; error?: string }> {
  // 2026-07-20:可选 channel_id 参数,用于主播抽屉 per-channel 文集下拉。
  // 缺省(全局发布卡)不传 channel_id,等价旧行为(全局默认发布账号)。
  return get('/api/bili/series/list', channelId ? { channel_id: channelId } : undefined)
}

export function exportConfig(): Promise<Blob> {
  return client.get('/api/config/export', { responseType: 'blob' }).then(r => r.data)
}

export function importConfig(json: string, strategy: 'merge' | 'overwrite'): Promise<ConfigImportResult> {
  return client.post(`/api/config/import?strategy=${strategy}`, json, {
    headers: { 'Content-Type': 'application/json' },
  }).then(r => r.data)
}
