package biliutil

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// stubRedirectDoer 模拟 b23.tv 短链服务:对收到的请求,返回一个 Response,
// 其 resp.Request.URL 指向 finalURL(模拟"已跟随重定向"后的最终落地 URL)。
// 用 finalBV="" 表示落地 URL 不含 BV(用于测试降级)。
// 用 networkErr != nil 表示模拟网络失败。
type stubRedirectDoer struct {
	finalBV    string // 落地 URL 里的 BV 号;空串 → 落地到一个不含 BV 的 URL
	networkErr error  // 非 nil → 直接返回该错误模拟网络失败
	calls      int32  // 记录 Do 调用次数
}

func (d *stubRedirectDoer) Do(req *http.Request) (*http.Response, error) {
	d.calls++
	if d.networkErr != nil {
		return nil, d.networkErr
	}
	finalURL := "https://www.bilibili.com/landing"
	if d.finalBV != "" {
		finalURL = "https://www.bilibili.com/video/" + d.finalBV
	}
	// 构造一个"看起来已跟随重定向"的 response:resp.Request.URL = finalURL。
	// ResolveShortLink 只读 resp.Request.URL.String(),不读 body/status。
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       http.NoBody,
		Request: &http.Request{
			URL:  parseURL(finalURL),
			Host: "www.bilibili.com",
		},
	}
	return resp, nil
}

func parseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// TestResolveShortLink_FollowsRedirectToBV:合法 b23.tv 短链应解析为含 BV 的落地 URL。
func TestResolveShortLink_FollowsRedirectToBV(t *testing.T) {
	d := &stubRedirectDoer{finalBV: "BV1xx411c7mD"}
	const in = "https://b23.tv/AJIsbvW"
	got := ResolveShortLink(context.Background(), d, in)
	if !strings.Contains(got, "BV1xx411c7mD") {
		t.Fatalf("ResolveShortLink = %q, want URL containing BV1xx411c7mD", got)
	}
	if d.calls != 1 {
		t.Errorf("应发 1 次 HTTP,实际 %d", d.calls)
	}
}

// TestResolveShortLink_NonB23ReturnsAsIs:非 b23.tv 链接应原样返回且不发任何 HTTP。
func TestResolveShortLink_NonB23ReturnsAsIs(t *testing.T) {
	d := &stubRedirectDoer{finalBV: "BV1xx411c7mD"} // 即使能解析,也不该被调用
	cases := []string{
		"https://www.bilibili.com/video/BV1xx411c7mD",
		"https://m.bilibili.com/video/BV1xx411c7mD",
		"BV1xx411c7mD", // 纯 BV 号
		"https://example.com/some/path?from=b23.tv", // query 里偶现 b23.tv 但 host 不是
		"",
		"  ",
	}
	for _, in := range cases {
		got := ResolveShortLink(context.Background(), d, in)
		if got != in {
			t.Errorf("ResolveShortLink(%q) = %q, want原值返回", in, got)
		}
	}
	if d.calls != 0 {
		t.Errorf("非 b23.tv 链接不应发 HTTP,实际 calls=%d", d.calls)
	}
}

// TestResolveShortLink_NoBVInFinalFallback:落地 URL 不含 BV 时应降级返回原短链。
func TestResolveShortLink_NoBVInFinalFallback(t *testing.T) {
	d := &stubRedirectDoer{finalBV: ""} // 落地 URL 无 BV
	const in = "https://b23.tv/xyz"
	got := ResolveShortLink(context.Background(), d, in)
	if got != in {
		t.Fatalf("落地无 BV 时应降级返回原值 %q, got %q", in, got)
	}
	if d.calls != 1 {
		t.Errorf("仍应发起 1 次请求用于判定,实际 %d", d.calls)
	}
}

// TestResolveShortLink_NetworkErrorFallback:网络错误应降级返回原短链,不 panic。
func TestResolveShortLink_NetworkErrorFallback(t *testing.T) {
	d := &stubRedirectDoer{networkErr: &url.Error{
		Op: "Get", URL: "https://b23.tv/", Err: errConnectionRefused{},
	}}
	const in = "https://b23.tv/AJIsbvW"
	got := ResolveShortLink(context.Background(), d, in)
	if got != in {
		t.Fatalf("网络错误时应降级返回原值 %q, got %q", in, got)
	}
}

type errConnectionRefused struct{}

func (errConnectionRefused) Error() string { return "connection refused (test stub)" }

// TestIsB23ShortLink:单元测试 host 判定逻辑(含容易混淆的边界 case)。
func TestIsB23ShortLink(t *testing.T) {
	cases := map[string]bool{
		"https://b23.tv/AJIsbvW":              true,
		"http://b23.tv/abc":                   true,
		"b23.tv/abc":                          true, // 裸域名补 scheme 后 Host 仍为 b23.tv
		"https://B23.TV/abc":                  true, // 大小写不敏感(codex r16 Suggestion#1)
		"https://b23.tv./abc":                 true, // 尾点(DNS 等价,codex r16 Suggestion#1)
		"https://www.bilibili.com/video/BV1x": false,
		"https://m.bilibili.com/video/BV1x":   false,
		"https://example.com/?from=b23.tv":    false, // query 含 b23.tv 但 host 不是
		"https://b23.tv.evil.com/x":           false, // host 仿冒(b23.tv 作为子串)
		"":                                    false,
	}
	for in, want := range cases {
		if got := isB23ShortLink(in); got != want {
			t.Errorf("isB23ShortLink(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestResolveShortLink_NonBilibiliFinalFallback:落地 URL host 非 B 站官方域时应降级返回原短链
// (codex r16 Suggestion#2:避免把异常跳转/钓鱼站当结果)。
func TestResolveShortLink_NonBilibiliFinalFallback(t *testing.T) {
	// 桩返回一个含 BV 但 host 是 evil.com 的 URL(模拟异常跳转)。
	d := &nonBiliBVDoer{}
	const in = "https://b23.tv/xyz"
	got := ResolveShortLink(context.Background(), d, in)
	if got != in {
		t.Fatalf("落地 host 非 B 站时应降级返回原值 %q, got %q", in, got)
	}
}

type nonBiliBVDoer struct{}

func (*nonBiliBVDoer) Do(req *http.Request) (*http.Response, error) {
	// 落地到 evil.com 但 URL 里含 BV(测试校验逻辑不只看 BV,还看 host)。
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Request: &http.Request{
			URL:  parseURL("https://evil.com/redirect?to=BV1xx411c7mD"),
			Host: "evil.com",
		},
	}, nil
}
