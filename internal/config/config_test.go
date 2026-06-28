package config

import (
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
	if cfg.Worker.LiveRecordNum != 2 {
		t.Errorf("worker.live_record_num = %d, 期望 2", cfg.Worker.LiveRecordNum)
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
		Worker:     WorkerConfig{Num: 3, LiveRecordNum: 1},
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
