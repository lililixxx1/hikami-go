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
	if cfg.Downloader.AutoRetry || cfg.Downloader.MaxRetryAttempts != 12 {
		t.Errorf("downloader retry defaults = (%v, %d), 期望 (false, 12)", cfg.Downloader.AutoRetry, cfg.Downloader.MaxRetryAttempts)
	}
	if cfg.Downloader.MaxConcurrent != 0 || cfg.Downloader.MinIntervalSeconds != 0 || cfg.Downloader.FailureBackoffSeconds != 0 {
		t.Errorf("downloader risk defaults = (%d, %d, %d), 期望 (0, 0, 0)",
			cfg.Downloader.MaxConcurrent, cfg.Downloader.MinIntervalSeconds, cfg.Downloader.FailureBackoffSeconds)
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
		VAD:        validVADDefaults(),
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

func TestSetDefaults_OutputRoot(t *testing.T) {
	// config.yaml 不写 output_root → 应落到代码默认值(与 config.example.yaml 对齐)。
	// 回归防线:2026-07-25 发现默认值 hikami-go 与 module 同名导致录播产物误入暂存区,
	// 改为 ./data 后用此测试钉死,防止未来再次漂移。
	path := writeTestConfig(t, "db_path: x.db\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OutputRoot != "./data" {
		t.Errorf("OutputRoot 默认值 = %q, 期望 %q(与 config.example.yaml 对齐)", cfg.OutputRoot, "./data")
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
				VAD:        validVADDefaults(),
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

func TestValidate_DownloaderRiskControl(t *testing.T) {
	fields := []struct {
		name string
		set  func(*DownloaderConfig)
	}{
		{name: "max retry attempts", set: func(c *DownloaderConfig) { c.MaxRetryAttempts = -1 }},
		{name: "max concurrent", set: func(c *DownloaderConfig) { c.MaxConcurrent = -1 }},
		{name: "minimum interval", set: func(c *DownloaderConfig) { c.MinIntervalSeconds = -1 }},
		{name: "failure backoff", set: func(c *DownloaderConfig) { c.FailureBackoffSeconds = -1 }},
	}
	for _, tt := range fields {
		t.Run(tt.name, func(t *testing.T) {
			downloader := DownloaderConfig{Backend: "auto"}
			tt.set(&downloader)
			cfg := &Config{
				OutputRoot: "/tmp/test",
				DBPath:     "test.db",
				Web:        WebConfig{Enabled: true, Listen: "127.0.0.1:6334"},
				Worker:     WorkerConfig{Num: 1},
				LiveRecord: LiveRecordConfig{AudioContainer: "m4a"},
				Downloader: downloader,
				VAD:        validVADDefaults(),
			}
			if err := cfg.Validate(); err == nil {
				t.Fatal("期望负数下载风控配置报错")
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
		VAD:        validVADDefaults(),
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

	// CLI provider 留空时使用 CLI 当前配置的默认模型，不能误显示或传入 DeepSeek 模型名。
	cli := RecapAIConfig{Provider: "codex_cli"}
	if got := cli.EffectiveModel(); got != "" {
		t.Fatalf("EffectiveModel() codex_cli empty = %q, want empty CLI default", got)
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

// TestDashScopeEffectiveURLs 验证 DashScopeConfig 的 EffectiveASRURL/EffectiveTasksURL 留空兜底。
// 修复 2026-07-20 BUG #1:配置备份导入会把空串持久化到 runtime_settings,覆盖 viper SetDefault 默认值,
// 导致 ASR POST 到空 URL。Effective 方法在调用点兜底,无论数据来自 config.yaml/runtime_settings/import
// 都不会失败。
func TestDashScopeEffectiveURLs(t *testing.T) {
	// 全空 → 回落 DashScope 官方默认
	empty := DashScopeConfig{}
	if got := empty.EffectiveASRURL(); got != DefaultDashScopeASRURL {
		t.Fatalf("EffectiveASRURL() empty = %q, want %q", got, DefaultDashScopeASRURL)
	}
	if got := empty.EffectiveTasksURL(); got != DefaultDashScopeTasksURL {
		t.Fatalf("EffectiveTasksURL() empty = %q, want %q", got, DefaultDashScopeTasksURL)
	}

	// 纯空白 → 回落默认(防止用户误填空格)
	whitespace := DashScopeConfig{ASRURL: "   ", TasksURL: "\t\n"}
	if got := whitespace.EffectiveASRURL(); got != DefaultDashScopeASRURL {
		t.Fatalf("EffectiveASRURL() whitespace = %q, want default (trimmed to empty)", got)
	}
	if got := whitespace.EffectiveTasksURL(); got != DefaultDashScopeTasksURL {
		t.Fatalf("EffectiveTasksURL() whitespace = %q, want default (trimmed to empty)", got)
	}

	// 非空自定义值原样返回(含 trim)
	filled := DashScopeConfig{
		ASRURL:   "  https://custom.example.com/asr  ",
		TasksURL: "https://custom.example.com/tasks/",
	}
	if got := filled.EffectiveASRURL(); got != "https://custom.example.com/asr" {
		t.Fatalf("EffectiveASRURL() filled = %q (should trim), want https://custom.example.com/asr", got)
	}
	if got := filled.EffectiveTasksURL(); got != "https://custom.example.com/tasks/" {
		t.Fatalf("EffectiveTasksURL() filled = %q, want https://custom.example.com/tasks/", got)
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
				VAD:        validVADDefaults(),
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
		VAD:    validVADDefaults(),
	}
}

// validVADDefaults 返回能通过 Validate() 的 VADConfig 默认值。
// 供手动构造 Config 字面量的测试复用,避免 VAD 引入后这些测试因 VAD 字段零值而 Validate 失败。
func validVADDefaults() VADConfig {
	return VADConfig{
		Enabled:        true,
		ThresholdDB:    -40,
		MinSilenceSec:  2.0,
		PaddingSec:     0.2,
		DetectionMode:  "peak",
		MinOutputRatio: 0.3,
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

// mcp section 覆盖测试(MCP 搜索工具配置)。
func TestApplyOverrides_OverridesMCPFields(t *testing.T) {
	cfg := baseCfg()
	enabled := true
	rounds := 8
	servers := []MCPServerConfig{{Name: "test", Transport: "http", URL: "http://localhost:9090", Enabled: true, Headers: map[string]string{"Authorization": "Bearer x"}}}
	builtin := MCPBuiltinConfig{BraveAPIKey: "key123"}
	overrides := map[string]json.RawMessage{
		"mcp": rawJSON(t, MCPSectionDTO{
			Enabled:       &enabled,
			MaxToolRounds: &rounds,
			Servers:       &servers,
			Builtin:       &builtin,
		}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if !cfg.MCP.Enabled {
		t.Errorf("MCP.Enabled should be true")
	}
	if cfg.MCP.MaxToolRounds != 8 {
		t.Errorf("MaxToolRounds = %d, want 8", cfg.MCP.MaxToolRounds)
	}
	if len(cfg.MCP.Servers) != 1 || cfg.MCP.Servers[0].Name != "test" {
		t.Errorf("Servers not overridden: %+v", cfg.MCP.Servers)
	}
	if got := cfg.MCP.Servers[0].Headers["Authorization"]; got != "Bearer x" {
		t.Errorf("Servers[0].Headers[Authorization] = %q, want %q", got, "Bearer x")
	}
	if cfg.MCP.Builtin.BraveAPIKey != "key123" {
		t.Errorf("Builtin.BraveAPIKey = %q", cfg.MCP.Builtin.BraveAPIKey)
	}
}

// presence-aware:MCPSectionDTO 部分字段 nil 保留基线。
func TestApplyOverrides_MCPPresenceAware(t *testing.T) {
	cfg := baseCfg()
	cfg.MCP.MaxToolRounds = 10 // 设基线
	enabled := true
	overrides := map[string]json.RawMessage{
		"mcp": rawJSON(t, MCPSectionDTO{Enabled: &enabled}), // 其余字段 nil
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if !cfg.MCP.Enabled {
		t.Errorf("Enabled should be overridden to true")
	}
	if cfg.MCP.MaxToolRounds != 10 {
		t.Errorf("MaxToolRounds should retain baseline 10, got %d", cfg.MCP.MaxToolRounds)
	}
}

// Servers 为空切片(非 nil)= 全量清空。
func TestApplyOverrides_MCPClearServers(t *testing.T) {
	cfg := baseCfg()
	cfg.MCP.Servers = []MCPServerConfig{{Name: "old", Transport: "http"}}
	emptyServers := []MCPServerConfig{}
	overrides := map[string]json.RawMessage{
		"mcp": rawJSON(t, MCPSectionDTO{Servers: &emptyServers}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if len(cfg.MCP.Servers) != 0 {
		t.Errorf("Servers should be cleared, got %d items", len(cfg.MCP.Servers))
	}
}

// EffectiveMaxToolRounds 兜底测试。
func TestMCPConfig_EffectiveMaxToolRounds(t *testing.T) {
	if (&MCPConfig{}).EffectiveMaxToolRounds() != 5 {
		t.Error("零值应兜底 5")
	}
	m := MCPConfig{MaxToolRounds: 3}
	if m.EffectiveMaxToolRounds() != 3 {
		t.Error("正值应原样返回")
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

// TestVADDefaults 验证 VAD 默认值(空配置 → 推荐参数,零配置开箱即用)。
// 见 plans/plan-vad-2026-07-27.md Phase 1。
func TestVADDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if !cfg.VAD.Enabled {
		t.Errorf("VAD.Enabled = false, want true (default on)")
	}
	if cfg.VAD.EffectiveEngine() != "silence" {
		t.Errorf("VAD.Engine = %q, want silence", cfg.VAD.Engine)
	}
	if cfg.VAD.ThresholdDB != -40 {
		t.Errorf("VAD.ThresholdDB = %d, want -40", cfg.VAD.ThresholdDB)
	}
	if cfg.VAD.MinSilenceSec != 2.0 {
		t.Errorf("VAD.MinSilenceSec = %v, want 2.0", cfg.VAD.MinSilenceSec)
	}
	if cfg.VAD.PaddingSec != 0.2 {
		t.Errorf("VAD.PaddingSec = %v, want 0.2", cfg.VAD.PaddingSec)
	}
	if cfg.VAD.DetectionMode != "peak" {
		t.Errorf("VAD.DetectionMode = %q, want \"peak\"", cfg.VAD.DetectionMode)
	}
	if cfg.VAD.MinOutputRatio != 0.3 {
		t.Errorf("VAD.MinOutputRatio = %v, want 0.3", cfg.VAD.MinOutputRatio)
	}
	if cfg.VAD.EffectiveInaPython() != "python3" || cfg.VAD.EffectiveInaScript() != "scripts/ina_segment.py" {
		t.Errorf("ina commands = %q / %q", cfg.VAD.InaPython, cfg.VAD.InaScript)
	}
	if cfg.VAD.InaBatchSize != 256 || cfg.VAD.InaMinSpeechSec != 0.6 || cfg.VAD.InaMergeGapSec != 0.4 {
		t.Errorf("ina defaults = batch %d, min %v, gap %v", cfg.VAD.InaBatchSize, cfg.VAD.InaMinSpeechSec, cfg.VAD.InaMergeGapSec)
	}
}

// TestVADValidate 验证 VADConfig.Validate 的边界校验。
func TestVADValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  VADConfig
		ok   bool
	}{
		{"default", VADConfig{ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 0.3}, true},
		{"threshold_zero", VADConfig{ThresholdDB: 0, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 0.3}, true},
		{"threshold_too_high", VADConfig{ThresholdDB: 10, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 0.3}, false},
		{"threshold_too_low", VADConfig{ThresholdDB: -81, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 0.3}, false},
		{"silence_zero", VADConfig{ThresholdDB: -40, MinSilenceSec: 0, PaddingSec: 0.2, MinOutputRatio: 0.3}, false},
		{"silence_negative", VADConfig{ThresholdDB: -40, MinSilenceSec: -1, PaddingSec: 0.2, MinOutputRatio: 0.3}, false},
		{"padding_negative", VADConfig{ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: -0.1, MinOutputRatio: 0.3}, false},
		{"ratio_zero", VADConfig{ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 0}, false},
		{"ratio_over_one", VADConfig{ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 1.5}, false},
		{"ratio_one_ok", VADConfig{ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 1}, true},
		{"bad_engine", VADConfig{Engine: "unknown", ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 1}, false},
		{"ina_ok", VADConfig{Engine: "ina", ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 1, InaBatchSize: 256, InaMinSpeechSec: 0.6, InaMergeGapSec: 0.4}, true},
		{"ina_bad_batch", VADConfig{Engine: "ina", ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: 0.2, MinOutputRatio: 1, InaBatchSize: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("Validate() expected error, got nil")
			}
		})
	}
}

// TestApplyOverrides_OverridesVADFields 验证 ApplyOverrides 完整覆盖 VAD 段所有字段。
func TestApplyOverrides_OverridesVADFields(t *testing.T) {
	cfg := baseCfg()
	enabled := false
	threshold := -50
	minSilence := 3.0
	padding := 0.5
	ratio := 0.5
	engine := "ina"
	inaPython := "/opt/ina/bin/python"
	inaScript := "/opt/ina/segment.py"
	inaBatch := 128
	inaMinSpeech := 0.8
	inaMergeGap := 0.5
	overrides := map[string]json.RawMessage{
		"vad": rawJSON(t, VADSectionDTO{
			Enabled:         &enabled,
			Engine:          &engine,
			ThresholdDB:     &threshold,
			MinSilenceSec:   &minSilence,
			PaddingSec:      &padding,
			MinOutputRatio:  &ratio,
			InaPython:       &inaPython,
			InaScript:       &inaScript,
			InaBatchSize:    &inaBatch,
			InaMinSpeechSec: &inaMinSpeech,
			InaMergeGapSec:  &inaMergeGap,
		}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if cfg.VAD.Enabled {
		t.Errorf("VAD.Enabled = true, want false")
	}
	if cfg.VAD.ThresholdDB != -50 {
		t.Errorf("VAD.ThresholdDB = %d, want -50", cfg.VAD.ThresholdDB)
	}
	if cfg.VAD.MinSilenceSec != 3.0 {
		t.Errorf("VAD.MinSilenceSec = %v, want 3.0", cfg.VAD.MinSilenceSec)
	}
	if cfg.VAD.PaddingSec != 0.5 {
		t.Errorf("VAD.PaddingSec = %v, want 0.5", cfg.VAD.PaddingSec)
	}
	if cfg.VAD.MinOutputRatio != 0.5 {
		t.Errorf("VAD.MinOutputRatio = %v, want 0.5", cfg.VAD.MinOutputRatio)
	}
	if cfg.VAD.Engine != engine || cfg.VAD.InaPython != inaPython || cfg.VAD.InaScript != inaScript ||
		cfg.VAD.InaBatchSize != inaBatch || cfg.VAD.InaMinSpeechSec != inaMinSpeech || cfg.VAD.InaMergeGapSec != inaMergeGap {
		t.Errorf("ina override fields = %+v", cfg.VAD)
	}
}

// TestApplyOverrides_VADPresenceAware 验证 VADSectionDTO 的 presence-aware 语义:
// nil 字段保留基线,非 nil(含零值)覆盖。
func TestApplyOverrides_VADPresenceAware(t *testing.T) {
	cfg := baseCfg()
	// 基线:Enabled=true, ThresholdDB=-40
	if !cfg.VAD.Enabled || cfg.VAD.ThresholdDB != -40 {
		t.Fatalf("baseline VAD not as expected: %+v", cfg.VAD)
	}
	// 只传 Enabled=false,其他字段 nil → 只改 Enabled
	enabled := false
	overrides := map[string]json.RawMessage{
		"vad": rawJSON(t, VADSectionDTO{Enabled: &enabled}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if cfg.VAD.Enabled {
		t.Errorf("Enabled should be false after override")
	}
	// 其他字段保留基线
	if cfg.VAD.ThresholdDB != -40 {
		t.Errorf("ThresholdDB = %d, want baseline -40 (presence-aware)", cfg.VAD.ThresholdDB)
	}
}

// TestApplyOverrides_VADCorruptJSON 验证损坏的 VAD JSON 被跳过(不 fatal)。
func TestApplyOverrides_VADCorruptJSON(t *testing.T) {
	cfg := baseCfg()
	baselineEnabled := cfg.VAD.Enabled
	baselineThreshold := cfg.VAD.ThresholdDB
	overrides := map[string]json.RawMessage{
		"vad": json.RawMessage(`{not valid json`),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides with corrupt vad JSON: %v (should not error)", err)
	}
	// 基线保留
	if cfg.VAD.Enabled != baselineEnabled || cfg.VAD.ThresholdDB != baselineThreshold {
		t.Errorf("corrupt VAD JSON should leave baseline intact, got %+v", cfg.VAD)
	}
}

// TestReplayDefaults 验证 replay 段默认值(两者皆 false,升级零行为变化)。
func TestReplayDefaults(t *testing.T) {
	cfg := baseCfg()
	if cfg.Replay.AutoASR || cfg.Replay.AutoRecap {
		t.Fatalf("replay defaults must be false, got auto_asr=%v auto_recap=%v", cfg.Replay.AutoASR, cfg.Replay.AutoRecap)
	}
}

// TestApplyOverrides_ReplayPresenceAware 验证 replay 段 presence-aware 覆盖(nil 字段保留基线)。
func TestApplyOverrides_ReplayPresenceAware(t *testing.T) {
	cfg := baseCfg()
	// 基线为 false,只传 AutoASR=true,AutoRecap=nil → 只改 AutoASR
	autoASR := true
	overrides := map[string]json.RawMessage{
		"replay": rawJSON(t, ReplaySectionDTO{AutoASR: &autoASR}),
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if !cfg.Replay.AutoASR {
		t.Errorf("AutoASR should be true after override")
	}
	// AutoRecap 保留基线 false
	if cfg.Replay.AutoRecap {
		t.Errorf("AutoRecap = %v, want baseline false (presence-aware)", cfg.Replay.AutoRecap)
	}
}

// TestReplayAutoEnabled 表驱动测试 helper(qoder 审核 I-4):覆盖「主播优先 + 全局兜底」全部分支。
func TestReplayAutoEnabled(t *testing.T) {
	cases := []struct {
		name        string
		sourceType  string
		sessOK      bool
		channelFlag bool
		globalFlag  bool
		want        bool
	}{
		// 主播优先:channelFlag=true 短路,不看 source_type/全局
		{"channel_on_shortcircuits_replay", "download", true, true, false, true},
		{"channel_on_shortcircuits_live", "live_record", true, true, true, true},
		{"channel_on_even_when_sess_fetch_failed", "", false, true, true, true},
		// 主播关 + 非回放类 → false(录播自动链靠主播开关,不受全局影响)
		{"live_channel_off_global_on", "live_record", true, false, true, false},
		// 主播关 + 取 session 失败 → false(零回归:按非回放处理)
		{"sess_fetch_failed", "download", false, false, true, false},
		// 主播关 + 回放类 + 全局开 → true(全局兜底)
		{"replay_global_on", "import", true, false, true, true},
		{"replay_download_global_on", "download", true, false, true, true},
		// 主播关 + 回放类 + 全局关 → false
		{"replay_global_off", "import", true, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ReplayAutoEnabled(c.sourceType, c.sessOK, c.channelFlag, c.globalFlag)
			if got != c.want {
				t.Errorf("ReplayAutoEnabled(%q, sessOK=%v, channel=%v, global=%v) = %v, want %v",
					c.sourceType, c.sessOK, c.channelFlag, c.globalFlag, got, c.want)
			}
		})
	}
}

func TestRerecordDefaultsAndEffective(t *testing.T) {
	// 2026-08-20 同场重录修复(plans/plan-liverecord-rerecord-2026-08-20.md):
	// viper 默认 600s/3 次;Effective 归一化 cooldown<=0→0(禁用)、max<=0→3。
	path := writeTestConfig(t, "output_root: /tmp/test\ndb_path: test.db\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LiveRecord.RerecordCooldownSeconds != DefaultRerecordCooldownSeconds {
		t.Errorf("live_record.rerecord_cooldown_seconds 默认 = %d, 期望 %d",
			cfg.LiveRecord.RerecordCooldownSeconds, DefaultRerecordCooldownSeconds)
	}
	if cfg.LiveRecord.RerecordMaxAttempts != DefaultRerecordMaxAttempts {
		t.Errorf("live_record.rerecord_max_attempts 默认 = %d, 期望 %d",
			cfg.LiveRecord.RerecordMaxAttempts, DefaultRerecordMaxAttempts)
	}
	for _, v := range []int{0, -5} {
		if got := (LiveRecordConfig{RerecordCooldownSeconds: v}).EffectiveRerecordCooldown(); got != 0 {
			t.Errorf("EffectiveRerecordCooldown(%d) = %d, 期望 0(禁用)", v, got)
		}
	}
	if got := (LiveRecordConfig{RerecordCooldownSeconds: 300}).EffectiveRerecordCooldown(); got != 300 {
		t.Errorf("EffectiveRerecordCooldown(300) = %d, 期望 300", got)
	}
	for _, v := range []int{0, -1} {
		if got := (LiveRecordConfig{RerecordMaxAttempts: v}).EffectiveRerecordMaxAttempts(); got != DefaultRerecordMaxAttempts {
			t.Errorf("EffectiveRerecordMaxAttempts(%d) = %d, 期望 %d", v, got, DefaultRerecordMaxAttempts)
		}
	}
	if got := (LiveRecordConfig{RerecordMaxAttempts: 5}).EffectiveRerecordMaxAttempts(); got != 5 {
		t.Errorf("EffectiveRerecordMaxAttempts(5) = %d, 期望 5", got)
	}
}
