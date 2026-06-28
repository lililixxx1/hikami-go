package config

import (
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
	LiveRecordNum    int  `mapstructure:"live_record_num"`
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
}

// 回顾 AI 默认值(留空兜底用,单一来源)。
// viper SetDefault、provider 兜底、probe、handler 响应全部引用此处,避免多处字面量不一致。
const (
	DefaultRecapProvider = "openai_compatible"
	DefaultRecapBaseURL  = "https://api.deepseek.com"
	DefaultRecapModel    = "deepseek-v4-pro"
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

func (c *ASRS3Config) Configured() bool {
	return strings.TrimSpace(c.Endpoint) != "" &&
		strings.TrimSpace(c.Bucket) != "" &&
		strings.TrimSpace(c.AccessKeyID) != "" &&
		strings.TrimSpace(c.SecretResolved()) != "" &&
		strings.TrimSpace(c.PublicURLPrefix) != ""
}

type WebDAVConfig struct {
	Remote      string `mapstructure:"remote"`
	BasePath    string `mapstructure:"base_path"`
	URL         string `mapstructure:"url"`
	Username    string `mapstructure:"username"`
	Password    string `mapstructure:"password"`
	PasswordEnv string `mapstructure:"password_env"`
}

func (c *ASRTempConfig) NativeConfigured() bool {
	return c.Enabled && strings.TrimSpace(c.LocalDir) != "" && strings.TrimSpace(c.PublicBaseURL) != ""
}

func (c *ASRTempConfig) RcloneConfigured() bool {
	return strings.TrimSpace(c.RcloneRemote) != ""
}

func (c *WebDAVConfig) PasswordResolved() string {
	if c.PasswordEnv != "" {
		if password := os.Getenv(c.PasswordEnv); password != "" {
			return password
		}
	}
	return c.Password
}

func (c *WebDAVConfig) NativeConfigured() bool {
	return strings.TrimSpace(c.URL) != ""
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
	v.SetDefault("output_root", "huizeman")
	v.SetDefault("db_path", "hikami.db")
	v.SetDefault("ffmpeg", defaultCommandPath("ffmpeg"))
	v.SetDefault("ffprobe", defaultCommandPath("ffprobe"))
	v.SetDefault("yt_dlp", defaultCommandPath("yt-dlp"))
	v.SetDefault("rclone", defaultCommandPath("rclone"))
	v.SetDefault("web.enabled", true)
	v.SetDefault("web.listen", "127.0.0.1:6334")
	v.SetDefault("web.auto_open_browser", true)
	v.SetDefault("worker.num", 3)
	v.SetDefault("worker.live_record_num", 2)
	v.SetDefault("worker.auto_retry", false)
	v.SetDefault("worker.max_retry_attempts", 3)
	v.SetDefault("worker.retry_delay_seconds", 30)
	v.SetDefault("cron.discovery", "@every 20m")
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
	v.SetDefault("dashscope.asr_url", "https://dashscope.aliyuncs.com/api/v1/services/audio/asr/transcription")
	v.SetDefault("dashscope.tasks_url", "https://dashscope.aliyuncs.com/api/v1/tasks")
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
	v.SetDefault("downloader.backend", "auto")
	v.SetDefault("publish.enabled", false)
	v.SetDefault("publish.mode", "draft")
	v.SetDefault("publish.category_id", 15)
	v.SetDefault("publish.list_id", 0)
	v.SetDefault("publish.private_pub", 2)
	v.SetDefault("publish.summary_len", 100)
	v.SetDefault("publish.aigc", 0)
	v.SetDefault("publish.timer_pub_time", 0)
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
	if c.Worker.LiveRecordNum < 0 {
		return fmt.Errorf("worker.live_record_num must be greater than or equal to 0")
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
	if c.Logs.Dir != "" {
		if err := os.MkdirAll(c.Logs.Dir, 0o755); err != nil {
			return err
		}
	}
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
