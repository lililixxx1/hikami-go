package biliutil

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// stubSigner 是不经网络的桩签名器，避免测试触发真实 WBI nav 请求。
// 直接返回原 URL（不签名），仅用于测试。
type stubSigner struct{}

func (stubSigner) SignURL(rawURL string) (string, error) { return rawURL, nil }
func (stubSigner) RefreshKeys() error                    { return nil }

// handleAntiRisk 放行 view 端点 -352 风控对抗的副请求（spi）。
// 2026-07-06 VideoClient 加 buvid 注入后，Fetch 会先打 spi。
// 测试 mock 需放行该路径并返回让 buvid store 降级的空响应（b_3 空 → GetBuvids 报错 → 不改 cookie）。
// signer 已由 SetSignerFactory 注入桩，不会打 nav。已处理返回 true。
func handleAntiRisk(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/x/frontend/finger/spi" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"b_3":"","b_4":""}}`))
		return true
	}
	return false
}

// newStubVideoClient 构造一个注入了桩 buvid store + 桩 signer 的 VideoClient，
// 供测试使用，避免 fetch 触发真实 spi/nav 副请求。
// buvid store 用指向 httptest 的桩（见各测试），signer 用 stubSigner。
func newStubVideoClient(handler http.Handler) *VideoClient {
	vc := &VideoClient{
		HTTPClient: mockHTTPDoer(handler),
		BaseURL:    "https://api.test",
	}
	vc.SetSignerFactory(func(string) URLSigner { return stubSigner{} })
	return vc
}

func TestVideoClientFetch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleAntiRisk(w, r) {
			return
		}
		if r.URL.Path != "/x/web-interface/view" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("bvid"); got != "BV1xx411c7mD" {
			t.Fatalf("bvid = %q", got)
		}
		// Cookie 可能被 buvid 注入改写（SESSDATA=sess; buvid3=...），只校验包含原始段。
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "SESSDATA=sess") {
			t.Fatalf("Cookie = %q, want contain SESSDATA=sess", got)
		}
		if got := r.Header.Get("Referer"); got != biliReferer {
			t.Fatalf("Referer = %q", got)
		}
		if got := r.Header.Get("Origin"); got != biliReferer {
			t.Fatalf("Origin = %q, want %q", got, biliReferer)
		}
		if got := r.Header.Get("User-Agent"); got != BrowserUA {
			t.Fatalf("User-Agent = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"aid": 123,
				"bvid": "BV1xx411c7mD",
				"title": "测试视频",
				"pages": [{"cid": 456, "part": "P1", "page": 1}]
			}
		}`))
	})

	// buvid store 指向同一个 httptest server，spi 端点返回空 buvid（降级不注入）。
	vc := newStubVideoClient(handler)
	vc.SetBuvidStore(NewBuvidStoreWithHTTPClient(mockHTTPDoer(handler)))
	info, err := vc.Fetch(context.Background(), "BV1xx411c7mD", "SESSDATA=sess")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.AID != 123 || info.BVID != "BV1xx411c7mD" || info.Title != "测试视频" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(info.Pages) != 1 || info.Pages[0].CID != 456 {
		t.Fatalf("unexpected pages: %+v", info.Pages)
	}
}

func TestVideoClientFetchHTTPError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fail", http.StatusBadGateway)
	})
	vc := newStubVideoClient(handler)
	_, err := vc.Fetch(context.Background(), "BV1xx411c7mD", "")
	if err == nil || !strings.Contains(err.Error(), "view http status 502") {
		t.Fatalf("err = %v, want http status error", err)
	}
}

func TestVideoClientFetchAPICodeError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-404,"message":"not found"}`))
	})
	vc := newStubVideoClient(handler)
	_, err := vc.Fetch(context.Background(), "BV1xx411c7mD", "")
	if err == nil || !strings.Contains(err.Error(), "view api code -404") {
		t.Fatalf("err = %v, want api code error", err)
	}
}
