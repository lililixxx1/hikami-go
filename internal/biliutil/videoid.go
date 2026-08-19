package biliutil

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
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

// ExtractVideoPart 返回 URL 的 p 查询参数显式选择的 B 站分 P（从 1 开始）。
// 没有合法正数 p 的 URL 视为选择整个视频。
func ExtractVideoPart(rawURL string) (int, bool) {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return 0, false
	}
	if !strings.Contains(s, "://") {
		if strings.HasPrefix(s, "//") {
			s = "https:" + s
		} else {
			s = "https://" + strings.TrimPrefix(s, "/")
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return 0, false
	}
	part, err := strconv.Atoi(strings.TrimSpace(u.Query().Get("p")))
	if err != nil || part <= 0 {
		return 0, false
	}
	return part, true
}

// ExtractVideoSourceID 构造场次级来源标识。显式带 p 的 B 站多 P URL 必须与
// 整个 BV 及其它分 P 区分；普通 URL 继续沿用 ExtractVideoID 语义。
func ExtractVideoSourceID(rawURL string) string {
	videoID := ExtractVideoID(rawURL)
	if videoID == "" || !bvPattern.MatchString(videoID) {
		return videoID
	}
	if part, ok := ExtractVideoPart(rawURL); ok {
		return fmt.Sprintf("%s_p%03d", videoID, part)
	}
	return videoID
}

// SourceIDWithPart 在调用方已持有视频 ID(如 yt-dlp entry.ID,可能是列表器
// 自定义格式、不保证匹配 BV 正则)时构造场次级来源标识:URL 显式带 p 则追加
// 与 ExtractVideoSourceID 一致的 _pNNN 分 P 后缀,否则原样返回 videoID
// (videoID 为 BV-less 而 URL 携带 BV 时例外,见下)。
// 后缀格式与 ExtractVideoSourceID 单一来源,保证两条路径去重口径一致。
// yt-dlp 对多 P 合集的 entry.ID 会剥掉 BV 前缀(实测 2026-08-19,BV1SW411P7Du
// 的 flat-playlist entry.id 为 "1SW411P7Du"),裸用会使 discover 与
// download-by-url 的 ExtractVideoSourceID("BV..._pNNN")键脱节,同一分 P 经
// 两条路径各建一场、重复下载。entry.ID 不匹配 BV 而 URL 携带 BV 时锚定 URL
// 的 BV;URL 无 BV(如 ep/ss 或非 B 站列表)保持 entry.ID 原样,历史去重连续。
func SourceIDWithPart(videoID, rawURL string) string {
	if videoID == "" {
		return ExtractVideoSourceID(rawURL)
	}
	base := videoID
	if !bvPattern.MatchString(videoID) {
		if urlBV := bvPattern.FindString(rawURL); urlBV != "" {
			base = urlBV
		}
	}
	if part, ok := ExtractVideoPart(rawURL); ok {
		return fmt.Sprintf("%s_p%03d", base, part)
	}
	return base
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
