package channel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/config"
)

func TestNormalizeIdentifyInput(t *testing.T) {
	tests := []struct {
		name   string
		input  IdentifyInput
		source string
		uid    int64
		roomID int64
	}{
		{name: "live url", input: IdentifyInput{Input: "https://live.bilibili.com/123"}, source: "live_url", roomID: 123},
		{name: "space url", input: IdentifyInput{Input: "https://space.bilibili.com/456/video"}, source: "space_url", uid: 456},
		{name: "numeric uid", input: IdentifyInput{Input: "789"}, source: "uid", uid: 789},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source, err := normalizeIdentifyInput(tt.input)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if source != tt.source || got.UID != tt.uid || got.LiveRoomID != tt.roomID {
				t.Fatalf("got=%+v source=%s", got, source)
			}
		})
	}
}

func TestIdentifyByLiveRoom(t *testing.T) {
	server := newIdentifyServer(t)
	identifier := NewIdentifierWithBaseURL(server.URL)

	result, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 123})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.ID != "bili_456" || result.Channel.Name != "主播名" || result.Channel.LiveRoomID != 123 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Channel.SpaceURL != "https://space.bilibili.com/456" {
		t.Fatalf("space url = %s", result.Channel.SpaceURL)
	}
}

func TestIdentifyByUIDLooksUpLiveRoom(t *testing.T) {
	server := newIdentifyServer(t)
	identifier := NewIdentifierWithBaseURL(server.URL)

	result, err := identifier.Identify(context.Background(), IdentifyInput{UID: 456})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.UID != 456 || result.Channel.LiveRoomID != 123 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestIdentifyUsesConfiguredDownloadCookie(t *testing.T) {
	var cookieHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieHeader = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"room_info":{"uid":456,"room_id":999,"title":"直播标题"},
				"anchor_info":{"base_info":{"uid":456,"uname":"主播名"}}
			}
		}`))
	}))
	defer server.Close()

	store := NewStore(setupDB(t))
	cookieFile := writeIdentifyCookieFile(t)
	if _, err := store.Create(context.Background(), UpsertInput{
		ID:                 "configured",
		Name:               "已配置主播",
		UID:                1,
		LiveRoomID:         123,
		ReplaySourceURL:    "https://space.bilibili.com/1/video",
		SpaceURL:           "https://space.bilibili.com/1",
		TitlePrefix:        "【直播回放】",
		DownloadCookieFile: cookieFile,
		Enabled:            true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	identifier := NewIdentifierWithBaseURL(server.URL)
	identifier.cookieFile = downloadCookieFileProvider(store, nil)
	result, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 999})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.DownloadCookieFile != cookieFile {
		t.Fatalf("DownloadCookieFile = %q, want %q", result.Channel.DownloadCookieFile, cookieFile)
	}
	for _, want := range []string{"SESSDATA=sess", "bili_jct=csrf", "DedeUserID=42", "buvid3=buvid"} {
		if !strings.Contains(cookieHeader, want) {
			t.Fatalf("Cookie header %q missing %q", cookieHeader, want)
		}
	}
}

func TestIdentifyFallsBackToBootstrapDownloadCookie(t *testing.T) {
	var cookieHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieHeader = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"room_info":{"uid":456,"room_id":999,"title":"直播标题"},
				"anchor_info":{"base_info":{"uid":456,"uname":"主播名"}}
			}
		}`))
	}))
	defer server.Close()

	identifier := NewIdentifierWithBaseURL(server.URL)
	cookieFile := writeIdentifyCookieFile(t)
	identifier.cookieFile = downloadCookieFileProvider(NewStore(setupDB(t)), []config.BootstrapChannel{
		{ID: "configured", UID: 1, LiveRoomID: 123, DownloadCookieFile: cookieFile},
	})
	result, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 999})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.DownloadCookieFile != cookieFile {
		t.Fatalf("DownloadCookieFile = %q, want %q", result.Channel.DownloadCookieFile, cookieFile)
	}
	if !strings.Contains(cookieHeader, "SESSDATA=sess") {
		t.Fatalf("Cookie header %q missing SESSDATA", cookieHeader)
	}
}

func writeIdentifyCookieFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bili-cookies.txt")
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
	return path
}

func newIdentifyServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/room/v1/Room/getRoomInfoOld":
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"roomid":123}}`))
		case "/xlive/web-room/v1/index/getInfoByRoom":
			_, _ = w.Write([]byte(`{
				"code":0,
				"message":"0",
				"data":{
					"room_info":{"uid":456,"room_id":123,"title":"直播标题"},
					"anchor_info":{"base_info":{"uid":456,"uname":"主播名"}}
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
}
