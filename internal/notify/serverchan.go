package notify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ServerChanNotifier 通过 Server酱 发送通知
type ServerChanNotifier struct {
	SendKey    string
	HTTPClient *http.Client
}

// NewServerChanNotifier 创建 ServerChanNotifier
func NewServerChanNotifier(sendKey string) *ServerChanNotifier {
	return &ServerChanNotifier{
		SendKey: sendKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Send 发送 Server酱 通知
func (s *ServerChanNotifier) Send(ctx context.Context, title, body string) error {
	reqURL := fmt.Sprintf("https://sctapi.ftqq.com/%s.send", s.SendKey)

	formData := url.Values{}
	formData.Set("title", title)
	formData.Set("desp", body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("create serverchan request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send serverchan request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("serverchan response status: %d", resp.StatusCode)
	}
	return nil
}
