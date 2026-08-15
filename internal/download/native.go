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
	"sync"
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
	// PerURLStallSeconds 控制单 URL 的「无进度超时」：持续收到字节即重置，连续 N 秒无字节才切 backupUrl。
	// <=0 用默认（defaultPerURLStallTimeout = 60s）。2026-07-31 修正：取代原固定 5 分钟总时长超时（误掐长视频）。
	PerURLStallSeconds int
	// PerURLMaxMinutes 控制单 URL 的「总时长兜底」：<0=不限，0=用默认（4h），>0=对应分钟数。
	PerURLMaxMinutes int
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
	if selected, ok := biliutil.ExtractVideoPart(sourceURL); ok {
		for i, page := range info.Pages {
			pageIndex := page.Page
			if pageIndex <= 0 {
				pageIndex = i + 1
			}
			if pageIndex == selected {
				return d.downloadSingleP(ctx, rawDir, cookieHeader, info, page)
			}
		}
		return fmt.Errorf("multi-P page %d does not exist", selected)
	}
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
		durSecs, err := probeDuration(ctx, d.FFprobe, result.audio)
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

	if err := concatAudio(ctx, d.FFmpeg, concatListPath, filepath.Join(rawDir, "audio.m4a")); err != nil {
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

// defaultPerURLStallTimeout 是单 URL 的「无进度超时」默认值：
// 只要持续收到字节就重置计时器，不限总时长；连续该时长收不到任何字节才判定节点卡死，
// 切下一个 URL（baseUrl → backupUrl）。
//
// 设计意图（2026-07-31 修正）：原实现用固定 5 分钟「总时长」超时，目的是防 CDN 节点不响应卡死，
// 但误伤了正常的长视频传输（直播回放可达数小时、上百 MB，本机带宽 ~140KB/s 时需十几分钟，
// 必然 5 分钟超时失败）。正确语义是「无进度」而非「总时长」——持续有数据流入即视为健康。
const defaultPerURLStallTimeout = 60 * time.Second

// defaultPerURLMaxTimeout 是单 URL 的「总时长兜底」默认值，防止极端情况下无限挂死。
// 0 表示不限（依赖父任务 context 控制总寿命）；默认兜底设一个远大于任何合法直播回放的值。
const defaultPerURLMaxTimeout = 4 * time.Hour

// errStalled 表示下载过程中持续无数据流入（节点卡死），应由调用方切换 backupUrl 重试。
var errStalled = errors.New("audio download stalled: no data received within stall timeout")

// effectiveStallTimeout 返回生效的无进度超时（PerURLStallSeconds<=0 用默认）。
// 配置值有上界保护:超过 1 小时截断,防止 int→Duration 溢出为负数导致 timer 立即触发。
func (d NativeDownloader) effectiveStallTimeout() time.Duration {
	if d.PerURLStallSeconds > 0 {
		secs := d.PerURLStallSeconds
		if secs > 3600 {
			secs = 3600
		}
		return time.Duration(secs) * time.Second
	}
	return defaultPerURLStallTimeout
}

// effectiveMaxTimeout 返回生效的单 URL 总时长兜底（PerURLMaxMinutes<0=不限，0 或正数=对应时长）。
// 配置值有上界保护:超过 30 天截断,防溢出。
func (d NativeDownloader) effectiveMaxTimeout() time.Duration {
	switch {
	case d.PerURLMaxMinutes < 0:
		return 0 // 不限
	case d.PerURLMaxMinutes > 0:
		mins := d.PerURLMaxMinutes
		if mins > 30*24*60 {
			mins = 30 * 24 * 60
		}
		return time.Duration(mins) * time.Minute
	default:
		return defaultPerURLMaxTimeout
	}
}

func (d NativeDownloader) downloadAudio(ctx context.Context, urls []string, cookie string, targetPath string) error {
	tmpPath := targetPath + ".tmp"
	stallTimeout := d.effectiveStallTimeout()
	maxTimeout := d.effectiveMaxTimeout()
	var lastErr error
	for _, rawURL := range urls {
		_ = os.Remove(tmpPath)
		// 单 URL 超时策略：无进度超时（stall）为主 + 可选总时长兜底。
		//   - stall：持续 stallTimeout 无字节 → 切 backupUrl（防节点卡死，不误伤长传输）。
		//   - max：超过 maxTimeout → 切 backupUrl（极端兜底，默认 4h，可配 0/-1 关闭）。
		var reqCtx context.Context
		var cancel context.CancelFunc
		if maxTimeout > 0 {
			reqCtx, cancel = context.WithTimeout(ctx, maxTimeout)
		} else {
			reqCtx, cancel = context.WithCancel(ctx)
		}
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
		writeErr := writeSuccessfulBody(resp, tmpPath, targetPath, stallTimeout, cancel)
		cancel()
		if writeErr != nil {
			_ = os.Remove(tmpPath)
			lastErr = writeErr
			continue
		}
		return nil
	}
	if lastErr != nil {
		// 用两个 %w 保留原始 cause 链:上层可用 errors.Is(err, errStalled) /
		// errors.Is(err, context.DeadlineExceeded) 判定,而非脆弱的字符串匹配。
		return fmt.Errorf("%w: %w", ErrAudioDownloadFailed, lastErr)
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

// writeSuccessfulBody 把响应体写入临时文件再原子替换。
// stallTimeout > 0 时启用无进度超时：持续收到字节即重置计时器，连续 stallTimeout 无字节
// 触发 onCancel 中断读取并返回 errStalled（调用方据此切 backupUrl）。
// stallTimeout <= 0 时退化为普通 io.Copy（无 stall 检测，由外层 context 控制总寿命）。
func writeSuccessfulBody(resp *http.Response, tmpPath string, targetPath string, stallTimeout time.Duration, onCancel context.CancelFunc) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create temp audio: %w", err)
	}

	var copyErr error
	if stallTimeout > 0 {
		reader := newStallReader(resp.Body, stallTimeout, onCancel)
		_, copyErr = io.Copy(file, reader)
		// 无论成功失败,都显式停止 stall 计时器,避免本地写入错误提前退出后
		// timer 延迟触发 onCancel 污染错误语义(Codex #4)。
		reader.stop()
	} else {
		_, copyErr = io.Copy(file, resp.Body)
	}
	if copyErr != nil {
		_ = file.Close()
		if errors.Is(copyErr, errStalled) {
			return copyErr // 已是语义化错误，直接返回
		}
		return fmt.Errorf("write temp audio: %w", copyErr)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temp audio: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("replace audio: %w", err)
	}
	return nil
}

// stallReader 包裹一个 io.Reader，实现「无进度超时」：
// 每次 Read 收到任意字节即更新 lastProgress；stallTimeout 内无进度才触发 onCancel 并返回 errStalled。
// 只要数据持续流入（哪怕是慢速长传输），就不会超时——这才是「防节点卡死」的正确语义。
//
// 并发设计（缩小 AfterFunc 边界竞态窗口）：
//   - fire 不直接判定，而是二次确认「距上次进度是否真的 ≥ stall」，缩小（但不完全消除）
//     timer 回调与 Read 更新 lastProgress 交错时的误判窗口。若要求严格语义，需改用
//     watcher/progress 协议；当前实现对正常进度足够稳健。
//   - timer.Reset 与 timer.Stop 都在持有 mu 时完成，使 rearm 与 stop 串行化，
//     避免 stop 后旧 fire 回调仍 rearm 导致 timer 残留。
type stallReader struct {
	r            io.Reader
	stall        time.Duration
	onStall      context.CancelFunc
	timer        *time.Timer
	mu           sync.Mutex
	stopped      bool
	lastProgress time.Time
}

func newStallReader(r io.Reader, stall time.Duration, onCancel context.CancelFunc) *stallReader {
	now := time.Now()
	sr := &stallReader{
		r:            r,
		stall:        stall,
		onStall:      onCancel,
		lastProgress: now,
	}
	sr.timer = time.AfterFunc(stall, sr.fire)
	return sr
}

// fire 是 stall 计时器回调：二次确认无进度后再触发 onCancel。
func (s *stallReader) fire() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	// 二次确认:距上次进度是否真的 ≥ stall。若进度正常(Read 刚更新过 lastProgress),
	// 在锁内重新调度下一次检查,而非立即判定 stall。
	if elapsed := time.Since(s.lastProgress); elapsed < s.stall {
		remain := s.stall - elapsed
		// 持锁 Reset:与 stop() 的 Stop 串行化,避免解锁后 stop 又被旧回调 rearm。
		s.timer.Reset(remain)
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()
	if s.onStall != nil {
		s.onStall()
	}
}

// stop 显式停止计时器,标记结束。writeSuccessfulBody 返回前调用,避免 io.Copy 因本地错误
// 提前退出后 timer 仍在跑、延迟触发 onCancel 污染错误语义。
func (s *stallReader) stop() {
	s.mu.Lock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		// 收到数据,更新进度时间戳(喂狗)
		s.mu.Lock()
		if !s.stopped {
			s.lastProgress = time.Now()
		}
		s.mu.Unlock()
	}
	if err != nil {
		// 若 stall 已触发（fire 设了 stopped），优先返回语义化错误 errStalled。
		// 否则 onCancel 中断底层连接会产生 "use of closed network connection" 等噪声错误，
		// 调用方无法识别为"超时换 backupUrl"语义。
		s.mu.Lock()
		stalled := s.stopped
		s.stopped = true
		s.mu.Unlock()
		if stalled {
			return n, errStalled
		}
	}
	return n, err
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
