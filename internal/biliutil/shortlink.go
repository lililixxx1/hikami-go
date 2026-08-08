package biliutil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// b23ShortDomain 是 B 站官方短链域名。仅该域名的链接需要 HTTP 解析,
// 其余(bilibili.com / m.bilibili.com / 已含 BV 的长链)零开销原样返回。
const b23ShortDomain = "b23.tv"

// isB23ShortLink 判断 rawURL 是否为 b23.tv 短链。
// 先用大小写不敏感的 strings.Contains 快速短路(绝大多数 URL 不含 b23.tv,避免无谓 url.Parse),
// 再用 url.Parse 严格判 Host == b23.tv(大小写不敏感 + 去尾点),排除 query/fragment 里
// 偶现 "b23.tv" 的奇葩长链,以及 b23.tv.evil.com 这类 host 仿冒。
func isB23ShortLink(rawURL string) bool {
	if !strings.Contains(strings.ToLower(rawURL), b23ShortDomain) {
		return false
	}
	s := rawURL
	if !strings.Contains(s, "://") {
		s = "https://" + s // 容错:裸 b23.tv/xxx 补 scheme 让 url.Parse 正确取 Host
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.TrimSuffix(u.Hostname(), ".") // 去尾点(如 b23.tv.)
	return strings.EqualFold(host, b23ShortDomain)
}

// ResolveShortLink 把 b23.tv 短链解析为最终落地 URL(通常含 BV 号)。
//
// 非 b23.tv 链接直接返回原值(零开销,不发 HTTP);
// 解析失败(网络错误 / 落地 URL 不含 BV)打 WARN 并返回原值(降级不阻断调用方主流程,
// 与 DownloadCover 的降级策略一致——音频/标题/弹幕任一失败都不阻断下载主体)。
//
// client 为 nil 时用 httpClientOrDefault(带 30s 超时)。Go http.Client 默认跟随最多 10 次
// 重定向(b23.tv 通常一次 302 → bilibili.com/video/BVxxx),最终 URL 从 resp.Request.URL 取。
// 复用 setBiliHeaders 满足 B 站基本防盗链。
//
// 用途:download.CreateFromURL 入口处把短链解析成长链,后续 ExtractVideoID(取 BV)、
// NormalizeSourceURL(归一化)、singlePCid/fetchCidMapForMultiP(取 cid 抓弹幕)、
// ResolveDownloadTitle(取标题)统一受益——避免在 4 处下游各加重定向,DRY。
func ResolveShortLink(ctx context.Context, client HTTPDoer, rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" || !isB23ShortLink(s) {
		return rawURL
	}
	client = httpClientOrDefault(client)

	finalURL, err := resolveShortLinkOnce(ctx, client, s)
	if err != nil {
		slog.Warn("resolve b23 shortlink failed, fallback to original url",
			"url", s, "error", err)
		return rawURL
	}
	// 校验落地 URL:① host 属于 B 站官方域(www./m. bilibili.com),② 含 BV 号。
	// 任一不满足视为解析失败降级——避免把风控中间页 / 登录页 / 异常跳转当结果传给下游。
	if !isBilibiliVideoURL(finalURL) {
		slog.Warn("b23 shortlink resolved but final url not bilibili video, fallback",
			"short", s, "final", finalURL)
		return rawURL
	}
	if bvPattern.FindString(finalURL) == "" {
		slog.Warn("b23 shortlink resolved but no BV in final url, fallback",
			"short", s, "final", finalURL)
		return rawURL
	}
	slog.Info("b23 shortlink resolved", "short", s, "final", finalURL)
	return finalURL
}

// bilibiliVideoHosts 是短链解析结果允许的 B 站官方域名(host 语义比较,大小写不敏感)。
// b23.tv 正常只跳转到这里;落在其他域(风控中间页 / 钓鱼站)视为解析失败降级。
var bilibiliVideoHosts = map[string]bool{
	"www.bilibili.com": true,
	"m.bilibili.com":   true,
	"bilibili.com":     true,
}

// isBilibiliVideoURL 判断 finalURL 是否落在 B 站官方域名(仅看 host,不要求含 BV)。
func isBilibiliVideoURL(finalURL string) bool {
	u, err := url.Parse(finalURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	return bilibiliVideoHosts[strings.ToLower(host)]
}

// resolveShortLinkOnce 发起一次 GET(跟随重定向)并返回最终落地 URL。
// 用 GET 而非 HEAD:b23.tv/部分 CDN 对 HEAD 返回 405。只关心重定向后的 URL,丢弃 body。
func resolveShortLinkOnce(ctx context.Context, client HTTPDoer, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create shortlink request: %w", err)
	}
	setBiliHeaders(req, "")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("shortlink request: %w", err)
	}
	// 读取少量 body 后关闭(LimitReader 防异常大响应耗内存)。
	// 注:跟随到 B 站视频页时 HTML 可能 >64KB,此处不保证读完全部 body,
	// 连接通常不复用,但资源会正常释放(无泄漏)。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	if resp.Request == nil || resp.Request.URL == nil {
		return "", fmt.Errorf("shortlink: no final url in response")
	}
	return resp.Request.URL.String(), nil
}
