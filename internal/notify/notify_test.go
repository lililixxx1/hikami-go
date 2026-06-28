package notify

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type mockNotifier struct {
	sendFn func(ctx context.Context, title, body string) error
}

func (m *mockNotifier) Send(ctx context.Context, title, body string) error {
	return m.sendFn(ctx, title, body)
}

// ---------------------------------------------------------------------------
// TestNewManager
// ---------------------------------------------------------------------------

func TestNewManager(t *testing.T) {
	tests := []struct {
		name       string
		notifier   Notifier
		events     []string
		wantNil    bool
		wantEvents int
	}{
		{
			name:       "with events",
			notifier:   &mockNotifier{},
			events:     []string{EventRecordStart, EventTaskFailed},
			wantNil:    false,
			wantEvents: 2,
		},
		{
			name:       "empty events",
			notifier:   &mockNotifier{},
			events:     nil,
			wantNil:    false,
			wantEvents: 0,
		},
		{
			name:       "nil notifier still creates manager",
			notifier:   nil,
			events:     []string{EventRecordStart},
			wantNil:    false,
			wantEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager(tt.notifier, tt.events)
			if (m == nil) != tt.wantNil {
				t.Fatalf("NewManager() nil = %v, want nil = %v", m == nil, tt.wantNil)
			}
			if len(m.events) != tt.wantEvents {
				t.Fatalf("expected %d events, got %d", tt.wantEvents, len(m.events))
			}
			if m.notifier != tt.notifier {
				t.Fatal("notifier not stored correctly")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestShouldSend
// ---------------------------------------------------------------------------

func TestShouldSend(t *testing.T) {
	m := NewManager(&mockNotifier{}, []string{EventRecordStart, EventTaskFailed})

	tests := []struct {
		name  string
		event string
		want  bool
	}{
		{"enabled event record_start", EventRecordStart, true},
		{"enabled event task_failed", EventTaskFailed, true},
		{"disabled event record_stop", EventRecordStop, false},
		{"disabled event recap_done", EventRecapDone, false},
		{"unknown event", "unknown_event", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.ShouldSend(tt.event); got != tt.want {
				t.Errorf("ShouldSend(%q) = %v, want %v", tt.event, got, tt.want)
			}
		})
	}
}

func TestShouldSendWithEmptyEvents(t *testing.T) {
	m := NewManager(&mockNotifier{}, nil)
	if m.ShouldSend(EventRecordStart) {
		t.Error("expected false for empty event list")
	}
}

// ---------------------------------------------------------------------------
// TestShouldSendNilManager
// ---------------------------------------------------------------------------

func TestShouldSendNilManager(t *testing.T) {
	var m *Manager
	if m.ShouldSend(EventRecordStart) {
		t.Error("expected false for nil manager")
	}
	if m.ShouldSend("") {
		t.Error("expected false for nil manager with empty event")
	}
}

func TestShouldSendNilNotifier(t *testing.T) {
	m := &Manager{notifier: nil, events: map[string]bool{EventRecordStart: true}}
	if m.ShouldSend(EventRecordStart) {
		t.Error("expected false when notifier is nil")
	}
}

// ---------------------------------------------------------------------------
// TestSendAsync
// ---------------------------------------------------------------------------

func TestSendAsync(t *testing.T) {
	var called atomic.Int32

	slowNotifier := &mockNotifier{
		sendFn: func(ctx context.Context, title, body string) error {
			time.Sleep(500 * time.Millisecond)
			called.Add(1)
			return nil
		},
	}

	m := NewManager(slowNotifier, []string{EventRecordStart})

	start := time.Now()
	m.Send(context.Background(), EventRecordStart, "title", "body")
	elapsed := time.Since(start)

	if elapsed >= 400*time.Millisecond {
		t.Fatalf("Send should be async, but took %v", elapsed)
	}

	// Wait for the goroutine to finish.
	time.Sleep(700 * time.Millisecond)
	if called.Load() != 1 {
		t.Fatalf("expected notifier to be called once, got %d", called.Load())
	}
}

func TestSendSkipped(t *testing.T) {
	var called atomic.Int32

	notifier := &mockNotifier{
		sendFn: func(ctx context.Context, title, body string) error {
			called.Add(1)
			return nil
		},
	}

	m := NewManager(notifier, []string{EventRecordStart})
	m.Send(context.Background(), EventRecordStop, "title", "body")

	time.Sleep(100 * time.Millisecond)
	if called.Load() != 0 {
		t.Error("disabled event should not trigger send")
	}
}

// ---------------------------------------------------------------------------
// TestNoopManager
// ---------------------------------------------------------------------------

func TestNoopManager(t *testing.T) {
	// NoopManager should not panic on any call.
	if NoopManager.ShouldSend(EventRecordStart) {
		t.Error("NoopManager.ShouldSend should return false")
	}

	// Send on NoopManager should not panic.
	NoopManager.Send(context.Background(), EventRecordStart, "title", "body")
}

// ---------------------------------------------------------------------------
// TestNewNotifierFromConfig
// ---------------------------------------------------------------------------

func TestNewNotifierFromConfig(t *testing.T) {
	tests := []struct {
		name          string
		notifyType    string
		webhookURL    string
		barkURL       string
		barkKey       string
		serverChanKey string
		wantNil       bool
		wantType      string
	}{
		{
			name:       "webhook",
			notifyType: "webhook",
			webhookURL: "https://example.com/hook",
			wantNil:    false,
			wantType:   "*notify.WebhookNotifier",
		},
		{
			name:       "webhook missing url",
			notifyType: "webhook",
			webhookURL: "",
			wantNil:    true,
		},
		{
			name:       "bark",
			notifyType: "bark",
			barkURL:    "https://api.day.app",
			barkKey:    "mykey",
			wantNil:    false,
			wantType:   "*notify.BarkNotifier",
		},
		{
			name:       "bark missing key",
			notifyType: "bark",
			barkURL:    "https://api.day.app",
			barkKey:    "",
			wantNil:    true,
		},
		{
			name:       "bark missing url",
			notifyType: "bark",
			barkURL:    "",
			barkKey:    "mykey",
			wantNil:    true,
		},
		{
			name:          "serverchan",
			notifyType:    "serverchan",
			serverChanKey: "sct123",
			wantNil:       false,
			wantType:      "*notify.ServerChanNotifier",
		},
		{
			name:          "serverchan missing key",
			notifyType:    "serverchan",
			serverChanKey: "",
			wantNil:       true,
		},
		{
			name:       "unknown type",
			notifyType: "telegram",
			wantNil:    true,
		},
		{
			name:       "empty type",
			notifyType: "",
			wantNil:    true,
		},
		{
			name:       "case insensitive webhook",
			notifyType: "WebHook",
			webhookURL: "https://example.com/hook",
			wantNil:    false,
			wantType:   "*notify.WebhookNotifier",
		},
		{
			name:          "case insensitive serverchan",
			notifyType:    "ServerChan",
			serverChanKey: "sct123",
			wantNil:       false,
			wantType:      "*notify.ServerChanNotifier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewNotifierFromConfig(tt.notifyType, tt.webhookURL, tt.barkURL, tt.barkKey, tt.serverChanKey)
			if (n == nil) != tt.wantNil {
				t.Fatalf("NewNotifierFromConfig() nil = %v, want nil = %v", n == nil, tt.wantNil)
			}
			if n != nil {
				gotType := typeof(n)
				if gotType != tt.wantType {
					t.Errorf("got type %q, want %q", gotType, tt.wantType)
				}
			}
		})
	}
}

func typeof(v any) string {
	switch v.(type) {
	case *WebhookNotifier:
		return "*notify.WebhookNotifier"
	case *BarkNotifier:
		return "*notify.BarkNotifier"
	case *ServerChanNotifier:
		return "*notify.ServerChanNotifier"
	default:
		return "unknown"
	}
}
