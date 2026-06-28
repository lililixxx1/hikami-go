package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WebhookNotifier
// ---------------------------------------------------------------------------

func TestWebhookSend(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		title   string
		body    string
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			title:   "录制开始",
			body:    "主播 xxx 开始了录制",
			wantErr: false,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			title:   "错误通知",
			body:    "任务失败",
			wantErr: true,
		},
		{
			name: "client error 400",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
			},
			title:   "bad request",
			body:    "test",
			wantErr: true,
		},
		{
			name: "json format and content type",
			handler: func(w http.ResponseWriter, r *http.Request) {
				contentType := r.Header.Get("Content-Type")
				if contentType != "application/json" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				data, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				defer r.Body.Close()

				var payload map[string]string
				if err := json.Unmarshal(data, &payload); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if payload["title"] != "测试标题" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if payload["body"] != "测试内容" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			title:   "测试标题",
			body:    "测试内容",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			n := NewWebhookNotifier(srv.URL)
			err := n.Send(context.Background(), tt.title, tt.body)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Send() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BarkNotifier
// ---------------------------------------------------------------------------

func TestBarkSend(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		title   string
		body    string
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			title:   "直播开始",
			body:    "主播开始了直播",
			wantErr: false,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			},
			title:   "错误",
			body:    "请求失败",
			wantErr: true,
		},
		{
			name: "url encoding chinese text",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// httptest server populates RequestURI with the raw, un-decoded URI.
				uri := r.RequestURI
				if !strings.Contains(uri, "test_device_key") {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				encodedTitle := url.PathEscape("标题带空格")
				encodedBody := url.PathEscape("内容有特殊字符")
				if !strings.Contains(uri, encodedTitle) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if !strings.Contains(uri, encodedBody) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			title:   "标题带空格",
			body:    "内容有特殊字符",
			wantErr: false,
		},
		{
			name: "emoji in title",
			handler: func(w http.ResponseWriter, r *http.Request) {
				uri := r.RequestURI
				if !strings.Contains(uri, url.PathEscape("录制提醒！🎉")) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			title:   "录制提醒！🎉",
			body:    "body",
			wantErr: false,
		},
		{
			name: "space and cjk characters",
			handler: func(w http.ResponseWriter, r *http.Request) {
				uri := r.RequestURI
				if !strings.Contains(uri, url.PathEscape("录制 完成")) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			title:   "录制 完成",
			body:    "body",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			n := NewBarkNotifier(srv.URL, "test_device_key")
			err := n.Send(context.Background(), tt.title, tt.body)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Send() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ServerChanNotifier
// ---------------------------------------------------------------------------

// redirectTransport redirects all requests to the test server, preserving
// path and query. This lets us test ServerChanNotifier which hardcodes
// sctapi.ftqq.com in its URL.
type redirectTransport struct {
	target *url.URL
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme = rt.target.Scheme
	out.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(out)
}

func TestServerChanSend(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		title   string
		body    string
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			title:   "任务完成",
			body:    "所有任务已处理完成",
			wantErr: false,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			title:   "失败通知",
			body:    "服务器错误",
			wantErr: true,
		},
		{
			name: "form encoding",
			handler: func(w http.ResponseWriter, r *http.Request) {
				contentType := r.Header.Get("Content-Type")
				if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				if err := r.ParseForm(); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.FormValue("title") != "表单标题" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.FormValue("desp") != "表单描述" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			title:   "表单标题",
			body:    "表单描述",
			wantErr: false,
		},
		{
			name: "api url contains send key",
			handler: func(w http.ResponseWriter, r *http.Request) {
				expected := "/mykey.send"
				if !strings.HasSuffix(r.URL.Path, expected) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			title:   "URL格式",
			body:    "验证URL",
			wantErr: false,
		},
		{
			name: "special characters in body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.FormValue("title") != "特殊字符" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.FormValue("desp") != "描述含&符号" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			title:   "特殊字符",
			body:    "描述含&符号",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			srvURL, _ := url.Parse(srv.URL)

			n := &ServerChanNotifier{
				SendKey: "mykey",
				HTTPClient: &http.Client{
					Transport: &redirectTransport{target: srvURL},
					Timeout:   10 * time.Second,
				},
			}
			err := n.Send(context.Background(), tt.title, tt.body)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Send() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
