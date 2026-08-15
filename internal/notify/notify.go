package notify

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	EventRecordStart = "record_start"
	EventRecordStop  = "record_stop"
	EventTaskFailed  = "task_failed"
	EventRecapDone   = "recap_done"
	EventPublishDone = "publish_done"
)

// Notifier 通知发送接口
type Notifier interface {
	Send(ctx context.Context, title, body string) error
}

// Manager 通知管理器
type Manager struct {
	notifier Notifier
	events   map[string]bool
}

// NewManager 创建通知管理器
func NewManager(notifier Notifier, events []string) *Manager {
	eventMap := make(map[string]bool, len(events))
	for _, e := range events {
		eventMap[e] = true
	}
	return &Manager{notifier: notifier, events: eventMap}
}

// ShouldSend 检查事件是否需要通知
func (m *Manager) ShouldSend(eventType string) bool {
	if m == nil || m.notifier == nil {
		return false
	}
	return m.events[eventType]
}

// sendTimeout 是 Send/SendTest 派生 ctx 的超时上限,包级变量便于测试缩短,生产恒 15s。
var sendTimeout = 15 * time.Second

// Send 发送通知（异步，不阻塞调用者）
func (m *Manager) Send(ctx context.Context, eventType, title, body string) {
	if !m.ShouldSend(eventType) {
		return
	}
	// 调用方 ctx(worker 任务 ctx / HTTP 请求 ctx)在 Send 返回后即被取消,
	// goroutine 直接捕获会导致通知必然 context canceled(2026-08-15 全项目审核 H7)。
	// WithoutCancel 保留 ctx 的 values 仅剥离取消链;超时上限防 goroutine 泄漏。
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	go func() {
		defer cancel()
		if err := m.notifier.Send(sendCtx, title, body); err != nil {
			slog.Error("notify send failed", "event", eventType, "error", err)
		}
	}()
}

// Configured 返回通知渠道是否已配置(区别于 NoopManager)。
// 存在性判断用它而非 ShouldSend(某具体事件)——用户把该事件从 events 排除是合法配置,
// 用事件判断会把「已配置但关了该事件」误报成「未配置」。
func (m *Manager) Configured() bool {
	return m != nil && m.notifier != nil
}

// SendTest 发送测试通知。绕过 events 白名单直发 notifier("test" 不在事件枚举里,
// 走 Send 会被 ShouldSend 过滤掉),同步返回错误让测试端点把真实发送结果反馈给用户。
// 同样脱离调用方取消链(HTTP handler 返回后请求 ctx 即取消)。
func (m *Manager) SendTest(ctx context.Context, title, body string) error {
	if !m.Configured() {
		return errors.New("notify not configured")
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()
	return m.notifier.Send(sendCtx, title, body)
}

// NoopManager 空通知管理器（通知未启用时使用）
var NoopManager = &Manager{}
