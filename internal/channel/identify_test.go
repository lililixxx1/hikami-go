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

	"hikami-go/internal/biliutil"
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
	withTestBuvidStore(identifier, server)

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
	withTestBuvidStore(identifier, server)

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
		if r.URL.Path == "/x/frontend/finger/spi" {
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"newbuvid3","b_4":"newbuvid4"}}`))
			return
		}
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
	withTestBuvidStore(identifier, server)
	identifier.cookieFile = downloadCookieFileProvider(store, nil)
	result, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 999})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.DownloadCookieFile != cookieFile {
		t.Fatalf("DownloadCookieFile = %q, want %q", result.Channel.DownloadCookieFile, cookieFile)
	}
	for _, want := range []string{"SESSDATA=sess", "bili_jct=csrf", "DedeUserID=42", "buvid3=newbuvid3", "buvid4=newbuvid4"} {
		if !strings.Contains(cookieHeader, want) {
			t.Fatalf("Cookie header %q missing %q", cookieHeader, want)
		}
	}
	// InjectBuvids 的 replace 语义：cookie 文件里的旧 buvid3=buvid 必须被新值覆盖，不能重复出现
	if strings.Contains(cookieHeader, "buvid3=buvid") {
		t.Fatalf("Cookie header %q should not retain old buvid3=buvid (replace semantics)", cookieHeader)
	}
	if got := strings.Count(cookieHeader, "buvid3="); got != 1 {
		t.Fatalf("Cookie header %q has %d buvid3= occurrences, want 1 (no duplicates)", cookieHeader, got)
	}
}

func TestIdentifyFallsBackToBootstrapDownloadCookie(t *testing.T) {
	var cookieHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/frontend/finger/spi" {
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"newbuvid3","b_4":"newbuvid4"}}`))
			return
		}
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
	withTestBuvidStore(identifier, server)
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

// TestIdentifyContinuesWhenBuvidFetchFails 验证容错：当 finger/spi 拉取失败（HTTP 500）时，
// identify 不应中断，仍能完成主播识别（仅不带 buvid，可能被风控，但容错策略与 publisher/danmaku 一致）。
func TestIdentifyContinuesWhenBuvidFetchFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			w.WriteHeader(http.StatusInternalServerError) // 模拟指纹接口故障
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
	defer server.Close()

	identifier := NewIdentifierWithBaseURL(server.URL)
	withTestBuvidStore(identifier, server)

	result, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 123})
	if err != nil {
		t.Fatalf("identify should succeed even when buvid fetch fails, got: %v", err)
	}
	if result.Channel.UID != 456 {
		t.Fatalf("UID = %d, want 456", result.Channel.UID)
	}
}

// TestIdentifySignsGetInfoByRoomWithWBI 验证 identify 真的对 getInfoByRoom 端点做 WBI 签名：
// 用 recordingSigner 记录入参 URL，断言签名器被调用且端点路径正确。
func TestIdentifySignsGetInfoByRoomWithWBI(t *testing.T) {
	server := newIdentifyServer(t)
	identifier := NewIdentifierWithBaseURL(server.URL)
	withTestBuvidStore(identifier, server)
	signer := &recordingSigner{}
	identifier.SetSignerFactory(func(cookie string) biliutil.URLSigner { return signer })

	if _, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 123}); err != nil {
		t.Fatalf("identify: %v", err)
	}

	if len(signer.calls) == 0 {
		t.Fatal("WBI signer was not invoked; expected SignURL call for getInfoByRoom")
	}
	var found bool
	for _, u := range signer.calls {
		if strings.Contains(u, "/xlive/web-room/v1/index/getInfoByRoom") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("signer calls did not include getInfoByRoom endpoint: %v", signer.calls)
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
		case "/x/frontend/finger/spi":
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"newbuvid3","b_4":"newbuvid4"}}`))
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

// withTestBuvidStore 把 identifier 的 buvid store 指向测试 server 的 spi 路径，
// 避免测试走真实 api.bilibili.com。同时注入 passthrough 签名器，避免 WBI SignURL 打真实 nav。
func withTestBuvidStore(i *Identifier, server *httptest.Server) {
	i.SetBuvidStore(biliutil.NewBuvidStoreWithOptions(nil, server.URL+"/x/frontend/finger/spi"))
	i.SetSignerFactory(func(cookie string) biliutil.URLSigner { return passthroughSigner{} })
}

// passthroughSigner 返回原 URL 不签名（测试用，让 httptest 桩按 Path 路由）。
type passthroughSigner struct{}

func (passthroughSigner) SignURL(rawURL string) (string, error) { return rawURL, nil }

// recordingSigner 记录收到的 URL 并追加签名参数，用于断言 identify 真的对端点做了 WBI 签名。
type recordingSigner struct {
	calls []string
}

func (r *recordingSigner) SignURL(rawURL string) (string, error) {
	r.calls = append(r.calls, rawURL)
	return rawURL + "&w_rid=fake&wts=1", nil
}
