package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/fsutil"
)

var (
	// ErrNativeCookieMissing 表示 native 下载缺少可用 Cookie。
	ErrNativeCookieMissing = errors.New("native downloader cookie missing")
	// ErrAudioDownloadFailed 表示 DASH 音频下载失败。
	ErrAudioDownloadFailed = errors.New("audio download failed")
	// ErrNativeUnsupported 表示当前链接不适合 native 后端处理，应由调用方决定是否回退。
	ErrNativeUnsupported = errors.New("native downloader unsupported source")
)

// nativeBVPattern 匹配 B 站视频的 BV 号（BV + 10 位 base58 字符）。
// base58 字母表排除易混淆字符 0/O/I/l，故字符类为 [1-9A-HJ-NP-Za-km-z]。
// 早期误用 [0-9A-HJ-NP-Za-hj-km-oq-z]（排除 i/l/n/p）会漏匹配含这些字符的合法 BV。
var nativeBVPattern = regexp.MustCompile(`(?i)\bBV[1-9A-HJ-NP-Za-km-z]{10}\b`)

// NativeDownloader 使用 Go 原生 HTTP 链路下载 B 站回放单 P 音频和弹幕。
type NativeDownloader struct {
	HTTPClient  biliutil.HTTPDoer
	ViewBaseURL string
	APIBaseURL  string
	CommentURL  string
	Signer      biliutil.URLSigner
	Cookie      *biliutil.BiliCookie
	FFmpeg      string
	FFprobe     string
	// ViewBuvids/ViewSignerFactory 是 view 端点 -352 风控对抗的可选注入点（2026-07-06）。
	// 零值时 VideoClient 内部懒初始化真实 BuvidStore/WBISigner；测试注入桩以避免 spi/nav 副请求。
	ViewBuvids        *biliutil.BuvidStore
	ViewSignerFactory func(cookie string) biliutil.URLSigner
}

type nativeMetadata struct {
	ID        string               `json:"id"`
	Title     string               `json:"title"`
	AID       int64                `json:"aid"`
	BVID      string               `json:"bvid"`
	CID       int64                `json:"cid"`
	Page      int                  `json:"page"`
	Part      string               `json:"part"`
	Pages     []biliutil.VideoPage `json:"pages"`
	Extractor string               `json:"extractor"`
	Native    map[string]any       `json:"native"`
}

// SetCookie 注入内存 Cookie，供后续 native 专用路径绕过临时 cookie 文件。
func (d *NativeDownloader) SetCookie(cookie *biliutil.BiliCookie) {
	d.Cookie = cookie
}

// Download 下载音频、弹幕 XML 和元数据；多 P 会产出 normalize 可直接消费的分 P 结构。
func (d NativeDownloader) Download(ctx context.Context, sourceURL string, rawDir string, cookieFile string) error {
	// TODO: 支持番剧链接识别与下载。
	// TODO: native 当前经临时 Netscape 文件读取 Cookie，后续可由 Handler 直接注入完整内存 Cookie，
	// 避免丢失 buvid3/buvid4 等风控相关字段。
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return fmt.Errorf("create raw dir: %w", err)
	}

	cookieHeader, err := d.cookieHeader(cookieFile)
	if err != nil {
		return err
	}

	bvid := extractNativeBVID(sourceURL)
	if bvid == "" {
		return fmt.Errorf("%w: only BV video URLs are supported", ErrNativeUnsupported)
	}
	viewClient := &biliutil.VideoClient{
		HTTPClient: d.HTTPClient,
		BaseURL:    firstNonEmpty(d.ViewBaseURL, d.APIBaseURL),
	}
	// 注入可选的风控对抗组件（测试用桩，生产留空走真实 BuvidStore/WBISigner）。
	if d.ViewBuvids != nil {
		viewClient.SetBuvidStore(d.ViewBuvids)
	}
	if d.ViewSignerFactory != nil {
		viewClient.SetSignerFactory(d.ViewSignerFactory)
	}
	info, err := viewClient.Fetch(ctx, bvid, cookieHeader)
	if err != nil {
		return fmt.Errorf("fetch video info: %w", err)
	}
	if len(info.Pages) == 0 {
		return fmt.Errorf("video has no pages")
	}
	// 下载视频官方封面到 raw/cover.*（供 publisher 作为专栏封面）。失败不阻断下载。
	biliutil.DownloadCover(ctx, d.HTTPClient, info.Pic, cookieHeader, rawDir)
	if len(info.Pages) == 1 {
		return d.downloadSingleP(ctx, rawDir, cookieHeader, info, info.Pages[0])
	}
	return d.downloadMultiP(ctx, rawDir, cookieHeader, info)
}

func (d NativeDownloader) downloadSingleP(ctx context.Context, rawDir string, cookieHeader string, info *biliutil.VideoInfo, page biliutil.VideoPage) error {
	playClient := biliutil.PlayURLClient{
		HTTPClient: d.HTTPClient,
		BaseURL:    d.APIBaseURL,
		Signer:     d.Signer,
	}
	streams, err := playClient.Fetch(ctx, info.AID, page.CID, info.BVID, cookieHeader)
	if err != nil {
		return fmt.Errorf("fetch playurl: %w", err)
	}
	stream, err := biliutil.SelectBestAudioStream(streams)
	if err != nil {
		return err
	}

	if err := d.downloadAudio(ctx, stream.URLs(), cookieHeader, filepath.Join(rawDir, "audio.m4a")); err != nil {
		return err
	}

	danmakuXML := d.fetchDanmakuWithFallback(ctx, page.CID, cookieHeader)
	if err := fsutil.WriteFileAtomic(filepath.Join(rawDir, "danmaku.xml"), danmakuXML, 0o644); err != nil {
		return fmt.Errorf("write danmaku xml: %w", err)
	}

	if err := fsutil.WriteJSONAtomic(filepath.Join(rawDir, "metadata.ytdlp.json"), nativeMetadataFor(info, page, stream), 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	return nil
}

func (d NativeDownloader) downloadMultiP(ctx context.Context, rawDir string, cookieHeader string, info *biliutil.VideoInfo) error {
	partsDir := filepath.Join(rawDir, "parts")
	danmakuPartsDir := filepath.Join(rawDir, "danmaku_parts")
	metadataPartsDir := filepath.Join(rawDir, "metadata_parts")
	for _, dir := range []string{partsDir, danmakuPartsDir, metadataPartsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create multi-p dir: %w", err)
		}
	}
	defer os.RemoveAll(partsDir)

	playClient := biliutil.PlayURLClient{
		HTTPClient: d.HTTPClient,
		BaseURL:    d.APIBaseURL,
		Signer:     d.Signer,
	}
	results := make([]partDownloadResult, 0, len(info.Pages))
	for i, page := range info.Pages {
		index := page.Page
		if index <= 0 {
			index = i + 1
		}
		partDir := filepath.Join(partsDir, fmt.Sprintf("p%03d", index))
		if err := os.MkdirAll(partDir, 0o755); err != nil {
			return fmt.Errorf("create part dir %d: %w", index, err)
		}

		streams, err := playClient.Fetch(ctx, info.AID, page.CID, info.BVID, cookieHeader)
		if err != nil {
			return fmt.Errorf("fetch playurl for part %d: %w", index, err)
		}
		stream, err := biliutil.SelectBestAudioStream(streams)
		if err != nil {
			return fmt.Errorf("select audio for part %d: %w", index, err)
		}

		audioPath := filepath.Join(partDir, "audio.m4a")
		if err := d.downloadAudio(ctx, stream.URLs(), cookieHeader, audioPath); err != nil {
			return fmt.Errorf("download audio for part %d: %w", index, err)
		}
		results = append(results, partDownloadResult{index: index, audio: audioPath})

		danmakuXML := d.fetchDanmakuWithFallback(ctx, page.CID, cookieHeader)
		if err := fsutil.WriteFileAtomic(filepath.Join(danmakuPartsDir, fmt.Sprintf("p%03d.xml", index)), danmakuXML, 0o644); err != nil {
			return fmt.Errorf("write danmaku for part %d: %w", index, err)
		}
		metadataPath := filepath.Join(metadataPartsDir, fmt.Sprintf("p%03d.info.json", index))
		if err := fsutil.WriteJSONAtomic(metadataPath, nativeMetadataFor(info, page, stream), 0o644); err != nil {
			return fmt.Errorf("write metadata for part %d: %w", index, err)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].index < results[j].index
	})

	concatListPath := filepath.Join(rawDir, "concat.list")
	concatList, err := os.Create(concatListPath)
	if err != nil {
		return fmt.Errorf("create concat list: %w", err)
	}
	defer os.Remove(concatListPath)

	durations := make([]partDuration, 0, len(results))
	for _, result := range results {
		durSecs, err := probeDuration(d.FFprobe, result.audio)
		if err != nil {
			_ = concatList.Close()
			return fmt.Errorf("probe duration for part %d: %w", result.index, err)
		}
		durations = append(durations, partDuration{Index: result.index, DurSecs: durSecs})
		// TODO: 与 yt-dlp 多 P 共用 ffconcat 路径转义 helper，处理单引号等特殊字符。
		// 写绝对路径：ffmpeg concat demuxer 会以 listfile 自身目录为基准解析相对条目，
		// OutputRoot 为相对路径时会叠加成 raw/raw/audio.m4a 导致打开失败。
		if _, err := fmt.Fprintf(concatList, "file '%s'\n", escapeConcatListPath(result.audio)); err != nil {
			_ = concatList.Close()
			return fmt.Errorf("write concat list: %w", err)
		}
	}
	if err := concatList.Close(); err != nil {
		return fmt.Errorf("close concat list: %w", err)
	}

	if err := concatAudio(d.FFmpeg, concatListPath, filepath.Join(rawDir, "audio.m4a")); err != nil {
		return fmt.Errorf("concat multi-P audio: %w", err)
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(rawDir, "part_durations.json"), durations, 0o644); err != nil {
		return fmt.Errorf("write part durations: %w", err)
	}
	return nil
}

func nativeMetadataFor(info *biliutil.VideoInfo, page biliutil.VideoPage, stream biliutil.AudioStream) nativeMetadata {
	return nativeMetadata{
		ID:        info.BVID,
		Title:     info.Title,
		AID:       info.AID,
		BVID:      info.BVID,
		CID:       page.CID,
		Page:      page.Page,
		Part:      page.Part,
		Pages:     info.Pages,
		Extractor: "hikami-native-bilibili",
		Native: map[string]any{
			"audio_id":        stream.ID,
			"audio_bandwidth": stream.Bandwidth,
			"audio_mime_type": stream.MimeType,
			"audio_codecs":    stream.Codecs,
			"downloaded_at":   time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func (d NativeDownloader) fetchDanmakuWithFallback(ctx context.Context, cid int64, cookieHeader string) []byte {
	return fetchDanmakuShared(ctx, d.HTTPClient, d.APIBaseURL, d.CommentURL, cid, cookieHeader)
}

// fetchDanmakuShared 是弹幕下载的共享实现（seg.so 优先 + XML 回退 + 双失败写 <i></i>），
// 供 native 与 yt-dlp 多 P 路径复用。httpClient/apiBaseURL/commentURL 传零值时，
// biliutil 客户端内部 fallback 到默认 http.Client（与生产 native 一致）。
func fetchDanmakuShared(ctx context.Context, httpClient biliutil.HTTPDoer, apiBaseURL, commentURL string, cid int64, cookieHeader string) []byte {
	segClient := biliutil.SegDanmakuClient{
		HTTPClient: httpClient,
		BaseURL:    apiBaseURL,
	}
	if danmakuXML, err := segClient.FetchSegments(ctx, cid, cookieHeader); err == nil && hasDanmakuContent(danmakuXML) {
		return danmakuXML
	} else if err != nil {
		slog.Warn("fetch seg danmaku failed, falling back to xml", "cid", cid, "error", err)
	}

	danmakuClient := biliutil.DanmakuClient{
		HTTPClient: httpClient,
		BaseURL:    commentURL,
	}
	danmakuXML, err := danmakuClient.FetchXML(ctx, cid, cookieHeader)
	if err != nil {
		slog.Warn("fetch danmaku xml failed, writing empty danmaku", "cid", cid, "error", err)
		return []byte("<i></i>")
	}
	return danmakuXML
}

func hasDanmakuContent(data []byte) bool {
	return strings.Contains(string(data), "<d ")
}

func (d NativeDownloader) cookieHeader(cookieFile string) (string, error) {
	if d.Cookie != nil {
		header := d.Cookie.CookieHeader()
		if strings.TrimSpace(header) != "" {
			return header, nil
		}
	}
	if strings.TrimSpace(cookieFile) == "" {
		return "", ErrNativeCookieMissing
	}
	cookie, err := biliutil.LoadCookie(cookieFile)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNativeCookieMissing, err)
	}
	return cookie.CookieHeader(), nil
}

// perURLAudioTimeout 限制单个音频 URL 的下载时长：慢/不响应的 CDN 节点超时后切 backupUrl，
// 避免 baseUrl 卡死导致整个下载永久 hang（联调发现多 P 某分 P 的 CDN 节点偶发不响应 body）。
const perURLAudioTimeout = 5 * time.Minute

func (d NativeDownloader) downloadAudio(ctx context.Context, urls []string, cookie string, targetPath string) error {
	tmpPath := targetPath + ".tmp"
	var lastErr error
	for _, rawURL := range urls {
		_ = os.Remove(tmpPath)
		// 单 URL 超时：超时后切下一个 URL（baseUrl → backupUrl），防止单节点卡死。
		reqCtx, cancel := context.WithTimeout(ctx, perURLAudioTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", biliutil.BrowserUA)
		req.Header.Set("Referer", "https://www.bilibili.com")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		resp, err := audioHTTPClientOrDefault(d.HTTPClient).Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		writeErr := writeSuccessfulBody(resp, tmpPath, targetPath)
		cancel()
		if writeErr != nil {
			_ = os.Remove(tmpPath)
			lastErr = writeErr
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("%w: %v", ErrAudioDownloadFailed, lastErr)
	}
	return ErrAudioDownloadFailed
}

func nativeHTTPClientOrDefault(client biliutil.HTTPDoer) biliutil.HTTPDoer {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func audioHTTPClientOrDefault(client biliutil.HTTPDoer) biliutil.HTTPDoer {
	if client != nil {
		return client
	}
	return &http.Client{Transport: newAudioTransport()}
}

func newAudioTransport() *http.Transport {
	base, _ := http.DefaultTransport.(*http.Transport)
	if base == nil {
		return &http.Transport{ForceAttemptHTTP2: false}
	}
	transport := base.Clone()
	transport.ForceAttemptHTTP2 = false
	return transport
}

func writeSuccessfulBody(resp *http.Response, tmpPath string, targetPath string) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create temp audio: %w", err)
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temp audio: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temp audio: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("replace audio: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func extractNativeBVID(sourceURL string) string {
	return nativeBVPattern.FindString(strings.TrimSpace(sourceURL))
}
