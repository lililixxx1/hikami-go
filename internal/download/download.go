package download

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/fsutil"
	"hikami-go/internal/normalize"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

const TaskType = "download"

// Downloader downloads replay audio and metadata into rawDir.
type Downloader interface {
	Download(ctx context.Context, sourceURL string, rawDir string, cookieFile string) error
}

// YTDLPDownloader implements Downloader using yt-dlp.
// It supports single-P and multi-P (playlist) Bilibili videos.
type YTDLPDownloader struct {
	Command string
	// FFprobe is the path to ffprobe binary, used for multi-P duration detection.
	FFprobe string
	// FFmpeg is the path to ffmpeg binary, used for multi-P concatenation.
	FFmpeg string
}

func (d YTDLPDownloader) Download(ctx context.Context, sourceURL string, rawDir string, cookieFile string) error {
	command := d.Command
	if command == "" {
		command = "yt-dlp"
	}

	// Check if this is a multi-P video by probing the playlist entries.
	entries, err := d.listPlaylist(ctx, command, sourceURL, cookieFile)
	if err != nil {
		slog.Warn("yt-dlp: failed to list playlist, falling back to single-P", "error", err)
		return d.downloadSingleP(ctx, command, sourceURL, rawDir, cookieFile)
	}

	if len(entries) <= 1 {
		// Single P or not a playlist -- use the existing single-P path.
		return d.downloadSingleP(ctx, command, sourceURL, rawDir, cookieFile)
	}

	// Multi-P download
	return d.downloadMultiP(ctx, command, sourceURL, rawDir, entries, cookieFile)
}

// ytDlpArgs 在 baseArgs 前面插入 --cookies cookieFile（当 cookieFile 非空时）。
func (d YTDLPDownloader) ytDlpArgs(cookieFile string, baseArgs ...string) []string {
	if cookieFile == "" {
		return baseArgs
	}
	return append([]string{"--cookies", cookieFile}, baseArgs...)
}

// playlistEntry represents a single entry from yt-dlp --dump-json --flat-playlist.
type playlistEntry struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	WebpageURL string `json:"webpage_url"`
	Index      int    `json:"playlist_index"`
}

type partDownloadResult struct {
	index int
	audio string
}

type partDuration struct {
	Index   int     `json:"index"`
	DurSecs float64 `json:"dur_secs"`
}

// listPlaylist queries yt-dlp for the playlist and returns the entries.
func (d YTDLPDownloader) listPlaylist(ctx context.Context, command, sourceURL string, cookieFile string) ([]playlistEntry, error) {
	args := d.ytDlpArgs(cookieFile,
		"--dump-json",
		"--flat-playlist",
		"--no-download",
		sourceURL,
	)
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp list playlist failed: %w", err)
	}

	var entries []playlistEntry
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry playlistEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.ID == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

// downloadSingleP downloads a single-P video using the original --no-playlist approach.
func (d YTDLPDownloader) downloadSingleP(ctx context.Context, command, sourceURL, rawDir string, cookieFile string) error {
	outputTemplate := filepath.Join(rawDir, "audio.%(ext)s")
	args := d.ytDlpArgs(cookieFile,
		"--no-playlist",
		"-x",
		"--audio-format", "m4a",
		"--write-info-json",
		"--write-thumbnail",
		"-o", outputTemplate,
		sourceURL,
	)
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("yt-dlp download failed: %w: %s", err, string(output))
	}
	if err := normalizeMetadataName(rawDir); err != nil {
		return err
	}
	normalizeCoverName(rawDir)
	return nil
}

// downloadMultiP downloads each part of a multi-P video, retrieves durations,
// concatenates audio, and downloads danmaku per part.
// Each part is downloaded into its own subdirectory under rawDir/parts/pNNN/.
func (d YTDLPDownloader) downloadMultiP(ctx context.Context, command, sourceURL, rawDir string, entries []playlistEntry, cookieFile string) error {
	partsDir := filepath.Join(rawDir, "parts")
	danmakuPartsDir := filepath.Join(rawDir, "danmaku_parts")
	metadataPartsDir := filepath.Join(rawDir, "metadata_parts")
	if err := os.MkdirAll(partsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(danmakuPartsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(metadataPartsDir, 0o755); err != nil {
		return err
	}

	var results []partDownloadResult

	// yt-dlp 的 info.json 不含 cid（BiliBian extractor 不输出），但弹幕 API 需要 cid。
	// 这里通过 view API 用 BV 号查出 pages[].cid，建 index→cid 映射供循环内弹幕下载使用。
	// 查询失败时 cidMap 为空，循环内逐 P 跳过弹幕（与 native 双失败降级一致）。
	cidMap := fetchCidMapForMultiP(ctx, sourceURL, cookieFile)

	for _, entry := range entries {
		// Each part gets its own subdirectory to avoid file collisions.
		partDir := filepath.Join(partsDir, fmt.Sprintf("p%03d", entry.Index))
		if err := os.MkdirAll(partDir, 0o755); err != nil {
			return err
		}

		outputTemplate := filepath.Join(partDir, "audio.%(ext)s")
		args := d.ytDlpArgs(cookieFile,
			"--no-playlist",
			"-x",
			"--audio-format", "m4a",
			"--write-info-json",
			"--write-comments",
			"--write-thumbnail",
			"-o", outputTemplate,
			entry.WebpageURL,
		)
		cmd := exec.CommandContext(ctx, command, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("yt-dlp download part %d failed: %w: %s", entry.Index, err, string(output))
		}

		// Find the downloaded audio file in the part subdirectory.
		audioPath, err := findAudioInDir(partDir)
		if err != nil {
			return fmt.Errorf("audio file not found for part %d: %w", entry.Index, err)
		}

		// Move info.json to metadata_parts/pNNN.info.json.
		if err := moveInfoJSON(partDir, metadataPartsDir, entry.Index); err != nil {
			return err
		}

		// Download danmaku for this part. yt-dlp 不下载弹幕，这里用循环前查到的
		// cid（view API 返回的 pages[].cid），复用 native 的弹幕下载逻辑（seg.so + XML 回退），
		// 写到 danmaku_parts/pNNN.xml（与 native 多 P 产物结构一致）。
		if cid, ok := cidMap[entry.Index]; ok {
			cookieHeader, _ := cookieHeaderFromCookieFile(cookieFile)
			xml := fetchDanmakuShared(ctx, nil, "", "", cid, cookieHeader)
			xmlPath := filepath.Join(danmakuPartsDir, fmt.Sprintf("p%03d.xml", entry.Index))
			if err := fsutil.WriteFileAtomic(xmlPath, xml, 0o644); err != nil {
				slog.Warn("write danmaku failed", "part", entry.Index, "error", err)
			}
		} else {
			slog.Warn("no cid for part, skip danmaku", "part", entry.Index)
		}

		results = append(results, partDownloadResult{
			index: entry.Index,
			audio: audioPath,
		})
	}

	// Sort by part index.
	sort.Slice(results, func(i, j int) bool {
		return results[i].index < results[j].index
	})

	var durations []partDuration
	concatListPath := filepath.Join(rawDir, "concat.list")
	f, err := os.Create(concatListPath)
	if err != nil {
		return fmt.Errorf("create concat list: %w", err)
	}
	defer f.Close()
	defer os.Remove(concatListPath)

	for _, r := range results {
		durSecs, err := probeDuration(d.FFprobe, r.audio)
		if err != nil {
			return fmt.Errorf("probe duration for part %d: %w", r.index, err)
		}
		durations = append(durations, partDuration{
			Index:   r.index,
			DurSecs: durSecs,
		})
		// TODO: 与 native 多 P 共用 ffconcat 路径转义 helper，处理单引号等特殊字符。
		// 写绝对路径：ffmpeg concat demuxer 以 listfile 所在目录解析相对条目，
		// OutputRoot 为相对路径时会叠加成 raw/raw/audio.m4a 导致打开失败。
		fmt.Fprintf(f, "file '%s'\n", escapeConcatListPath(r.audio))
	}
	f.Close()

	// Concatenate with ffmpeg concat demuxer.
	targetAudio := filepath.Join(rawDir, "audio.m4a")
	if err := concatAudio(d.FFmpeg, concatListPath, targetAudio); err != nil {
		return fmt.Errorf("concat multi-P audio: %w", err)
	}

	// Write part durations for use by normalize when merging danmaku.
	if err := fsutil.WriteJSONAtomic(filepath.Join(rawDir, "part_durations.json"), durations, 0o644); err != nil {
		return err
	}

	// Promote the first available thumbnail (across all parts) to raw/cover.<ext>
	// before the parts directory is removed. Cover is per-video, not per-P.
	normalizeCoverFromParts(partsDir, rawDir)

	// Clean up parts directory.
	os.RemoveAll(partsDir)

	return nil
}

// probeDuration uses ffprobe to get the duration in seconds of an audio file.
func probeDuration(ffprobe string, audioPath string) (float64, error) {
	probeCmd := ffprobe
	if probeCmd == "" {
		probeCmd = "ffprobe"
	}
	cmd := exec.Command(probeCmd,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		audioPath,
	)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration failed: %w", err)
	}
	return strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
}

// concatAudio uses ffmpeg concat demuxer to merge audio parts.
func concatAudio(ffmpeg string, concatListPath, outputPath string) error {
	ffmpegCmd := ffmpeg
	if ffmpegCmd == "" {
		ffmpegCmd = "ffmpeg"
	}
	cmd := exec.Command(ffmpegCmd,
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-f", "concat",
		"-safe", "0",
		"-i", concatListPath,
		"-c", "copy",
		outputPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg concat failed: %w: %s", err, string(output))
	}
	return nil
}

// escapeConcatListPath renders a part path for an ffmpeg concat listfile.
//
// ffmpeg's concat demuxer resolves relative entries against the listfile's own
// directory (not the process CWD). When OutputRoot is relative (e.g. "./output"),
// writing the part path verbatim makes ffmpeg look for
// "<listfileDir>/<relativePart>" and double up the path, failing with
// "Impossible to open '...raw/...raw/audio.m4a'". We therefore absolutize the
// path first, then apply the standard single-quote escaping required by the
// concat demuxer syntax.
func escapeConcatListPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.ToSlash(p)
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `'`, `'\''`)
	return p
}

// findAudioInDir finds the first audio file in a directory.
// 用音频扩展名白名单（而非“非 json/xml”黑名单），避免 yt-dlp 的 --write-thumbnail
// 把缩略图写成 audio.jpg/audio.webp 时被误当成音频返回（会导致 ffprobe/concat 失败）。
func findAudioInDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "audio.") && isAudioExt(name) {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("no audio file found in %s", dir)
}

// isAudioExt 判断文件名是否为已知音频扩展名。
func isAudioExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".m4a", ".mp3", ".aac", ".flac", ".opus", ".ogg", ".wav", ".mka", ".wma":
		return true
	}
	return false
}

// moveInfoJSON moves a .info.json file from srcDir to dstDir with a pNNN prefix.
func moveInfoJSON(srcDir, dstDir string, index int) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read metadata dir for part %d: %w", index, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".info.json") {
			src := filepath.Join(srcDir, name)
			dst := filepath.Join(dstDir, fmt.Sprintf("p%03d.info.json", index))
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("move metadata for part %d: %w", index, err)
			}
			return nil
		}
	}
	return fmt.Errorf("metadata info json not found for part %d", index)
}

// fetchCidMapForMultiP 通过 view API 用 BV 号查出各分 P 的 cid，返回 page 序号(1-based)→cid 映射。
// yt-dlp 的 info.json 不含 cid（BiliBian extractor 不输出），而弹幕 API 需要 cid。
// 查询失败或无 BV 时返回空 map，调用方逐 P 跳过弹幕（与 native 双失败降级一致）。
func fetchCidMapForMultiP(ctx context.Context, sourceURL, cookieFile string) map[int]int64 {
	bvid := extractNativeBVID(sourceURL)
	if bvid == "" {
		slog.Warn("fetch cid map: no bvid in source url, skip danmaku for all parts", "source_url", sourceURL)
		return nil
	}
	cookieHeader, _ := cookieHeaderFromCookieFile(cookieFile)
	viewClient := biliutil.VideoClient{} // 用默认 httpClient + 默认 base url
	info, err := viewClient.Fetch(ctx, bvid, cookieHeader)
	if err != nil {
		slog.Warn("fetch cid map: view api failed, skip danmaku for all parts", "bvid", bvid, "error", err)
		return nil
	}
	m := make(map[int]int64, len(info.Pages))
	for _, p := range info.Pages {
		m[p.Page] = p.CID
	}
	return m
}

// cookieHeaderFromCookieFile 从 Netscape cookie 文件读出 Cookie header 字符串。
// cookieFile 为空时返回 ("", nil)——不阻断，让 seg.so/XML 弹幕接口在无 cookie 下降级（仍可拉到部分弹幕）。
func cookieHeaderFromCookieFile(cookieFile string) (string, error) {
	if strings.TrimSpace(cookieFile) == "" {
		return "", nil
	}
	cookie, err := biliutil.LoadCookie(cookieFile)
	if err != nil {
		return "", err
	}
	return cookie.CookieHeader(), nil
}

// Handler handles download tasks submitted to the worker pool.
type Handler struct {
	cfg                *config.Config
	sessions           *session.Store
	states             *state.Store
	workers            *worker.Pool
	downloader         Downloader
	channels           *channel.Store
	cookieAccountStore *biliutil.CookieAccountStore
}

func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store, workers *worker.Pool, downloader Downloader, channels *channel.Store) *Handler {
	return &Handler{
		cfg:        cfg,
		sessions:   sessions,
		states:     states,
		workers:    workers,
		downloader: downloader,
		channels:   channels,
	}
}

// SetCookieAccountStore 注入账号池，使下载支持账号化 cookie 解析
// （账号池 → 默认下载账号 → 主播 legacy download_cookie_file）。
// 未注入时退化为只用 ch.DownloadCookieFile（旧行为）。
func (h *Handler) SetCookieAccountStore(store *biliutil.CookieAccountStore) {
	h.cookieAccountStore = store
}

func (h *Handler) Register(pool *worker.Pool) {
	pool.Register(TaskType, h.HandleTask)
}

// CreateFromURL 接收用户粘贴的视频链接（如 B 站 BV 号），为指定主播创建下载场次并入队。
// 从 URL 解析视频 ID 作为去重键与 slug 来源；同一视频重复提交返回 ErrTaskConflict。
// 镜像 importer.CreateFromMultipart 的「创建场次 + 入队」形态。
func (h *Handler) CreateFromURL(ctx context.Context, channelID, rawURL string) (worker.Task, error) {
	channelID = strings.TrimSpace(channelID)
	rawURL = strings.TrimSpace(rawURL)
	if channelID == "" {
		return worker.Task{}, fmt.Errorf("%w: channel_id is required", session.ErrInvalid)
	}
	if rawURL == "" {
		return worker.Task{}, fmt.Errorf("%w: url is required", session.ErrInvalid)
	}
	sourceID := biliutil.ExtractVideoID(rawURL)
	cleanURL := biliutil.NormalizeSourceURL(rawURL)
	title := h.ResolveDownloadTitle(ctx, channelID, sourceID)
	createdSession, created, err := h.sessions.CreateDownload(ctx, session.CreateDownloadInput{
		ChannelID: channelID,
		SourceID:  sourceID,
		Title:     title,
		SourceURL: cleanURL,
	})
	if err != nil {
		return worker.Task{}, err
	}
	if !created {
		return worker.Task{}, fmt.Errorf("%w: session already exists for %s", worker.ErrTaskConflict, sourceID)
	}
	return h.workers.Enqueue(ctx, worker.CreateInput{
		ChannelID: channelID,
		SessionID: createdSession.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
}

// ResolveDownloadTitle 通过 view API 取视频真实标题并清洗为直播主题（如「晚上好」），
// 修复「用官方录播链接导入时标题变成 BV 号」的问题。取标题失败（风控/网络/无 cookie）时
// 退回 sourceID（BV 号），不阻断导入——与历史兜底行为一致，下游仍可正常跑流水线。
// cookie 解析复用 HandleTask 的策略：账号池（账号化配置 → 默认下载账号 → legacy 文件）→ 退化到频道配置。
// 导出方法，实现 discover.TitleResolver 接口。
func (h *Handler) ResolveDownloadTitle(ctx context.Context, channelID, sourceID string) string {
	cookieHeader := h.downloadCookieHeader(ctx, channelID)
	info, err := biliutil.FetchVideoInfo(ctx, sourceID, cookieHeader)
	if err != nil {
		slog.Warn("resolve download title: view api failed, fallback to source id",
			"channel_id", channelID, "source_id", sourceID, "error", err)
		return sourceID
	}
	cleaned := biliutil.CleanReplayTitle(info.Title)
	if cleaned == "" {
		return sourceID
	}
	slog.Info("resolve download title",
		"channel_id", channelID, "source_id", sourceID, "raw_title", info.Title, "title", cleaned)
	return cleaned
}

// downloadCookieHeader 解析频道下载用 cookie，返回 Cookie header 字符串（供 view API 使用）。
// 复用 HandleTask 的 cookie 解析优先级：账号池优先，退化到频道 DownloadCookieFile。失败返回空串。
func (h *Handler) downloadCookieHeader(ctx context.Context, channelID string) string {
	var cookieFile string
	var downloadAccountID *int64
	if ch, err := h.channels.Get(ctx, channelID); err == nil {
		cookieFile = ch.DownloadCookieFile
		downloadAccountID = ch.DownloadAccountID
	} else {
		slog.Warn("resolve download title: channel lookup failed",
			"channel_id", channelID, "error", err)
	}
	if h.cookieAccountStore != nil {
		if resolved, err := h.cookieAccountStore.ResolveCookie(ctx, nullInt64FromPtr(downloadAccountID), sql.NullInt64{}, "download", cookieFile); err == nil {
			return resolved.CookieHeader()
		} else if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
			slog.Warn("resolve download title: resolve cookie failed, try legacy file",
				"channel_id", channelID, "error", err)
		}
	}
	header, err := cookieHeaderFromCookieFile(cookieFile)
	if err != nil {
		slog.Warn("resolve download title: legacy cookie file failed",
			"channel_id", channelID, "cookie_file", cookieFile, "error", err)
		return ""
	}
	return header
}

func (h *Handler) Enqueue(ctx context.Context, sessionID string) (worker.Task, error) {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return worker.Task{}, err
	}
	return h.workers.Enqueue(ctx, worker.CreateInput{
		ChannelID: sessionInfo.ChannelID,
		SessionID: sessionInfo.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
}

func (h *Handler) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	sessionInfo, err := h.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	if _, err := h.states.Apply(ctx, task.SessionID, state.EventDownloadStarted, task.ID, ""); err != nil {
		return err
	}

	// 解析下载用 cookie：优先账号池（账号化配置 → 默认下载账号 → legacy 文件），
	// 退化到主播 ch.DownloadCookieFile（旧行为）。账号池返回的是内存 cookie，
	// 需落盘成 yt-dlp 可读的明文 Netscape 文件。
	var cookieFile string
	var downloadAccountID *int64
	ch, chErr := h.channels.Get(ctx, task.ChannelID)
	if chErr == nil {
		cookieFile = ch.DownloadCookieFile
		downloadAccountID = ch.DownloadAccountID
	} else {
		slog.Warn("channel lookup failed during download cookie resolution",
			"channel_id", task.ChannelID, "error", chErr)
	}
	if h.cookieAccountStore != nil {
		if resolved, err := h.cookieAccountStore.ResolveCookie(ctx, nullInt64FromPtr(downloadAccountID), sql.NullInt64{}, "download", cookieFile); err == nil {
			if p, wErr := writeTempCookieFile(h.cfg.OutputRoot, task.SessionID, resolved); wErr != nil {
				slog.Warn("failed to write temp cookie file for download, falling back to legacy",
					"session_id", task.SessionID, "error", wErr)
			} else {
				cookieFile = p
				defer os.Remove(p)
			}
		} else if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
			slog.Warn("resolve download cookie failed, falling back to legacy",
				"session_id", task.SessionID, "error", err)
		}
	}

	sessionDir := filepath.Join(h.cfg.OutputRoot, task.ChannelID, sessionInfo.Slug)
	rawDir := filepath.Join(sessionDir, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return err
	}
	if err := reporter.Progress(ctx, 10, "downloading replay audio"); err != nil {
		return err
	}
	slog.Info("download started",
		"channel_id", task.ChannelID,
		"session_id", task.SessionID,
		"url", sessionInfo.SourceURL,
		"output_path", filepath.ToSlash(rawDir))
	if err := h.downloader.Download(ctx, sessionInfo.SourceURL, rawDir, cookieFile); err != nil {
		return err
	}
	slog.Info("download completed",
		"channel_id", task.ChannelID,
		"session_id", task.SessionID,
		"file_size", dirSize(rawDir))
	if err := reporter.Progress(ctx, 80, "replay media downloaded"); err != nil {
		return err
	}
	if _, err := h.states.Apply(ctx, task.SessionID, state.EventDownloadSucceeded, task.ID, ""); err != nil {
		return err
	}
	_, err = h.workers.Enqueue(ctx, worker.CreateInput{
		ChannelID: task.ChannelID,
		SessionID: task.SessionID,
		Type:      normalize.TaskType,
		Payload:   "{}",
	})
	return err
}

// nullInt64FromPtr 将 *int64 转为 sql.NullInt64（nil → 无效）。
// 复刻自 live_record/manager.go，供 ResolveCookie 接收主播账号覆盖。
func nullInt64FromPtr(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Valid: true, Int64: *value}
}

// writeTempCookieFile 把账号池解析出的内存 cookie 写成 yt-dlp 可读的明文
// Netscape cookie 文件（账号池落盘是加密的，yt-dlp 无法读取），返回临时文件路径。
// 调用方负责在下载完成后 os.Remove 该文件。
func writeTempCookieFile(outputRoot, sessionID string, cookie *biliutil.BiliCookie) (string, error) {
	if cookie == nil {
		return "", fmt.Errorf("cookie is nil")
	}
	dir := filepath.Join(outputRoot, ".cookies", "bilibili")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cookie dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("ytdlp_%s.txt", sessionID))
	if err := os.WriteFile(path, cookie.NetscapeBytes(), 0o600); err != nil {
		return "", fmt.Errorf("write temp cookie file: %w", err)
	}
	return path, nil
}

func normalizeMetadataName(rawDir string) error {
	target := filepath.Join(rawDir, "metadata.ytdlp.json")
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".info.json") {
			return os.Rename(filepath.Join(rawDir, name), target)
		}
	}
	return nil
}

// normalizeCoverName 把 yt-dlp 写下的缩略图（文件名通常含视频标题）规范化为
// rawDir/cover.<ext>，供 publisher 的 findCoverImage 命中。已是 cover.* 则不动。
// 仅在 rawDir 不存在 cover.* 时搬移首个图片缩略图；找不到则静默跳过（封面非关键）。
func normalizeCoverName(rawDir string) {
	if coverExists(rawDir) {
		return
	}
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "cover") {
			continue
		}
		if ext, ok := thumbnailExt(name); ok {
			src := filepath.Join(rawDir, name)
			dst := filepath.Join(rawDir, "cover"+ext)
			if err := os.Rename(src, dst); err != nil {
				slog.Warn("normalize cover name failed", "src", src, "error", err)
				return
			}
			slog.Info("cover normalized", "from", name, "to", filepath.Base(dst))
			return
		}
	}
}

// normalizeCoverFromParts 从多 P 的 partsDir 各 part 子目录里取首个缩略图，
// 规范化搬到 rawDir/cover.<ext>。仅当 rawDir 无 cover.* 时执行（封面不分 P）。
func normalizeCoverFromParts(partsDir, rawDir string) {
	if coverExists(rawDir) {
		return
	}
	parts, err := os.ReadDir(partsDir)
	if err != nil {
		return
	}
	for _, part := range parts {
		if !part.IsDir() {
			continue
		}
		partDir := filepath.Join(partsDir, part.Name())
		files, err := os.ReadDir(partDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			if ext, ok := thumbnailExt(f.Name()); ok {
				src := filepath.Join(partDir, f.Name())
				dst := filepath.Join(rawDir, "cover"+ext)
				if err := os.Rename(src, dst); err != nil {
					slog.Warn("normalize cover from parts failed", "src", src, "error", err)
					return
				}
				slog.Info("cover promoted from part", "from", filepath.Join(part.Name(), f.Name()))
				return
			}
		}
	}
}

// coverExists 检查 dir 下是否已存在 cover.{png,jpg,jpeg,webp}。
func coverExists(dir string) bool {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		if _, err := os.Stat(filepath.Join(dir, "cover"+ext)); err == nil {
			return true
		}
	}
	return false
}

// thumbnailExt 判断文件名是否为图片缩略图，返回归一化扩展名（.png/.jpg/.webp）。
func thumbnailExt(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return ".png", true
	case ".jpg", ".jpeg":
		return ".jpg", true
	case ".webp":
		return ".webp", true
	}
	return "", false
}

func dirSize(root string) int64 {
	var size int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}
