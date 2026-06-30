package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hikami-go/internal/biliutil"
)

func TestMapBiliError(t *testing.T) {
	tests := []struct {
		name    string
		code    int
		message string
		want    error
	}{
		{"cookie expired", -101, "login needed", ErrCookieExpired},
		{"content rejected", -403, "forbidden", ErrContentRejected},
		{"rate limited", -509, "too fast", ErrRateLimited},
		{"unknown error", -999, "something wrong", ErrBilibiliAPI},
		{"zero code", 0, "ok", ErrBilibiliAPI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapBiliError(tt.code, tt.message)
			if !errors.Is(err, tt.want) {
				t.Errorf("mapBiliError(%d, %q) error type = %v, want %v", tt.code, tt.message, err, tt.want)
			}
		})
	}
}

// testClient wraps BiliOpusClient with an overridable base URL for testing.
type testClient struct {
	*BiliOpusClient
	baseURL string
}

func newTestClient(srv *httptest.Server) *testClient {
	return &testClient{
		BiliOpusClient: &BiliOpusClient{httpClient: srv.Client()},
		baseURL:        srv.URL,
	}
}

func (tc *testClient) saveDraft(ctx context.Context, cookie *BiliCookie, req *DraftRequest) (string, error) {
	paragraphs := make([]any, len(req.Paragraphs))
	for i, p := range req.Paragraphs {
		paragraphs[i] = p
	}
	body := map[string]any{
		"arg": map[string]any{
			"type":             4,
			"category_id":      req.CategoryID,
			"title":            req.Title,
			"private_pub":      req.PrivatePub,
			"reprint":          map[int]int{0: 1, 1: 0}[req.Original],
			"original":         req.Original,
			"list_id":          req.ListID,
			"comment_selected": req.UpChooseComment,
			"up_closed_reply":  req.CloseComment,
			"timer_pub_time":   req.TimerPubTime,
			"tags":             req.Tags,
			"topic_id":         req.TopicID,
			"summary":          req.Summary,
			"csrf":             cookie.BiliJct,
			"opus": map[string]any{
				"opus_source": 2,
				"title":       req.Title,
				"content":     map[string]any{"paragraphs": paragraphs},
				"attachments": map[string]any{"is_aigc": req.Aigc},
			},
		},
	}
	url := tc.baseURL + "/x/dynamic/feed/article/draft/add?csrf=" + cookie.BiliJct
	data, _ := json.Marshal(body)
	resp, err := doTestRequest(ctx, tc.httpClient, cookie, url, data)
	if err != nil {
		return "", err
	}
	draftID, ok := resp["article_id"]
	if !ok {
		return "", fmt.Errorf("missing article_id in response")
	}
	return fmt.Sprintf("%v", draftID), nil
}

func (tc *testClient) publishOpus(ctx context.Context, cookie *BiliCookie, req *PublishRequest) (string, error) {
	url := tc.baseURL + "/x/dynamic/feed/create/opus?csrf=" + cookie.BiliJct
	data, _ := json.Marshal(map[string]any{
		"opus_req": map[string]any{
			"option": map[string]any{
				"aigc":              req.Aigc,
				"tags":              req.Tags,
				"topic_id":          req.TopicID,
				"close_comment":     req.CloseComment,
				"up_choose_comment": req.UpChooseComment,
				"private_pub":       req.PrivatePub,
			},
		},
	})
	resp, err := doTestRequest(ctx, tc.httpClient, cookie, url, data)
	if err != nil {
		return "", err
	}
	dynID, ok := resp["dyn_id_str"]
	if !ok {
		return "", fmt.Errorf("missing dyn_id_str in response")
	}
	return fmt.Sprintf("%v", dynID), nil
}

func (tc *testClient) deleteDraft(ctx context.Context, cookie *BiliCookie, draftID string) error {
	url := tc.baseURL + "/x/dynamic/feed/article/draft/del?csrf=" + cookie.BiliJct
	data, _ := json.Marshal(map[string]any{"article_id": draftID})
	_, err := doTestRequest(ctx, tc.httpClient, cookie, url, data)
	return err
}

func doTestRequest(ctx context.Context, client *http.Client, cookie *BiliCookie, url string, body []byte) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie.CookieHeader())
	req.Header.Set("User-Agent", biliutil.BiliUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.Code != 0 {
		return nil, mapBiliError(result.Code, result.Message)
	}
	return result.Data, nil
}

func TestSaveDraft(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "testcsrf", DedeUserID: "42"}

	t.Run("success returns draft ID", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Errorf("method = %s, want POST", r.Method)
			}
			var body struct {
				Arg map[string]any `json:"arg"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if body.Arg["up_closed_reply"] != float64(1) {
				t.Errorf("up_closed_reply = %v, want 1", body.Arg["up_closed_reply"])
			}
			if body.Arg["comment_selected"] != float64(1) {
				t.Errorf("comment_selected = %v, want 1", body.Arg["comment_selected"])
			}
			if body.Arg["tags"] != "test" {
				t.Errorf("tags = %v, want test", body.Arg["tags"])
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"article_id":12345}}`)
		}))
		defer srv.Close()

		tc := newTestClient(srv)
		draftID, err := tc.saveDraft(context.Background(), cookie, &DraftRequest{
			Title: "Test Article", CategoryID: 15, CloseComment: 1, UpChooseComment: 1, Tags: "test",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if draftID != "12345" {
			t.Errorf("draftID = %q, want 12345", draftID)
		}
	})

	t.Run("api error maps correctly", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":-403,"message":"content rejected"}`)
		}))
		defer srv.Close()

		tc := newTestClient(srv)
		_, err := tc.saveDraft(context.Background(), cookie, &DraftRequest{})
		if !errors.Is(err, ErrContentRejected) {
			t.Errorf("error = %v, want ErrContentRejected", err)
		}
	})
}

func TestPublishOpus(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}

	t.Run("success returns dyn ID", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				OpusReq struct {
					Option map[string]any `json:"option"`
				} `json:"opus_req"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if body.OpusReq.Option["close_comment"] != float64(1) {
				t.Errorf("close_comment = %v, want 1", body.OpusReq.Option["close_comment"])
			}
			if body.OpusReq.Option["up_choose_comment"] != float64(1) {
				t.Errorf("up_choose_comment = %v, want 1", body.OpusReq.Option["up_choose_comment"])
			}
			if body.OpusReq.Option["tags"] != "test" {
				t.Errorf("tags = %v, want test", body.OpusReq.Option["tags"])
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"dyn_id_str":"dyn_999"}}`)
		}))
		defer srv.Close()

		tc := newTestClient(srv)
		dynID, err := tc.publishOpus(context.Background(), cookie, &PublishRequest{
			Title: "Test", CloseComment: 1, UpChooseComment: 1, Tags: "test",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dynID != "dyn_999" {
			t.Errorf("dynID = %q, want dyn_999", dynID)
		}
	})

	t.Run("api error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":-101,"message":"login needed"}`)
		}))
		defer srv.Close()

		tc := newTestClient(srv)
		_, err := tc.publishOpus(context.Background(), cookie, &PublishRequest{})
		if !errors.Is(err, ErrCookieExpired) {
			t.Errorf("error = %v, want ErrCookieExpired", err)
		}
	})
}

func TestDeleteDraft(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}

	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0}`)
		}))
		defer srv.Close()

		tc := newTestClient(srv)
		err := tc.deleteDraft(context.Background(), cookie, "12345")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":-509,"message":"too fast"}`)
		}))
		defer srv.Close()

		tc := newTestClient(srv)
		err := tc.deleteDraft(context.Background(), cookie, "12345")
		if !errors.Is(err, ErrRateLimited) {
			t.Errorf("error = %v, want ErrRateLimited", err)
		}
	})
}

func TestUploadCover(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}

	t.Run("success returns cover URL", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/finger/spi") {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"code":0,"data":{"b_3":"fakebuvid3","b_4":"fakebuvid4"}}`)
				return
			}
			if r.Method != "POST" {
				t.Errorf("method = %s, want POST", r.Method)
			}
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			// 经抓包确认:新接口 draw/upload_bfs 的 form 字段为 file_up
			if r.MultipartForm == nil || len(r.MultipartForm.File["file_up"]) == 0 {
				t.Errorf("期望 multipart 字段 file_up, 实际: %+v", r.MultipartForm)
			}
			w.Header().Set("Content-Type", "application/json")
			// 经抓包确认:响应字段为 data.image_url(不是老接口的 data.url)
			fmt.Fprintf(w, `{"code":0,"data":{"image_url":"https://example.com/cover.png"}}`)
		}))
		defer srv.Close()

		// 用 newOpusClientWithRedirect 构造完整 client(含 buvidCache 初始化),
		// 并让 getBuvids+upload 都重定向到 srv。
		client := newOpusClientWithRedirect(srv)
		dir := t.TempDir()
		imgPath := filepath.Join(dir, "cover.png")
		os.WriteFile(imgPath, []byte("fake png"), 0644)

		coverURL, err := client.UploadCover(context.Background(), cookie, imgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if coverURL != "https://example.com/cover.png" {
			t.Errorf("coverURL = %q, want https://example.com/cover.png", coverURL)
		}
	})

	t.Run("file not found returns error", func(t *testing.T) {
		client := &BiliOpusClient{httpClient: http.DefaultClient}
		_, err := client.UploadCover(context.Background(), cookie, "/nonexistent/cover.png")
		if err == nil {
			t.Error("expected error for missing file")
		}
	})

	t.Run("buvid injected into cookie", func(t *testing.T) {
		// 验证 uploadCoverToURL 像 doRequest 一样注入 buvid3/buvid4 以通过风控。
		// 用 newOpusClientWithRedirect 让 getBuvids 也走 httptest server。
		var uploadCookie string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/finger/spi") {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"code":0,"data":{"b_3":"fakebuvid3","b_4":"fakebuvid4"}}`)
				return
			}
			uploadCookie = r.Header.Get("Cookie")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"image_url":"https://example.com/c.png"}}`)
		}))
		defer srv.Close()

		client := newOpusClientWithRedirect(srv)
		dir := t.TempDir()
		imgPath := filepath.Join(dir, "cover.png")
		os.WriteFile(imgPath, []byte("fake"), 0644)

		if _, err := client.UploadCover(context.Background(), cookie, imgPath); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(uploadCookie, "buvid3=fakebuvid3") {
			t.Errorf("上传请求 Cookie 未注入 buvid3, Cookie=%q", uploadCookie)
		}
		if !strings.Contains(uploadCookie, "buvid4=fakebuvid4") {
			t.Errorf("上传请求 Cookie 未注入 buvid4, Cookie=%q", uploadCookie)
		}
	})

	t.Run("protocol fix // to https://", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/finger/spi") {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"code":0,"data":{"b_3":"b3","b_4":"b4"}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"image_url":"//example.com/cover.png"}}`)
		}))
		defer srv.Close()

		client := newOpusClientWithRedirect(srv)
		dir := t.TempDir()
		imgPath := filepath.Join(dir, "cover.png")
		os.WriteFile(imgPath, []byte("fake"), 0644)

		coverURL, err := client.UploadCover(context.Background(), cookie, imgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if coverURL != "https://example.com/cover.png" {
			t.Errorf("coverURL = %q, want https:// prefix", coverURL)
		}
	})

	// TestUploadCover_BFSDomainRewrite 验证上传返回的 BFS 通用图床域名(i0.hdslb.com 等)
	// 被改写为专栏图床域名 article.biliimg.com。
	// 原因:SaveDraft 时 B站服务端会把上传的 BFS 封面 URL 改写为 article.biliimg.com,
	// 但 PublishOpus 的 article.cover 不触发该改写,直接传 i0.hdslb.com 会导致封面丢失。
	t.Run("BFS domain rewrite to article.biliimg.com", func(t *testing.T) {
		cases := []struct {
			name string
			resp string
			want string
		}{
			{"http i0.hdslb.com", `{"code":0,"data":{"image_url":"http://i0.hdslb.com/bfs/new_dyn/abc.png"}}`, "http://article.biliimg.com/bfs/new_dyn/abc.png"},
			{"https i0.hdslb.com", `{"code":0,"data":{"image_url":"https://i0.hdslb.com/bfs/x.jpg"}}`, "https://article.biliimg.com/bfs/x.jpg"},
			{"protocol-relative i0", `{"code":0,"data":{"image_url":"//i0.hdslb.com/bfs/y.png"}}`, "https://article.biliimg.com/bfs/y.png"},
			{"i1 hdslb.com", `{"code":0,"data":{"image_url":"https://i1.hdslb.com/bfs/z.png"}}`, "https://article.biliimg.com/bfs/z.png"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/finger/spi") {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"code":0,"data":{"b_3":"b3","b_4":"b4"}}`)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, tc.resp)
				}))
				defer srv.Close()

				client := newOpusClientWithRedirect(srv)
				dir := t.TempDir()
				imgPath := filepath.Join(dir, "cover.png")
				os.WriteFile(imgPath, []byte("fake"), 0644)

				coverURL, err := client.UploadCover(context.Background(), cookie, imgPath)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if coverURL != tc.want {
					t.Errorf("coverURL = %q, want %q", coverURL, tc.want)
				}
			})
		}
	})
}

// newRedirectingClient 返回一个 http.Client,把所有请求(无论原始 URL 指向哪个 host)
// 重定向到 httptest.Server。用于在 HTTP 层测试硬编码了真实 host 的方法
// (SaveDraft/PublishOpus 及其依赖的 getBuvids),验证其构造的请求体 JSON 字段结构与抓包一致。
func newRedirectingClient(srv *httptest.Server) *http.Client {
	srvURL := srv.URL
	return &http.Client{
		Transport: &roundTripperFunc{
			rt: func(req *http.Request) (*http.Response, error) {
				// 把 scheme://api.bilibili.com/path 改写为 httptest server 的 URL
				req.URL.Scheme = "http"
				req.URL.Host = strings.TrimPrefix(srvURL, "http://")
				req.Host = req.URL.Host
				return http.DefaultTransport.RoundTrip(req)
			},
		},
	}
}

type roundTripperFunc struct {
	rt func(*http.Request) (*http.Response, error)
}

func (r *roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.rt(req)
}

// newOpusClientWithRedirect 返回一个走 httptest.Server 的完整 BiliOpusClient。
// 必须用 NewBiliOpusClient() 构造以初始化 buvidCache(否则 getBuvids 会 nil panic),
// 再覆盖 httpClient 将请求重定向到测试服务器。
func newOpusClientWithRedirect(srv *httptest.Server) *BiliOpusClient {
	c := NewBiliOpusClient()
	c.httpClient = newRedirectingClient(srv)
	return c
}

// TestSaveDraft_CoverAndFields 验证真实 SaveDraft 构造的请求体含抓包确认的字段:
// - 封面字段 arg.image_urls(字符串数组,草稿端结构)
// - 不含无效的 arg.topic_id / arg.tags(草稿端不支持)
// 关联修复:此前 DraftRequest.CoverURL 从不写入 JSON,导致封面失效。
func TestSaveDraft_CoverAndFields(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// getBuvids 会先发 GET /x/frontend/finger/spi,返回模拟 buvid 避免它报错中断。
		if strings.HasSuffix(r.URL.Path, "/finger/spi") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"b_3":"fakebuvid3","b_4":"fakebuvid4"}}`)
			return
		}
		var body struct {
			Arg map[string]any `json:"arg"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		// 封面:必须是字符串数组
		imgURLs, ok := body.Arg["image_urls"].([]any)
		if !ok || len(imgURLs) != 1 {
			t.Errorf("arg.image_urls = %v, want [\"https://example.com/cover.png\"]", body.Arg["image_urls"])
		} else if imgURLs[0] != "https://example.com/cover.png" {
			t.Errorf("arg.image_urls[0] = %v, want https://example.com/cover.png", imgURLs[0])
		}
		// 草稿端不应有 topic_id(无效字段)
		if _, exists := body.Arg["topic_id"]; exists {
			t.Error("arg.topic_id 不应存在:草稿端不支持话题")
		}
		// tags 不应写入(死字段)
		if _, exists := body.Arg["tags"]; exists {
			t.Error("arg.tags 不应存在:Opus 专栏无标签输入")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"data":{"article_id":12345}}`)
	}))
	defer srv.Close()

	client := newOpusClientWithRedirect(srv)
	draftID, err := client.SaveDraft(context.Background(), cookie, &DraftRequest{
		Title:    "封面测试",
		CoverURL: "https://example.com/cover.png",
		TopicID:  67890,     // 草稿端应忽略,不写入 topic_id
		Tags:     "ignored", // 死字段,不写入
	})
	if err != nil {
		t.Fatalf("SaveDraft 失败: %v", err)
	}
	if draftID != "12345" {
		t.Errorf("draftID = %q, want 12345", draftID)
	}
}

// TestSaveDraft_NoCoverOmitsImageURLs 验证无封面时不写入 image_urls 字段。
func TestSaveDraft_NoCoverOmitsImageURLs(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Arg map[string]any `json:"arg"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if _, exists := body.Arg["image_urls"]; exists {
			t.Error("无封面时 arg.image_urls 不应存在")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"data":{"article_id":1}}`)
	}))
	defer srv.Close()

	client := newOpusClientWithRedirect(srv)
	_, err := client.SaveDraft(context.Background(), cookie, &DraftRequest{Title: "无封面"})
	if err != nil {
		t.Fatalf("SaveDraft 失败: %v", err)
	}
}

// TestPublishOpus_TopicAndCoverStructure 验证真实 PublishOpus 构造的请求体含抓包确认的字段:
// - 话题 opus_req.topic = {id, name}(对象,在 opus_req 顶层,不是 option.topic_id)
// - 封面 opus_req.opus.article.cover = [{url}] (对象数组)
// - option 下不应有无效的 topic_id / tags
// 关联修复:此前 option.topic_id 字段名/层级均错误,封面完全缺失。
func TestPublishOpus_TopicAndCoverStructure(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// getBuvids 会先发 GET /x/frontend/finger/spi,返回模拟 buvid 避免它报错中断。
		if strings.HasSuffix(r.URL.Path, "/finger/spi") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"b_3":"fakebuvid3","b_4":"fakebuvid4"}}`)
			return
		}
		var body struct {
			OpusReq struct {
				Topic  map[string]any `json:"topic"`
				Option map[string]any `json:"option"`
				Opus   struct {
					Article map[string]any `json:"article"`
				} `json:"opus"`
			} `json:"opus_req"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		// 话题:opus_req.topic = {id, name}
		if id := body.OpusReq.Topic["id"]; id != float64(1330620) {
			t.Errorf("opus_req.topic.id = %v, want 1330620", id)
		}
		if name := body.OpusReq.Topic["name"]; name != "灰泽满的记录册" {
			t.Errorf("opus_req.topic.name = %v, want 灰泽满的记录册", name)
		}
		// 封面:opus_req.opus.article.cover = [{url}]
		cover, ok := body.OpusReq.Opus.Article["cover"].([]any)
		if !ok || len(cover) != 1 {
			t.Fatalf("opus_req.opus.article.cover = %v, want [{url}]", body.OpusReq.Opus.Article["cover"])
		}
		coverObj, ok := cover[0].(map[string]any)
		if !ok {
			t.Fatalf("cover[0] 类型错误: %T", cover[0])
		}
		if coverObj["url"] != "https://example.com/cover.png" {
			t.Errorf("cover[0].url = %v, want https://example.com/cover.png", coverObj["url"])
		}
		// option 下不应有 topic_id(旧错误字段)
		if _, exists := body.OpusReq.Option["topic_id"]; exists {
			t.Error("option.topic_id 不应存在:话题应在 opus_req.topic")
		}
		// tags 不应写入(死字段)
		if _, exists := body.OpusReq.Option["tags"]; exists {
			t.Error("option.tags 不应存在:Opus 专栏无标签输入")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"data":{"dyn_id_str":"dyn_999"}}`)
	}))
	defer srv.Close()

	client := newOpusClientWithRedirect(srv)
	dynID, _, _, err := client.PublishOpus(context.Background(), cookie, &PublishRequest{
		Title:     "话题封面测试",
		DraftID:   "380272",
		CoverURL:  "https://example.com/cover.png",
		TopicID:   1330620,
		TopicName: "灰泽满的记录册",
		Tags:      "ignored", // 死字段,不写入
	})
	if err != nil {
		t.Fatalf("PublishOpus 失败: %v", err)
	}
	if dynID != "dyn_999" {
		t.Errorf("dynID = %q, want dyn_999", dynID)
	}
}

// TestPublishOpus_NoTopicOmitsTopicField 验证 TopicID=0(无话题)时不写入 topic 字段。
func TestPublishOpus_NoTopicOmitsTopicField(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OpusReq map[string]any `json:"opus_req"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if _, exists := body.OpusReq["topic"]; exists {
			t.Error("TopicID=0 时 opus_req.topic 不应存在")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"data":{"dyn_id_str":"dyn_1"}}`)
	}))
	defer srv.Close()

	client := newOpusClientWithRedirect(srv)
	_, _, _, err := client.PublishOpus(context.Background(), cookie, &PublishRequest{
		Title:   "无话题",
		DraftID: "1",
	})
	if err != nil {
		t.Fatalf("PublishOpus 失败: %v", err)
	}
}

// TestPublishOpus_TimerPubTimeWrittenToBothLocations 验证定时发布(TimerPubTime>0)时,
// timer_pub_time 经抓包确认需在 opus.pub_info 和 option 两处冗余写入。
func TestPublishOpus_TimerPubTimeWrittenToBothLocations(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}
	const wantTimer = int64(1782673380)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/finger/spi") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"b_3":"b3","b_4":"b4"}}`)
			return
		}
		var body struct {
			OpusReq struct {
				Opus struct {
					PubInfo map[string]any `json:"pub_info"`
				} `json:"opus"`
				Option map[string]any `json:"option"`
			} `json:"opus_req"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if got := body.OpusReq.Opus.PubInfo["timer_pub_time"]; got != float64(wantTimer) {
			t.Errorf("opus.pub_info.timer_pub_time = %v, want %d", got, wantTimer)
		}
		if got := body.OpusReq.Option["timer_pub_time"]; got != float64(wantTimer) {
			t.Errorf("option.timer_pub_time = %v, want %d", got, wantTimer)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"data":{"dyn_id_str":"dyn_t"}}`)
	}))
	defer srv.Close()

	client := newOpusClientWithRedirect(srv)
	_, _, _, err := client.PublishOpus(context.Background(), cookie, &PublishRequest{
		Title:        "定时测试",
		DraftID:      "1",
		TimerPubTime: wantTimer,
	})
	if err != nil {
		t.Fatalf("PublishOpus 失败: %v", err)
	}
}

// TestPublishOpus_ImmediatePublishOmitsTimer 验证立即发布(TimerPubTime==0)时不写入 timer_pub_time。
func TestPublishOpus_ImmediatePublishOmitsTimer(t *testing.T) {
	cookie := &BiliCookie{SESSDATA: "sess", BiliJct: "csrf", DedeUserID: "1"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/finger/spi") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"b_3":"b3","b_4":"b4"}}`)
			return
		}
		var body struct {
			OpusReq struct {
				Opus struct {
					PubInfo map[string]any `json:"pub_info"`
				} `json:"opus"`
				Option map[string]any `json:"option"`
			} `json:"opus_req"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if _, exists := body.OpusReq.Opus.PubInfo["timer_pub_time"]; exists {
			t.Error("立即发布时 opus.pub_info.timer_pub_time 不应存在")
		}
		if _, exists := body.OpusReq.Option["timer_pub_time"]; exists {
			t.Error("立即发布时 option.timer_pub_time 不应存在")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"data":{"dyn_id_str":"dyn_i"}}`)
	}))
	defer srv.Close()

	client := newOpusClientWithRedirect(srv)
	_, _, _, err := client.PublishOpus(context.Background(), cookie, &PublishRequest{
		Title:   "立即发布",
		DraftID: "1",
	})
	if err != nil {
		t.Fatalf("PublishOpus 失败: %v", err)
	}
}
