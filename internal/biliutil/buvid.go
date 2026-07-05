package biliutil

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// buvidTTL 是 finger/spi 返回的 buvid3/buvid4 的缓存有效期（与 B 站指纹生命周期一致）。
const buvidTTL = 24 * time.Hour

// defaultBuvidSpiURL 是 B 站前端指纹接口，返回设备指纹 buvid3/buvid4。
const defaultBuvidSpiURL = "https://api.bilibili.com/x/frontend/finger/spi"

// cachedBuvid 是按 cookieHeader 缓存的 buvid 指纹对。
type cachedBuvid struct {
	buvid3    string
	buvid4    string
	expiresAt time.Time
}

// BuvidStore 拉取并按 cookieHeader 缓存 B 站设备指纹（buvid3/buvid4），
// 供识别、发布、弹幕等链路注入到请求 Cookie 头以通过 -352 风控。
//
// nil-safe：nil 接收者的 GetBuvids 直接返回空串 + nil，不打网络，
// 用于测试 helper 字面量构造未注入 store 的禁用场景。
type BuvidStore struct {
	httpClient HTTPDoer
	spiURL     string
	cache      map[string]cachedBuvid
	mu         sync.Mutex
}

// NewBuvidStore 创建带默认 HTTP 客户端与默认 spi URL 的 store。
func NewBuvidStore() *BuvidStore {
	return NewBuvidStoreWithHTTPClient(nil)
}

// NewBuvidStoreWithHTTPClient 用指定 HTTP 客户端（nil 则用默认 30s client）+ 默认 spi URL 创建 store。
// 传同一份 client 可避免连接池分裂（调用方业务请求与 buvid 请求复用连接）。
func NewBuvidStoreWithHTTPClient(client HTTPDoer) *BuvidStore {
	return NewBuvidStoreWithOptions(client, defaultBuvidSpiURL)
}

// NewBuvidStoreWithOptions 用指定 HTTP 客户端 + spi URL 创建 store。
// 主要供测试注入 httptest URL；生产代码用 NewBuvidStore / NewBuvidStoreWithHTTPClient。
func NewBuvidStoreWithOptions(client HTTPDoer, spiURL string) *BuvidStore {
	return &BuvidStore{
		httpClient: httpClientOrDefault(client),
		spiURL:     spiURL,
		cache:      make(map[string]cachedBuvid),
	}
}

// GetBuvids 返回指定 cookie 对应的 buvid3/buvid4，24h 内命中缓存。
// nil 接收者返回 ""、""、nil（不打网络）。
// 指纹接口失败、HTTP 非 2xx、业务码非 0、b_3 为空时返回 error；调用方应降级容错（只 warn 不阻断）。
func (s *BuvidStore) GetBuvids(ctx context.Context, cookieHeader string) (buvid3, buvid4 string, err error) {
	if s == nil {
		return "", "", nil
	}
	now := time.Now()
	s.mu.Lock()
	if cached, ok := s.cache[cookieHeader]; ok && now.Before(cached.expiresAt) {
		s.mu.Unlock()
		return cached.buvid3, cached.buvid4, nil
	}
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.spiURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", BiliUserAgent)
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("get buvids http status %d", resp.StatusCode)
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			B3 string `json:"b_3"`
			B4 string `json:"b_4"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}
	if result.Code != 0 {
		return "", "", fmt.Errorf("get buvids code=%d message=%s", result.Code, result.Message)
	}
	if result.Data.B3 == "" {
		return "", "", fmt.Errorf("get buvids returned empty b_3")
	}

	s.mu.Lock()
	s.cache[cookieHeader] = cachedBuvid{
		buvid3:    result.Data.B3,
		buvid4:    result.Data.B4,
		expiresAt: now.Add(buvidTTL),
	}
	s.mu.Unlock()

	slog.Info("buvids fetched and cached", "buvid3", result.Data.B3, "buvid4", result.Data.B4)
	return result.Data.B3, result.Data.B4, nil
}

// InjectBuvids 把 buvid3/buvid4 注入 cookie 头，采用 **replace 语义**：
// 先剔除 cookieHeader 里已存在的 buvid3=/buvid4= 段，再追加新值。
// 这样无论源 cookie 文件是否带旧指纹，最终头里同名 key 只剩新值，
// 避免 B 站按首个同名 cookie 解析导致新指纹失效（这是 -352 风控对抗的关键）。
// 空值的 buvid3/buvid4 不会被追加（但仍会剔除旧的同名字段）。
func InjectBuvids(cookieHeader, buvid3, buvid4 string) string {
	var kept []string
	if cookieHeader != "" {
		for _, part := range strings.Split(cookieHeader, ";") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if strings.HasPrefix(p, "buvid3=") || strings.HasPrefix(p, "buvid4=") {
				continue // 剔除旧的同名字段
			}
			kept = append(kept, p)
		}
	}
	if buvid3 != "" {
		kept = append(kept, "buvid3="+buvid3)
	}
	if buvid4 != "" {
		kept = append(kept, "buvid4="+buvid4)
	}
	return strings.Join(kept, "; ")
}
