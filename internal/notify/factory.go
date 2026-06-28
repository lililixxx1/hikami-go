package notify

import "strings"

// NewNotifierFromConfig 根据配置创建对应的 Notifier
func NewNotifierFromConfig(notifyType string, webhookURL, barkURL, barkKey, serverChanKey string) Notifier {
	switch strings.ToLower(notifyType) {
	case "webhook":
		if webhookURL == "" {
			return nil
		}
		return NewWebhookNotifier(webhookURL)
	case "bark":
		if barkURL == "" || barkKey == "" {
			return nil
		}
		return NewBarkNotifier(barkURL, barkKey)
	case "serverchan":
		if serverChanKey == "" {
			return nil
		}
		return NewServerChanNotifier(serverChanKey)
	default:
		return nil
	}
}
