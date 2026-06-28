package biliutil

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrWBIKeyUnavailable 表示无法获取 WBI 签名密钥。
	ErrWBIKeyUnavailable = errors.New("wbi key unavailable")
	// ErrRiskControl 表示触发了 B 站风控（-352）。
	ErrRiskControl = errors.New("risk control triggered")
)

// URLSigner 是可测试的 URL 签名接口。
type URLSigner interface {
	SignURL(rawURL string) (string, error)
}

// mixinKeyEncTab 是 WBI 签名使用的 64 元素置换表。
var mixinKeyEncTab = [64]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4,
	22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

// WBISigner 实现 B 站 WBI URL 签名。
type WBISigner struct {
	httpClient *http.Client
	cookie     string
	navBaseURL string // 可覆盖的 nav API 基础 URL，仅测试用
	mu         sync.Mutex
	mixinKey   string
	updatedAt  time.Time
}

// NewWBISigner 创建一个新的 WBI 签名器。
func NewWBISigner(cookie string) *WBISigner {
	return &WBISigner{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		cookie:     cookie,
	}
}

// SignURL 对 URL 进行 WBI 签名，附加 w_rid 和 wts 参数。
func (s *WBISigner) SignURL(rawURL string) (string, error) {
	if err := s.ensureKeys(); err != nil {
		return "", err
	}

	s.mu.Lock()
	mixinKey := s.mixinKey
	s.mu.Unlock()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}

	query := parsed.Query()
	wts := strconv.FormatInt(time.Now().Unix(), 10)
	query.Set("wts", wts)

	// 按 key 排序
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 清理值并拼接
	var sb strings.Builder
	for i, k := range keys {
		v := sanitizeValue(query.Get(k))
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(v)
	}

	// 计算 w_rid
	hash := md5.Sum([]byte(sb.String() + mixinKey))
	wRid := hex.EncodeToString(hash[:])

	// 追加到 URL
	query.Set("w_rid", wRid)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// RefreshKeys 强制刷新 WBI 签名密钥。
func (s *WBISigner) RefreshKeys() error {
	return s.fetchKeys()
}

// ensureKeys 确保密钥可用，未缓存或已过期（>1小时）时自动刷新。
func (s *WBISigner) ensureKeys() error {
	s.mu.Lock()
	if s.mixinKey != "" && time.Since(s.updatedAt) < time.Hour {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.fetchKeys()
}

// fetchKeys 从 nav API 获取 WBI 密钥。
func (s *WBISigner) fetchKeys() error {
	navURL := "https://api.bilibili.com/x/web-interface/nav"
	if s.navBaseURL != "" {
		navURL = s.navBaseURL + "/x/web-interface/nav"
	}
	req, err := http.NewRequest(http.MethodGet, navURL, nil)
	if err != nil {
		return fmt.Errorf("create nav request: %w", err)
	}
	req.Header.Set("User-Agent", BiliUserAgent)
	req.Header.Set("Referer", "https://www.bilibili.com")
	if s.cookie != "" {
		req.Header.Set("Cookie", s.cookie)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: nav request: %v", ErrWBIKeyUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: nav http status %d", ErrWBIKeyUnavailable, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read nav response: %v", ErrWBIKeyUnavailable, err)
	}

	var navResp struct {
		Code int `json:"code"`
		Data struct {
			WbiImg struct {
				ImgURL string `json:"img_url"`
				SubURL string `json:"sub_url"`
			} `json:"wbi_img"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &navResp); err != nil {
		return fmt.Errorf("%w: parse nav response: %v", ErrWBIKeyUnavailable, err)
	}
	if navResp.Data.WbiImg.ImgURL == "" || navResp.Data.WbiImg.SubURL == "" {
		return ErrWBIKeyUnavailable
	}

	imgKey := extractKeyFromURL(navResp.Data.WbiImg.ImgURL)
	subKey := extractKeyFromURL(navResp.Data.WbiImg.SubURL)
	if imgKey == "" || subKey == "" {
		return ErrWBIKeyUnavailable
	}

	mixinKey := getMixinKey(imgKey, subKey)

	s.mu.Lock()
	s.mixinKey = mixinKey
	s.updatedAt = time.Now()
	s.mu.Unlock()

	return nil
}

// extractKeyFromURL 从 URL 提取文件名并去掉 .png 后缀。
func extractKeyFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	return strings.TrimSuffix(base, ".png")
}

// getMixinKey 使用置换表从 imgKey 和 subKey 生成 mixinKey。
func getMixinKey(imgKey, subKey string) string {
	combined := imgKey + subKey
	var result strings.Builder
	for _, idx := range mixinKeyEncTab {
		if idx < len(combined) {
			result.WriteByte(combined[idx])
		}
	}
	mixed := result.String()
	if len(mixed) > 32 {
		return mixed[:32]
	}
	return mixed
}

// sanitizeValue 移除值中的特殊字符 !'()*
func sanitizeValue(v string) string {
	var sb strings.Builder
	for _, ch := range v {
		if !strings.ContainsRune("!'()*", ch) {
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

// SetMixinKeyForTest 设置 mixinKey 用于测试，避免真实 nav 请求。
// 仅用于测试。
func (s *WBISigner) SetMixinKeyForTest(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mixinKey = key
	s.updatedAt = time.Now()
}
