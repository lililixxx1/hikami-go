package notify

import (
	"context"
	"log/slog"
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

// Send 发送通知（异步，不阻塞调用者）
func (m *Manager) Send(ctx context.Context, eventType, title, body string) {
	if !m.ShouldSend(eventType) {
		return
	}
	go func() {
		if err := m.notifier.Send(ctx, title, body); err != nil {
			slog.Error("notify send failed", "event", eventType, "error", err)
		}
	}()
}

// NoopManager 空通知管理器（通知未启用时使用）
var NoopManager = &Manager{}
