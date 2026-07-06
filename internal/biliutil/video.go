package biliutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
//
// 2026-07-06 起改为指针语义：Fetch 接收者为 *VideoClient，因新增的 buvid/signer 懒缓存
// 字段需要稳定的归属实例（值拷贝会丢失缓存一致性）。零值 VideoClient（含复合字面量构造）
// 仍可用——首次 Fetch 会在 signersMu 保护下懒初始化缺失字段（见 ensure）。
type VideoClient struct {
	HTTPClient HTTPDoer
	BaseURL    string
	// buvids/signers/newSigner 是 -352 风控对抗组件，零值时由 ensure() 懒初始化。
	// signersMu 保护 buvids/signers/newSigner 三者的初始化与读写（含 signerForCookie）。
	buvids    *BuvidStore
	signers   map[string]URLSigner
	signersMu sync.Mutex
	// newSigner 按 cookie 创建 WBISigner（默认 NewWBISigner），测试可经 SetSignerFactory 注入桩。
	// ensure() 只在它为 nil 时设默认值，绝不覆盖 setter 注入。
	newSigner func(cookie string) URLSigner
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
	vc := &VideoClient{}
	return vc.Fetch(ctx, bvid, cookie)
}

// ensure 懒初始化 -352 风控对抗字段。
// 整个方法持有 signersMu，保护 buvids/signers/newSigner 三字段的初始化，
// 避免并发首次 Fetch 的数据竞争（与 signerForCookie 共用同一把锁）。
// newSigner 只在为 nil 时设默认值，不覆盖 SetSignerFactory 注入的桩。
func (c *VideoClient) ensure() {
	c.signersMu.Lock()
	defer c.signersMu.Unlock()
	if c.buvids == nil {
		c.buvids = NewBuvidStoreWithHTTPClient(httpClientOrDefault(c.HTTPClient))
	}
	if c.signers == nil {
		c.signers = make(map[string]URLSigner)
	}
	if c.newSigner == nil {
		// 沿用 VideoClient.HTTPClient 创建 signer，避免 WBI nav 请求绕过配置的 transport
		// （测试 httptest 桩 / 生产代理）。NewWBISignerWithHTTPClient 对 nil client 降级为默认 15s。
		c.newSigner = func(cookie string) URLSigner {
			return NewWBISignerWithHTTPClient(cookie, c.HTTPClient)
		}
	}
}

// signerForCookie 返回指定 cookie 对应的 URLSigner，按 cookie 懒初始化和缓存
// （与 identify/live_record.bilibili.go 同模式）。读写均在 signersMu 保护下。
//
// signer 缓存 key 用【原始 cookie】（注入 buvid 前）而非注入后的 cookie：
// WBI 签名密钥源自 B站账号身份（nav API）不随 buvid 变，buvid 注入只改请求 Cookie 头。
// 对齐 live_record/bilibili.go:103-115 的 v3 修正。
func (c *VideoClient) signerForCookie(cookie string) URLSigner {
	c.signersMu.Lock()
	defer c.signersMu.Unlock()
	if s, ok := c.signers[cookie]; ok {
		return s
	}
	s := c.newSigner(cookie)
	c.signers[cookie] = s
	return s
}

// SetBuvidStore 替换内部 buvid 存储，仅用于测试注入指向 httptest 桩的 spi URL。
func (c *VideoClient) SetBuvidStore(store *BuvidStore) {
	c.signersMu.Lock()
	defer c.signersMu.Unlock()
	c.buvids = store
}

// SetSignerFactory 替换签名器工厂，仅用于测试注入桩签名器（避免打真实 WBI nav）。
// 必须在首次 Fetch 前调用；ensure() 不会覆盖此注入。
func (c *VideoClient) SetSignerFactory(fn func(cookie string) URLSigner) {
	c.signersMu.Lock()
	defer c.signersMu.Unlock()
	c.newSigner = fn
}

// Fetch 获取 B 站视频信息。
//
// -352 风控对抗三件套（2026-07-06 加入，对齐 identify/live_record/publisher 共享层）：
//  1. buvid 注入（GetBuvids + InjectBuvids，失败降级仅 warn，不剔除已有 buvid3）
//  2. WBI 签名（SignURL，按原始 cookie 缓存 signer，失败降级为不签名）
//  3. 请求头补 Origin（UA + Referer 原有，Origin 新增）
//
// 三件套失败时均降级继续，不阻断主流程，避免风控对抗组件本身成为单点故障。
func (c *VideoClient) Fetch(ctx context.Context, bvid string, cookie string) (*VideoInfo, error) {
	bvid = strings.TrimSpace(bvid)
	if bvid == "" {
		return nil, fmt.Errorf("bvid is required")
	}
	c.ensure()

	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = biliAPIBaseURL
	}
	endpoint := baseURL + "/x/web-interface/view?bvid=" + url.QueryEscape(bvid)

	// signer key 用原始 cookie（baseCookie），buvid 注入只改请求 Cookie 头（cookieHeader）。
	baseCookie := cookie
	cookieHeader := baseCookie
	if b3, b4, berr := c.buvids.GetBuvids(ctx, baseCookie); berr != nil {
		slog.Warn("video: get buvids failed, continuing without buvid", "error", berr)
	} else if b3 != "" || b4 != "" {
		cookieHeader = InjectBuvids(cookieHeader, b3, b4)
	}
	// WBI 签名失败降级为不签名（仍可能被风控，但保持容错）。
	if signed, err := c.signerForCookie(baseCookie).SignURL(endpoint); err == nil {
		endpoint = signed
	} else {
		slog.Warn("video: wbi sign failed, continuing unsigned", "error", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create view request: %w", err)
	}
	setBiliHeaders(req, cookieHeader)

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
	req.Header.Set("Origin", biliReferer) // 2026-07-06 加入：-352 风控对抗组成部分
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
}
