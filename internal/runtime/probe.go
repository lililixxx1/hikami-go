package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"hikami-go/internal/asr"
	"hikami-go/internal/config"
)

type Status struct {
	CheckedAt      time.Time             `json:"checked_at"`
	Tools          map[string]ToolStatus `json:"tools"`
	Capabilities   Capabilities          `json:"capabilities"`
	ConfigStatus   ConfigStatus          `json:"config_status"`
	CookieWarnings []CookieWarning       `json:"cookie_warnings,omitempty"`
	DiskUsage      []DiskInfo            `json:"disk_usage,omitempty"`
}

type ToolStatus struct {
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	Required    bool   `json:"required"`
	Available   bool   `json:"available"`
	Error       string `json:"error,omitempty"`
	InstallHint string `json:"install_hint,omitempty"`
	Source      string `json:"source,omitempty"`
	Managed     bool   `json:"managed"`
}

type Capabilities struct {
	ReplayDownload bool   `json:"replay_download"`
	ASRSubmit      bool   `json:"asr_submit"`
	ASRModel       string `json:"asr_model,omitempty"`
	ASRRequestMode string `json:"asr_request_mode,omitempty"`
	RecapGenerate  bool   `json:"recap_generate"`
	WebDAVUpload   bool   `json:"webdav_upload"`
	PublishOpus    bool   `json:"publish_opus"`
	Reason         string `json:"reason,omitempty"`
}

type ConfigStatus struct {
	DashScopeKeySet   bool   `json:"dashscope_key_set"`
	DashScopeKeyEnv   string `json:"dashscope_key_env"`
	ASRTempConfigured bool   `json:"asr_temp_configured"`
	ASRS3Configured   bool   `json:"asr_s3_configured"`
	ASRS3Endpoint     string `json:"asr_s3_endpoint,omitempty"`
	RecapProvider     string `json:"recap_provider"`
	RecapKeySet       bool   `json:"recap_key_set"`
	RecapKeyEnv       string `json:"recap_key_env"`
	RecapModel        string `json:"recap_model"`
	WebDAVConfigured  bool   `json:"webdav_configured"`
	PublishEnabled    bool   `json:"publish_enabled"`
}

// linuxInstallHints provides installation commands for external tools on Linux.
var linuxInstallHints = map[string]string{
	"ffmpeg":  "apt install ffmpeg       # Debian/Ubuntu, 或 yum install ffmpeg (CentOS)",
	"ffprobe": "apt install ffmpeg",
	"yt-dlp":  "pip install yt-dlp",
	"rclone":  "curl https://rclone.org/install.sh | sudo bash",
}

// windowsInstallHints provides installation commands for external tools on Windows.
var windowsInstallHints = map[string]string{
	"ffmpeg":  "winget install ffmpeg    # 或 choco install ffmpeg",
	"ffprobe": "winget install ffmpeg",
	"yt-dlp":  "winget install yt-dlp    # 或 pip install yt-dlp",
	"rclone":  "winget install rclone    # 或 choco install rclone",
}

// getInstallHint returns the installation hint for the given tool based on the current OS.
func getInstallHint(name string) string {
	switch runtime.GOOS {
	case "windows":
		return windowsInstallHints[name]
	default:
		return linuxInstallHints[name]
	}
}

func Probe(cfg *config.Config) *Status {
	tools := map[string]ToolStatus{
		"ffmpeg":  probeTool("ffmpeg", cfg.FFmpeg, true),
		"ffprobe": probeTool("ffprobe", cfg.FFprobe, true),
		"yt-dlp":  probeTool("yt-dlp", cfg.YTDLP, false),
		"rclone":  probeTool("rclone", cfg.Rclone, false),
	}
	applyFFmpegResolutionStatus(tools)

	recapProviderAvailable := probeRecapProvider(cfg, tools)
	// recap key 检测走 EffectiveAPIKeyEnv 兜底,与响应层/运行时一致(空 env 名视为 AI_API_KEY)。
	recapKeySet := os.Getenv(cfg.RecapAI.EffectiveAPIKeyEnv()) != ""
	rcloneAvailable := tools["rclone"].Available
	ytDLPAvailable := tools["yt-dlp"].Available
	// 走 EffectiveAPIKeyEnv 兜底,与 recap/响应层一致(空 env 名视为 DASHSCOPE_API_KEY)。
	dashScopeKeySet := os.Getenv(cfg.DashScope.EffectiveAPIKeyEnv()) != ""
	asrRcloneFallbackAvailable := cfg.ASRTemp.RcloneConfigured() && rcloneAvailable && cfg.ASRTemp.PublicBaseURL != ""
	asrS3Available := cfg.ASRS3.Configured()
	webDAVRcloneFallbackAvailable := cfg.WebDAV.RcloneConfigured() && rcloneAvailable

	capabilities := Capabilities{
		ReplayDownload: ytDLPAvailable,
		ASRSubmit:      dashScopeKeySet && (cfg.ASRTemp.NativeConfigured() || asrS3Available || asrRcloneFallbackAvailable),
		ASRModel:       asr.NormalizeDashScopeASRModel(cfg.DashScope.Model),
		ASRRequestMode: asr.DashScopeRequestMode(cfg.DashScope.Model),
		RecapGenerate:  cfg.RecapAI.Enabled && recapKeySet && recapProviderAvailable,
		WebDAVUpload:   cfg.WebDAV.NativeConfigured() || webDAVRcloneFallbackAvailable,
		PublishOpus:    cfg.Publish.Enabled,
	}
	capabilities.Reason = capabilityReason(capabilities, cfg, tools)

	configStatus := ConfigStatus{
		DashScopeKeySet:   dashScopeKeySet,
		DashScopeKeyEnv:   cfg.DashScope.EffectiveAPIKeyEnv(),
		ASRTempConfigured: asrTempConfigured(cfg),
		ASRS3Configured:   cfg.ASRS3.Configured(),
		ASRS3Endpoint:     cfg.ASRS3.Endpoint,
		// 走 Effective* 兜底,与能力判断/响应层一致(codex 审核低[5])。
		RecapProvider:    cfg.RecapAI.EffectiveProvider(),
		RecapKeySet:      recapKeySet,
		RecapKeyEnv:      cfg.RecapAI.EffectiveAPIKeyEnv(),
		RecapModel:       cfg.RecapAI.EffectiveModel(),
		WebDAVConfigured: cfg.WebDAV.NativeConfigured() || cfg.WebDAV.RcloneConfigured(),
		PublishEnabled:   cfg.Publish.Enabled,
	}

	return &Status{
		CheckedAt:    time.Now(),
		Tools:        tools,
		Capabilities: capabilities,
		ConfigStatus: configStatus,
	}
}

func applyFFmpegResolutionStatus(tools map[string]ToolStatus) {
	resolution := getLastFFmpegResolution()
	if resolution == nil {
		return
	}
	managed := resolution.Source != "" && resolution.Source != "system"
	if ffmpeg, ok := tools["ffmpeg"]; ok && ffmpeg.Available && ffmpeg.Path == resolution.FFmpegPath {
		ffmpeg.Source = resolution.Source
		ffmpeg.Managed = managed
		tools["ffmpeg"] = ffmpeg
	}
	if ffprobe, ok := tools["ffprobe"]; ok && ffprobe.Available && ffprobe.Path == resolution.FFprobePath {
		ffprobe.Source = resolution.Source
		ffprobe.Managed = managed
		tools["ffprobe"] = ffprobe
	}
}

func (s *Status) StartupError() error {
	for _, tool := range s.Tools {
		if tool.Required && !tool.Available {
			return fmt.Errorf("required tool %s is unavailable: %s", tool.Name, tool.Error)
		}
	}
	return nil
}

func probeTool(name, command string, required bool) ToolStatus {
	status := ToolStatus{Name: name, Required: required}
	if strings.TrimSpace(command) == "" {
		status.Error = "command is empty"
		if hint := getInstallHint(name); hint != "" {
			status.InstallHint = hint
		}
		return status
	}
	path, err := exec.LookPath(command)
	if err != nil {
		status.Error = err.Error()
		if hint := getInstallHint(name); hint != "" {
			status.InstallHint = hint
		}
		return status
	}
	status.Path = path
	status.Available = true
	return status
}

func probeRecapProvider(cfg *config.Config, tools map[string]ToolStatus) bool {
	// 走 Effective* 兜底,与响应层/运行时一致:provider/base_url/model/api_key_env 空值回落 DeepSeek 默认,
	// 避免用户清空字段后运行时已正常但首页能力仍显示红色(codex 审核第 2 条)。
	switch cfg.RecapAI.EffectiveProvider() {
	case "claude_cli":
		command := cfg.RecapAI.CLIPath
		if command == "" {
			command = "claude"
		}
		tools["claude"] = probeTool("claude", command, false)
		return tools["claude"].Available
	case "codex_cli":
		command := cfg.RecapAI.CLIPath
		if command == "" {
			command = "codex"
		}
		tools["codex"] = probeTool("codex", command, false)
		return tools["codex"].Available
	case "openai_compatible", "anthropic":
		// EffectiveBaseURL/EffectiveModel 永远非空(回落默认),故只需校验密钥已配置
		return os.Getenv(cfg.RecapAI.EffectiveAPIKeyEnv()) != ""
	default:
		return false
	}
}

func asrTempConfigured(cfg *config.Config) bool {
	return cfg.ASRTemp.NativeConfigured() ||
		(cfg.ASRTemp.RcloneConfigured() &&
			cfg.ASRTemp.BasePath != "" &&
			cfg.ASRTemp.PublicBaseURL != "")
}

func capabilityReason(capabilities Capabilities, cfg *config.Config, tools map[string]ToolStatus) string {
	var reasons []string
	if !capabilities.ReplayDownload {
		reasons = append(reasons, "yt-dlp unavailable")
	}
	if !capabilities.ASRSubmit {
		// 走 EffectiveAPIKeyEnv 兜底,与 recap/响应层一致。
		dashScopeKeySet := os.Getenv(cfg.DashScope.EffectiveAPIKeyEnv()) != ""
		if !asrTempConfigured(cfg) && !cfg.ASRS3.Configured() {
			reasons = append(reasons, "asr_temp not configured")
		}
		if !cfg.ASRS3.Configured() {
			reasons = append(reasons, "asr_s3 not configured")
		}
		if cfg.ASRTemp.RcloneConfigured() && !cfg.ASRTemp.NativeConfigured() && !tools["rclone"].Available {
			reasons = append(reasons, "rclone unavailable for asr_temp fallback")
		}
		if !dashScopeKeySet {
			reasons = append(reasons, "dashscope api key not configured")
		}
	}
	if !capabilities.RecapGenerate {
		recapKeySet := cfg.RecapAI.APIKeyEnv != "" && os.Getenv(cfg.RecapAI.APIKeyEnv) != ""
		if !cfg.RecapAI.Enabled {
			reasons = append(reasons, "recap not enabled")
		}
		if !recapKeySet {
			reasons = append(reasons, "recap api key not configured")
		}
		if cfg.RecapAI.Enabled && recapKeySet {
			reasons = append(reasons, "recap provider unavailable")
		}
	}
	if !capabilities.WebDAVUpload {
		if !cfg.WebDAV.NativeConfigured() && !cfg.WebDAV.RcloneConfigured() {
			reasons = append(reasons, "webdav not configured")
		} else if cfg.WebDAV.RcloneConfigured() && !cfg.WebDAV.NativeConfigured() && !tools["rclone"].Available {
			reasons = append(reasons, "rclone unavailable for webdav fallback")
		}
	}
	if !capabilities.PublishOpus {
		reasons = append(reasons, "publish not enabled")
	}
	return strings.Join(reasons, "; ")
}

type CookieWarning struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	CookieType  string `json:"cookie_type"` // "publish" or "download"
	ExpiresAt   string `json:"expires_at,omitempty"`
	DaysLeft    int    `json:"days_left"`
	IsExpired   bool   `json:"is_expired"`
}

type DiskInfo struct {
	Path        string  `json:"path"`
	TotalGB     float64 `json:"total_gb"`
	UsedGB      float64 `json:"used_gb"`
	FreeGB      float64 `json:"free_gb"`
	UsedPercent float64 `json:"used_percent"`
}
