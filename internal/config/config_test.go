package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// writeTestConfig 在临时目录写入 config.yaml 并返回路径。
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试配置: %v", err)
	}
	return path
}

func TestLoad_DefaultValues(t *testing.T) {
	path := writeTestConfig(t, `
output_root: /tmp/hikami-test
db_path: /tmp/hikami-test/hikami.db
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Web 默认值
	if !cfg.Web.Enabled {
		t.Error("web.enabled 默认应为 true")
	}
	if cfg.Web.Listen != "127.0.0.1:6334" {
		t.Errorf("web.listen = %q, 期望 %q", cfg.Web.Listen, "127.0.0.1:6334")
	}

	// Worker 默认值
	if cfg.Worker.Num != 3 {
		t.Errorf("worker.num = %d, 期望 3", cfg.Worker.Num)
	}

	// DashScope 默认值
	if cfg.DashScope.APIKeyEnv != "DASHSCOPE_API_KEY" {
		t.Errorf("dashscope.api_key_env = %q", cfg.DashScope.APIKeyEnv)
	}
	if cfg.DashScope.Model != "fun-asr" {
		t.Errorf("dashscope.model = %q, 期望 %q", cfg.DashScope.Model, "fun-asr")
	}

	// RecapAI 默认值
	if cfg.RecapAI.Provider != "openai_compatible" {
		t.Errorf("recap_ai.provider = %q", cfg.RecapAI.Provider)
	}
	if cfg.RecapAI.BaseURL != "https://api.deepseek.com" {
		t.Errorf("recap_ai.base_url = %q", cfg.RecapAI.BaseURL)
	}
	if cfg.RecapAI.Model != "deepseek-v4-pro" {
		t.Errorf("recap_ai.model = %q", cfg.RecapAI.Model)
	}
	if cfg.RecapAI.MaxTokens != 16384 {
		t.Errorf("recap_ai.max_tokens = %d, 期望 16384", cfg.RecapAI.MaxTokens)
	}
	if cfg.RecapAI.MaxContinuations != 2 {
		t.Errorf("recap_ai.max_continuations = %d, 期望 2", cfg.RecapAI.MaxContinuations)
	}
	if cfg.RecapAI.TimeoutSeconds != 180 {
		t.Errorf("recap_ai.timeout_seconds = %d, 期望 180", cfg.RecapAI.TimeoutSeconds)
	}

	if cfg.Downloader.Backend != "auto" {
		t.Errorf("downloader.backend = %q, 期望 auto", cfg.Downloader.Backend)
	}
}

func TestValidate_MissingOutputRoot(t *testing.T) {
	cfg := &Config{DBPath: "test.db"}
	if err := cfg.Validate(); err == nil {
		t.Error("期望 output_root 缺失时报错")
	}
}

func TestValidate_MissingDbPath(t *testing.T) {
	cfg := &Config{OutputRoot: "/tmp/test"}
	if err := cfg.Validate(); err == nil {
		t.Error("期望 db_path 缺失时报错")
	}
}

func TestValidate_Success(t *testing.T) {
	cfg := &Config{
		OutputRoot: "/tmp/test",
		DBPath:     "test.db",
		Web:        WebConfig{Enabled: true, Listen: "127.0.0.1:6334"},
		Worker:     WorkerConfig{Num: 3},
		LiveRecord: LiveRecordConfig{AudioContainer: "m4a"},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("完整配置校验失败: %v", err)
	}
}

func TestLogLevel_Default(t *testing.T) {
	cfg := &Config{}
	if level := cfg.LogLevel(); level != slog.LevelInfo {
		t.Errorf("默认日志级别 = %v, 期望 LevelInfo", level)
	}
}

func TestLogLevel_Explicit(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"info", slog.LevelInfo},
	}
	for _, tt := range tests {
		cfg := &Config{Logs: LogsConfig{Level: tt.input}}
		if got := cfg.LogLevel(); got != tt.want {
			t.Errorf("LogLevel(%q) = %v, 期望 %v", tt.input, got, tt.want)
		}
	}
}

func TestSetDefaults_WebListen(t *testing.T) {
	path := writeTestConfig(t, "output_root: /tmp/test\ndb_path: test.db\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Web.Listen != "127.0.0.1:6334" {
		t.Errorf("web.listen = %q, 期望 %q", cfg.Web.Listen, "127.0.0.1:6334")
	}
}

func TestSetDefaults_DashScope(t *testing.T) {
	path := writeTestConfig(t, "output_root: /tmp/test\ndb_path: test.db\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DashScope.Language != "zh" {
		t.Errorf("dashscope.language = %q, 期望 %q", cfg.DashScope.Language, "zh")
	}
	if !cfg.DashScope.DiarizationEnabled {
		t.Error("dashscope.diarization_enabled 默认应为 true")
	}
}

func TestLogFormat(t *testing.T) {
	// 默认 json
	path := writeTestConfig(t, "output_root: /tmp/test\ndb_path: test.db\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("log_format = %q, 期望 %q", cfg.LogFormat, "json")
	}

	// 显式 text
	path2 := writeTestConfig(t, "output_root: /tmp/test\ndb_path: test.db\nlog_format: text\n")
	cfg2, err := Load(path2)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg2.LogFormat != "text" {
		t.Errorf("log_format = %q, 期望 %q", cfg2.LogFormat, "text")
	}
}

func TestLoad_ExplicitOverrides(t *testing.T) {
	path := writeTestConfig(t, `
output_root: /data/hikami
db_path: /data/hikami/hikami.db
log_format: text
web:
  listen: "127.0.0.1:9090"
recap_ai:
  model: gpt-4
  max_tokens: 8192
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Web.Listen != "127.0.0.1:9090" {
		t.Errorf("web.listen = %q, 期望 %q", cfg.Web.Listen, "127.0.0.1:9090")
	}
	if cfg.RecapAI.Model != "gpt-4" {
		t.Errorf("recap_ai.model = %q, 期望 %q", cfg.RecapAI.Model, "gpt-4")
	}
	if cfg.RecapAI.MaxTokens != 8192 {
		t.Errorf("recap_ai.max_tokens = %d, 期望 8192", cfg.RecapAI.MaxTokens)
	}
}

func TestValidate_WorkerNumZero(t *testing.T) {
	cfg := &Config{
		OutputRoot: "/tmp/test",
		DBPath:     "test.db",
		Worker:     WorkerConfig{Num: 0},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("期望 worker.num=0 报错")
	}
}

func TestValidate_PublishModeInvalid(t *testing.T) {
	cfg := &Config{
		OutputRoot: "/tmp/test",
		DBPath:     "test.db",
		Publish:    PublishConfig{Mode: "invalid"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("期望 publish.mode 无效时报错")
	}
}

func TestValidate_DownloaderBackend(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		wantErr bool
	}{
		{name: "empty", backend: "", wantErr: false},
		{name: "auto", backend: "auto", wantErr: false},
		{name: "native", backend: "native", wantErr: false},
		{name: "ytdlp", backend: "ytdlp", wantErr: false},
		{name: "invalid", backend: "curl", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				OutputRoot: "/tmp/test",
				DBPath:     "test.db",
				Web:        WebConfig{Enabled: true, Listen: "127.0.0.1:6334"},
				Worker:     WorkerConfig{Num: 1},
				LiveRecord: LiveRecordConfig{AudioContainer: "m4a"},
				Downloader: DownloaderConfig{Backend: tt.backend},
			}
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("期望 downloader.backend 无效时报错")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestDownloaderConfigHelpers(t *testing.T) {
	if !(&DownloaderConfig{Backend: "auto"}).NativeConfigured() {
		t.Fatal("auto 应视为 native")
	}
	if !(&DownloaderConfig{Backend: "native"}).NativeConfigured() {
		t.Fatal("native 应启用 native 后端")
	}
	if !(&DownloaderConfig{Backend: "ytdlp"}).YTDLPConfigured() {
		t.Fatal("ytdlp 应启用 ytdlp 后端")
	}
}

// TestValidate_NonLoopbackRequiresAdminToken 验证 ISS-2：绑非 loopback 强制要求 admin_token。
func TestValidate_NonLoopbackRequiresAdminToken(t *testing.T) {
	// 0.0.0.0 绑定且无 token → 报错
	cfg := &Config{
		OutputRoot: "/tmp/test",
		DBPath:     "test.db",
		Web:        WebConfig{Enabled: true, Listen: "0.0.0.0:6334"},
		Worker:     WorkerConfig{Num: 1},
		LiveRecord: LiveRecordConfig{AudioContainer: "m4a"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("绑 0.0.0.0 无 admin_token 期望报错")
	}

	// 0.0.0.0 绑定 + token → 通过
	cfg.Web.AdminToken = "secret-token"
	if err := cfg.Validate(); err != nil {
		t.Errorf("绑 0.0.0.0 配 admin_token 期望通过: %v", err)
	}

	// loopback 无 token → 通过
	cfg.Web.Listen = "127.0.0.1:6334"
	cfg.Web.AdminToken = ""
	if err := cfg.Validate(); err != nil {
		t.Errorf("loopback 无 admin_token 期望通过: %v", err)
	}

	// 空主机 ":port" 视为非 loopback → 需 token
	cfg.Web.Listen = ":6334"
	if err := cfg.Validate(); err == nil {
		t.Error(`空主机 ":port" 无 admin_token 期望报错`)
	}
}

// TestRecapEffectiveHelpers 验证 RecapAIConfig 的 Effective* 方法留空兜底:
// provider/base_url/model/api_key_env 空值时回落到 DeepSeek 默认。
func TestRecapEffectiveHelpers(t *testing.T) {
	// 全空 → 全部回落默认
	empty := RecapAIConfig{}
	if got := empty.EffectiveProvider(); got != DefaultRecapProvider {
		t.Fatalf("EffectiveProvider() empty = %q, want %q", got, DefaultRecapProvider)
	}
	if got := empty.EffectiveBaseURL(); got != DefaultRecapBaseURL {
		t.Fatalf("EffectiveBaseURL() empty = %q, want %q", got, DefaultRecapBaseURL)
	}
	if got := empty.EffectiveModel(); got != DefaultRecapModel {
		t.Fatalf("EffectiveModel() empty = %q, want %q", got, DefaultRecapModel)
	}
	if got := empty.EffectiveAPIKeyEnv(); got != "AI_API_KEY" {
		t.Fatalf("EffectiveAPIKeyEnv() empty = %q, want AI_API_KEY", got)
	}

	// 非空值原样返回(含 trim 空白)
	filled := RecapAIConfig{
		Provider:  "anthropic",
		BaseURL:   "  https://x.example.com  ",
		Model:     "custom-model",
		APIKeyEnv: "MY_KEY",
	}
	if got := filled.EffectiveProvider(); got != "anthropic" {
		t.Fatalf("EffectiveProvider() filled = %q, want anthropic", got)
	}
	if got := filled.EffectiveBaseURL(); got != "https://x.example.com" {
		t.Fatalf("EffectiveBaseURL() filled = %q (should trim)", got)
	}
	if got := filled.EffectiveModel(); got != "custom-model" {
		t.Fatalf("EffectiveModel() filled = %q, want custom-model", got)
	}
	if got := filled.EffectiveAPIKeyEnv(); got != "MY_KEY" {
		t.Fatalf("EffectiveAPIKeyEnv() filled = %q, want MY_KEY", got)
	}
}

// TestDashScopeEffectiveAPIKeyEnv 验证 DashScopeConfig 留空兜底到 DASHSCOPE_API_KEY。
func TestDashScopeEffectiveAPIKeyEnv(t *testing.T) {
	if got := (DashScopeConfig{}).EffectiveAPIKeyEnv(); got != "DASHSCOPE_API_KEY" {
		t.Fatalf("DashScope EffectiveAPIKeyEnv() empty = %q, want DASHSCOPE_API_KEY", got)
	}
	if got := (DashScopeConfig{APIKeyEnv: "DS_KEY"}).EffectiveAPIKeyEnv(); got != "DS_KEY" {
		t.Fatalf("DashScope EffectiveAPIKeyEnv() filled = %q, want DS_KEY", got)
	}
}

// TestASRS3EffectiveAccessKeyEnv 验证 ASRS3Config 留空兜底到 ASR_S3_ACCESS_KEY_SECRET。
func TestASRS3EffectiveAccessKeyEnv(t *testing.T) {
	if got := (ASRS3Config{}).EffectiveAccessKeyEnv(); got != "ASR_S3_ACCESS_KEY_SECRET" {
		t.Fatalf("ASRS3 EffectiveAccessKeyEnv() empty = %q, want ASR_S3_ACCESS_KEY_SECRET", got)
	}
	if got := (ASRS3Config{AccessKeyEnv: "OSS_KEY"}).EffectiveAccessKeyEnv(); got != "OSS_KEY" {
		t.Fatalf("ASRS3 EffectiveAccessKeyEnv() filled = %q, want OSS_KEY", got)
	}
}

// --- Archive 配置校验测试 ---

func TestValidate_ArchiveCleanupPolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		wantErr bool
	}{
		{name: "empty", policy: "", wantErr: false},
		{name: "none", policy: "none", wantErr: false},
		{name: "temp", policy: "temp", wantErr: false},
		{name: "generated", policy: "generated", wantErr: false},
		{name: "all", policy: "all", wantErr: false},
		{name: "invalid", policy: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				OutputRoot: "/tmp/test",
				DBPath:     "test.db",
				Web:        WebConfig{Enabled: true, Listen: "127.0.0.1:6334"},
				Worker:     WorkerConfig{Num: 1},
				LiveRecord: LiveRecordConfig{AudioContainer: "m4a"},
				Downloader: DownloaderConfig{Backend: "auto"},
				Archive:    ArchiveConfig{CleanupPolicy: tt.policy},
			}
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("期望 archive.cleanup_policy 无效时报错")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

// --- ApplyOverrides / Effective* / tombstone 测试（计划 v6 核心） ---

func baseCfg() *Config {
	return &Config{
		OutputRoot: "./data",
		DBPath:     "./hikami.db",
		YTDLP:      "yt-dlp",
		Rclone:     "rclone",
		Worker:     WorkerConfig{Num: 2},
		LiveRecord: LiveRecordConfig{AudioContainer: "m4a"},
		Publish: PublishConfig{
			Mode:       "draft",
			CategoryID: 15,
			AutoCover:  true,
		},
		WebDAV: WebDAVConfig{URL: "http://w", Password: "yaml-plain"},
		ASRS3:  ASRS3Config{Endpoint: "http://s", AccessKeySecret: "yaml-secret"},
	}
}

func rawJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestApplyOverrides_OverridesPublishFields(t *testing.T) {
	cfg := baseCfg()
	cover := "/x.png"
	mode := "publish"
	auto := false
	overrides := map[string]json.RawMessage{
		"publish": rawJSON(t, PublishSectionDTO{CoverURL: &cover, Mode: &mode, AutoCover: &auto}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if cfg.Publish.CoverURL != "/x.png" || cfg.Publish.Mode != "publish" || cfg.Publish.AutoCover {
		t.Fatalf("publish not overridden: %+v", cfg.Publish)
	}
	// 未覆盖字段保留基线
	if cfg.Publish.CategoryID != 15 {
		t.Fatalf("CategoryID should retain baseline 15, got %d", cfg.Publish.CategoryID)
	}
}

func TestApplyOverrides_MissingSectionRetainsBaseline(t *testing.T) {
	cfg := baseCfg()
	if err := ApplyOverrides(cfg, map[string]json.RawMessage{}); err != nil {
		t.Fatalf("ApplyOverrides empty: %v", err)
	}
	if cfg.Publish.Mode != "draft" {
		t.Fatalf("baseline publish.Mode should be retained, got %s", cfg.Publish.Mode)
	}
}

func TestApplyOverrides_EmptyObjectRetainsBaseline(t *testing.T) {
	cfg := baseCfg()
	overrides := map[string]json.RawMessage{"publish": json.RawMessage(`{}`)}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides empty obj: %v", err)
	}
	if cfg.Publish.Mode != "draft" {
		t.Fatalf("empty {} should retain baseline, got %s", cfg.Publish.Mode)
	}
}

func TestApplyOverrides_CorruptJSONSkippedNotFatal(t *testing.T) {
	cfg := baseCfg()
	overrides := map[string]json.RawMessage{
		"publish": json.RawMessage(`not-json`), // corrupt
	}
	// 不应 fatal；publish 保留基线
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("corrupt section should not error out: %v", err)
	}
	if cfg.Publish.Mode != "draft" {
		t.Fatalf("corrupt publish should retain baseline, got %s", cfg.Publish.Mode)
	}
}

func TestApplyOverrides_DoesNotFreezeHiddenRecapFields(t *testing.T) {
	cfg := baseCfg()
	cfg.RecapAI.CLIPath = "/usr/local/bin/claude" // 隐藏字段，UI 不管理
	cfg.RecapAI.Model = "old"
	newModel := "new-model"
	overrides := map[string]json.RawMessage{
		"recap_ai": rawJSON(t, RecapAISectionDTO{Model: &newModel}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if cfg.RecapAI.Model != "new-model" {
		t.Fatalf("Model should be overridden: %s", cfg.RecapAI.Model)
	}
	if cfg.RecapAI.CLIPath != "/usr/local/bin/claude" {
		t.Fatalf("hidden CLIPath must NOT be frozen/overwritten: %s", cfg.RecapAI.CLIPath)
	}
}

// r11/r13 [High] tombstone：managed=true 时清除 env，EffectivePassword 不回落 yaml 明文。
func TestEffectivePassword_ManagedTrueDoesNotFallBackToYaml(t *testing.T) {
	cfg := baseCfg()
	os.Unsetenv("WEBDAV_PASSWORD")
	cfg.WebDAV.passwordManaged = true
	// yaml 有明文 password="yaml-plain"，但 managed=true 且 env 空 → 必须返回空。
	if got := cfg.WebDAV.EffectivePassword(); got != "" {
		t.Fatalf("managed=true + empty env must not fall back to yaml plaintext, got %q", got)
	}
	// managed=true + env 有值 → 返回 env。
	t.Setenv("WEBDAV_PASSWORD", "env-val")
	if got := cfg.WebDAV.EffectivePassword(); got != "env-val" {
		t.Fatalf("managed=true + env set should return env, got %q", got)
	}
}

func TestEffectivePassword_ManagedFalseFallsBackToYaml(t *testing.T) {
	cfg := baseCfg()
	os.Unsetenv("WEBDAV_PASSWORD")
	cfg.WebDAV.passwordManaged = false
	// managed=false → 向后兼容，回落 yaml 明文。
	if got := cfg.WebDAV.EffectivePassword(); got != "yaml-plain" {
		t.Fatalf("managed=false should fall back to yaml plaintext, got %q", got)
	}
}

// r13 [High] 状态保持：managed=true 通过 ApplyOverrides 注入。
func TestApplyOverrides_InjectsWebDAVTombstone(t *testing.T) {
	cfg := baseCfg()
	os.Unsetenv("WEBDAV_PASSWORD")
	managed := true
	overrides := map[string]json.RawMessage{
		"webdav": rawJSON(t, WebDAVSectionDTO{PasswordManaged: &managed}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if !cfg.WebDAV.PasswordManaged() {
		t.Fatal("PasswordManaged should be injected true")
	}
	// 注入后 EffectivePassword 不回落明文
	if got := cfg.WebDAV.EffectivePassword(); got != "" {
		t.Fatalf("after managed injection, EffectivePassword should be empty, got %q", got)
	}
}

// tools section(yt_dlp/rclone 路径)覆盖测试。
func TestApplyOverrides_OverridesToolsFields(t *testing.T) {
	cfg := baseCfg()
	ytdlp := "/custom/yt-dlp"
	rclone := "/usr/bin/rclone"
	overrides := map[string]json.RawMessage{
		"tools": rawJSON(t, ToolsSectionDTO{YTDLP: &ytdlp, Rclone: &rclone}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if cfg.YTDLP != "/custom/yt-dlp" || cfg.Rclone != "/usr/bin/rclone" {
		t.Fatalf("tools not overridden: yt_dlp=%q rclone=%q", cfg.YTDLP, cfg.Rclone)
	}
}

// presence-aware:nil 字段(未传 rclone)保留基线。
func TestApplyOverrides_ToolsPresenceAware(t *testing.T) {
	cfg := baseCfg()
	ytdlp := "/custom/yt-dlp"
	overrides := map[string]json.RawMessage{
		"tools": rawJSON(t, ToolsSectionDTO{YTDLP: &ytdlp}), // rclone 字段为 nil
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if cfg.YTDLP != "/custom/yt-dlp" {
		t.Fatalf("YTDLP should be overridden, got %q", cfg.YTDLP)
	}
	if cfg.Rclone != "rclone" {
		t.Fatalf("Rclone should retain baseline, got %q", cfg.Rclone)
	}
}

// presence-aware:空字符串 "" 被覆盖为空(probe 会降级,符合"清空回退默认探测"语义)。
func TestApplyOverrides_ToolsEmptyStringClears(t *testing.T) {
	cfg := baseCfg()
	empty := ""
	overrides := map[string]json.RawMessage{
		"tools": rawJSON(t, ToolsSectionDTO{YTDLP: &empty}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if cfg.YTDLP != "" {
		t.Fatalf("YTDLP should be cleared to empty, got %q", cfg.YTDLP)
	}
	if cfg.Rclone != "rclone" {
		t.Fatalf("Rclone should retain baseline, got %q", cfg.Rclone)
	}
}

// r13 [Medium] NativeConfigured 要求密码：清除密码后 capability 关闭。
func TestNativeConfigured_RequiresPassword(t *testing.T) {
	cfg := baseCfg()
	cfg.WebDAV.passwordManaged = true
	os.Unsetenv("WEBDAV_PASSWORD")
	if cfg.WebDAV.NativeConfigured() {
		t.Fatal("managed=true + empty password: NativeConfigured should be false")
	}
	t.Setenv("WEBDAV_PASSWORD", "env-val")
	if !cfg.WebDAV.NativeConfigured() {
		t.Fatal("with password set: NativeConfigured should be true")
	}
}

// ASRS3 EffectiveAccessKey / Configured 同构验证。
func TestEffectiveAccessKey_ManagedDoesNotFallBack(t *testing.T) {
	cfg := baseCfg()
	os.Unsetenv("ASR_S3_ACCESS_KEY_SECRET")
	cfg.ASRS3.accessKeyManaged = true
	if got := cfg.ASRS3.EffectiveAccessKey(); got != "" {
		t.Fatalf("managed=true + empty env must not fall back, got %q", got)
	}
	if cfg.ASRS3.Configured() {
		t.Fatal("Configured should be false when access key empty")
	}
	t.Setenv("ASR_S3_ACCESS_KEY_SECRET", "env-secret")
	if got := cfg.ASRS3.EffectiveAccessKey(); got != "env-secret" {
		t.Fatalf("managed=true + env set should return env, got %q", got)
	}
}

func TestEffectivePasswordEnv_DefaultFallback(t *testing.T) {
	w := WebDAVConfig{}
	if got := w.EffectivePasswordEnv(); got != "WEBDAV_PASSWORD" {
		t.Fatalf("empty PasswordEnv should fall back to WEBDAV_PASSWORD, got %q", got)
	}
	w.PasswordEnv = "CUSTOM_WD"
	if got := w.EffectivePasswordEnv(); got != "CUSTOM_WD" {
		t.Fatalf("explicit PasswordEnv should win, got %q", got)
	}
}

// TestLoadConfigBackcompatLiveRecordNumRemoved 验证异常 #5:旧配置文件含 worker.live_record_num
// 字段(已删除)时,Load 不报错(viper 默认忽略未知字段),向后兼容。
func TestLoadConfigBackcompatLiveRecordNumRemoved(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.yaml"
	// 旧配置仍含 live_record_num(已删的字段),应被静默忽略。
	configContent := []byte(`
output_root: ./data
db_path: ./test.db
worker:
  num: 3
  live_record_num: 2
`)
	if err := os.WriteFile(configPath, configContent, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load with legacy live_record_num field failed: %v", err)
	}
	if cfg.Worker.Num != 3 {
		t.Errorf("worker.num = %d, want 3", cfg.Worker.Num)
	}
}
