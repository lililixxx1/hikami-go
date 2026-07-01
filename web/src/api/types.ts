// ---------- Channel ----------

export interface Channel {
  id: string
  name: string
  uid: number
  live_room_id: number
  replay_source_url: string
  space_url: string
  title_prefix: string
  cookie_file: string
	download_cookie_file: string
  enabled: boolean
  auto_record: boolean
  auto_asr: boolean
  auto_recap: boolean
  record_danmaku: boolean
  source_mode: string
  discover_limit: number
  publish_enabled: boolean
  publish_mode: string
  publish_category_id: number
  publish_list_id: number
  publish_private_pub: number
  publish_original: number
  auto_publish: boolean
  publish_aigc: number
  publish_timer_pub_time: number
  publish_cover_url: string
  publish_topics: string
  recap_model: string
  max_continuations: number
  download_account_id: number | null
  created_at: string
  updated_at: string
}

export interface UpsertChannelInput {
  id: string
  name: string
  uid: number
  live_room_id: number
  replay_source_url: string
  space_url: string
  title_prefix: string
  cookie_file: string
	download_cookie_file: string
  enabled: boolean
  auto_record: boolean
  auto_asr: boolean
  auto_recap: boolean
  record_danmaku: boolean
  source_mode: string
  discover_limit: number
  publish_enabled: boolean
  publish_mode: string
  publish_category_id: number
  publish_list_id: number
  publish_private_pub: number
  publish_original: number
  auto_publish: boolean
  publish_aigc: number
  publish_timer_pub_time: number
  publish_cover_url: string
  publish_topics: string
  recap_model: string
  max_continuations: number
  download_account_id: number | null
}

export interface IdentifyInput {
  input: string
  uid?: number
  live_room_id?: number
}

export interface IdentifyResult {
  channel: UpsertChannelInput
  source: string
}

export interface IdentifySaveResult {
  channel: Channel
  source: string
  created: boolean
}

// ---------- Bilibili QR Login ----------

export type QRCodeLoginStatus = 'pending' | 'scanned' | 'expired' | 'succeeded' | 'failed'
export type QRCodeCookieUsage = 'download' | 'publish'

export interface QRCodeSession {
  session_id: string
  url: string
  expires_at: string
}

export interface QRCodePollResult {
  session_id: string
  status: QRCodeLoginStatus
  message: string
  uid?: number
  expires_at: string
}

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

export interface BiliCookieAccount {
  id: number
  uid: number
  nickname: string
  cookie_file: string
  is_default_download: boolean
  is_default_publish: boolean
  created_at: string
  updated_at: string
}

// ---------- Session ----------

export interface Session {
  id: string
  slug: string
  channel_id: string
  source_type: string
  source_id: string
  title: string
  started_at: string
  ended_at: string
  source_url: string
  status: string
  current_task_id: string
  last_error: string
  local_available: boolean
  uploaded_at: string
  published_at: string
  archived_at: string
  publish_target: string
  created_at: string
  updated_at: string
}

export interface SessionFile {
  path: string
  size: number
}

export interface SessionDetail {
  session: Session
  files: SessionFile[]
}

// ---------- Task ----------

export type TaskStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface Task {
  id: string
  channel_id: string
  session_id: string
  type: string
  status: TaskStatus
  payload: string
  progress: number
  message: string
  error: string
  attempt: number
  started_at: string
  finished_at: string
  created_at: string
  updated_at: string
}

// ---------- Live ----------

export interface LiveStatus {
  channel_id: string
  room_id: number
  live: boolean
  title: string
  started_at: string
  recording: boolean
  session_id: string
  task_id: string
  error: string
}

// ---------- Runtime ----------

export interface ToolStatus {
  name: string
  path: string
  required: boolean
  available: boolean
  error: string
  install_hint?: string
}

export interface Capabilities {
  replay_download: boolean
  asr_submit: boolean
  asr_model: string
  asr_request_mode: string
  recap_generate: boolean
  webdav_upload: boolean
  publish_opus: boolean
  reason: string
}

export interface ConfigStatus {
  dashscope_key_set: boolean
  dashscope_key_env: string
  asr_temp_configured: boolean
  recap_provider: string
  recap_key_set: boolean
  recap_key_env: string
  recap_model: string
  webdav_configured: boolean
  publish_enabled: boolean
  glossary_configured: boolean
  glossary_path: string
}

export interface RuntimeStatus {
  checked_at: string
  tools: Record<string, ToolStatus>
  capabilities: Capabilities
  config_status: ConfigStatus
  cookie_warnings?: { channel_id: string; channel_name: string; is_expired: boolean; expires_at: string }[]
  disk_usage?: { path: string; total: number; used: number; used_percent: number }[]
  // 账号池是否存在默认下载/发布账号；用于修正主播 cookie 状态显示（无主播级 cookie 时回退判定）
  has_default_download?: boolean
  has_default_publish?: boolean
}

// ---------- WebSocket Events ----------

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

// ---------- Discover ----------

export interface DiscoverResult {
  channel_id: string
  session_id: string
  source_id: string
  title: string
  created: boolean
  task_id: string
  error: string
}

// ---------- Generic ----------

export interface ListResponse<T> {
  items: T[]
}

// ---------- Stats ----------

export interface DashboardMonth {
  month: string
  session_count: number
  asr_hours: number
}

export interface DashboardChannel {
  channel_id: string
  channel_name: string
  session_count: number
  recap_count: number
  publish_count: number
}

export interface DashboardCost {
  month: string
  asr_hours: number
  asr_cost: number
  ai_cost: number
  total_cost: number
}

export interface DashboardDanmaku {
  channel_id: string
  channel_name: string
  danmaku_count: number
}

export interface DashboardData {
  sessions_by_month: DashboardMonth[]
  sessions_by_channel: DashboardChannel[]
  cost_trend: DashboardCost[]
  danmaku_top: DashboardDanmaku[]
  recap_count: number
  publish_count: number
}


// ---------- Publish Config ----------

export interface PublishConfig {
  enabled: boolean
  mode: string
  category_id: number
  list_id: number
  private_pub: number
  summary_len: number
  original: number
  aigc: number
  timer_pub_time: number
  cover_url: string
  auto_cover: boolean
  topics: string
  topic_id: number
  topic_name: string
  close_comment: number
  up_choose_comment: number
}

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

// ---------- Secrets ----------

export interface SecretEntry {
  key: string
  masked_value: string
  set: boolean
  source: string
  updated_at: string
}

export interface ConfigImportResult {
  imported: boolean
  strategy: string
  warnings?: string[]
  details: {
    secrets_count: number
    channels_count: number
    glossary_count: number
    templates_count: number
    bili_accounts_count: number
  }
}

export interface ApiError {
  error: string
  reason: string
}

// ---------- Glossary ----------

export interface GlossaryEntry {
  id: number
  channel_id: string
  term: string
  canonical: string
  category: string
  enabled: boolean
  source?: string  // "global" or "channel", only in merged view
  created_at: string
  updated_at: string
}

export interface GlossaryNote {
  note: string
}

// ---------- Recap ----------

export interface RecapContent {
  available: boolean
  markdown: string
  bilibili: string
  prompt: string
  raw_response: string
  suggested_terms?: string[]
}

// ---------- Recap Template ----------

export interface RecapConfig {
  enabled: boolean
  provider: string
  api_key_env: string
  api_key_set: boolean
  base_url: string
  model: string
  max_tokens: number
  max_continuations: number
  timeout_seconds: number
  include_speaker_info: boolean
  // 仅写入:保存时随配置一起提交,响应永不返回明文
  api_key?: string
  clear_key?: boolean
}

// DashScopeConfig 是阿里云 DashScope(ASR 转写)配置(GET/PUT /api/config/dashscope)。
export interface DashScopeConfig {
  api_key_env: string
  api_key_set: boolean
  asr_url: string
  tasks_url: string
  model: string
  language: string
  diarization_enabled: boolean
  speaker_count: number
  vocabulary_id: string
  // 仅写入:保存时随配置一起提交,响应永不返回明文
  api_key?: string
  clear_key?: boolean
}

// ASRS3Config 是 ASR 临时音频发布的对象存储配置(GET/PUT /api/config/asr-s3)。
// 密钥走 secrets.Store,响应永不返回 access_key_secret 明文。
export interface ASRS3Config {
  endpoint: string
  bucket: string
  access_key_id: string
  access_key_env: string
  region: string
  public_url_prefix: string
  use_path_style: boolean
  access_key_set: boolean
  // 仅写入:保存时随配置一起提交,响应永不返回明文
  access_key_secret?: string
  clear_secret?: boolean
}

// RecapModelOption 是后端推荐的回顾模型快捷选项（GET /api/config/recap/models）
export interface RecapModelOption {
  value: string
  label: string
  group: string
}

export interface WebDAVConfig {
  url: string
  username: string
  password?: string
  password_env: string
  base_path: string
  remote: string
  password_set: boolean
  clear_password?: boolean
}

/** 发布后自动归档到 WebDAV 的配置（独立任务，不推进 session 主状态） */
export interface ArchiveConfig {
  auto_after_publish: boolean
  cleanup_policy: 'none' | 'temp' | 'generated' | 'all'
}

export interface RecapTemplate {
  id: number
  channel_id: string
  name: string
  system_prompt: string
  user_format: string
  fan_name: string
  extra_vars: string
  enabled: boolean
  is_default: boolean
  created_at: string
  updated_at: string
}

export interface TemplatePreset {
  name: string
  description: string
  system_prompt: string
  user_format: string
}

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
