package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookNotifier 通过 Webhook 发送通知
type WebhookNotifier struct {
	URL        string
	HTTPClient *http.Client
}

// NewWebhookNotifier 创建 WebhookNotifier
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		URL: url,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Send 发送 Webhook 通知
func (w *WebhookNotifier) Send(ctx context.Context, title, body string) error {
	payload := map[string]string{
		"title": title,
		"body":  body,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook response status: %d", resp.StatusCode)
	}
	return nil
}
