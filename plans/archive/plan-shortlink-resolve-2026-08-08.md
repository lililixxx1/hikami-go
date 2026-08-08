# 修复计划:b23.tv 短链解析缺失致回放类标题/弹幕失败

> **状态**:2026-08-08 起草,待 codex 审核
> **触发**:8-7 官方录播用 `https://b23.tv/AJIsbvW`(b23.tv 短链)下载,音频正常,但回顾文档无弹幕、标题变 BV 号兜底。

## 背景与根因(已诊断)

8-7 官方录播用 `https://b23.tv/AJIsbvW`(b23.tv 短链)下载,音频正常(yt-dlp 自动跟随 302 重定向),但**回顾文档无弹幕、标题变成 BV 号兜底**。

**根因**:项目对 BV 号的提取依赖正则 `BV[1-9A-HJ-NP-Za-km-z]{10}`(`internal/biliutil/videoid.go:14` 的 `bvPattern` 与 `internal/download/native.go:34` 的 `nativeBVPattern`),而 b23.tv 短链 URL 里**不含 BV 号字面量**,需要先 HTTP 302 重定向到 `https://www.bilibili.com/video/BVxxx` 才能拿到。

**URL 在流水线里的传链**(全部用同一个 `sourceURL` 原值):
- `download.go:547` `ExtractVideoID(rawURL)` → 正则匹配不到 BV → sha1[:16] 兜底 ID → 去重键不稳定(但本次无重复所以未暴露)
- `download.go:549` `ResolveDownloadTitle(ctx, channelID, sourceID)` → sourceID 是 sha1 兜底 → view API 查不到真实标题 → 退回 BV 号显示
- `download.go:176` `singlePCid(ctx, sourceURL, ...)` → 内部 `extractNativeBVID(sourceURL)`(`native.go:574`)正则匹配不到 BV → cid=0 → **跳过弹幕抓取**(命中 `:182-184` 的 WARN 分支 "no cid resolved, skip danmaku")
- `download.go:210` `fetchCidMapForMultiP(ctx, sourceURL, ...)` → 同理拿不到 cid map(本次单 P 不走这)

**关键事实**:yt-dlp 自己跟随重定向所以音频能下,但项目 Go 代码的 BV 提取不跟随,导致标题/cid/弹幕全失效。

## 设计原则

1. **单一入口收口**:只在 `CreateFromURL`(`download.go:538`)入口处把短链解析成完整 URL 一次,4 处下游(标题/单P弹幕/多P弹幕/native BV 提取)自动修复——避免在 4 处各加重定向,DRY。
2. **只处理 b23.tv**:其他域名(bilibili.com / m.bilibili.com / 已含 BV 的 URL)零开销直接返回原值,不做无谓 HTTP。
3. **失败降级不阻断**(用户已确认):解析失败打 WARN + 返回原短链继续 = 当前行为,只是 WARN 更明确。音频/标题/弹幕任一失败都不阻断主流程(与现有 cover/danmaku 的降级策略一致)。
4. **放 biliutil 包**:复用 `HTTPDoer`/`httpClientOrDefault`/`setBiliHeaders`/`bvPattern`,签名对齐 `DownloadCover`(项目既有范式,`cover.go:28`)。
5. **不加缓存**:短链解析一次请求 < 500ms,且每个 URL 通常只经一次 CreateFromURL,加缓存复杂度收益小(codex 可评估是否需要)。

## 改动 1:新建 `internal/biliutil/shortlink.go`

```go
package biliutil

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// shortlinkHTTPClient 是 ResolveShortLink 在 client 入参为 nil 时使用的默认客户端。
// 不设超时上限靠 context 控制;但给一个兜底超时避免 context 无 deadline 时挂住。
var shortlinkHTTPClient = &http.Client{Timeout: 10 * time.Second}

// ResolveShortLink 把 b23.tv 短链解析为最终落地 URL(通常含 BV 号)。
// 非 b23.tv 链接直接返回原值(零开销);解析失败(WARN)也返回原值(降级不阻断)。
// client 为 nil 用带 10s 超时的默认客户端;Go http.Client 默认跟随最多 10 次重定向,
// 最终 URL 从 resp.Request.URL.String() 取。复用 setBiliHeaders 满足 B 站基本防盗链。
func ResolveShortLink(ctx context.Context, client HTTPDoer, rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" || !strings.Contains(s, "://b23.tv") && !strings.HasPrefix(s, "b23.tv") {
		// 不是 b23.tv 短链(含 http://b23.tv / https://b23.tv / 裸 b23.tv),原样返回
		return rawURL
	}
	if client == nil {
		client = shortlinkHTTPClient
	}
	finalURL, err := resolveShortLinkOnce(ctx, client, s)
	if err != nil {
		slog.Warn("resolve b23 shortlink failed, fallback to original url",
			"url", s, "error", err)
		return rawURL
	}
	// 校验落地 URL 确含 BV 号,否则视为解析失败降级
	if bvPattern.FindString(finalURL) == "" {
		slog.Warn("b23 shortlink resolved but no BV in final url, fallback",
			"short", s, "final", finalURL)
		return rawURL
	}
	slog.Info("b23 shortlink resolved", "short", s, "final", finalURL)
	return finalURL
}

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
	defer resp.Body.Close()
	if resp.Request == nil || resp.Request.URL == nil {
		return "", fmt.Errorf("shortlink: no final url in response")
	}
	return resp.Request.URL.String(), nil
}
```

**codex 审核点**:① b23.tv host 匹配方式(Contains vs 解析 url.Parse 判 Host)——Contains 简单但可能误匹配 query 里含 b23.tv 的奇葩 URL,url.Parse 更严;② 是否要禁用 body 读取加速(io.Copy(io.Discard, resp.Body));③ 10s 超时是否合理。

## 改动 2:`internal/download/download.go` CreateFromURL 插入一行

在 `:547` `ExtractVideoID` 调用**之前**插入短链解析:

```go
// before
rawURL = strings.TrimSpace(rawURL)
if rawURL == "" { ... }
sourceID := biliutil.ExtractVideoID(rawURL)
// after
rawURL = strings.TrimSpace(rawURL)
if rawURL == "" { ... }
// b23.tv 短链解析为完整 URL(含 BV 号),后续标题/cid/弹幕/native 提取统一受益。
// 失败降级返回原 URL(见 ResolveShortLink 文档),不阻断导入。
rawURL = biliutil.ResolveShortLink(ctx, nil, rawURL)
sourceID := biliutil.ExtractVideoID(rawURL)
```

`ctx` 取 CreateFromURL 的入参 context。后续 `cleanURL := biliutil.NormalizeSourceURL(rawURL)` 也自动受益。

## 改动 3:测试 `internal/biliutil/shortlink_test.go`(4 用例)

参照 `cover_test.go` 的 httptest.NewServer 模式:

1. `TestResolveShortLink_Follows302ToBV` —— httptest server 对 `/short` 返回 302 → `https://www.bilibili.com/video/BV1xxx`(另一个 server 路径返回 200),断言返回值含 BV。
2. `TestResolveShortLink_NonB23ReturnsAsIs` —— 传 `https://www.bilibili.com/video/BV1xxx`,断言**不发 HTTP**(用计数 transport)+ 原样返回。
3. `TestResolveShortLink_NoBVInFinalFallback` —— 302 落地到一个不含 BV 的 URL,断言 WARN 降级返回原短链。
4. `TestResolveShortLink_NetworkErrorFallback` —— 传不可达 URL(client 超时/连接拒绝),断言返回原短链不报错。

## 改动 4:测试 `internal/download/download_test.go`(+1)

`TestCreateFromURL_ResolvesB23ShortLink` —— mock `viewClient.Fetch`(或用 httptest)验证 CreateFromURL 传入 b23.tv 短链时,内部 ExtractVideoID 拿到的是解析后的真实 BV(而非 sha1 兜底)。参照现有 `TestResolveDownloadTitle_ReusesViewClientAcrossCalls`。

## 验证

1. `go test ./internal/biliutil/...`(全过,含 4 个新测试 + 现有 84 用例)
2. `go test ./internal/download/...`(全过,含 1 个新测试 + 现有 58 用例)
3. `go vet ./internal/biliutil/... ./internal/download/...` 干净
4. `gofmt -w` 改动文件
5. `go build -tags embedded_web -o /tmp/hikami-verify ./cmd/hikami`(编译通过)

## 文档

- `AGENTS.md` changelog 补本次条目(8-8,b23.tv 短链解析)
- 根 `CLAUDE.md` 模块索引 `biliutil`(84→88)+ `download`(58→59)测试数
- `internal/biliutil/CLAUDE.md` 文件清单 +shortlink.go + 测试段 + changelog
- `internal/download/CLAUDE.md` CreateFromURL 说明补短链解析 + 测试段 + changelog
- 计划文件归档 `plans/plan-shortlink-resolve-2026-08-08.md` → `plans/archive/`

## 回归风险评估:零

- 非 b23.tv URL 不经 HTTP 分支(`Contains("://b23.tv")` 短路返回),行为完全等价现状
- b23.tv 解析失败退回原短链 = 当前行为(只是多了 WARN)
- b23.tv 解析成功才改善(拿到 BV → 标题/cid/弹幕全部修复)
- 唯一新增 HTTP 请求仅在 CreateFromURL 且 URL 含 b23.tv 时触发,不并发,不重复

## codex 审核记录

计划本身无代码 diff 可审(codex-review skill 审核代码改动),故直接实施后跑 codex 代码审核,两轮收敛:

**第 1 轮(r16,路由 pppzzz,NEEDS_FIX)**:1 High + 1 Low + 2 Suggestion,全部采纳。
- **High**(download 测试依赖真实外网 + 不能钉死解析行为):给 Handler 加 `shortLinkResolver` 字段 + `SetShortLinkResolver` setter(对齐 `SetViewClient` 范式),CreateFromURL 经字段调用(nil 兜底包级 `biliutil.ResolveShortLink`);download 测试改用注入桩——`TestCreateFromURL_B23ShortLinkResolvesToBV` 断言 `SourceID==BV` + `SourceURL==落地长链`;`TestCreateFromURL_B23ShortLinkDegradesSafely` 断言降级 `SourceID` 是 sha1(非 BV)。不再打真实 b23.tv 外网。
- **Low**(LimitReader 64KB 不保证排空/连接复用):注释改为"读取少量 body 后关闭,不保证读完全部 body,连接通常不复用但资源正常释放"。
- **Suggestion#1**(host 大小写/尾点):`isB23ShortLink` 预筛选改 `ToLower Contains`,最终比较改 `EqualFold` + `TrimSuffix` 尾点;`TestIsB23ShortLink` 加 `B23.TV`(大写)+ `b23.tv.`(尾点)case。
- **Suggestion#2**(落地域名校验):新增 `isBilibiliVideoURL` + `bilibiliVideoHosts` map(www./m. bilibili.com),`ResolveShortLink` 在 BV 校验前先校验落地 host 属 B 站官方域;新增 `TestResolveShortLink_NonBilibiliFinalFallback` 验证 evil.com 落地降级。

**第 2 轮(r17,路由 pppzzz,APPROVED)**:4 个问题逐项确认收敛,0 阻塞。1 个非阻塞观察(降级测试未校验 exact sha1,属测试严格度非功能缺陷,保留不改)。

**实际测试数**(codex 审核后比计划增量):
- biliutil:`shortlink_test.go` 6 用例(计划 4,codex Suggestion#1/#2 补 2:`TestIsB23ShortLink` 大小写/尾点 case、`TestResolveShortLink_NonBilibiliFinalFallback`),84→**90**。
- download:2 用例(计划 1,codex High 拆成解析成功 + 降级两个),58→**60**。

