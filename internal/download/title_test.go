package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"hikami-go/internal/biliutil"
)

// newViewCountingServer 是一个 httptest server,记录 spi/view 两类请求的命中次数,
// 用于钉死 ResolveDownloadTitle 跨调用复用同一 VideoClient 时 BuvidStore 缓存不再被击穿。
//
// nav(WBI 密钥)由测试经 SetSignerFactory 注入预置 mixinKey 的桩 signer 跳过,
// 聚焦验证主因 BuvidStore 24h 缓存的复用契约。
//
// 修复前包级 FetchVideoInfo 每次新建 VideoClient,BuvidStore 形同虚设 → 每条视频重打 spi。
// 修复后 Handler.viewClient 长生命周期 → 一次预览内 spi 只打 1 次(首批)。
func newViewCountingServer(t *testing.T, bvid string) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var spi, view int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			atomic.AddInt32(&spi, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"b_3":"test-buvid3-aaaaaaaaaaaaaaaaaaaaaaaa","b_4":"test-buvid4"}}`))
		case "/x/web-interface/view":
			atomic.AddInt32(&view, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"aid":1001,"bvid":"` + bvid + `","title":"【直播回放】测试标题 2026年07月29日20点场","pic":"https://example.com/x.png","pages":[{"cid":9999,"part":"P1","page":1}]}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &spi, &view
}

// noNavSigner 预置 mixinKey 跳过真实 nav 请求(WBISigner.SetMixinKeyForTest),
// 让测试聚焦 BuvidStore 缓存复用,避免 nav 副请求干扰 spi 计数。
type noNavSigner struct{ inner *biliutil.WBISigner }

func newNoNavSigner(cookie string) biliutil.URLSigner {
	s := biliutil.NewWBISigner(cookie)
	s.SetMixinKeyForTest("0123456789abcdef0123456789abcdef")
	return &noNavSigner{inner: s}
}

func (n *noNavSigner) SignURL(rawURL string) (string, error) { return n.inner.SignURL(rawURL) }

// TestResolveDownloadTitle_ReusesViewClientAcrossCalls 钉死主修复 A:Handler 持有的
// 长生命周期 viewClient 在多次 ResolveDownloadTitle 调用间复用 BuvidStore 缓存。
// 修复前(包级 FetchVideoInfo 每次新建实例)此测试会失败:spi 计数 == 调用次数。
func TestResolveDownloadTitle_ReusesViewClientAcrossCalls(t *testing.T) {
	const bvid = "BV1TestReuses000"
	srv, spiPtr, viewPtr := newViewCountingServer(t, bvid)

	// 构造指向 httptest 的 VideoClient:
	//   - BaseURL 覆盖 view 端点
	//   - SetBuvidStore 注入指向 httptest 的 spi URL(BuvidStore 独立持有 spiURL,BaseURL 不影响它)
	//   - SetSignerFactory 返回预置 mixinKey 的 signer,跳过 nav 副请求
	vc := &biliutil.VideoClient{BaseURL: srv.URL}
	vc.SetBuvidStore(biliutil.NewBuvidStoreWithOptions(nil, srv.URL+"/x/frontend/finger/spi"))
	vc.SetSignerFactory(newNoNavSigner)

	fix := setupDownloadTest(t)
	fix.insertChannel(t, "test-ch")
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, &mockDownloader{}, fix.channelStore)
	h.SetViewClient(vc)

	// 连续两次解析同一视频标题。
	for i := 0; i < 2; i++ {
		title := h.ResolveDownloadTitle(context.Background(), "test-ch", bvid)
		if title != "测试标题" {
			t.Fatalf("call %d: ResolveDownloadTitle = %q, want %q (CleanReplayTitle 剥掉【直播回放】与日期)", i, title, "测试标题")
		}
	}

	if got := atomic.LoadInt32(viewPtr); got != 2 {
		t.Errorf("view hits = %d, want 2 (每次 ResolveDownloadTitle 必打一次 view)", got)
	}
	if got := atomic.LoadInt32(spiPtr); got != 1 {
		t.Errorf("spi hits = %d, want 1 (BuvidStore 24h 缓存应在两次调用间复用;修复前会=2)", got)
	}
}

// TestNewHandler_HasViewClient 钉死 NewHandler 注入了非 nil 的默认 viewClient,
// 即生产路径(不调 SetViewClient)也能享受长生命周期缓存,不会 nil 解引用 panic。
func TestNewHandler_HasViewClient(t *testing.T) {
	fix := setupDownloadTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, &mockDownloader{}, fix.channelStore)
	if h.viewClient == nil {
		t.Fatal("NewHandler().viewClient = nil, want non-nil default client")
	}
}
