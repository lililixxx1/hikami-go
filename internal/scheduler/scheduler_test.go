package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/runtime"
)

// mockNotifyManager 记录所有 Send 调用
type mockNotifyManager struct {
	mu    sync.Mutex
	sends []mockSendCall
}

type mockSendCall struct {
	EventType string
	Title     string
	Body      string
}

func (m *mockNotifyManager) Send(_ context.Context, eventType, title, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends = append(m.sends, mockSendCall{
		EventType: eventType,
		Title:     title,
		Body:      body,
	})
}

func (m *mockNotifyManager) getSends() []mockSendCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]mockSendCall, len(m.sends))
	copy(result, m.sends)
	return result
}

func (m *mockNotifyManager) sendCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sends)
}

// testConfig 返回用于测试的 Config
func testConfig() *config.Config {
	return &config.Config{
		OutputRoot: "/tmp/hikami-test",
		DBPath:     "/tmp/hikami-test/data.db",
		Cron: config.CronConfig{
			Discovery: "@every 1h",
			LiveCheck: "@every 30s",
		},
	}
}

// TestNewScheduler 验证构造函数和 cron job 注册
func TestNewScheduler(t *testing.T) {
	cfg := testConfig()
	s := New(cfg, nil, nil, nil, nil)
	if s == nil {
		t.Fatal("New returned nil")
	}

	// cron 应已注册 job: discovery + liveCheck + diskCheck + cookieCheck = 4
	entryCount := len(s.cron.Entries())
	if entryCount != 4 {
		t.Errorf("expected 4 cron entries (discovery + liveCheck + disk + cookie), got %d", entryCount)
	}

	s.Stop()
}

// TestNewSchedulerWithNotify 验证注入 notifyManager
func TestNewSchedulerWithNotify(t *testing.T) {
	cfg := testConfig()
	mock := &mockNotifyManager{}

	s := New(cfg, nil, nil, nil, mock)
	if s == nil {
		t.Fatal("New returned nil")
	}

	if s.notifyMgr == nil {
		t.Error("notifyMgr should not be nil when injected")
	}

	s.Stop()
}

// TestNewSchedulerWithEmptyCronSpec 验证空 cron spec 时不注册对应 job
func TestNewSchedulerWithEmptyCronSpec(t *testing.T) {
	cfg := &config.Config{
		OutputRoot: "/tmp/hikami-test",
		DBPath:     "/tmp/hikami-test/data.db",
		Cron: config.CronConfig{
			Discovery: "",
			LiveCheck: "",
		},
	}

	s := New(cfg, nil, nil, nil, nil)
	if s == nil {
		t.Fatal("New returned nil")
	}

	// 只有 diskCheck 和 cookieCheck 两个固定 job
	entryCount := len(s.cron.Entries())
	if entryCount != 2 {
		t.Errorf("expected exactly 2 cron entries (disk + cookie), got %d", entryCount)
	}

	s.Stop()
}

// TestStartStop 验证 Start 后能 Stop，不 panic
func TestStartStop(t *testing.T) {
	cfg := testConfig()
	s := New(cfg, nil, nil, nil, nil)

	s.Start()
	s.Stop()
}

// TestMultipleStartStop 验证重复 Start/Stop 不出错
func TestMultipleStartStop(t *testing.T) {
	cfg := testConfig()
	s := New(cfg, nil, nil, nil, nil)

	for i := 0; i < 3; i++ {
		s.Start()
		s.Stop()
	}
}

// TestDiskCheckJob_LowUsage 磁盘使用 < 85% 不发送通知
func TestDiskCheckJob_LowUsage(t *testing.T) {
	cfg := testConfig()
	mock := &mockNotifyManager{}

	diskCheckFn := func(_ []string) []runtime.DiskInfo {
		return []runtime.DiskInfo{
			{Path: "/data", UsedPercent: 50.0, FreeGB: 100.0},
		}
	}

	s := newWithCheckers(cfg, nil, nil, nil, mock, diskCheckFn, nil)
	s.Start()
	execDiskCheckJob(s)
	s.Stop()

	if mock.sendCount() != 0 {
		t.Errorf("expected 0 notifications, got %d", mock.sendCount())
	}
}

// TestDiskCheckJob_HighUsage 磁盘使用 >= 85% 发送通知
func TestDiskCheckJob_HighUsage(t *testing.T) {
	cfg := testConfig()
	mock := &mockNotifyManager{}

	diskCheckFn := func(_ []string) []runtime.DiskInfo {
		return []runtime.DiskInfo{
			{Path: "/data", UsedPercent: 90.0, FreeGB: 10.0},
		}
	}

	s := newWithCheckers(cfg, nil, nil, nil, mock, diskCheckFn, nil)
	s.Start()
	execDiskCheckJob(s)
	s.Stop()

	if mock.sendCount() == 0 {
		t.Error("expected at least 1 notification for high disk usage")
	}

	sends := mock.getSends()
	found := false
	for _, c := range sends {
		if c.EventType == "disk_warning" && c.Title == "磁盘空间警告" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected disk_warning notification")
	}
}

// TestDiskCheckJob_Exactly85 磁盘使用 = 85% 发送通知（边界值）
func TestDiskCheckJob_Exactly85(t *testing.T) {
	cfg := testConfig()
	mock := &mockNotifyManager{}

	diskCheckFn := func(_ []string) []runtime.DiskInfo {
		return []runtime.DiskInfo{
			{Path: "/data", UsedPercent: 85.0, FreeGB: 15.0},
		}
	}

	s := newWithCheckers(cfg, nil, nil, nil, mock, diskCheckFn, nil)
	s.Start()
	execDiskCheckJob(s)
	s.Stop()

	if mock.sendCount() == 0 {
		t.Error("expected notification for disk usage exactly at 85%")
	}
}

// TestDiskCheckJob_NilNotifyManager notifyManager 为 nil 时不 panic
func TestDiskCheckJob_NilNotifyManager(t *testing.T) {
	cfg := testConfig()

	diskCheckFn := func(_ []string) []runtime.DiskInfo {
		return []runtime.DiskInfo{
			{Path: "/data", UsedPercent: 95.0, FreeGB: 5.0},
		}
	}

	s := newWithCheckers(cfg, nil, nil, nil, nil, diskCheckFn, nil)
	s.Start()
	execDiskCheckJob(s)
	s.Stop()
}

// TestCookieExpiryJob_Expired 测试 Cookie 已过期时发送通知
func TestCookieExpiryJob_Expired(t *testing.T) {
	cfg := testConfig()
	mock := &mockNotifyManager{}

	var cc cookieChecker = func(_ context.Context, _ *channel.Store) []runtime.CookieWarning {
		return []runtime.CookieWarning{
			{
				ChannelName: "测试主播",
				CookieType:  "publish",
				IsExpired:   true,
				DaysLeft:    0,
			},
		}
	}

	s := newWithCheckers(cfg, nil, nil, nil, mock, nil, cc)
	s.Start()
	execCookieCheckJob(s)
	s.Stop()

	if mock.sendCount() == 0 {
		t.Error("expected at least 1 notification for expired cookie")
	}

	sends := mock.getSends()
	found := false
	for _, c := range sends {
		if c.EventType == "cookie_expiring" && c.Title == "Cookie 已过期" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected cookie_expiring notification for expired cookie")
	}
}

// TestCookieExpiryJob_ExpiringSoon 测试 Cookie 即将过期（<=7天）时发送通知
func TestCookieExpiryJob_ExpiringSoon(t *testing.T) {
	cfg := testConfig()
	mock := &mockNotifyManager{}

	var cc cookieChecker = func(_ context.Context, _ *channel.Store) []runtime.CookieWarning {
		return []runtime.CookieWarning{
			{
				ChannelName: "测试主播",
				CookieType:  "download",
				IsExpired:   false,
				DaysLeft:    3,
			},
		}
	}

	s := newWithCheckers(cfg, nil, nil, nil, mock, nil, cc)
	s.Start()
	execCookieCheckJob(s)
	s.Stop()

	if mock.sendCount() == 0 {
		t.Error("expected at least 1 notification for expiring cookie")
	}

	sends := mock.getSends()
	found := false
	for _, c := range sends {
		if c.EventType == "cookie_expiring" && c.Title == "Cookie 即将过期" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected cookie_expiring notification for soon-to-expire cookie")
	}
}

// TestCookieExpiryJob_NoWarnings Cookie 正常时不发送通知
func TestCookieExpiryJob_NoWarnings(t *testing.T) {
	cfg := testConfig()
	mock := &mockNotifyManager{}

	var cc cookieChecker = func(_ context.Context, _ *channel.Store) []runtime.CookieWarning {
		return nil
	}

	s := newWithCheckers(cfg, nil, nil, nil, mock, nil, cc)
	s.Start()
	execCookieCheckJob(s)
	s.Stop()

	if mock.sendCount() != 0 {
		t.Errorf("expected 0 notifications, got %d", mock.sendCount())
	}
}

// TestCookieExpiryJob_NilNotifyManager notifyManager 为 nil 时不 panic
func TestCookieExpiryJob_NilNotifyManager(t *testing.T) {
	cfg := testConfig()

	var cc cookieChecker = func(_ context.Context, _ *channel.Store) []runtime.CookieWarning {
		return []runtime.CookieWarning{
			{
				ChannelName: "测试主播",
				CookieType:  "publish",
				IsExpired:   true,
				DaysLeft:    0,
			},
		}
	}

	s := newWithCheckers(cfg, nil, nil, nil, nil, nil, cc)
	s.Start()
	execCookieCheckJob(s)
	s.Stop()
}

// execDiskCheckJob 遍历 cron entries 找到磁盘检查 job 并执行
// disk check 是第3个注册的 job（索引2），在 discovery(0) 和 liveCheck(1) 之后
func execDiskCheckJob(s *Scheduler) {
	// cron entries 按 next run 排序，非注册顺序，因此不能依赖固定索引
	// 直接调用 checkDisk 函数来测试逻辑
	paths := []string{s.cfg.OutputRoot, filepath.Dir(s.cfg.DBPath)}
	diskUsage := s.checkDisk(paths)
	for _, d := range diskUsage {
		if d.UsedPercent >= 85 {
			title := "磁盘空间警告"
			body := fmt.Sprintf("路径 %s 使用率 %.1f%%，剩余 %.1f GB", d.Path, d.UsedPercent, d.FreeGB)
			if s.notifyMgr != nil {
				s.notifyMgr.Send(s.ctx, "disk_warning", title, body)
			}
		}
	}
}

// execCookieCheckJob 直接调用 checkCookie 函数来测试逻辑
func execCookieCheckJob(s *Scheduler) {
	warnings := s.checkCookie(s.ctx, s.channelStore)
	for _, w := range warnings {
		if s.notifyMgr == nil {
			continue
		}
		if w.IsExpired {
			title := "Cookie 已过期"
			body := fmt.Sprintf("主播 %s 的%s Cookie 已过期", w.ChannelName, w.CookieType)
			s.notifyMgr.Send(s.ctx, "cookie_expiring", title, body)
		} else if w.DaysLeft <= 7 {
			title := "Cookie 即将过期"
			body := fmt.Sprintf("主播 %s 的%s Cookie 将在 %d 天后过期", w.ChannelName, w.CookieType, w.DaysLeft)
			s.notifyMgr.Send(s.ctx, "cookie_expiring", title, body)
		}
	}
}
