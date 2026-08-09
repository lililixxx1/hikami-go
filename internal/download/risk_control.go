package download

import (
	"sync"
	"time"

	"hikami-go/internal/config"
)

const downloadBusyRetryDelay = 30 * time.Second

// downloadLimiter 协调同一服务进程里的回放下载。它通过限制批量/多 P 下载突发来
// 降低 B 站风控概率，并让 worker 延期等待任务而不是阻塞其它任务类型。
type downloadLimiter struct {
	mu             sync.Mutex
	maxConcurrent  int
	active         int
	notBefore      time.Time
	minInterval    time.Duration
	failureBackoff time.Duration
	now            func() time.Time
}

func newDownloadLimiter(cfg config.DownloaderConfig) *downloadLimiter {
	// 0 保持升级前行为：不限制并发、不插入等待。这样新功能不会在用户未配置时
	// 静默降低现有下载吞吐。
	if cfg.MaxConcurrent <= 0 {
		return nil
	}
	return &downloadLimiter{
		maxConcurrent:  cfg.MaxConcurrent,
		minInterval:    time.Duration(cfg.MinIntervalSeconds) * time.Second,
		failureBackoff: time.Duration(cfg.FailureBackoffSeconds) * time.Second,
		now:            time.Now,
	}
}

// tryAcquire 要么预留下载槽位，要么返回 worker 应延期的时长。获取成功后必须且只能
// 调用一次 release(remoteFailed)。远端失败会延长共享冷却窗口，避免批量任务继续
// 重复触发可能产生 412/429 的请求。
func (l *downloadLimiter) tryAcquire() (release func(remoteFailed bool), retryAfter time.Duration) {
	if l == nil {
		return func(bool) {}, 0
	}
	l.mu.Lock()
	now := l.now()
	if l.active >= l.maxConcurrent {
		l.mu.Unlock()
		return nil, downloadBusyRetryDelay
	}
	if now.Before(l.notBefore) {
		delay := l.notBefore.Sub(now)
		l.mu.Unlock()
		if delay < time.Second {
			delay = time.Second
		}
		return nil, delay
	}
	l.active++
	if next := now.Add(l.minInterval); next.After(l.notBefore) {
		l.notBefore = next
	}
	l.mu.Unlock()

	var once sync.Once
	return func(remoteFailed bool) {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.active > 0 {
				l.active--
			}
			cooldown := l.minInterval
			if remoteFailed {
				cooldown = l.failureBackoff
			}
			if next := l.now().Add(cooldown); next.After(l.notBefore) {
				l.notBefore = next
			}
		})
	}, 0
}
