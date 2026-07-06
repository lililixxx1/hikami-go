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

// refreshableFakeSigner 是可观测的桩签名器:实现 SignURL + RefreshKeys,
// 用于验证 -352 重试路径 RefreshKeys 调用的是 query() 实际签名用的 signer(codex v2 #1)。
type refreshableFakeSigner struct {
	signCalls    int
	refreshCalls int
}

func (s *refreshableFakeSigner) SignURL(rawURL string) (string, error) {
	s.signCalls++
	sep := "&"
	if !contains(rawURL, "?") {
		sep = "?"
	}
	return rawURL + sep + "w_rid=fakesign&wts=1700000000", nil
}

func (s *refreshableFakeSigner) RefreshKeys() error {
	s.refreshCalls++
	return nil
}

// responseSequence 按顺序返回预设的 getInfoByRoom 响应,耗尽后返回最后一个,用于模拟 -352→0 / -352→-352。
func newSequenceServer(t *testing.T, responses []string, requestCount *int) *httptest.Server {
	t.Helper()
	var idx int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/xlive/web-room/v1/index/getInfoByRoom" {
			http.NotFound(w, r)
			return
		}
		*requestCount++
		resp := responses[idx]
		if idx < len(responses)-1 {
			idx++
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCheckLiveRetriesOn352ThenSucceeds:首次 -352 → RefreshKeys+Invalidate → 重试 → code=0 成功。
func TestCheckLiveRetriesOn352ThenSucceeds(t *testing.T) {
	var reqCount int
	server := newSequenceServer(t, []string{
		`{"code":-352,"message":"风控","data":null}`,
		`{"code":0,"message":"0","data":{"room_info":{"room_id":123,"live_status":1,"title":"ok","live_start_time":1700000000},"anchor_info":{"base_info":{"uname":"a"}}}}`,
	}, &reqCount)

	client := NewBilibiliClientWithBaseURL(server.URL)
	signer := &refreshableFakeSigner{}
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return signer })
	client.SetBuvidStore(nil)

	info, err := client.CheckLive(context.Background(), 123, "SESSDATA=abc")
	if err != nil {
		t.Fatalf("CheckLive should succeed after retry, got: %v", err)
	}
	if !info.Live || info.Title != "ok" {
		t.Fatalf("unexpected info after retry: %+v", info)
	}
	if reqCount != 2 {
		t.Fatalf("request count = %d, want 2 (initial + retry)", reqCount)
	}
	if signer.refreshCalls != 1 {
		t.Fatalf("RefreshKeys calls = %d, want 1", signer.refreshCalls)
	}
}

// TestCheckLiveReturnsErr352OnPersistent352:重试仍 -352 → 返回 ErrRiskControl352 哨兵(errors.Is 可判)。
func TestCheckLiveReturnsErr352OnPersistent352(t *testing.T) {
	var reqCount int
	server := newSequenceServer(t, []string{
		`{"code":-352,"message":"风控","data":null}`,
		`{"code":-352,"message":"风控","data":null}`,
	}, &reqCount)

	client := NewBilibiliClientWithBaseURL(server.URL)
	signer := &refreshableFakeSigner{}
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return signer })
	client.SetBuvidStore(nil)

	_, err := client.CheckLive(context.Background(), 123, "SESSDATA=abc")
	if err == nil {
		t.Fatal("expected ErrRiskControl352, got nil")
	}
	if !errors.Is(err, ErrRiskControl352) {
		t.Fatalf("expected errors.Is(err, ErrRiskControl352), got: %v", err)
	}
	if reqCount != 2 {
		t.Fatalf("request count = %d, want 2 (initial + retry)", reqCount)
	}
	if signer.refreshCalls != 1 {
		t.Fatalf("RefreshKeys calls = %d, want 1 (only one retry)", signer.refreshCalls)
	}
}

// TestCheckLiveRefreshKeysCalledOnActualSigningSigner (codex v2 #1):
// 验证 RefreshKeys 刷的是 query() 实际签名用的 signer(baseCookie 维度)。
// 通过 signer.signCalls 必须 >0(确认 query 用了它)+ RefreshKeys 调用后 query 仍用同一 signer(signCalls 持续递增)来间接验证。
func TestCheckLiveRefreshKeysCalledOnActualSigningSigner(t *testing.T) {
	var reqCount int
	server := newSequenceServer(t, []string{
		`{"code":-352,"message":"风控","data":null}`,
		`{"code":0,"message":"0","data":{"room_info":{"room_id":123,"live_status":0,"title":"x","live_start_time":1700000000},"anchor_info":{"base_info":{"uname":"a"}}}}`,
	}, &reqCount)

	client := NewBilibiliClientWithBaseURL(server.URL)
	signer := &refreshableFakeSigner{}
	// signerFactory 按 cookie 返回 signer;CheckLive 用 baseCookie="SESSDATA=abc" 选 signer
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner {
		if cookie != "SESSDATA=abc" {
			t.Errorf("signerFactory called with cookie=%q, want baseCookie SESSDATA=abc", cookie)
		}
		return signer
	})
	client.SetBuvidStore(nil)

	_, err := client.CheckLive(context.Background(), 123, "SESSDATA=abc")
	if err != nil {
		t.Fatalf("CheckLive: %v", err)
	}
	// query 调了 2 次(初始+重试),每次 SignURL 一次 → signCalls==2,且都用同一个 signer 实例
	if signer.signCalls != 2 {
		t.Fatalf("signCalls = %d, want 2 (query signed twice with same signer)", signer.signCalls)
	}
	if signer.refreshCalls != 1 {
		t.Fatalf("RefreshKeys = %d, want 1", signer.refreshCalls)
	}
}

// TestCheckLiveInvalidatesBaseCookieWithBuvidStore:验证带真实 BuvidStore 时 -352 重试不 panic,
// 且 baseCookie 维度的 buvid 在 -352 后被 Invalidate(用 spi 请求计数验证:重试的 injectAntiRisk 重拉 spi)。
// Invalidate API 本身的正确性由 buvid_test.go 的 TestBuvidStoreInvalidate_* 单元覆盖。
func TestCheckLiveInvalidatesBaseCookieWithBuvidStore(t *testing.T) {
	var apiCount, spiCount int
	apiResponses := []string{
		`{"code":-352,"message":"风控","data":null}`,
		`{"code":0,"message":"0","data":{"room_info":{"room_id":123,"live_status":0,"title":"x","live_start_time":1700000000},"anchor_info":{"base_info":{"uname":"a"}}}}`,
	}
	var apiIdx int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xlive/web-room/v1/index/getInfoByRoom":
			apiCount++
			resp := apiResponses[apiIdx]
			if apiIdx < len(apiResponses)-1 {
				apiIdx++
			}
			_, _ = w.Write([]byte(resp))
		case "/spi":
			spiCount++
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"bv3","b_4":"bv4"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	client.SetSignerFactory(func(cookie string) biliutil.URLSigner { return &refreshableFakeSigner{} })
	client.SetBuvidStore(biliutil.NewBuvidStoreWithOptions(client.httpClient, server.URL+"/spi"))

	if _, err := client.CheckLive(context.Background(), 123, "SESSDATA=abc"); err != nil {
		t.Fatalf("CheckLive with buvid store: %v", err)
	}
	if apiCount != 2 {
		t.Fatalf("api count = %d, want 2 (initial -352 + retry)", apiCount)
	}
	// spi:首次 injectAntiRisk 拉 1 次 → -352 Invalidate 清缓存 → 重试 injectAntiRisk 再拉 1 次 = 2
	if spiCount != 2 {
		t.Fatalf("spi count = %d, want 2 (initial fetch + refetch after Invalidate)", spiCount)
	}
}
