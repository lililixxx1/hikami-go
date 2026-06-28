package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/robfig/cron/v3"

	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/discover"
	"hikami-go/internal/live_record"
	"hikami-go/internal/runtime"
)

// notifyManager 接口（避免循环依赖）
type notifyManager interface {
	Send(ctx context.Context, eventType, title, body string)
}

// diskChecker 检查磁盘使用情况（可 mock）
type diskChecker func(paths []string) []runtime.DiskInfo

// cookieChecker 检查 Cookie 过期情况（可 mock）
type cookieChecker func(ctx context.Context, cs *channel.Store) []runtime.CookieWarning

// defaultDiskChecker 使用 runtime.CheckDiskUsage
func defaultDiskChecker(paths []string) []runtime.DiskInfo {
	return runtime.CheckDiskUsage(paths)
}

// defaultCookieChecker 使用 runtime.CheckCookieExpiry
func defaultCookieChecker(ctx context.Context, cs *channel.Store) []runtime.CookieWarning {
	return runtime.CheckCookieExpiry(ctx, cs)
}

// Scheduler wraps robfig/cron to run periodic discovery and live-check jobs.
type Scheduler struct {
	ctx             context.Context
	cancel          context.CancelFunc
	cron            *cron.Cron
	discoverManager *discover.Manager
	liveManager     *live_record.Manager
	channelStore    *channel.Store
	cfg             *config.Config
	notifyMgr       notifyManager
	checkDisk       diskChecker
	checkCookie     cookieChecker
}

// New creates a Scheduler with the given config and module dependencies.
func New(cfg *config.Config, dm *discover.Manager, lm *live_record.Manager, cs *channel.Store, notifyMgr notifyManager) *Scheduler {
	return newWithCheckers(cfg, dm, lm, cs, notifyMgr, defaultDiskChecker, defaultCookieChecker)
}

// newWithCheckers creates a Scheduler with injectable check functions (for testing).
func newWithCheckers(cfg *config.Config, dm *discover.Manager, lm *live_record.Manager, cs *channel.Store, notifyMgr notifyManager, dc diskChecker, cc cookieChecker) *Scheduler {
	if dc == nil {
		dc = defaultDiskChecker
	}
	if cc == nil {
		cc = defaultCookieChecker
	}

	c := cron.New()
	schedulerCtx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{
		ctx:             schedulerCtx,
		cancel:          cancel,
		cron:            c,
		discoverManager: dm,
		liveManager:     lm,
		channelStore:    cs,
		cfg:             cfg,
		notifyMgr:       notifyMgr,
		checkDisk:       dc,
		checkCookie:     cc,
	}

	discoverySpec := cfg.Cron.Discovery
	liveCheckSpec := cfg.Cron.LiveCheck

	if discoverySpec != "" {
		if _, err := c.AddFunc(discoverySpec, func() {
			slog.Info("scheduler: running discovery")
			results, err := dm.DiscoverAll(s.ctx)
			if err != nil {
				slog.Error("scheduler: discovery failed", "error", err)
				return
			}
			slog.Info("scheduler: discovery completed", "results", len(results))
		}); err != nil {
			slog.Error("scheduler: failed to register discovery job", "spec", discoverySpec, "error", err)
		}
	}

	if liveCheckSpec != "" {
		if _, err := c.AddFunc(liveCheckSpec, func() {
			slog.Info("scheduler: running live check")
			statuses, err := lm.CheckAndStartAll(s.ctx)
			if err != nil {
				slog.Error("scheduler: live check failed", "error", err)
				return
			}
			slog.Info("scheduler: live check completed", "channels", len(statuses))
		}); err != nil {
			slog.Error("scheduler: failed to register live_check job", "spec", liveCheckSpec, "error", err)
		}
	}

	// 每小时检查磁盘空间
	if _, err := c.AddFunc("@every 1h", func() {
		slog.Info("scheduler: running disk check")
		paths := []string{cfg.OutputRoot, filepath.Dir(cfg.DBPath)}
		diskUsage := dc(paths)
		for _, d := range diskUsage {
			if d.UsedPercent >= 85 {
				title := "磁盘空间警告"
				body := fmt.Sprintf("路径 %s 使用率 %.1f%%，剩余 %.1f GB", d.Path, d.UsedPercent, d.FreeGB)
				slog.Warn("disk usage high", "path", d.Path, "percent", d.UsedPercent)
				if notifyMgr != nil {
					notifyMgr.Send(s.ctx, "disk_warning", title, body)
				}
			}
		}
	}); err != nil {
		slog.Error("scheduler: failed to register disk check", "error", err)
	}

	// 每天凌晨 3:00 检查 Cookie 过期
	if _, err := c.AddFunc("0 3 * * *", func() {
		slog.Info("scheduler: running cookie expiry check")
		warnings := cc(s.ctx, cs)
		for _, w := range warnings {
			if notifyMgr == nil {
				continue
			}
			if w.IsExpired {
				title := "Cookie 已过期"
				body := fmt.Sprintf("主播 %s 的%s Cookie 已过期", w.ChannelName, w.CookieType)
				notifyMgr.Send(s.ctx, "cookie_expiring", title, body)
			} else if w.DaysLeft <= 7 {
				title := "Cookie 即将过期"
				body := fmt.Sprintf("主播 %s 的%s Cookie 将在 %d 天后过期", w.ChannelName, w.CookieType, w.DaysLeft)
				notifyMgr.Send(s.ctx, "cookie_expiring", title, body)
			}
		}
	}); err != nil {
		slog.Error("scheduler: failed to register cookie check", "error", err)
	}

	return s
}

// Start begins the cron scheduler.
func (s *Scheduler) Start() {
	s.cron.Start()
	slog.Info("scheduler: started")
}

// Stop gracefully stops the cron scheduler.
func (s *Scheduler) Stop() {
	s.cancel()
	ctx := s.cron.Stop()
	<-ctx.Done()
	slog.Info("scheduler: stopped")
}
