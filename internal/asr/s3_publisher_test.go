package asr

import (
	"os"
	"testing"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

func TestS3ObjectKey(t *testing.T) {
	si := session.Session{ChannelID: "12345", ID: "sess-abc"}
	key := s3ObjectKey(si)
	expected := "12345/sess-abc/audio.asr.mp3"
	if key != expected {
		t.Errorf("s3ObjectKey = %q, want %q", key, expected)
	}
}

func TestS3PublicURL(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		key      string
		expected string
	}{
		{"trailing slash", "https://oss.example.com/bucket/", "ch/sess/audio.asr.mp3", "https://oss.example.com/bucket/ch/sess/audio.asr.mp3"},
		{"no trailing slash", "https://oss.example.com/bucket", "ch/sess/audio.asr.mp3", "https://oss.example.com/bucket/ch/sess/audio.asr.mp3"},
		{"double trailing slash", "https://oss.example.com/bucket//", "ch/sess/audio.asr.mp3", "https://oss.example.com/bucket/ch/sess/audio.asr.mp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := s3PublicURL(tt.prefix, tt.key)
			if url != tt.expected {
				t.Errorf("s3PublicURL(%q, %q) = %q, want %q", tt.prefix, tt.key, url, tt.expected)
			}
		})
	}
}

func TestNewS3Publisher_MissingConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.ASRS3Config
	}{
		{"empty", config.ASRS3Config{}},
		{"no endpoint", config.ASRS3Config{Bucket: "b", AccessKeyID: "ak", AccessKeySecret: "sk", PublicURLPrefix: "https://oss.example.com/b"}},
		{"no bucket", config.ASRS3Config{Endpoint: "https://oss.example.com", AccessKeyID: "ak", AccessKeySecret: "sk", PublicURLPrefix: "https://oss.example.com/b"}},
		{"no access key", config.ASRS3Config{Endpoint: "https://oss.example.com", Bucket: "b", PublicURLPrefix: "https://oss.example.com/b"}},
		{"no public url prefix", config.ASRS3Config{Endpoint: "https://oss.example.com", Bucket: "b", AccessKeyID: "ak", AccessKeySecret: "sk"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cfg.Configured() {
				t.Error("expected Configured() = false")
			}
		})
	}
}

func TestASRS3Config_SecretResolved(t *testing.T) {
	t.Run("from env", func(t *testing.T) {
		os.Setenv("TEST_S3_SECRET", "env-secret")
		defer os.Unsetenv("TEST_S3_SECRET")
		cfg := config.ASRS3Config{AccessKeyEnv: "TEST_S3_SECRET", AccessKeySecret: "direct-secret"}
		if got := cfg.SecretResolved(); got != "env-secret" {
			t.Errorf("SecretResolved() = %q, want %q", got, "env-secret")
		}
	})
	t.Run("fallback to direct", func(t *testing.T) {
		cfg := config.ASRS3Config{AccessKeySecret: "direct-secret"}
		if got := cfg.SecretResolved(); got != "direct-secret" {
			t.Errorf("SecretResolved() = %q, want %q", got, "direct-secret")
		}
	})
	t.Run("empty", func(t *testing.T) {
		cfg := config.ASRS3Config{}
		if got := cfg.SecretResolved(); got != "" {
			t.Errorf("SecretResolved() = %q, want empty", got)
		}
	})
}

func TestASRS3Config_Configured(t *testing.T) {
	cfg := config.ASRS3Config{
		Endpoint:        "https://oss.example.com",
		Bucket:          "test-bucket",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
		PublicURLPrefix: "https://oss.example.com/test-bucket",
	}
	if !cfg.Configured() {
		t.Error("expected Configured() = true")
	}
}

func TestNewConfiguredTranscriber_S3Fallback(t *testing.T) {
	os.Setenv("DASHSCOPE_API_KEY", "test-key")
	defer os.Unsetenv("DASHSCOPE_API_KEY")

	cfg := &config.Config{
		DashScope: config.DashScopeConfig{APIKeyEnv: "DASHSCOPE_API_KEY"},
		ASRTemp:   config.ASRTempConfig{},
		ASRS3: config.ASRS3Config{
			Endpoint:        "https://oss.example.com",
			Bucket:          "b",
			AccessKeyID:     "ak",
			AccessKeySecret: "sk",
			PublicURLPrefix: "https://oss.example.com/b",
		},
	}
	tr := NewConfiguredTranscriber(cfg)
	dst, ok := tr.(*DashScopeTranscriber)
	if !ok {
		t.Fatalf("expected *DashScopeTranscriber, got %T", tr)
	}
	if dst.s3Publisher == nil {
		t.Error("expected s3Publisher to be set")
	}
	if dst.tempServer != nil {
		t.Error("expected tempServer to be nil when S3 is configured but TempAudioServer is not")
	}
}

func TestNewConfiguredTranscriber_ThreeTierPriority(t *testing.T) {
	os.Setenv("DASHSCOPE_API_KEY", "test-key")
	defer os.Unsetenv("DASHSCOPE_API_KEY")

	t.Run("temp server takes priority", func(t *testing.T) {
		cfg := &config.Config{
			DashScope: config.DashScopeConfig{APIKeyEnv: "DASHSCOPE_API_KEY"},
			ASRTemp: config.ASRTempConfig{
				Enabled:       true,
				LocalDir:      "/tmp/asr-test",
				PublicBaseURL: "http://1.2.3.4:9999/asr-temp",
			},
			ASRS3: config.ASRS3Config{
				Endpoint:        "https://oss.example.com",
				Bucket:          "b",
				AccessKeyID:     "ak",
				AccessKeySecret: "sk",
				PublicURLPrefix: "https://oss.example.com/b",
			},
		}
		tr := NewConfiguredTranscriber(cfg)
		dst, ok := tr.(*DashScopeTranscriber)
		if !ok {
			t.Fatalf("expected *DashScopeTranscriber, got %T", tr)
		}
		if dst.tempServer == nil {
			t.Error("expected tempServer to be set (priority 1)")
		}
		if dst.s3Publisher != nil {
			t.Error("expected s3Publisher to be nil when tempServer is configured")
		}
	})

	t.Run("s3 is second priority", func(t *testing.T) {
		cfg := &config.Config{
			DashScope: config.DashScopeConfig{APIKeyEnv: "DASHSCOPE_API_KEY"},
			ASRTemp:   config.ASRTempConfig{},
			ASRS3: config.ASRS3Config{
				Endpoint:        "https://oss.example.com",
				Bucket:          "b",
				AccessKeyID:     "ak",
				AccessKeySecret: "sk",
				PublicURLPrefix: "https://oss.example.com/b",
			},
		}
		tr := NewConfiguredTranscriber(cfg)
		dst, ok := tr.(*DashScopeTranscriber)
		if !ok {
			t.Fatalf("expected *DashScopeTranscriber, got %T", tr)
		}
		if dst.s3Publisher == nil {
			t.Error("expected s3Publisher to be set (priority 2)")
		}
	})

	t.Run("no backend returns local", func(t *testing.T) {
		cfg := &config.Config{
			DashScope: config.DashScopeConfig{APIKeyEnv: "DASHSCOPE_API_KEY"},
			ASRTemp:   config.ASRTempConfig{},
			ASRS3:     config.ASRS3Config{},
		}
		tr := NewConfiguredTranscriber(cfg)
		if _, ok := tr.(LocalTranscriber); !ok {
			t.Fatalf("expected LocalTranscriber when no backend configured, got %T", tr)
		}
	})
}
