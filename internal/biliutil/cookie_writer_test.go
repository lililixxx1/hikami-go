package biliutil

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormatNetscapeCookies(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	expires := now.Add(24 * time.Hour)
	content, expiresAt, names, err := FormatNetscapeCookies([]*http.Cookie{
		{Name: "SESSDATA", Value: "sess", Domain: ".bilibili.com", Path: "/", Secure: true, HttpOnly: true, Expires: expires},
		{Name: "bili_jct", Value: "csrf", Domain: ".bilibili.com", Path: "/", Secure: true},
		{Name: "DedeUserID", Value: "42", Domain: ".bilibili.com", Path: "/", Secure: true},
	}, now)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	body := string(content)
	if !strings.Contains(body, "#HttpOnly_.bilibili.com\tTRUE\t/\tTRUE\t") {
		t.Fatalf("body missing httponly SESSDATA row: %s", body)
	}
	for _, want := range []string{"SESSDATA\tsess", "bili_jct\tcsrf", "DedeUserID\t42"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	if !expiresAt.Equal(expires) {
		t.Fatalf("expiresAt = %v, want %v", expiresAt, expires)
	}
	if strings.Join(names, ",") != "DedeUserID,SESSDATA,bili_jct" {
		t.Fatalf("names = %v", names)
	}
}

func TestFormatNetscapeCookiesRequiresCoreCookies(t *testing.T) {
	_, _, _, err := FormatNetscapeCookies([]*http.Cookie{
		{Name: "SESSDATA", Value: "sess"},
	}, time.Now())
	if !errors.Is(err, ErrCookieMissing) {
		t.Fatalf("err = %v, want ErrCookieMissing", err)
	}
}

func TestWriteNetscapeCookieFile(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	// 所有字段显式设置远期 Expires，避免 LoadCookie 用真实 time.Now() 判定过期
	// （曾因 bili_jct/DedeUserID 走 30 天默认过期 + 硬编码 now=2026-05-12 导致 6/11 后定时失败）。
	farFuture := now.Add(365 * 24 * time.Hour)
	result, err := WriteNetscapeCookieFile([]*http.Cookie{
		{Name: "SESSDATA", Value: "sess", Expires: farFuture},
		{Name: "bili_jct", Value: "csrf", Expires: farFuture},
		{Name: "DedeUserID", Value: "42", Expires: farFuture},
	}, CookieWriteOptions{
		Dir:   dir,
		UID:   42,
		Usage: "download",
		Now:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if result.Path != filepath.Join(dir, "bili_42_download.txt") {
		t.Fatalf("path = %s", result.Path)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if _, err := LoadCookie(result.Path); err != nil {
		t.Fatalf("load written cookie: %v", err)
	}
}
