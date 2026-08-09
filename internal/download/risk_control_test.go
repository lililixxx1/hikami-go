package download

import (
	"testing"
	"time"

	"hikami-go/internal/config"
)

func TestDownloadLimiterDisabledByDefault(t *testing.T) {
	if limiter := newDownloadLimiter(config.DownloaderConfig{}); limiter != nil {
		t.Fatalf("默认配置应关闭下载保护，实际为 %#v", limiter)
	}
}

func TestDownloadLimiterSerializesAndSharesFailureBackoff(t *testing.T) {
	now := time.Date(2026, 8, 9, 4, 0, 0, 0, time.Local)
	limiter := newDownloadLimiter(config.DownloaderConfig{
		MaxConcurrent:         1,
		MinIntervalSeconds:    30,
		FailureBackoffSeconds: 600,
	})
	limiter.now = func() time.Time { return now }

	release, delay := limiter.tryAcquire()
	if release == nil || delay != 0 {
		t.Fatalf("first acquire = (%v, %s), want acquired", release != nil, delay)
	}
	if release2, delay := limiter.tryAcquire(); release2 != nil || delay != downloadBusyRetryDelay {
		t.Fatalf("concurrent acquire = (%v, %s), want busy delay %s", release2 != nil, delay, downloadBusyRetryDelay)
	}

	release(false)
	if release2, delay := limiter.tryAcquire(); release2 != nil || delay != 30*time.Second {
		t.Fatalf("post-success acquire = (%v, %s), want 30s interval", release2 != nil, delay)
	}

	now = now.Add(30 * time.Second)
	release, delay = limiter.tryAcquire()
	if release == nil || delay != 0 {
		t.Fatalf("second acquire = (%v, %s), want acquired", release != nil, delay)
	}
	release(true)
	if release2, delay := limiter.tryAcquire(); release2 != nil || delay != 600*time.Second {
		t.Fatalf("post-failure acquire = (%v, %s), want shared 10m backoff", release2 != nil, delay)
	}
}
