package asr

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var publicIPEndpoints = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
	"https://checkip.amazonaws.com",
}

// DetectPublicIP tries external services to determine the host's public IP.
// Returns empty string if detection fails or the detected IP is not globally routable.
// Total timeout is capped at 10 seconds.
func DetectPublicIP(parent context.Context) string {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	for _, endpoint := range publicIPEndpoints {
		if ctx.Err() != nil {
			return ""
		}
		ip, err := fetchPublicIP(ctx, endpoint)
		if err != nil {
			continue
		}
		parsed := net.ParseIP(ip)
		if parsed == nil || !isPublicIP(parsed) {
			continue
		}
		return ip
	}
	return ""
}

func fetchPublicIP(ctx context.Context, endpoint string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func isPublicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
		return false
	}
	return true
}
