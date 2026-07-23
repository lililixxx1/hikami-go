package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hikami-go/internal/notify"

	"github.com/spf13/viper"
)

type Config struct {
	OutputRoot string `mapstructure:"output_root"`
	DBPath     string `mapstructure:"db_path"`
	LogFormat  string `mapstructure:"log_format" yaml:"log_format"`

	FFmpeg  string `mapstructure:"ffmpeg"`
	FFprobe string `mapstructure:"ffprobe"`
	YTDLP   string `mapstructure:"yt_dlp"`
	Rclone  string `mapstructure:"rclone"`

	Web        WebConfig        `mapstructure:"web"`
	Worker     WorkerConfig     `mapstructure:"worker"`
	Cron       CronConfig       `mapstructure:"cron"`
	LiveRecord LiveRecordConfig `mapstructure:"live_record"`
	Logs       LogsConfig       `mapstructure:"logs"`
	DashScope  DashScopeConfig  `mapstructure:"dashscope"`
	ASRTemp    ASRTempConfig    `mapstructure:"asr_temp"`
	ASRS3      ASRS3Config      `mapstructure:"asr_s3"`
	RecapAI    RecapAIConfig    `mapstructure:"recap_ai"`
	WebDAV     WebDAVConfig     `mapstructure:"webdav"`
	Upload     UploadConfig     `mapstructure:"upload"`
	Archive    ArchiveConfig    `mapstructure:"archive"`
	Downloader DownloaderConfig `mapstructure:"downloader"`
	Publish    PublishConfig    `mapstructure:"publish"`
	MCP        MCPConfig        `mapstructure:"mcp"`

	Notify NotifyConfig `mapstructure:"notify"`

	CookieEncryptionKey string `mapstructure:"cookie_encryption_key"`

	BootstrapChannels []BootstrapChannel `mapstructure:"bootstrap_channels"`
}

type WebConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	Listen          string `mapstructure:"listen"`
	AutoOpenBrowser bool   `mapstructure:"auto_open_browser"`
	AdminToken      string `mapstructure:"admin_token"`
	AdminTokenEnv   string `mapstructure:"admin_token_env"`
}

// isLoopbackListen 判断 listen 地址是否仅绑定回环地址。
// 空主机（":port"）、"0.0.0.0"、"::" 视为绑定所有接口（非 loopback），需配合 admin_token。
func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

type WorkerConfig struct {
	Num              int  `mapstructure:"num"`
	AutoRetry        bool `mapstructure:"auto_retry"`
	MaxRetryAttempts int  `mapstructure:"max_retry_attempts"`
	RetryDelay       int  `mapstructure:"retry_delay_seconds"`
}

type CronConfig struct {
	Discovery string `mapstructure:"discovery"`
	LiveCheck string `mapstructure:"live_check"`
}

type LiveRecordConfig struct {
	Enabled              bool   `mapstructure:"enabled"`
	AudioOnly            bool   `mapstructure:"audio_only"`
	RecordDanmaku        bool   `mapstructure:"record_danmaku"`
	AudioContainer       string `mapstructure:"audio_container"`
	RequireAudioStream   bool   `mapstructure:"require_audio_stream"`
	FallbackExtractAudio bool   `mapstructure:"fallback_extract_audio"`
	GenerateASRAudio     bool   `mapstructure:"generate_asr_audio"`
	SegmentMinutes       int    `mapstructure:"segment_minutes"`
	StopGraceSeconds     int    `mapstructure:"stop_grace_seconds"`
	AutoReconnect        bool   `mapstructure:"auto_reconnect"`
	MaxReconnect         int    `mapstructure:"max_reconnect"`
	ReconnectDelay       int    `mapstructure:"reconnect_delay_seconds"`
}

type LogsConfig struct {
	Dir    string `mapstructure:"dir"`
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type DashScopeConfig struct {
	APIKeyEnv          string `mapstructure:"api_key_env"`
	ASRURL             string `mapstructure:"asr_url"`
	TasksURL           string `mapstructure:"tasks_url"`
	Model              string `mapstructure:"model"`
	Language           string `mapstructure:"language"`
	DiarizationEnabled bool   `mapstructure:"diarization_enabled"`
	SpeakerCount       int    `mapstructure:"speaker_count"`
	VocabularyID       string `mapstructure:"vocabulary_id"`
}

type ASRTempConfig struct {
	RcloneRemote        string `mapstructure:"rclone_remote"`
	BasePath            string `mapstructure:"base_path"`
	PublicBaseURL       string `mapstructure:"public_base_url"`
	CleanupAfterSuccess bool   `mapstructure:"cleanup_after_success"`
	Enabled             bool   `mapstructure:"enabled"`
	Listen              string `mapstructure:"listen"`
	LocalDir            string `mapstructure:"local_dir"`
}

type RecapAIConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	Provider           string `mapstructure:"provider"`
	APIKeyEnv          string `mapstructure:"api_key_env"`
	BaseURL            string `mapstructure:"base_url"`
	Model              string `mapstructure:"model"`
	MaxTokens          int    `mapstructure:"max_tokens"`
	MaxContinuations   int    `mapstructure:"max_continuations"`
	TimeoutSeconds     int    `mapstructure:"timeout_seconds"`
	IncludeSpeakerInfo bool   `mapstructure:"include_speaker_info"`
	CLIPath            string `mapstructure:"cli_path"`
	// deprecated: glossary is now stored in database, use /api/glossary endpoints
	GlossaryFile        string `mapstructure:"glossary_file"`
	EnableSummarization bool   `mapstructure:"enable_summarization"`
}

type ASRS3Config struct {
	Endpoint        string `mapstructure:"endpoint"`
	Bucket          string `mapstructure:"bucket"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	AccessKeySecret string `mapstructure:"access_key_secret"`
	AccessKeyEnv    string `mapstructure:"access_key_env"`
	Region          string `mapstructure:"region"`
	PublicURLPrefix string `mapstructure:"public_url_prefix"`
	UsePathStyle    bool   `mapstructure:"use_path_style"`

	// accessKeyManaged 同 WebDAVConfig.passwordManaged：标记密钥是否被 UI/secrets 接管，
	// managed=true 时 EffectiveAccessKey 不回落 config.yaml 明文。无 tag，仅 ApplyOverrides 注入。
	accessKeyManaged bool
}

// 回顾 AI 默认值(留空兜底用,单一来源)。
// viper SetDefault、provider 兜底、probe、handler 响应全部引用此处,避免多处字面量不一致。
const (
	DefaultRecapProvider = "openai_compatible"
	DefaultRecapBaseURL  = "https://api.deepseek.com"
	DefaultRecapModel    = "deepseek-v4-pro"
)

// DashScope 默认值(留空兜底用,单一来源)。
// viper SetDefault、Effective 方法、调用方全部引用此处,避免多处字面量不一致。
// 修复 2026-07-20 BUG #1:配置备份导入会把空串持久化到 runtime_settings,
// 覆盖 viper SetDefault 默认值,导致 ASR POST 到空 URL。
// Effective 方法在调用点兜底,无论数据来自 config.yaml/runtime_settings/import 都不会失败。
const (
	DefaultDashScopeASRURL   = "https://dashscope.aliyuncs.com/api/v1/services/audio/asr/transcription"
	DefaultDashScopeTasksURL = "https://dashscope.aliyuncs.com/api/v1/tasks"
)

// EffectiveProvider 返回留空兜底后的有效 provider,空值回落到 DeepSeek 默认。
// newRecapConfigResponse、recap.NewConfiguredProvider、runtime.probeRecapProvider 必须使用。
func (r RecapAIConfig) EffectiveProvider() string {
	if p := strings.TrimSpace(r.Provider); p != "" {
		return p
	}
	return DefaultRecapProvider
}

// EffectiveBaseURL 返回留空兜底后的有效 base_url,空值回落到 DeepSeek 官方地址。
// provider_openai.Generate 必须使用,避免空 base_url 拼出无 host 的 /chat/completions。
func (r RecapAIConfig) EffectiveBaseURL() string {
	if u := strings.TrimSpace(r.BaseURL); u != "" {
		return u
	}
	return DefaultRecapBaseURL
}

// EffectiveModel 返回留空兜底后的有效 model,空值回落到 deepseek-v4-pro。
func (r RecapAIConfig) EffectiveModel() string {
	if m := strings.TrimSpace(r.Model); m != "" {
		return m
	}
	return DefaultRecapModel
}

// EffectiveAPIKeyEnv 返回留空兜底后的有效密钥环境变量名,空值回落到 AI_API_KEY。
func (r RecapAIConfig) EffectiveAPIKeyEnv() string {
	if e := strings.TrimSpace(r.APIKeyEnv); e != "" {
		return e
	}
	return "AI_API_KEY"
}

// EffectiveAPIKeyEnv 返回留空兜底后的有效密钥环境变量名,空值回落到 DASHSCOPE_API_KEY。
func (d DashScopeConfig) EffectiveAPIKeyEnv() string {
	if e := strings.TrimSpace(d.APIKeyEnv); e != "" {
		return e
	}
	return "DASHSCOPE_API_KEY"
}

// EffectiveASRURL 返回留空兜底后的有效 asr_url,空值回落到 DashScope 官方默认。
// internal/asr/dashscope.go 提交转写任务必须使用,避免 POST 到空 URL
// (修复 2026-07-20 BUG #1:配置备份导入会把空串持久化到 runtime_settings,
// 覆盖 viper SetDefault 默认值)。
func (d DashScopeConfig) EffectiveASRURL() string {
	if u := strings.TrimSpace(d.ASRURL); u != "" {
		return u
	}
	return DefaultDashScopeASRURL
}

// EffectiveTasksURL 返回留空兜底后的有效 tasks_url,空值回落到 DashScope 官方默认。
// internal/asr/dashscope.go 查询任务状态必须使用。
func (d DashScopeConfig) EffectiveTasksURL() string {
	if u := strings.TrimSpace(d.TasksURL); u != "" {
		return u
	}
	return DefaultDashScopeTasksURL
}

// EffectiveAccessKeyEnv 返回留空兜底后的有效密钥环境变量名,空值回落到 ASR_S3_ACCESS_KEY_SECRET。
func (a ASRS3Config) EffectiveAccessKeyEnv() string {
	if e := strings.TrimSpace(a.AccessKeyEnv); e != "" {
		return e
	}
	return "ASR_S3_ACCESS_KEY_SECRET"
}

func (c *ASRS3Config) SecretResolved() string {
	if envKey := c.EffectiveAccessKeyEnv(); envKey != "" {
		if secret := os.Getenv(envKey); secret != "" {
			return secret
		}
	}
	return c.AccessKeySecret
}

// AccessKeyManaged 报告密钥是否已被 UI/secrets 接管（tombstone）。同 WebDAV.PasswordManaged。
func (c ASRS3Config) AccessKeyManaged() bool { return c.accessKeyManaged }

// SetAccessKeyManaged 由 handler 设置 tombstone 状态（跨包写未导出字段）。
func (c *ASRS3Config) SetAccessKeyManaged(v bool) { c.accessKeyManaged = v }

// EffectiveAccessKey 返回运行时生效的 ASR S3 access key。
// managed=false：先 env 后回落明文（= SecretResolved，向后兼容）。
// managed=true（UI 接管）：仅 env，空则空，不回落 config.yaml 明文（r11/r13 tombstone）。
func (c ASRS3Config) EffectiveAccessKey() string {
	if c.accessKeyManaged {
		return os.Getenv(c.EffectiveAccessKeyEnv())
	}
	return c.SecretResolved()
}

func (c *ASRS3Config) Configured() bool {
	return strings.TrimSpace(c.Endpoint) != "" &&
		strings.TrimSpace(c.Bucket) != "" &&
		strings.TrimSpace(c.AccessKeyID) != "" &&
		strings.TrimSpace(c.EffectiveAccessKey()) != "" &&
		strings.TrimSpace(c.PublicURLPrefix) != ""
}

type WebDAVConfig struct {
	Remote      string `mapstructure:"remote"`
	BasePath    string `mapstructure:"base_path"`
	URL         string `mapstructure:"url"`
	Username    string `mapstructure:"username"`
	Password    string `mapstructure:"password"`
	PasswordEnv string `mapstructure:"password_env"`

	// passwordManaged 标记密码是否已被 UI/secrets 接管。无 mapstructure/json tag：
	// 不读 config.yaml、不参与 DTO marshal。仅由 ApplyOverrides 从 runtime_settings
	// 的 publish DTO 注入。managed=true 时 EffectivePassword 不回落 config.yaml 明文，
	// 使「UI 清除密码」真正生效（r11/r13 tombstone 语义）。
	passwordManaged bool
}

func (c *ASRTempConfig) NativeConfigured() bool {
	return c.Enabled && strings.TrimSpace(c.LocalDir) != "" && strings.TrimSpace(c.PublicBaseURL) != ""
}

func (c *ASRTempConfig) RcloneConfigured() bool {
	return strings.TrimSpace(c.RcloneRemote) != ""
}

// EffectivePasswordEnv 返回留空兜底后的有效密码环境变量名，空值回落到 WEBDAV_PASSWORD。
// 范本同 DashScopeConfig.EffectiveAPIKeyEnv。
func (c WebDAVConfig) EffectivePasswordEnv() string {
	if e := strings.TrimSpace(c.PasswordEnv); e != "" {
		return e
	}
	return "WEBDAV_PASSWORD"
}

// PasswordManaged 报告密码是否已被 UI/secrets 接管（tombstone）。
// handler 跨包只读此访问器；写入由 ApplyOverrides 在本包内完成（r14 跨包可见性修正）。
func (c WebDAVConfig) PasswordManaged() bool { return c.passwordManaged }

// SetPasswordManaged 由 handler 设置 tombstone 状态（跨包写未导出字段）。
func (c *WebDAVConfig) SetPasswordManaged(v bool) { c.passwordManaged = v }

// EffectivePassword 返回运行时生效的 WebDAV 密码。
// managed=false（未接管，向后兼容旧 config.yaml）：先 env 后回落明文 password（= PasswordResolved）。
// managed=true（UI 接管）：仅 env，env 空则返回空，**不回落 config.yaml 明文**，
// 使「UI 清除密码」真正生效（r11/r13 tombstone）。
func (c WebDAVConfig) EffectivePassword() string {
	if c.passwordManaged {
		return os.Getenv(c.EffectivePasswordEnv())
	}
	return c.PasswordResolved()
}

func (c *WebDAVConfig) PasswordResolved() string {
	if env := c.EffectivePasswordEnv(); env != "" {
		if password := os.Getenv(env); password != "" {
			return password
		}
	}
	return c.Password
}

// NativeConfigured 判定 native WebDAV 后端是否可用。
// 要求 URL 与密码都齐：上传能力需凭据完整，清除密码后 capability 关闭（r13 [Medium]）。
// 注意：这会把匿名/无密码 WebDAV 判为未配置——属既定产品语义，在 config 示例注释写明。
func (c *WebDAVConfig) NativeConfigured() bool {
	return strings.TrimSpace(c.URL) != "" && c.EffectivePassword() != ""
}

func (c *WebDAVConfig) RcloneConfigured() bool {
	return strings.TrimSpace(c.Remote) != ""
}

type UploadConfig struct {
	CleanupPolicy string `mapstructure:"cleanup_policy"`
}

// ArchiveConfig 发布成功后自动归档到 WebDAV 的配置。与 UploadConfig 的手动上传路径
// 解耦：归档任务不推进 session 主状态（保持 published），仅写 archived_at；删除策略
// 用独立的 archive.cleanup_policy，不复用 upload.cleanup_policy。
type ArchiveConfig struct {
	AutoAfterPublish bool   `mapstructure:"auto_after_publish"` // 发布成功后自动归档
	CleanupPolicy    string `mapstructure:"cleanup_policy"`     // 归档后删除范围（none/temp/generated/all）
}

type DownloaderConfig struct {
	Backend string `mapstructure:"backend"`
}

// MCPConfig 控制 MCP(Model Context Protocol)搜索工具集成。
// 开启后,recap(回顾生成)与 glossary(术语发现/复核)的 AI 调用可附带工具,
// 让模型主动联网查证人名/游戏名/专有词,增强术语校正准确性。
// 未配置或 provider 不支持 tool calling 时,静默降级为普通 Generate(零回归)。
type MCPConfig struct {
	Enabled       bool              `mapstructure:"enabled"`         // 总开关,false 时所有 MCP 能力关闭
	Servers       []MCPServerConfig `mapstructure:"servers"`         // 外部 MCP server 列表(stdio/http/sse)
	Builtin       MCPBuiltinConfig  `mapstructure:"builtin"`         // 内置 in-process 搜索工具(Brave/Tavily)
	MaxToolRounds int               `mapstructure:"max_tool_rounds"` // agent loop 最大工具调用轮次(防死循环)
}

// MCPServerConfig 描述一个外部 MCP server 连接。
// Transport 取值:"http"(Streamable HTTP)/"sse"/"stdio"。
// http/sse 用 URL;stdio 用 Command+Args+Env(子进程)。
type MCPServerConfig struct {
	Name       string   `mapstructure:"name"`        // 唯一标识,用于日志与工具名前缀
	Transport  string   `mapstructure:"transport"`   // http | sse | stdio
	URL        string   `mapstructure:"url"`         // http/sse 模式的 server URL
	Command    string   `mapstructure:"command"`     // stdio 模式的可执行命令
	Args       []string `mapstructure:"args"`        // stdio 模式的命令参数
	Env        []string `mapstructure:"env"`         // stdio 模式的环境变量(KEY=VALUE)
	Enabled    bool     `mapstructure:"enabled"`     // 是否启用此 server
	TimeoutSec int      `mapstructure:"timeout_sec"` // 单次工具调用超时(秒),<=0 用默认 30
}

// MCPBuiltinConfig 内置搜索工具的 API 密钥配置。
// 密钥走 env 名(与 RecapAI.APIKeyEnv 模式一致),也可直接填明文(便于 UI 配置)。
// 对应 key 为空时该内置工具不注册(降级)。
type MCPBuiltinConfig struct {
	BraveAPIKey     string `mapstructure:"brave_api_key"`      // Brave Search API key 明文(UI 填)
	BraveAPIKeyEnv  string `mapstructure:"brave_api_key_env"`  // Brave key 的环境变量名,空兜底 BRAVE_API_KEY
	TavilyAPIKey    string `mapstructure:"tavily_api_key"`     // Tavily API key 明文(UI 填)
	TavilyAPIKeyEnv string `mapstructure:"tavily_api_key_env"` // Tavily key 的环境变量名,空兜底 TAVILY_API_KEY
}

// EffectiveBraveAPIKey 返回生效的 Brave key:优先明文,其次 env(空名兜底 BRAVE_API_KEY)。
func (b MCPBuiltinConfig) EffectiveBraveAPIKey() string {
	if k := strings.TrimSpace(b.BraveAPIKey); k != "" {
		return k
	}
	envName := strings.TrimSpace(b.BraveAPIKeyEnv)
	if envName == "" {
		envName = "BRAVE_API_KEY"
	}
	return os.Getenv(envName)
}

// EffectiveTavilyAPIKey 返回生效的 Tavily key(同上逻辑)。
func (b MCPBuiltinConfig) EffectiveTavilyAPIKey() string {
	if k := strings.TrimSpace(b.TavilyAPIKey); k != "" {
		return k
	}
	envName := strings.TrimSpace(b.TavilyAPIKeyEnv)
	if envName == "" {
		envName = "TAVILY_API_KEY"
	}
	return os.Getenv(envName)
}

// EffectiveMaxToolRounds 返回生效的最大工具轮次,<=0 兜底 5。
func (m MCPConfig) EffectiveMaxToolRounds() int {
	if m.MaxToolRounds > 0 {
		return m.MaxToolRounds
	}
	return 5
}

func (c *DownloaderConfig) NativeConfigured() bool {
	backend := strings.ToLower(strings.TrimSpace(c.Backend))
	return backend == "" || backend == "auto" || backend == "native"
}

func (c *DownloaderConfig) YTDLPConfigured() bool {
	return strings.EqualFold(strings.TrimSpace(c.Backend), "ytdlp")
}

type PublishConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	Mode            string `mapstructure:"mode"`
	CategoryID      int    `mapstructure:"category_id"`
	ListID          int    `mapstructure:"list_id"`
	PrivatePub      int    `mapstructure:"private_pub"`
	SummaryLen      int    `mapstructure:"summary_len"`
	Original        int    `mapstructure:"original"`
	Aigc            int    `mapstructure:"aigc"`
	TimerPubTime    int64  `mapstructure:"timer_pub_time"`
	CoverURL        string `mapstructure:"cover_url"`
	AutoCover       bool   `mapstructure:"auto_cover"` // true=优先自动取视频/直播官方封面；取不到或关闭时回退 cover_url
	Topics          string `mapstructure:"topics"`
	TopicID         int    `mapstructure:"topic_id"`
	TopicName       string `mapstructure:"topic_name"`
	CloseComment    int    `mapstructure:"close_comment"`
	UpChooseComment int    `mapstructure:"up_choose_comment"`
}

type NotifyConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	Type          string   `mapstructure:"type"`
	WebhookURL    string   `mapstructure:"webhook_url"`
	BarkURL       string   `mapstructure:"bark_url"`
	BarkKey       string   `mapstructure:"bark_key"`
	ServerChanKey string   `mapstructure:"serverchan_key"`
	Events        []string `mapstructure:"events"`
}

type BootstrapChannel struct {
	ID                  string `mapstructure:"id"`
	Name                string `mapstructure:"name"`
	UID                 int64  `mapstructure:"uid"`
	LiveRoomID          int64  `mapstructure:"live_room_id"`
	ReplaySourceURL     string `mapstructure:"replay_source_url"`
	SpaceURL            string `mapstructure:"space_url"`
	TitlePrefix         string `mapstructure:"title_prefix"`
	CookieFile          string `mapstructure:"cookie_file"`
	DownloadCookieFile  string `mapstructure:"download_cookie_file"`
	SourceMode          string `mapstructure:"source_mode"`
	DiscoverLimit       int    `mapstructure:"discover_limit"`
	Enabled             bool   `mapstructure:"enabled"`
	AutoRecord          bool   `mapstructure:"auto_record"`
	AutoASR             bool   `mapstructure:"auto_asr"`
	AutoRecap           *bool  `mapstructure:"auto_recap"`
	PublishEnabled      bool   `mapstructure:"publish_enabled"`
	PublishMode         string `mapstructure:"publish_mode"`
	PublishCategoryID   int    `mapstructure:"publish_category_id"`
	PublishListID       int    `mapstructure:"publish_list_id"`
	PublishPrivatePub   int    `mapstructure:"publish_private_pub"`
	PublishOriginal     int    `mapstructure:"publish_original"`
	AutoPublish         bool   `mapstructure:"auto_publish"`
	PublishAigc         int    `mapstructure:"publish_aigc"`
	PublishTimerPubTime int64  `mapstructure:"publish_timer_pub_time"`
	PublishCoverURL     string `mapstructure:"publish_cover_url"`
	PublishTopics       string `mapstructure:"publish_topics"`
}

// --- 运行时配置覆盖（runtime_settings → 内存 cfg） ---
//
// 6 个全局设置 handler 改动持久化到 SQLite runtime_settings 表（per-section JSON）。
// 启动时 ApplyOverrides 用该表覆盖 viper 加载的基线。每个 SectionDTO 只含对应 handler
// 实际管理的字段（指针，presence-aware），**不**含完整 config struct 的隐藏字段
// （如 RecapAIConfig.CLIPath/GlossaryFile），避免冻结手工改 yaml 的字段（r10 [Medium]）。
// 密钥字段不进 DTO（走 secrets 表），WebDAV/ASRS3 通过 *_managed tombstone 标记接管状态。
//
// 覆盖优先级（高→低）：runtime_settings > config.yaml > viper SetDefault。

// PublishSectionDTO 对应 updatePublishConfig 管理的字段。
type PublishSectionDTO struct {
	Enabled         *bool   `json:"enabled,omitempty"`
	Mode            *string `json:"mode,omitempty"`
	CategoryID      *int    `json:"category_id,omitempty"`
	ListID          *int    `json:"list_id,omitempty"`
	PrivatePub      *int    `json:"private_pub,omitempty"`
	SummaryLen      *int    `json:"summary_len,omitempty"`
	Original        *int    `json:"original,omitempty"`
	Aigc            *int    `json:"aigc,omitempty"`
	TimerPubTime    *int64  `json:"timer_pub_time,omitempty"`
	CoverURL        *string `json:"cover_url,omitempty"`
	AutoCover       *bool   `json:"auto_cover,omitempty"`
	Topics          *string `json:"topics,omitempty"`
	TopicID         *int    `json:"topic_id,omitempty"`
	TopicName       *string `json:"topic_name,omitempty"`
	CloseComment    *int    `json:"close_comment,omitempty"`
	UpChooseComment *int    `json:"up_choose_comment,omitempty"`
}

// ASRS3SectionDTO 对应 updateASRS3Config 管理的字段。AccessKeySecret 不进 DTO（走 secrets）。
type ASRS3SectionDTO struct {
	Endpoint        *string `json:"endpoint,omitempty"`
	Bucket          *string `json:"bucket,omitempty"`
	AccessKeyID     *string `json:"access_key_id,omitempty"`
	AccessKeyEnv    *string `json:"access_key_env,omitempty"`
	Region          *string `json:"region,omitempty"`
	PublicURLPrefix *string `json:"public_url_prefix,omitempty"`
	UsePathStyle    *bool   `json:"use_path_style,omitempty"`
	// AccessKeyManaged = tombstone：UI 设/清/改 env 名后置 true，EffectiveAccessKey 不回落明文。
	AccessKeyManaged *bool `json:"access_key_managed,omitempty"`
}

// DashScopeSectionDTO 对应 updateDashScopeConfig 管理的字段。APIKey 不进 DTO（走 secrets）。
type DashScopeSectionDTO struct {
	APIKeyEnv          *string `json:"api_key_env,omitempty"`
	ASRURL             *string `json:"asr_url,omitempty"`
	TasksURL           *string `json:"tasks_url,omitempty"`
	Model              *string `json:"model,omitempty"`
	Language           *string `json:"language,omitempty"`
	DiarizationEnabled *bool   `json:"diarization_enabled,omitempty"`
	SpeakerCount       *int    `json:"speaker_count,omitempty"`
	VocabularyID       *string `json:"vocabulary_id,omitempty"`
}

// RecapAISectionDTO 对应 updateRecapConfig 管理的字段。
// **不含** CLIPath/GlossaryFile/EnableSummarization（隐藏字段，由 config.yaml 持有）。
// APIKey 不进 DTO（走 secrets）。
type RecapAISectionDTO struct {
	Enabled            *bool   `json:"enabled,omitempty"`
	Provider           *string `json:"provider,omitempty"`
	APIKeyEnv          *string `json:"api_key_env,omitempty"`
	BaseURL            *string `json:"base_url,omitempty"`
	Model              *string `json:"model,omitempty"`
	MaxTokens          *int    `json:"max_tokens,omitempty"`
	MaxContinuations   *int    `json:"max_continuations,omitempty"`
	TimeoutSeconds     *int    `json:"timeout_seconds,omitempty"`
	IncludeSpeakerInfo *bool   `json:"include_speaker_info,omitempty"`
}

// WebDAVSectionDTO 对应 updateWebDAVConfig 管理的字段。Password 不进 DTO（走 secrets）。
type WebDAVSectionDTO struct {
	URL         *string `json:"url,omitempty"`
	Username    *string `json:"username,omitempty"`
	PasswordEnv *string `json:"password_env,omitempty"`
	BasePath    *string `json:"base_path,omitempty"`
	Remote      *string `json:"remote,omitempty"`
	// PasswordManaged = tombstone：UI 设/清/改 env 名后置 true，EffectivePassword 不回落明文。
	PasswordManaged *bool `json:"password_managed,omitempty"`
}

// ArchiveSectionDTO 对应 updateArchiveConfig 管理的字段。
type ArchiveSectionDTO struct {
	AutoAfterPublish *bool   `json:"auto_after_publish,omitempty"`
	CleanupPolicy    *string `json:"cleanup_policy,omitempty"`
}

// ToolsSectionDTO 对应 updateToolsConfig 管理的字段。
// 只含软依赖工具路径(yt_dlp/rclone);ffmpeg/ffprobe 不在此暴露
// (其 required=true,改错路径会导致下次启动 fatal,web 不可达无法纠正,
//
//	风险过高;仍只能通过 config.yaml 修改)。
type ToolsSectionDTO struct {
	YTDLP  *string `json:"yt_dlp,omitempty"`
	Rclone *string `json:"rclone,omitempty"`
}

// MCPSectionDTO 对应 updateMCPConfig 管理的 MCP 搜索工具配置段。
// presence-aware:指针为 nil 表示该字段不改(保留基线),非 nil 表示更新(含零值)。
// 密钥字段(BraveAPIKey/TavilyAPIKey)在 GET 响应里只返回是否已设置(bool),
// PUT 传空串表示清除(与 webdav password 的只写模式一致)。
type MCPSectionDTO struct {
	Enabled       *bool              `json:"enabled,omitempty"`
	Servers       *[]MCPServerConfig `json:"servers,omitempty"`
	Builtin       *MCPBuiltinConfig  `json:"builtin,omitempty"`
	MaxToolRounds *int               `json:"max_tool_rounds,omitempty"`
}

// ApplyOverrides 用 runtime_settings 的 per-section JSON 覆盖 cfg 的对应段。
//
// 语义：
//   - section 缺失或 JSON 为空 {} → 保留基线（不覆盖）。
//   - DTO 单字段为 nil（指针）→ 该字段保留基线（presence-aware，r11 [Medium]）。
//   - WebDAV/ASRS3 的 *_managed tombstone 非 nil → 注入到未导出字段，驱动 Effective*。
//
// 损坏的 section JSON：跳过该 section 并 slog.Error（不 fatal，让其它 section 生效）。
// 全部覆盖完成后执行 cfg.Validate()（r10 [Medium]）。
func ApplyOverrides(cfg *Config, overrides map[string]json.RawMessage) error {
	if cfg == nil {
		return errors.New("ApplyOverrides: nil config")
	}

	apply := func(section string, dst interface{}) {
		raw, ok := overrides[section]
		if !ok || len(raw) == 0 || strings.TrimSpace(string(raw)) == "{}" {
			return
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			slog.Error("runtime_settings section JSON corrupt, skipping",
				"section", section, "error", err)
			return
		}
	}

	if raw, ok := overrides["publish"]; ok && len(raw) > 0 {
		var dto PublishSectionDTO
		apply("publish", &dto)
		if dto.Enabled != nil {
			cfg.Publish.Enabled = *dto.Enabled
		}
		if dto.Mode != nil {
			cfg.Publish.Mode = *dto.Mode
		}
		if dto.CategoryID != nil {
			cfg.Publish.CategoryID = *dto.CategoryID
		}
		if dto.ListID != nil {
			cfg.Publish.ListID = *dto.ListID
		}
		if dto.PrivatePub != nil {
			cfg.Publish.PrivatePub = *dto.PrivatePub
		}
		if dto.SummaryLen != nil {
			cfg.Publish.SummaryLen = *dto.SummaryLen
		}
		if dto.Original != nil {
			cfg.Publish.Original = *dto.Original
		}
		if dto.Aigc != nil {
			cfg.Publish.Aigc = *dto.Aigc
		}
		if dto.TimerPubTime != nil {
			cfg.Publish.TimerPubTime = *dto.TimerPubTime
		}
		if dto.CoverURL != nil {
			cfg.Publish.CoverURL = *dto.CoverURL
		}
		if dto.AutoCover != nil {
			cfg.Publish.AutoCover = *dto.AutoCover
		}
		if dto.Topics != nil {
			cfg.Publish.Topics = *dto.Topics
		}
		if dto.TopicID != nil {
			cfg.Publish.TopicID = *dto.TopicID
		}
		if dto.TopicName != nil {
			cfg.Publish.TopicName = *dto.TopicName
		}
		if dto.CloseComment != nil {
			cfg.Publish.CloseComment = *dto.CloseComment
		}
		if dto.UpChooseComment != nil {
			cfg.Publish.UpChooseComment = *dto.UpChooseComment
		}
	}

	if raw, ok := overrides["asr_s3"]; ok && len(raw) > 0 {
		var dto ASRS3SectionDTO
		apply("asr_s3", &dto)
		if dto.Endpoint != nil {
			cfg.ASRS3.Endpoint = *dto.Endpoint
		}
		if dto.Bucket != nil {
			cfg.ASRS3.Bucket = *dto.Bucket
		}
		if dto.AccessKeyID != nil {
			cfg.ASRS3.AccessKeyID = *dto.AccessKeyID
		}
		if dto.AccessKeyEnv != nil {
			cfg.ASRS3.AccessKeyEnv = *dto.AccessKeyEnv
		}
		if dto.Region != nil {
			cfg.ASRS3.Region = *dto.Region
		}
		if dto.PublicURLPrefix != nil {
			cfg.ASRS3.PublicURLPrefix = *dto.PublicURLPrefix
		}
		if dto.UsePathStyle != nil {
			cfg.ASRS3.UsePathStyle = *dto.UsePathStyle
		}
		// tombstone：非 nil 注入到未导出字段（同包赋值合法）。
		if dto.AccessKeyManaged != nil {
			cfg.ASRS3.accessKeyManaged = *dto.AccessKeyManaged
		}
	}

	if raw, ok := overrides["dashscope"]; ok && len(raw) > 0 {
		var dto DashScopeSectionDTO
		apply("dashscope", &dto)
		if dto.APIKeyEnv != nil {
			cfg.DashScope.APIKeyEnv = *dto.APIKeyEnv
		}
		if dto.ASRURL != nil {
			cfg.DashScope.ASRURL = *dto.ASRURL
		}
		if dto.TasksURL != nil {
			cfg.DashScope.TasksURL = *dto.TasksURL
		}
		if dto.Model != nil {
			cfg.DashScope.Model = *dto.Model
		}
		if dto.Language != nil {
			cfg.DashScope.Language = *dto.Language
		}
		if dto.DiarizationEnabled != nil {
			cfg.DashScope.DiarizationEnabled = *dto.DiarizationEnabled
		}
		if dto.SpeakerCount != nil {
			cfg.DashScope.SpeakerCount = *dto.SpeakerCount
		}
		if dto.VocabularyID != nil {
			cfg.DashScope.VocabularyID = *dto.VocabularyID
		}
	}

	if raw, ok := overrides["recap_ai"]; ok && len(raw) > 0 {
		var dto RecapAISectionDTO
		apply("recap_ai", &dto)
		if dto.Enabled != nil {
			cfg.RecapAI.Enabled = *dto.Enabled
		}
		if dto.Provider != nil {
			cfg.RecapAI.Provider = *dto.Provider
		}
		if dto.APIKeyEnv != nil {
			cfg.RecapAI.APIKeyEnv = *dto.APIKeyEnv
		}
		if dto.BaseURL != nil {
			cfg.RecapAI.BaseURL = *dto.BaseURL
		}
		if dto.Model != nil {
			cfg.RecapAI.Model = *dto.Model
		}
		if dto.MaxTokens != nil {
			cfg.RecapAI.MaxTokens = *dto.MaxTokens
		}
		if dto.MaxContinuations != nil {
			cfg.RecapAI.MaxContinuations = *dto.MaxContinuations
		}
		if dto.TimeoutSeconds != nil {
			cfg.RecapAI.TimeoutSeconds = *dto.TimeoutSeconds
		}
		if dto.IncludeSpeakerInfo != nil {
			cfg.RecapAI.IncludeSpeakerInfo = *dto.IncludeSpeakerInfo
		}
		// 注意：CLIPath/GlossaryFile/EnableSummarization 不在 DTO，保留 config.yaml 基线（r10）。
	}

	if raw, ok := overrides["webdav"]; ok && len(raw) > 0 {
		var dto WebDAVSectionDTO
		apply("webdav", &dto)
		if dto.URL != nil {
			cfg.WebDAV.URL = *dto.URL
		}
		if dto.Username != nil {
			cfg.WebDAV.Username = *dto.Username
		}
		if dto.PasswordEnv != nil {
			cfg.WebDAV.PasswordEnv = *dto.PasswordEnv
		}
		if dto.BasePath != nil {
			cfg.WebDAV.BasePath = *dto.BasePath
		}
		if dto.Remote != nil {
			cfg.WebDAV.Remote = *dto.Remote
		}
		if dto.PasswordManaged != nil {
			cfg.WebDAV.passwordManaged = *dto.PasswordManaged
		}
	}

	if raw, ok := overrides["archive"]; ok && len(raw) > 0 {
		var dto ArchiveSectionDTO
		apply("archive", &dto)
		if dto.AutoAfterPublish != nil {
			cfg.Archive.AutoAfterPublish = *dto.AutoAfterPublish
		}
		if dto.CleanupPolicy != nil {
			cfg.Archive.CleanupPolicy = *dto.CleanupPolicy
		}
	}

	if raw, ok := overrides["tools"]; ok && len(raw) > 0 {
		var dto ToolsSectionDTO
		apply("tools", &dto)
		if dto.YTDLP != nil {
			cfg.YTDLP = *dto.YTDLP
		}
		if dto.Rclone != nil {
			cfg.Rclone = *dto.Rclone
		}
	}

	// MCP 段:嵌套结构体,逐字段 presence-aware 覆盖。
	// Servers 为切片指针:nil=不改,非 nil(含空切片)=全量替换。
	if raw, ok := overrides["mcp"]; ok && len(raw) > 0 {
		var dto MCPSectionDTO
		apply("mcp", &dto)
		if dto.Enabled != nil {
			cfg.MCP.Enabled = *dto.Enabled
		}
		if dto.Servers != nil {
			cfg.MCP.Servers = *dto.Servers
		}
		if dto.Builtin != nil {
			cfg.MCP.Builtin = *dto.Builtin
		}
		if dto.MaxToolRounds != nil {
			cfg.MCP.MaxToolRounds = *dto.MaxToolRounds
		}
	}

	return cfg.Validate()
}

func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) && !os.IsNotExist(err) {
				return nil, err
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("output_root", "hikami-go")
	v.SetDefault("db_path", "hikami.db")
	v.SetDefault("ffmpeg", defaultCommandPath("ffmpeg"))
	v.SetDefault("ffprobe", defaultCommandPath("ffprobe"))
	v.SetDefault("yt_dlp", defaultCommandPath("yt-dlp"))
	v.SetDefault("rclone", defaultCommandPath("rclone"))
	v.SetDefault("web.enabled", true)
	v.SetDefault("web.listen", "127.0.0.1:6334")
	v.SetDefault("web.auto_open_browser", true)
	v.SetDefault("worker.num", 3)
	v.SetDefault("worker.auto_retry", false)
	v.SetDefault("worker.max_retry_attempts", 3)
	v.SetDefault("worker.retry_delay_seconds", 30)
	// cron.discovery 默认禁用(2026-07-19):回顾管理·回放页的「发现回放」改为独立 URL 入口,
	// 不再自动遍历主播表下载。用户若需要 scheduler 自动发现,在 config.yaml 显式配置
	// cron.discovery: "@every 20m" 即可恢复旧行为(viper 用户配置优先级高于 SetDefault)。
	// scheduler.go:85 的 `if discoverySpec != ""` 会跳过空串的 cron 注册。
	v.SetDefault("cron.discovery", "")
	v.SetDefault("cron.live_check", "@every 30s")
	v.SetDefault("live_record.enabled", true)
	v.SetDefault("live_record.audio_only", false)
	v.SetDefault("live_record.record_danmaku", true)
	v.SetDefault("live_record.audio_container", "m4a")
	v.SetDefault("live_record.require_audio_stream", false)
	v.SetDefault("live_record.fallback_extract_audio", true)
	v.SetDefault("live_record.generate_asr_audio", true)
	v.SetDefault("live_record.segment_minutes", 0)
	v.SetDefault("live_record.stop_grace_seconds", 30)
	v.SetDefault("live_record.auto_reconnect", true)
	v.SetDefault("live_record.max_reconnect", 3)
	v.SetDefault("live_record.reconnect_delay_seconds", 10)
	v.SetDefault("log_format", "json")
	v.SetDefault("logs.dir", "logs")
	v.SetDefault("logs.level", "info")
	v.SetDefault("logs.format", "json")
	v.SetDefault("dashscope.api_key_env", "DASHSCOPE_API_KEY")
	v.SetDefault("dashscope.asr_url", DefaultDashScopeASRURL)
	v.SetDefault("dashscope.tasks_url", DefaultDashScopeTasksURL)
	v.SetDefault("dashscope.model", "fun-asr")
	v.SetDefault("dashscope.language", "zh")
	v.SetDefault("dashscope.diarization_enabled", true)
	v.SetDefault("dashscope.speaker_count", 0)
	v.SetDefault("dashscope.vocabulary_id", "")
	v.SetDefault("asr_temp.cleanup_after_success", true)
	v.SetDefault("asr_temp.enabled", false)
	v.SetDefault("asr_temp.listen", "")
	v.SetDefault("asr_temp.local_dir", "")
	v.SetDefault("asr_s3.access_key_env", "ASR_S3_ACCESS_KEY_SECRET")
	v.SetDefault("asr_s3.use_path_style", false)
	v.SetDefault("webdav.url", "")
	v.SetDefault("webdav.username", "")
	v.SetDefault("webdav.password", "")
	v.SetDefault("webdav.password_env", "")
	v.SetDefault("recap_ai.enabled", true)
	v.SetDefault("recap_ai.provider", DefaultRecapProvider)
	v.SetDefault("recap_ai.api_key_env", "AI_API_KEY")
	v.SetDefault("recap_ai.base_url", DefaultRecapBaseURL)
	v.SetDefault("recap_ai.model", DefaultRecapModel)
	v.SetDefault("recap_ai.max_tokens", 16384)
	v.SetDefault("recap_ai.max_continuations", 2)
	v.SetDefault("recap_ai.timeout_seconds", 180)
	v.SetDefault("recap_ai.include_speaker_info", true)
	v.SetDefault("upload.cleanup_policy", "none")
	v.SetDefault("archive.auto_after_publish", false)
	v.SetDefault("archive.cleanup_policy", "none")
	// MCP 默认关闭(零回归:未配置即不启用,行为与无 MCP 完全一致)。
	v.SetDefault("mcp.enabled", false)
	v.SetDefault("mcp.max_tool_rounds", 5)
	v.SetDefault("mcp.builtin.brave_api_key_env", "BRAVE_API_KEY")
	v.SetDefault("mcp.builtin.tavily_api_key_env", "TAVILY_API_KEY")
	v.SetDefault("downloader.backend", "auto")
	v.SetDefault("publish.enabled", false)
	v.SetDefault("publish.mode", "draft")
	v.SetDefault("publish.category_id", 15)
	v.SetDefault("publish.list_id", 0)
	v.SetDefault("publish.private_pub", 2)
	v.SetDefault("publish.summary_len", 100)
	v.SetDefault("publish.aigc", 0)
	v.SetDefault("publish.timer_pub_time", 0)
	v.SetDefault("publish.auto_cover", true)
	v.SetDefault("publish.topic_id", 0)
	v.SetDefault("publish.topic_name", "")
	v.SetDefault("notify.enabled", false)
	v.SetDefault("notify.type", "webhook")
	v.SetDefault("notify.events", []string{
		notify.EventTaskFailed,
		notify.EventRecordStart,
		notify.EventRecordStop,
		notify.EventRecapDone,
		notify.EventPublishDone,
	})
}

func defaultCommandPath(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return name
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.OutputRoot) == "" {
		return fmt.Errorf("output_root is required")
	}
	if strings.TrimSpace(c.DBPath) == "" {
		return fmt.Errorf("db_path is required")
	}
	if c.Web.Enabled && strings.TrimSpace(c.Web.Listen) == "" {
		return fmt.Errorf("web.listen is required when web.enabled is true")
	}
	// 安全默认（ISS-2）：listen 绑定非 loopback（0.0.0.0/::/外网 IP）时强制要求 admin_token，
	// 避免敏感 REST API（secrets/config/cookies）在公网或内网暴露时无认证可被读写。
	if c.Web.Enabled && !isLoopbackListen(c.Web.Listen) && strings.TrimSpace(c.Web.AdminToken) == "" {
		return fmt.Errorf("web.admin_token is required when web.listen binds a non-loopback address (%q)", c.Web.Listen)
	}
	if c.Worker.Num <= 0 {
		return fmt.Errorf("worker.num must be greater than 0")
	}
	if strings.TrimSpace(c.LiveRecord.AudioContainer) == "" {
		return fmt.Errorf("live_record.audio_container is required")
	}
	if c.LiveRecord.SegmentMinutes < 0 {
		return fmt.Errorf("live_record.segment_minutes must be greater than or equal to 0")
	}
	if c.LiveRecord.StopGraceSeconds < 0 {
		return fmt.Errorf("live_record.stop_grace_seconds must be greater than or equal to 0")
	}
	if c.Publish.Mode != "" && c.Publish.Mode != "draft" && c.Publish.Mode != "publish" {
		return fmt.Errorf("publish.mode must be 'draft' or 'publish', got %s", c.Publish.Mode)
	}
	if c.Publish.SummaryLen < 0 {
		return fmt.Errorf("publish.summary_len must be greater than or equal to 0")
	}
	switch c.Upload.CleanupPolicy {
	case "", "none", "temp", "generated", "all":
	default:
		return fmt.Errorf("upload.cleanup_policy must be one of: none, temp, generated, all, got %s", c.Upload.CleanupPolicy)
	}
	switch c.Archive.CleanupPolicy {
	case "", "none", "temp", "generated", "all":
	default:
		return fmt.Errorf("archive.cleanup_policy must be one of: none, temp, generated, all, got %s", c.Archive.CleanupPolicy)
	}
	switch strings.ToLower(strings.TrimSpace(c.Downloader.Backend)) {
	case "", "auto", "native", "ytdlp":
	default:
		return fmt.Errorf("downloader.backend must be one of: auto, native, ytdlp, got %s", c.Downloader.Backend)
	}
	// CookieEncryptionKey 格式由启动阶段的 biliutil.SetCookieEncryptionKey 统一校验。
	return nil
}

func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.OutputRoot, 0o755); err != nil {
		return err
	}
	// 注：程序只把日志写到 stdout（main.go 的 slog handler 绑 os.Stdout），从不落盘，
	// 因此不再创建 logs/ 空目录。c.Logs.Dir 字段和 logs.dir 配置项保留仅为向后兼容
	// （老 config.yaml 里有这个 key 不会报错），但当前无任何代码读取它。
	dbDir := filepath.Dir(c.DBPath)
	if dbDir != "." && dbDir != "" {
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) LogLevel() slog.Level {
	switch strings.ToLower(c.Logs.Level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
