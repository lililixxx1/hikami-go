package biliutil

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

// bvPattern 匹配 B 站视频的 BV 号（BV + 10 位 base58 字符）。
// base58 字母表排除易混淆字符 0/O/I/l，故字符类为 [1-9A-HJ-NP-Za-km-z]。
// 早期误用 [0-9A-HJ-NP-Za-hj-km-oq-z]（排除 i/l/n/p）会漏匹配含这些字符的合法 BV。
var bvPattern = regexp.MustCompile(`(?i)\bBV[1-9A-HJ-NP-Za-km-z]{10}\b`)

// trackingParams 是 B 站链接常见的跟踪/来源参数，归一化时剔除，
// 避免同一视频因 ?spm=... 等差异被当作不同来源重复下载。
var trackingParams = map[string]bool{
	"spm":             true,
	"spm_id_from":     true,
	"from_spmid":      true,
	"from_spmid_from": true,
	"vd_source":       true,
	"share_source":    true,
	"share_medium":    true,
	"share_plat":      true,
	"share_tag":       true,
	"share_session":   true,
	"bbid":            true,
	"ts":              true,
	"buvid":           true,
	"is_story_h5":     true,
	"utm_source":      true,
	"utm_medium":      true,
	"utm_campaign":    true,
	"utm_content":     true,
	"utm_term":        true,
}

// ExtractVideoID 从原始 URL（或纯 BV 号串）中解析视频唯一标识。
// 优先返回 B 站 BV 号；无法提取时对归一化 URL 取 sha1 前 16 位作为兜底 ID，
// 保证任意链接都能得到稳定的去重键。空输入返回空串。
func ExtractVideoID(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}
	if m := bvPattern.FindString(s); m != "" {
		return m
	}
	normalized := NormalizeSourceURL(s)
	if normalized == "" {
		normalized = s
	}
	h := sha1.Sum([]byte(normalized))
	return hex.EncodeToString(h[:])[:16]
}

// NormalizeSourceURL 规范化视频链接：去 fragment、剔除跟踪参数、去首尾空白。
// 用于把"同一视频的不同形态链接"统一为稳定的存储值，作为去重与下载目标。
func NormalizeSourceURL(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}
	// 容错：缺少 scheme 时补 https:，便于 url.Parse 正确解析 host。
	if !strings.Contains(s, "://") {
		s = "https:" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		// 解析失败时退化为去 fragment 的原值
		if idx := strings.IndexByte(s, '#'); idx >= 0 {
			s = s[:idx]
		}
		return s
	}
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		if trackingParams[strings.ToLower(k)] {
			q.Del(k)
		}
	}
	// url.Values.Encode 按 key 排序输出，保证不同参数顺序的等价链接归一为同一结果，
	// 从而兜底 sha1 ID 稳定。
	u.RawQuery = q.Encode()
	return u.String()
}
