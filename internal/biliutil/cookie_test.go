package biliutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCookiePreservesFullHeaderAndHttpOnlyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	expiry := time.Now().Add(time.Hour).Unix()
	body := fmt.Sprintf(`# Netscape HTTP Cookie File
#HttpOnly_.bilibili.com	TRUE	/	TRUE	%d	SESSDATA	sess
.bilibili.com	TRUE	/	TRUE	%d	bili_jct	csrf
.bilibili.com	TRUE	/	TRUE	%d	DedeUserID	42
.bilibili.com	TRUE	/	TRUE	%d	buvid3	buvid
`, expiry, expiry, expiry, expiry)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	cookie, err := LoadCookie(path)
	if err != nil {
		t.Fatalf("load cookie: %v", err)
	}
	header := cookie.CookieHeader()
	for _, want := range []string{"SESSDATA=sess", "bili_jct=csrf", "DedeUserID=42", "buvid3=buvid"} {
		if !strings.Contains(header, want) {
			t.Fatalf("Cookie header %q missing %q", header, want)
		}
	}
}
