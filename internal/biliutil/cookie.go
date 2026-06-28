package biliutil

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	ErrCookieMissing = errors.New("bilibili cookie missing required fields (SESSDATA, bili_jct, DedeUserID)")
	ErrCookieExpired = errors.New("bilibili cookie expired")
)

type BiliCookie struct {
	SESSDATA   string
	BiliJct    string
	DedeUserID string
	values     []cookiePair
}

type cookiePair struct {
	Name  string
	Value string
}

func LoadCookie(cookiePath string) (*BiliCookie, error) {
	raw, err := os.ReadFile(cookiePath)
	if err != nil {
		return nil, fmt.Errorf("open cookie file: %w", err)
	}

	// 自动检测并解密
	plain, err := decryptCookieFile(raw)
	if err != nil {
		return nil, fmt.Errorf("decrypt cookie file: %w", err)
	}

	cookie := &BiliCookie{}
	scanner := bufio.NewScanner(strings.NewReader(string(plain)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		name := fields[5]
		value := fields[6]
		expiryStr := fields[4]

		if name == "" {
			continue
		}
		expired := false
		if exp, err := strconv.ParseInt(expiryStr, 10, 64); err == nil && exp > 0 && exp < time.Now().Unix() {
			expired = true
		}
		if expired {
			if name == "SESSDATA" {
				return nil, ErrCookieExpired
			}
			continue
		}

		cookie.set(name, value)
		switch name {
		case "SESSDATA":
			cookie.SESSDATA = value
		case "bili_jct":
			cookie.BiliJct = value
		case "DedeUserID":
			cookie.DedeUserID = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if cookie.SESSDATA == "" || cookie.BiliJct == "" || cookie.DedeUserID == "" {
		return nil, ErrCookieMissing
	}
	return cookie, nil
}

func (c *BiliCookie) CookieHeader() string {
	if len(c.values) > 0 {
		parts := make([]string, 0, len(c.values))
		for _, pair := range c.values {
			parts = append(parts, fmt.Sprintf("%s=%s", pair.Name, pair.Value))
		}
		return strings.Join(parts, "; ")
	}
	return fmt.Sprintf("SESSDATA=%s; bili_jct=%s; DedeUserID=%s", c.SESSDATA, c.BiliJct, c.DedeUserID)
}

// NetscapeBytes 将 cookie 序列化为 yt-dlp 可读的明文 Netscape 格式字节。
// 仅写出已知的核心三字段（SESSDATA/bili_jct/DedeUserID），域固定为 .bilibili.com，
// 过期时间取 30 天后（yt-dlp 仅需有效凭证，不依赖精确过期）。
// 供下载场景把 ResolveCookie 返回的内存 cookie 落盘成临时文件供 yt-dlp --cookies 使用。
func (c *BiliCookie) NetscapeBytes() []byte {
	expiry := time.Now().Add(30 * 24 * time.Hour).Unix()
	lines := []struct{ name, value string }{
		{"SESSDATA", c.SESSDATA},
		{"bili_jct", c.BiliJct},
		{"DedeUserID", c.DedeUserID},
	}
	var b strings.Builder
	b.WriteString("# Netscape HTTP Cookie File\n")
	for _, ln := range lines {
		fmt.Fprintf(&b, ".bilibili.com\tTRUE\t/\tTRUE\t%d\t%s\t%s\n", expiry, ln.name, ln.value)
	}
	return []byte(b.String())
}

func (c *BiliCookie) set(name, value string) {
	for idx := range c.values {
		if c.values[idx].Name == name {
			c.values[idx].Value = value
			return
		}
	}
	c.values = append(c.values, cookiePair{Name: name, Value: value})
}

// CheckCookieExpiry 检查 Cookie 文件中 SESSDATA 的过期情况
// 返回 (isExpired, daysLeft, expiresAtStr)
func CheckCookieExpiry(cookiePath string) (isExpired bool, daysLeft int, expiresAt string) {
	raw, err := os.ReadFile(cookiePath)
	if err != nil {
		return false, 999, ""
	}

	plain, err := decryptCookieFile(raw)
	if err != nil {
		return false, 999, ""
	}

	scanner := bufio.NewScanner(strings.NewReader(string(plain)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		name := fields[5]
		if name != "SESSDATA" {
			continue
		}
		expiryStr := fields[4]
		exp, err := strconv.ParseInt(expiryStr, 10, 64)
		if err != nil || exp <= 0 {
			return false, 999, ""
		}
		expiresAt = time.Unix(exp, 0).Format(time.RFC3339)
		daysLeft = int(time.Until(time.Unix(exp, 0)).Hours() / 24)
		isExpired = time.Now().After(time.Unix(exp, 0))
		return
	}
	return false, 999, ""
}
