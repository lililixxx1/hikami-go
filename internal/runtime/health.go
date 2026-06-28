package runtime

import (
	"context"
	"log/slog"
	"os"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
)

// CheckCookieExpiry checks cookie expiry for all channels
func CheckCookieExpiry(ctx context.Context, channelStore *channel.Store) []CookieWarning {
	channels, err := channelStore.List(ctx)
	if err != nil {
		slog.Error("cookie check: list channels failed", "error", err)
		return nil
	}

	var warnings []CookieWarning
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}

		if ch.CookieFile != "" {
			w := checkCookieFile(ch.ID, ch.Name, "publish", ch.CookieFile)
			if w != nil {
				warnings = append(warnings, *w)
			}
		}

		if ch.DownloadCookieFile != "" {
			w := checkCookieFile(ch.ID, ch.Name, "download", ch.DownloadCookieFile)
			if w != nil {
				warnings = append(warnings, *w)
			}
		}
	}
	return warnings
}

func checkCookieFile(channelID, channelName, cookieType, cookiePath string) *CookieWarning {
	if _, err := os.Stat(cookiePath); os.IsNotExist(err) {
		return nil
	}

	expired, daysLeft, expiresAt := biliutil.CheckCookieExpiry(cookiePath)
	if expired || daysLeft <= 7 {
		return &CookieWarning{
			ChannelID:   channelID,
			ChannelName: channelName,
			CookieType:  cookieType,
			ExpiresAt:   expiresAt,
			DaysLeft:    daysLeft,
			IsExpired:   expired,
		}
	}
	return nil
}
