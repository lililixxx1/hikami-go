package live_record

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/biliutil"

	"github.com/andybalholm/brotli"
	"github.com/gorilla/websocket"
)

func TestUnpackMessagesReadsPlainPacket(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,0,0,16777215],"hello",[123,"user"]]}`)
	packet := packPacket(operationMessage, 1, body)

	messages, err := unpackMessages(packet)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(messages) != 1 || string(messages[0]) != string(body) {
		t.Fatalf("unexpected messages: %q", messages)
	}
}

func TestUnpackMessagesReadsZlibPacket(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,0,0,16777215],"hello",[123,"user"]]}`)
	nested := packPacket(operationMessage, 1, body)
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	_, _ = writer.Write(nested)
	_ = writer.Close()

	packet := packPacket(operationMessage, 2, compressed.Bytes())
	messages, err := unpackMessages(packet)
	if err != nil {
		t.Fatalf("unpack zlib: %v", err)
	}
	if len(messages) != 1 || string(messages[0]) != string(body) {
		t.Fatalf("unexpected messages: %q", messages)
	}
}

func TestUnpackMessagesReadsBrotliPacket(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,0,0,16777215],"hello",[123,"user"]]}`)
	nested := packPacket(operationMessage, 1, body)
	var compressed bytes.Buffer
	writer := brotli.NewWriter(&compressed)
	_, _ = writer.Write(nested)
	_ = writer.Close()

	packet := packPacket(operationMessage, 3, compressed.Bytes())
	messages, err := unpackMessages(packet)
	if err != nil {
		t.Fatalf("unpack brotli: %v", err)
	}
	if len(messages) != 1 || string(messages[0]) != string(body) {
		t.Fatalf("unexpected messages: %q", messages)
	}
}

func TestParseDanmakuMessage(t *testing.T) {
	raw := json.RawMessage(`{"cmd":"DANMU_MSG","info":[[0,0,0,16777215,1770000001500],"hello",[123,"user"]]}`)
	startedAt := time.UnixMilli(1770000000000)
	receivedAt := startedAt.Add(2 * time.Second)

	item, ok := parseDanmakuMessage(raw, startedAt, receivedAt)
	if !ok {
		t.Fatalf("message not parsed")
	}
	if item["text"] != "hello" || item["user_id"] != "123" || item["user_name"] != "user" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if item["time_ms"] != int64(1500) {
		t.Fatalf("time_ms = %v", item["time_ms"])
	}
	if item["original_time_ms"] != int64(1500) || item["corrected_time_ms"] != int64(1500) {
		t.Fatalf("unexpected corrected fields: %+v", item)
	}
	if item["received_at"] != receivedAt.Format(time.RFC3339) {
		t.Fatalf("received_at = %v", item["received_at"])
	}
	if item["color"] != "#ffffff" {
		t.Fatalf("color = %v", item["color"])
	}
}

// mockSigner 是一个用于测试的 URLSigner 实现。
type mockSigner struct {
	signErr error
	signed  string
}

func (m *mockSigner) SignURL(rawURL string) (string, error) {
	if m.signErr != nil {
		return "", m.signErr
	}
	if m.signed != "" {
		return m.signed, nil
	}
	return rawURL, nil
}

// newTestRecorder 创建一个用于测试的 BilibiliDanmakuRecorder，
// 使用 mock signer 替换真实的 WBI 签名器。
func newTestRecorder(server *httptest.Server) *BilibiliDanmakuRecorder {
	recorder := &BilibiliDanmakuRecorder{
		httpClient: server.Client(),
		dialer:     websocket.DefaultDialer,
		baseURL:    server.URL,
		signers:    make(map[string]*biliutil.WBISigner),
	}
	return recorder
}

func TestGetDanmuInfoSendsCookie(t *testing.T) {
	var receivedCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"message":"","data":{"token":"test_token","host_list":[{"host":"broadcastlv.chat.bilibili.com","wss_port":2245}]}}`))
	}))
	defer server.Close()

	recorder := newTestRecorder(server)
	// 注入一个使用 mock nav 的 signer，避免真实网络请求
	signer := newMockSignerWithURL(server.URL, "test_cookie")
	recorder.signers["SESSDATA=abc; bili_jct=def; DedeUserID=789"] = signer

	info, err := recorder.getDanmuInfo(context.Background(), 12345, "SESSDATA=abc; bili_jct=def; DedeUserID=789")
	if err != nil {
		t.Fatalf("getDanmuInfo: %v", err)
	}
	if receivedCookie != "SESSDATA=abc; bili_jct=def; DedeUserID=789" {
		t.Fatalf("cookie header = %q, want full cookie string", receivedCookie)
	}
	if info.Token != "test_token" {
		t.Fatalf("token = %q, want test_token", info.Token)
	}
}

func TestGetDanmuInfoEmptyCookie(t *testing.T) {
	var hasCookie bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasCookie = r.Header.Get("Cookie") != ""
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"message":"","data":{"token":"tok","host_list":[]}}`))
	}))
	defer server.Close()

	recorder := newTestRecorder(server)
	signer := newMockSignerWithURL(server.URL, "")
	recorder.signers[""] = signer

	_, err := recorder.getDanmuInfo(context.Background(), 12345, "")
	if err != nil {
		t.Fatalf("getDanmuInfo: %v", err)
	}
	if hasCookie {
		t.Fatalf("empty cookie should not set Cookie header")
	}
}

func TestGetDanmuInfoRetryOn352(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// 第一次返回 -352
			_, _ = w.Write([]byte(`{"code":-352,"message":"risk control"}`))
			return
		}
		// 第二次返回成功
		_, _ = w.Write([]byte(`{"code":0,"message":"","data":{"token":"retried_token","host_list":[{"host":"broadcastlv.chat.bilibili.com","wss_port":2245}]}}`))
	}))
	defer server.Close()

	recorder := newTestRecorder(server)
	signer := newMockSignerWithURL(server.URL, "test_cookie")
	recorder.signers["test_cookie"] = signer

	info, err := recorder.getDanmuInfo(context.Background(), 12345, "test_cookie")
	if err != nil {
		t.Fatalf("getDanmuInfo: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 API calls (initial + retry), got %d", callCount)
	}
	if info.Token != "retried_token" {
		t.Fatalf("token = %q, want retried_token", info.Token)
	}
}

func TestGetDanmuInfoFallbackOn352(t *testing.T) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-352,"message":"risk control"}`))
	}))
	defer server.Close()

	recorder := newTestRecorder(server)
	signer := newMockSignerWithURL(server.URL, "test_cookie")
	recorder.signers["test_cookie"] = signer

	info, err := recorder.getDanmuInfo(context.Background(), 12345, "test_cookie")
	if err != nil {
		t.Fatalf("getDanmuInfo should not error on fallback: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 API calls (initial + retry + getConf), got %d", callCount)
	}
	if info.Token != "" {
		t.Fatalf("token should be empty on fallback, got %q", info.Token)
	}
	if info.Address != "wss://broadcastlv.chat.bilibili.com:2245/sub" {
		t.Fatalf("address = %q, want default fallback address", info.Address)
	}
}

func TestGetDanmuInfoFallbackToGetConf(t *testing.T) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getConf") {
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"token":"conf-token-123","host":"broadcastlv.chat.bilibili.com","port":2243,"host_server_list":[{"host":"hw-sg.example.com","wss_port":443},{"host":"broadcastlv.chat.bilibili.com","wss_port":443}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":-352,"message":"risk control"}`))
	}))
	defer server.Close()

	recorder := newTestRecorder(server)
	signer := newMockSignerWithURL(server.URL, "test_cookie")
	recorder.signers["test_cookie"] = signer

	info, err := recorder.getDanmuInfo(context.Background(), 12345, "test_cookie")
	if err != nil {
		t.Fatalf("getDanmuInfo should not error: %v", err)
	}
	if info.Token != "conf-token-123" {
		t.Fatalf("token = %q, want conf-token-123", info.Token)
	}
	if len(info.Addresses) != 2 {
		t.Fatalf("addresses = %v, want 2", info.Addresses)
	}
	if info.Address != "wss://hw-sg.example.com:443/sub" {
		t.Fatalf("address = %q, want first host_server_list entry", info.Address)
	}
}

func TestBuildAuthBodyWithUID(t *testing.T) {
	body := buildAuthBody(12345, "test_key", 98765)
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal auth body: %v", err)
	}
	uid, _ := parsed["uid"].(float64)
	if int64(uid) != 98765 {
		t.Fatalf("uid = %v, want 98765", uid)
	}
	roomID, _ := parsed["roomid"].(float64)
	if int64(roomID) != 12345 {
		t.Fatalf("roomid = %v, want 12345", roomID)
	}
	key, _ := parsed["key"].(string)
	if key != "test_key" {
		t.Fatalf("key = %q, want test_key", key)
	}
	protover, _ := parsed["protover"].(float64)
	if int(protover) != 3 {
		t.Fatalf("protover = %v, want 3", protover)
	}
}

func TestBuildAuthBodyWithProtover(t *testing.T) {
	body := buildAuthBodyWithProtover(12345, "test_key", 98765, 2)
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal auth body: %v", err)
	}
	protover, _ := parsed["protover"].(float64)
	if int(protover) != 2 {
		t.Fatalf("protover = %v, want 2", protover)
	}
}

// newMockSignerWithURL 创建一个使用指定 URL 作为 nav 服务的 WBISigner。
// 这样 signer 会成功初始化 mixinKey，而 danmu API 请求仍然发往测试服务器。
func newMockSignerWithURL(navURL string, cookie string) *biliutil.WBISigner {
	signer := biliutil.NewWBISigner(cookie)
	// 直接设置 mixinKey 和 updatedAt，避免 nav 请求
	// 这样 signer.SignURL 会成功返回原始 URL
	signer.SetMixinKeyForTest("testmixinkey1234567890abcdef")
	return signer
}
