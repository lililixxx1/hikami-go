package discover

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/download"
	"hikami-go/internal/executil"
	"hikami-go/internal/session"
	"hikami-go/internal/worker"
)

type Entry struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	WebpageURL string `json:"webpage_url"`
}

type Lister interface {
	List(ctx context.Context, sourceURL string, cookieFile string) ([]Entry, error)
}

// TitleResolver 按 channelID + sourceID 解析视频真实标题。
// 空标题时 discover 调用它取真实标题；失败时实现应返回 sourceID 作为兜底。
// 由 download.Handler 实现，通过 WithTitleResolver option 注入。
type TitleResolver interface {
	ResolveDownloadTitle(ctx context.Context, channelID, sourceID string) string
}

type YTDLPLister struct {
	Command string
}

func (l YTDLPLister) List(ctx context.Context, sourceURL string, cookieFile string) ([]Entry, error) {
	command := l.Command
	if command == "" {
		command = "yt-dlp"
	}
	args := []string{"--dump-json", "--flat-playlist"}
	if cookieFile != "" {
		args = append([]string{"--cookies", cookieFile}, args...)
	}
	args = append(args, sourceURL)
	cmd := exec.CommandContext(ctx, command, args...)
	executil.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("yt-dlp discover failed: %w: %s", err, string(exitErr.Stderr))
		}
		return nil, err
	}

	var entries []Entry
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, err
		}
		if entry.ID == "" {
			continue
		}
		entries = append(entries, normalizeEntry(entry))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

type Manager struct {
	channels       *channel.Store
	sessions       *session.Store
	workers        *worker.Pool
	lister         Lister
	titleResolver  TitleResolver                // 可选，nil 时不解析空标题
	cookieAccounts *biliutil.CookieAccountStore // 可选,nil 时发现阶段不走账号池(旧行为)
	outputRoot     string                       // 可选,临时 cookie 文件目录(默认 ".")
}

// Option 配置 Manager 的可选依赖。
type Option func(*Manager)

// WithTitleResolver 注入标题解析器，使 discover 在 yt-dlp --flat-playlist
// 返回空标题时通过 B站 view API 取真实标题。
func WithTitleResolver(r TitleResolver) Option {
	return func(m *Manager) { m.titleResolver = r }
}

// WithCookieAccountStore 注入 B 站账号池,使发现阶段在用户未显式指定 cookie 时,
// 自动回退到账号池:
//   - URL 模式(Preview):用全局默认下载账号的 cookie(明文临时文件给 yt-dlp)
//   - 频道模式(PreviewChannel/DiscoverChannel):走 ResolveCookie 三级链
//     (频道账号覆盖 → 全局默认 → channel.DownloadCookieFile legacy)
//
// 账号池落盘的 cookie 文件可能是加密的(HIKAMI_V1),yt-dlp 读不了,
// 所以 helper 内部会 LoadCookie 解密到内存 + 写明文临时文件(详见 cookie.go)。
//
// 语义对齐 download.Handler.SetCookieAccountStore(main.go:249)。
func WithCookieAccountStore(store *biliutil.CookieAccountStore) Option {
	return func(m *Manager) { m.cookieAccounts = store }
}

// WithOutputRoot 注入输出根目录,用于写临时 cookie 文件(<outputRoot>/.cookies/bilibili/)。
// 默认 "."(当前工作目录)。下载阶段从 cfg.OutputRoot 取,发现阶段用此 option 显式注入,
// 避免 Manager 持有整个 *config.Config(最小依赖原则)。
func WithOutputRoot(root string) Option {
	return func(m *Manager) { m.outputRoot = root }
}

type Result struct {
	ChannelID string `json:"channel_id"`
	SessionID string `json:"session_id"`
	SourceID  string `json:"source_id"`
	Title     string `json:"title"`
	SourceURL string `json:"source_url,omitempty"`
	Created   bool   `json:"created"`
	TaskID    string `json:"task_id,omitempty"`
	Error     string `json:"error,omitempty"`
	// Exists 标注该 source 是否已建过 download 场次（仅 PreviewAll 填充）。
	// 用于前端预览阶段标记「已处理」项，默认不勾选（CreateDownload 幂等，勾选也不会重复下载）。
	Exists bool `json:"exists"`
}

// ExecuteItem 是前端从预览结果里勾选后回传给 Execute 的单项。
// Execute 不重跑 yt-dlp——直接用前端已得的 entry 信息建场次+入队。
type ExecuteItem struct {
	ChannelID string `json:"channel_id"`
	SourceID  string `json:"source_id"`
	Title     string `json:"title"`
	SourceURL string `json:"source_url"`
}

func NewManager(channels *channel.Store, sessions *session.Store, workers *worker.Pool, lister Lister, options ...Option) *Manager {
	m := &Manager{
		channels: channels,
		sessions: sessions,
		workers:  workers,
		lister:   lister,
	}
	for _, opt := range options {
		opt(m)
	}
	return m
}

func (m *Manager) DiscoverAll(ctx context.Context) ([]Result, error) {
	channels, err := m.channels.ListVisible(ctx)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, item := range channels {
		if !item.Enabled || strings.TrimSpace(item.ReplaySourceURL) == "" {
			continue
		}
		if item.SourceMode == "live_only" {
			continue
		}
		channelResults, err := m.DiscoverChannel(ctx, item)
		if err != nil {
			results = append(results, Result{ChannelID: item.ID, Error: err.Error()})
			continue
		}
		results = append(results, channelResults...)
	}
	if results == nil {
		return []Result{}, nil
	}
	return results, nil
}

// PreviewAll 遍历所有启用且配了 ReplaySourceURL 的主播，列出会发现哪些回放但**不建场次、不入队**。
// 与 DiscoverAll 的区别：调 PreviewChannel 而非 DiscoverChannel，并额外查 session 表
// 为每条 Result 标注 Exists（是否已建过 download 场次），供前端预览阶段标记「已处理」。
// 同时复用 DiscoverChannel 的 discover_limit 语义：按频道原始顺序，仅保留前 DiscoverLimit 个
// 「新」（!Exists）项，超限的截断（与 DiscoverChannel 的 break 行为一致，避免两步流程绕过 limit
// 一次性下载超出配额的回放——codex 审核 P1）。
// 供两步式发现的「第一步预览」使用。
func (m *Manager) PreviewAll(ctx context.Context) ([]Result, error) {
	channels, err := m.channels.ListVisible(ctx)
	if err != nil {
		return nil, err
	}

	// 记录每个频道结果区间的起止下标，用于后续按频道做 limit 截断。
	type slice struct {
		channelID string
		limit     int
		start     int
		end       int // 不含
	}
	var spans []slice

	var results []Result
	for _, item := range channels {
		if !item.Enabled || strings.TrimSpace(item.ReplaySourceURL) == "" {
			continue
		}
		if item.SourceMode == "live_only" {
			continue
		}
		start := len(results)
		channelResults, err := m.PreviewChannel(ctx, item)
		if err != nil {
			// 频道级失败：错误项也纳入 span（作为不可计数项），避免后续 limit 过滤时被静默丢弃
			// 导致前端无法展示失败主播（codex 审核 P2）。
			results = append(results, Result{ChannelID: item.ID, Error: err.Error()})
		} else {
			results = append(results, channelResults...)
		}
		spans = append(spans, slice{channelID: item.ID, limit: item.DiscoverLimit, start: start, end: len(results)})
	}

	// 批量标注 Exists：一次性查出所有已存在的 (channel_id, source_id) 对，避免 N 次单查。
	if err := annotateExists(ctx, m.sessions, results); err != nil {
		// 标注失败不致命（前端最多把已处理项误判为新），降级返回不带 Exists 标记的结果。
		slog.Warn("discover preview: annotate exists failed", "error", err)
	}

	// 按 discover_limit 截断：完全镜像 DiscoverChannel 的 limit 语义。
	// DiscoverChannel 结构：每条 entry 处理时，先做 `if createdCount >= limit { break }`，
	// 再 CreateDownload 并在 created 时 createdCount++。即「累计新项数达 limit 后，下一条
	// entry（无论新旧）直接 break」。此处 PreviewAll 用相同结构：每项处理开头先检查
	// newCount >= limit，达限则该频道剩余所有项（含已存在项）全部丢弃（codex 审核 P2）。
	if len(spans) > 0 {
		filtered := make([]Result, 0, len(results))
		for _, sp := range spans {
			newCount := 0
			dropped := 0
			for i := sp.start; i < sp.end; i++ {
				r := results[i]
				// 镜像 DiscoverChannel：达限则 break（丢弃本项及后续所有）
				if sp.limit > 0 && newCount >= sp.limit {
					dropped += sp.end - i
					break
				}
				if !r.Exists && r.Error == "" {
					newCount++ // 新项计数（镜像 created 时 createdCount++）
				}
				filtered = append(filtered, r)
			}
			if dropped > 0 {
				slog.Info("discover preview truncated by limit", "channel_id", sp.channelID, "limit", sp.limit, "dropped", dropped)
			}
		}
		results = filtered
	}

	if results == nil {
		return []Result{}, nil
	}
	return results, nil
}

// Execute 按前端勾选的 entry 列表批量建 download 场次并入队下载任务。
// 不重跑 yt-dlp、不做 title_prefix/limit 过滤——这些在 PreviewChannel 阶段已由前端处理。
// 复用 session.CreateDownload 的幂等性：已存在的 source_id 返回 created=false 且不入队。
// 供两步式发现的「第二步执行」使用。
func (m *Manager) Execute(ctx context.Context, items []ExecuteItem) []Result {
	results := make([]Result, 0, len(items))
	for _, item := range items {
		title := m.resolveTitle(ctx, item.ChannelID, item.SourceID, item.Title)
		startedAt, ok := biliutil.ReplayDateFromTitle(title)
		if !ok {
			startedAt, _ = biliutil.ReplayDateFromTitle(item.Title)
		}
		result := Result{
			ChannelID: item.ChannelID,
			SourceID:  item.SourceID,
			Title:     title,
		}
		createdSession, created, err := m.sessions.CreateDownload(ctx, session.CreateDownloadInput{
			ChannelID: item.ChannelID,
			SourceID:  item.SourceID,
			Title:     title,
			SourceURL: item.SourceURL,
			StartedAt: startedAt,
		})
		if err != nil {
			result.Error = err.Error()
			slog.Info("discover execute skipped replay", "channel_id", item.ChannelID, "source_id", item.SourceID, "reason", "create_session_failed", "error", err.Error())
			results = append(results, result)
			continue
		}
		result.SessionID = createdSession.ID
		result.Created = created
		if !created {
			slog.Info("discover execute skipped replay", "channel_id", item.ChannelID, "source_id", item.SourceID, "session_id", createdSession.ID, "reason", "already_exists")
			results = append(results, result)
			continue
		}
		task, err := m.workers.Enqueue(ctx, worker.CreateInput{
			ChannelID: item.ChannelID,
			SessionID: createdSession.ID,
			Type:      download.TaskType,
			Payload:   "{}",
		})
		if err != nil {
			result.Error = err.Error()
		} else {
			result.TaskID = task.ID
		}
		slog.Info("discover execute accepted replay", "channel_id", item.ChannelID, "source_id", item.SourceID, "session_id", createdSession.ID, "task_id", result.TaskID, "title", title)
		results = append(results, result)
	}
	return results
}

// annotateExists 为 results 里每条标注 Exists（是否已建过 download 场次）。
// 对每个出现的 channel_id 做一次 IN 查询，避免 N 条结果 N 次查询。
func annotateExists(ctx context.Context, sessions *session.Store, results []Result) error {
	// 按 channel_id 分组收集 source_id
	byChannel := make(map[string][]string)
	for _, r := range results {
		if r.ChannelID != "" && r.SourceID != "" {
			byChannel[r.ChannelID] = append(byChannel[r.ChannelID], r.SourceID)
		}
	}
	if len(byChannel) == 0 {
		return nil
	}

	existingSet := make(map[string]bool) // key = channelID + "\x00" + sourceID
	db := sessions.DB()
	for channelID, sourceIDs := range byChannel {
		placeholders := make([]string, len(sourceIDs))
		args := make([]any, 0, len(sourceIDs)+1)
		args = append(args, channelID)
		for i, sid := range sourceIDs {
			placeholders[i] = "?"
			args = append(args, sid)
		}
		query := `SELECT source_id FROM sessions WHERE channel_id = ? AND source_type = 'download' AND source_id IN (` + strings.Join(placeholders, ",") + `)`
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sid string
			if err := rows.Scan(&sid); err != nil {
				_ = rows.Close()
				return err
			}
			existingSet[channelID+"\x00"+sid] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}

	for i := range results {
		key := results[i].ChannelID + "\x00" + results[i].SourceID
		results[i].Exists = existingSet[key]
	}
	return nil
}

// resolveTitle 对空标题做延迟解析：yt-dlp --flat-playlist 下 B站合集/系列的 title 经常为空，
// 此时通过 TitleResolver（由 download.Handler 实现）调 B站 view API 取真实标题。
// 无 resolver 或 resolver 返回空串时返回 sourceID（与 CreateDownload 的空标题兜底一致）。
func (m *Manager) resolveTitle(ctx context.Context, channelID, sourceID, currentTitle string) string {
	if strings.TrimSpace(currentTitle) != "" {
		return currentTitle
	}
	if m.titleResolver == nil {
		return sourceID
	}
	resolved := m.titleResolver.ResolveDownloadTitle(ctx, channelID, sourceID)
	if strings.TrimSpace(resolved) == "" {
		return sourceID
	}
	return resolved
}

func (m *Manager) DiscoverChannel(ctx context.Context, item channel.Channel) ([]Result, error) {
	cookieFile, cleanup := m.resolveChannelCookie(ctx, item.DownloadAccountID, item.DownloadCookieFile)
	if cleanup != nil {
		defer cleanup()
	}
	entries, err := m.lister.List(ctx, item.ReplaySourceURL, cookieFile)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(entries))
	createdCount := 0
	titlePrefix := strings.TrimSpace(item.TitlePrefix)
	for _, entry := range entries {
		// limit 检查在标题解析之前：已达上限直接 break，避免无意义的 view API 调用。
		if item.DiscoverLimit > 0 && createdCount >= item.DiscoverLimit {
			slog.Info("discover skipped replay", "channel_id", item.ID, "source_id", entry.ID, "reason", "discover_limit_reached", "title", entry.Title, "limit", item.DiscoverLimit)
			break
		}
		// title_prefix 匹配用原始标题（entry.Title）。
		// 注意：不能在 resolveTitle 之后匹配，因为 ResolveDownloadTitle 内部会调
		// CleanReplayTitle 剥掉「【直播回放】」前缀，清洗后的标题不再匹配 prefix。
		// entry.Title 为空时（--flat-playlist 常见），跳过 prefix 过滤——合集 URL
		// 本身已保证只有回放，prefix 是额外保险而非唯一过滤手段。
		if titlePrefix != "" && strings.TrimSpace(entry.Title) != "" && !matchAnyPrefix(entry.Title, titlePrefix) {
			slog.Info("discover skipped replay", "channel_id", item.ID, "source_id", entry.ID, "reason", "title_prefix_mismatch", "title", entry.Title, "title_prefix", titlePrefix)
			continue
		}
		title := m.resolveTitle(ctx, item.ID, entry.ID, entry.Title)
		startedAt, ok := biliutil.ReplayDateFromTitle(title)
		if !ok {
			startedAt, _ = biliutil.ReplayDateFromTitle(entry.Title)
		}
		// L14(2026-08-15):多 P 合集的 entry.ID 全是同一个 ID,SourceID 裸用
		// 会互相去重吞分 P;URL 带 ?p= 时追加 _pNNN 后缀,与
		// download.CreateFromURL 的 ExtractVideoSourceID 口径一致。锚定
		// entry.ID,但 yt-dlp 对多 P 的 entry.ID 剥掉 BV 前缀,此时锚定 URL 的
		// BV(SourceIDWithPart 内处理,2026-08-19);resolveTitle 仍用裸
		// entry.ID(view API 不认分 P 后缀)。
		sourceID := biliutil.SourceIDWithPart(entry.ID, entryURL(entry))
		createdSession, created, err := m.sessions.CreateDownload(ctx, session.CreateDownloadInput{
			ChannelID: item.ID,
			SourceID:  sourceID,
			Title:     title,
			SourceURL: entryURL(entry),
			StartedAt: startedAt,
		})
		result := Result{
			ChannelID: item.ID,
			SourceID:  sourceID,
			Title:     title,
			Created:   created,
		}
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			slog.Info("discover skipped replay", "channel_id", item.ID, "source_id", sourceID, "reason", "create_session_failed", "title", title, "error", err.Error())
			continue
		}
		result.SessionID = createdSession.ID
		if !created {
			slog.Info("discover accepted replay", "channel_id", item.ID, "source_id", sourceID, "session_id", createdSession.ID, "reason", "already_exists", "title", title, "created", false)
		}
		if created {
			createdCount++
			task, err := m.workers.Enqueue(ctx, worker.CreateInput{
				ChannelID: item.ID,
				SessionID: createdSession.ID,
				Type:      download.TaskType,
				Payload:   "{}",
			})
			if err != nil {
				result.Error = err.Error()
			} else {
				result.TaskID = task.ID
			}
		}
		results = append(results, result)
		if created {
			slog.Info("discover accepted replay", "channel_id", item.ID, "source_id", sourceID, "session_id", createdSession.ID, "task_id", result.TaskID, "title", title, "created", true)
		}
	}
	if results == nil {
		return []Result{}, nil
	}
	return results, nil
}

// PreviewChannel lists discovered replays for a channel without creating sessions.
// PreviewInput 是不绑定主播表的预览入参(2026-07-19 解耦改动,2026-07-25 改 cookie 字段)。
// 用于回顾管理·回放页「发现回放」的独立 URL 入口——用户直接粘贴 B 站收藏夹/合集/UP 主主页 URL,
// 不再依赖主播管理页的 channel 配置。
type PreviewInput struct {
	SourceURL   string // yt-dlp 输入 URL(B 站收藏夹/合集/UP 主主页)
	AccountID   *int64 // 可选,URL 模式显式选账号(nil/0 → 默认账号,详 cookie.go);与 channel.DownloadAccountID 语义一致(2026-07-25 替代 CookieFile)
	TitlePrefix string // 可选标题前缀过滤(逗号分隔,语义同 channel.TitlePrefix)
	ChannelID   string // 可选;空串时填 channel.UnassignedID。用于 annotateExists 去重键与 Result.ChannelID
}

// Preview 不绑定 channel 表的预览(2026-07-19)。
//
// 与 PreviewChannel 的区别:无需频道实体,SourceURL/AccountID/TitlePrefix 直接由调用方提供;
// ChannelID 为空时填 channel.UnassignedID。
// 内部:**自动调 annotateExists 标注 Exists 字段**(handler 不需要单独调),便于前端区分「新」「已处理」。
func (m *Manager) Preview(ctx context.Context, in PreviewInput) ([]Result, error) {
	if strings.TrimSpace(in.ChannelID) == "" {
		in.ChannelID = channel.UnassignedID
	}
	results, err := m.previewCore(ctx, in)
	if err != nil {
		return nil, err
	}
	// 标注 exists(codex r13b SUGGESTION:Preview 内部调,不暴露 annotate)。
	// 标注失败不致命(前端最多把已处理项误判为新),降级返回不带标记的结果。
	if err := annotateExists(ctx, m.sessions, results); err != nil {
		slog.Warn("discover preview: annotate exists failed", "error", err)
	}
	return results, nil
}

// previewCore 是 URL 模式(Preview/preview-by-url)的核心预览逻辑(不标注 exists)。
// 调用方负责后续的 annotateExists(Preview 内部标注一次)。
//
// Cookie 解析走 resolveURLCookie(显式 account_id 优先,详见 cookie.go);
// 与频道路径(previewCoreForChannel)隔离——2026-07-25 起 URL 模式也走 ResolveCookie(case 1
// 指定账号 / fallthrough 默认),但无 legacy fallback;频道模式有 legacy fallback。
func (m *Manager) previewCore(ctx context.Context, in PreviewInput) ([]Result, error) {
	cookieFile, cleanup := m.resolveURLCookie(ctx, in.AccountID)
	if cleanup != nil {
		defer cleanup()
	}
	return m.previewFromEntries(ctx, in, cookieFile)
}

// previewCoreForChannel 是频道模式(PreviewChannel)的核心预览逻辑(不标注 exists)。
// PreviewAll 在外层对聚合的所有频道结果做一次批量 annotateExists,避免每频道重复标注。
//
// Cookie 解析走 resolveChannelCookie(ResolveCookie 三级链,与下载链路对齐);
// 与 URL 路径(previewCore)隔离——v3 拆分,不再转发到 previewCore(codex r15b HIGH #2)。
func (m *Manager) previewCoreForChannel(ctx context.Context, item channel.Channel) ([]Result, error) {
	cookieFile, cleanup := m.resolveChannelCookie(ctx, item.DownloadAccountID, item.DownloadCookieFile)
	if cleanup != nil {
		defer cleanup()
	}
	return m.previewFromEntries(ctx, PreviewInput{
		SourceURL:   item.ReplaySourceURL,
		TitlePrefix: item.TitlePrefix,
		ChannelID:   item.ID,
	}, cookieFile)
}

// previewTitleConcurrency 是发现预览阶段并发解析标题(空标题回源 view API)的上限。
// 取值权衡(2026-07-29):B 站 -352 风控对短时高频请求敏感(live_record 冷却经验),
// 并发度过高会触发风控;过低则 38 条回放串行解析约 8-15s 仍有超 30s 风险。5 路有界并发
// 把 38 条压到约 3-5s,与保守的风控节奏相容。
const previewTitleConcurrency = 5

// previewFromEntries 共享的预览循环:title_prefix 过滤 + resolveTitle + Result 构造。
// 不涉及 cookie 解析(已在外层 previewCore/previewCoreForChannel 完成,传入 cookieFile)。
//
// 标题解析改为有界并发(2026-07-29):yt-dlp --flat-playlist 对合集页常返回空标题,
// 几乎每条都要回源 view API 取真实标题。串行下 38 条 × 单次 ~200-400ms 会撞前端 30s 超时。
// 改用 semaphore(channel)限流到 previewTitleConcurrency 路,结果按原始 index 写回保持顺序。
// title_prefix 过滤在并发前(用 entry 原始标题),仅 resolveTitle 走并发。
func (m *Manager) previewFromEntries(ctx context.Context, in PreviewInput, cookieFile string) ([]Result, error) {
	entries, err := m.lister.List(ctx, in.SourceURL, cookieFile)
	if err != nil {
		return nil, err
	}

	// 先做 title_prefix 过滤(用原始标题),得到待解析的子集及其原始 index(用于按序写回)。
	type pending struct {
		index int
		entry Entry
	}
	titlePrefix := strings.TrimSpace(in.TitlePrefix)
	var pendings []pending
	for i, entry := range entries {
		if titlePrefix != "" && strings.TrimSpace(entry.Title) != "" && !matchAnyPrefix(entry.Title, titlePrefix) {
			slog.Info("discover preview skipped replay", "channel_id", in.ChannelID, "source_id", entry.ID, "reason", "title_prefix_mismatch", "title", entry.Title, "title_prefix", titlePrefix)
			continue
		}
		pendings = append(pendings, pending{index: i, entry: entry})
	}

	// results 按 entries 原始 index 写回,保持前端展示顺序与串行版本一致。
	results := make([]Result, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, previewTitleConcurrency)
	for _, p := range pendings {
		// ctx 取消时不再启动新 goroutine(已启动的由 resolveTitle 内部 HTTP 自行收尾)。
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(p pending) {
			defer wg.Done()
			// 排队等槽位时也响应 ctx 取消(qoderclicn Minor#1):避免取消后仍阻塞到有槽位
			// 才退出,缩短取消延迟。取消则提前返回(results[p.index] 留零值,被后续零值过滤剔除)。
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			title := m.resolveTitle(ctx, in.ChannelID, p.entry.ID, p.entry.Title)
			// 审核 Minor:日志与 Result 用同一个 sourceID(含分 P 后缀),对账不脱节。
			sourceID := biliutil.SourceIDWithPart(p.entry.ID, entryURL(p.entry))
			slog.Info("discover preview accepted replay", "channel_id", in.ChannelID, "source_id", sourceID, "title", title)
			results[p.index] = Result{
				ChannelID: in.ChannelID,
				// L14:同 DiscoverChannel,SourceID 含分 P 后缀去重才不吞分 P。
				SourceID:  sourceID,
				Title:     title,
				SourceURL: entryURL(p.entry),
			}
		}(p)
	}
	wg.Wait()

	// 收集非零值结果(过滤后跳过的 index 处为零值 Result,需剔除),保持原始顺序。
	out := make([]Result, 0, len(pendings))
	for i := range results {
		// 只收 pendings 覆盖过的 index;跳过的 entry 留下零值,其 SourceID 为空。
		if results[i].SourceID != "" {
			out = append(out, results[i])
		}
	}
	if out == nil {
		return []Result{}, nil
	}
	return out, nil
}

// PreviewChannel 列出某个频道的回放(不建场次、不入队),供两步式发现的预览阶段使用。
// 2026-07-19 重构(v3):改走 previewCoreForChannel,使用 resolveChannelCookie(ResolveCookie 三级链),
// 与下载阶段 cookie 语义对齐。不再转发到 previewCore(URL 模式,codex r15b HIGH #2)。
// PreviewAll 在外层对聚合的所有频道结果做一次批量 annotateExists,避免每频道重复标注。
func (m *Manager) PreviewChannel(ctx context.Context, item channel.Channel) ([]Result, error) {
	return m.previewCoreForChannel(ctx, item)
}

func normalizeEntry(entry Entry) Entry {
	if entry.WebpageURL == "" && entry.URL != "" && strings.HasPrefix(entry.URL, "http") {
		entry.WebpageURL = entry.URL
	}
	if entry.WebpageURL == "" && strings.HasPrefix(entry.ID, "BV") {
		entry.WebpageURL = "https://www.bilibili.com/video/" + entry.ID
	}
	return entry
}

func entryURL(entry Entry) string {
	if entry.WebpageURL != "" {
		return entry.WebpageURL
	}
	if entry.URL != "" {
		return entry.URL
	}
	return "https://www.bilibili.com/video/" + entry.ID
}

// matchAnyPrefix 检查 title 是否以 prefixes（逗号分隔的多个前缀）中的任意一个开头
func matchAnyPrefix(title, prefixes string) bool {
	for _, p := range strings.Split(prefixes, ",") {
		p = strings.TrimSpace(p)
		if p != "" && strings.HasPrefix(title, p) {
			return true
		}
	}
	return false
}
