package live_record

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"hikami-go/internal/biliutil"
)

func TestBilibiliClientParsesLiveAndStreamInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xlive/web-room/v1/index/getInfoByRoom":
			_, _ = w.Write([]byte(`{
				"code":0,
				"message":"0",
				"data":{
					"room_info":{
						"room_id":123,
						"live_status":1,
						"title":"直播标题",
						"live_start_time":1770000000
					},
					"anchor_info":{"base_info":{"uname":"主播"}}
				}
			}`))
		case "/xlive/web-room/v2/index/getRoomPlayInfo":
			_, _ = w.Write([]byte(`{
				"code":0,
				"message":"0",
				"data":{
					"playurl_info":{
						"playurl":{
							"stream":[{
								"format":[{
									"codec":[{
										"codec_name":"hevc",
										"base_url":"/live.flv",
										"url_info":[{"host":"https://live.example.com","extra":"?token=1"}]
									}]
								}]
							}]
						}
					}
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	// 这些测试聚焦流选择逻辑,关闭 buvid 注入 + 桩签名器,避免打真实 B 站 WBI/spi。
	client.SetBuvidStore(nil)
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return fakeSigner{} })
	info, err := client.CheckLive(context.Background(), 123, "")
	if err != nil {
		t.Fatalf("check live: %v", err)
	}
	if !info.Live || info.RoomID != 123 || info.Title != "直播标题" {
		t.Fatalf("unexpected live info: %+v", info)
	}

	// 非 audioOnly 模式：应返回混合流
	stream, err := client.GetStream(context.Background(), 123, false, "")
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	if stream.URL != "https://live.example.com/live.flv?token=1" {
		t.Fatalf("stream url = %s", stream.URL)
	}
	if stream.AudioOnly {
		t.Fatalf("expected AudioOnly=false for non-audioOnly request")
	}
	if stream.Headers["Referer"] != "https://live.bilibili.com/" {
		t.Fatalf("missing bilibili referer header: %+v", stream.Headers)
	}

	// audioOnly 模式：hevc 不是音频 codec，应返回错误
	_, err = client.GetStream(context.Background(), 123, true, "")
	if err == nil {
		t.Fatalf("expected error when audioOnly=true with no audio codec, got nil")
	}
}

func TestBilibiliClientPrefersFLVMixedStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "SESSDATA=test" {
			t.Fatalf("cookie header = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"playurl_info":{
					"playurl":{
						"stream":[{
							"format":[{
								"format_name":"ts",
								"codec":[{
									"codec_name":"avc",
									"base_url":"/live.m3u8",
									"url_info":[{"host":"https://live.example.com","extra":"?hls=1"}]
								}]
							},{
								"format_name":"flv",
								"codec":[{
									"codec_name":"avc",
									"base_url":"/live.flv",
									"url_info":[{"host":"https://live.example.com","extra":"?flv=1"}]
								}]
							}]
						}]
					}
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	client.SetBuvidStore(nil)
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return fakeSigner{} })
	stream, err := client.GetStream(context.Background(), 123, false, "SESSDATA=test")
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	if stream.URL != "https://live.example.com/live.flv?flv=1" {
		t.Fatalf("stream url = %s", stream.URL)
	}
	if stream.Headers["Cookie"] != "SESSDATA=test" {
		t.Fatalf("stream cookie header = %q", stream.Headers["Cookie"])
	}
}

func TestBilibiliClientPrefersAudioCodec(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"playurl_info":{
					"playurl":{
						"stream":[{
							"format":[{
								"codec":[{
									"codec_name":"hevc",
									"base_url":"/video.flv",
									"url_info":[{"host":"https://live.example.com","extra":""}]
								},{
									"codec_name":"aac",
									"base_url":"/audio.m4a",
									"url_info":[{"host":"https://live.example.com","extra":""}]
								}]
							}]
						}]
					}
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	client.SetBuvidStore(nil)
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return fakeSigner{} })

	// audioOnly=true：应选择 aac 纯音频流
	stream, err := client.GetStream(context.Background(), 123, true, "")
	if err != nil {
		t.Fatalf("get audio stream: %v", err)
	}
	if stream.URL != "https://live.example.com/audio.m4a" {
		t.Fatalf("expected audio stream url, got %s", stream.URL)
	}
	if !stream.AudioOnly {
		t.Fatalf("expected AudioOnly=true")
	}

	// audioOnly=false：应选择第一个可用流
	stream, err = client.GetStream(context.Background(), 123, false, "")
	if err != nil {
		t.Fatalf("get mixed stream: %v", err)
	}
	if stream.AudioOnly {
		t.Fatalf("expected AudioOnly=false for non-audioOnly request")
	}
}

// fakeSigner 是 #7 测试用的桩 URLSigner：给 URL 追加固定的 w_rid/wts，避免打真实 WBI nav。
type fakeSigner struct{}

func (fakeSigner) SignURL(rawURL string) (string, error) {
	sep := "&"
	if !contains(rawURL, "?") {
		sep = "?"
	}
	return rawURL + sep + "w_rid=fakesign&wts=1700000000", nil
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestBilibiliClientAntiRiskHeaders 验证异常 #7:CheckLive 请求带 buvid cookie、WBI 签名(w_rid/wts)、
// Referer/Origin header。用 httptest 桩同时充当 B 站 API 和 buvid spi,捕获请求特征。
func TestBilibiliClientAntiRiskHeaders(t *testing.T) {
	var gotCookie, gotReferer, gotOrigin, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 捕获 getInfoByRoom 的请求特征(buvid spi 走 /api/spi,这里只关心 getInfoByRoom)。
		if r.URL.Path == "/xlive/web-room/v1/index/getInfoByRoom" {
			gotCookie = r.Header.Get("Cookie")
			gotReferer = r.Header.Get("Referer")
			gotOrigin = r.Header.Get("Origin")
			gotQuery = r.URL.RawQuery
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"room_info":{"room_id":123,"live_status":1,"title":"t","live_start_time":1700000000},"anchor_info":{"base_info":{"uname":"a"}}}}`))
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	// 注入桩签名器(给 URL 加 w_rid/wts),避免打真实 WBI nav。
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return fakeSigner{} })
	// 注入指向同一 httptest 的 buvid store(spi 端点返回 buvid3/buvid4)。
	// 用 server.URL 作 spi,让 GetBuvids 拿到固定 buvid。
	client.SetBuvidStore(biliutil.NewBuvidStoreWithOptions(client.httpClient, server.URL+"/api/spi"))

	// buvid spi 端点也要响应(返回 buvid3/buvid4)。上面 handler 对未知 path 也返回 JSON,
	// 但 buvid 解析逻辑要 code=0 + data.b_3,简单起见单独覆盖。
	// 为简化,直接用预填 cookie 让 injectAntiRisk 走 InjectBuvids:这里传一个含 buvid3 的 cookie,
	// 但 GetBuvids 会按 spi 拉。为避免依赖 spi 解析,改用 nil buvid store 让 injectAntiRisk 跳过,
	// 单独验证 header + 签名。下面用 nil 重置。
	client.SetBuvidStore(nil)

	_, err := client.CheckLive(context.Background(), 123, "SESSDATA=abc")
	if err != nil {
		t.Fatalf("CheckLive: %v", err)
	}

	// 1. WBI 签名:URL query 应含 w_rid 和 wts。
	if !contains(gotQuery, "w_rid=fakesign") || !contains(gotQuery, "wts=") {
		t.Errorf("URL missing WBI signature (w_rid/wts): query=%q", gotQuery)
	}
	// 2. Referer/Origin header(同步 identify.go 策略)。
	if gotReferer == "" || !contains(gotReferer, "live.bilibili.com") {
		t.Errorf("Referer header missing/invalid: %q", gotReferer)
	}
	if gotOrigin != "https://live.bilibili.com" {
		t.Errorf("Origin header = %q, want https://live.bilibili.com", gotOrigin)
	}
	// 3. Cookie 透传。
	if !contains(gotCookie, "SESSDATA=abc") {
		t.Errorf("Cookie not forwarded: %q", gotCookie)
	}
}

// TestBilibiliClientAntiRiskBuvidInjection 验证 buvid 注入:GetBuvids 成功时 cookie 加 buvid3/buvid4。
func TestBilibiliClientAntiRiskBuvidInjection(t *testing.T) {
	var gotCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/xlive/web-room/v1/index/getInfoByRoom" {
			gotCookie = r.Header.Get("Cookie")
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"room_info":{"room_id":123,"live_status":0,"title":"t","live_start_time":1700000000},"anchor_info":{"base_info":{"uname":"a"}}}}`))
	}))
	defer server.Close()

	// spi 端点返回 buvid3/buvid4。
	spiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"b_3":"injected_buvid3","b_4":"injected_buvid4"}}`))
	}))
	defer spiServer.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return fakeSigner{} })
	client.SetBuvidStore(biliutil.NewBuvidStoreWithOptions(client.httpClient, spiServer.URL))

	_, err := client.CheckLive(context.Background(), 123, "")
	if err != nil {
		t.Fatalf("CheckLive: %v", err)
	}

	if !contains(gotCookie, "buvid3=injected_buvid3") || !contains(gotCookie, "buvid4=injected_buvid4") {
		t.Errorf("buvid3/buvid4 not injected into cookie: %q", gotCookie)
	}
}

// TestBilibiliClientGetStreamAntiRisk 验证异常 #7:GetStream 也带 WBI 签名 + Referer/Origin header。
func TestBilibiliClientGetStreamAntiRisk(t *testing.T) {
	var gotQuery, gotReferer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/xlive/web-room/v2/index/getRoomPlayInfo" {
			gotQuery = r.URL.RawQuery
			gotReferer = r.Header.Get("Referer")
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"playurl_info":{"playurl":{"stream":[{"format":[{"codec":[{"codec_name":"hevc","base_url":"/live.flv","url_info":[{"host":"https://live.example.com","extra":"?flv=1"}]}]}]}]}}}}`))
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	client.SetBuvidStore(nil)
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return fakeSigner{} })

	_, err := client.GetStream(context.Background(), 456, false, "SESSDATA=xyz")
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if !contains(gotQuery, "w_rid=fakesign") {
		t.Errorf("GetStream URL missing WBI signature: query=%q", gotQuery)
	}
	if !contains(gotReferer, "live.bilibili.com") {
		t.Errorf("GetStream Referer missing: %q", gotReferer)
	}
}

// TestBilibiliClientAntiRiskSignerFailureDegradesGracefully 验证异常 #7 降级:WBI 签名失败时
// 不阻断,仍发未签名请求(signerFactory 返回 always-fail signer)。
func TestBilibiliClientAntiRiskSignerFailureDegradesGracefully(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"room_info":{"room_id":123,"live_status":1,"title":"t","live_start_time":1700000000},"anchor_info":{"base_info":{"uname":"a"}}}}`))
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	client.SetBuvidStore(nil)
	// alwaysFailSigner 总是签名失败,验证降级不阻断。
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return alwaysFailSigner{} })

	info, err := client.CheckLive(context.Background(), 123, "")
	if err != nil {
		t.Fatalf("CheckLive should degrade gracefully on signer failure, got: %v", err)
	}
	if !info.Live {
		t.Errorf("expected live=true, got false")
	}
}

type alwaysFailSigner struct{}

func (alwaysFailSigner) SignURL(rawURL string) (string, error) {
	return "", errors.New("wbi nav unavailable")
}
