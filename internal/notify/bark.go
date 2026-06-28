package notify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// BarkNotifier 通过 Bark App 发送通知
type BarkNotifier struct {
	ServerURL  string
	DeviceKey  string
	HTTPClient *http.Client
}

// NewBarkNotifier 创建 BarkNotifier
func NewBarkNotifier(serverURL, deviceKey string) *BarkNotifier {
	return &BarkNotifier{
		ServerURL: serverURL,
		DeviceKey: deviceKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Send 发送 Bark 通知
func (b *BarkNotifier) Send(ctx context.Context, title, body string) error {
	encodedTitle := url.PathEscape(title)
	encodedBody := url.PathEscape(body)
	reqURL := fmt.Sprintf("%s/%s/%s/%s", b.ServerURL, b.DeviceKey, encodedTitle, encodedBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("create bark request: %w", err)
	}

	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send bark request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("bark response status: %d", resp.StatusCode)
	}
	return nil
}
