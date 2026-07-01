package biliutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	biliAPIBaseURL     = "https://api.bilibili.com"
	biliCommentBaseURL = "https://comment.bilibili.com"
	biliReferer        = "https://www.bilibili.com"
)

// BrowserUA 是浏览器 User-Agent 的兼容别名，供原生下载链路复用。
const BrowserUA = BiliUserAgent

// HTTPDoer 是可测试的 HTTP 客户端接口。
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// VideoClient 调用 B 站视频信息接口。
type VideoClient struct {
	HTTPClient HTTPDoer
	BaseURL    string
}

// VideoInfo 是 view 接口返回的核心视频信息。
type VideoInfo struct {
	AID   int64       `json:"aid"`
	BVID  string      `json:"bvid"`
	Title string      `json:"title"`
	Pic   string      `json:"pic"` // 视频封面 URL（B 站 view 接口的 data.pic）
	Pages []VideoPage `json:"pages"`
}

// VideoPage 是 B 站视频分 P 信息。
type VideoPage struct {
	CID  int64  `json:"cid"`
	Part string `json:"part"`
	Page int    `json:"page"`
}

// FetchVideoInfo 获取 B 站视频信息。
func FetchVideoInfo(ctx context.Context, bvid string, cookie string) (*VideoInfo, error) {
	return VideoClient{}.Fetch(ctx, bvid, cookie)
}

// Fetch 获取 B 站视频信息。
func (c VideoClient) Fetch(ctx context.Context, bvid string, cookie string) (*VideoInfo, error) {
	bvid = strings.TrimSpace(bvid)
	if bvid == "" {
		return nil, fmt.Errorf("bvid is required")
	}

	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = biliAPIBaseURL
	}
	endpoint := baseURL + "/x/web-interface/view?bvid=" + url.QueryEscape(bvid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create view request: %w", err)
	}
	setBiliHeaders(req, cookie)

	resp, err := httpClientOrDefault(c.HTTPClient).Do(req)
	if err != nil {
		return nil, fmt.Errorf("view request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("view http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read view response: %w", err)
	}

	var result struct {
		Code    int       `json:"code"`
		Message string    `json:"message"`
		Data    VideoInfo `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse view response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("view api code %d: %s", result.Code, result.Message)
	}
	if result.Data.AID == 0 || result.Data.BVID == "" || len(result.Data.Pages) == 0 {
		return nil, fmt.Errorf("view response missing video data")
	}
	return &result.Data, nil
}

func httpClientOrDefault(client HTTPDoer) HTTPDoer {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func setBiliHeaders(req *http.Request, cookie string) {
	req.Header.Set("User-Agent", BrowserUA)
	req.Header.Set("Referer", biliReferer)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
}
