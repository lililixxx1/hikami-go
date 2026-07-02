package discover

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"hikami-go/internal/channel"
	"hikami-go/internal/download"
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
	channels *channel.Store
	sessions *session.Store
	workers  *worker.Pool
	lister   Lister
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

func NewManager(channels *channel.Store, sessions *session.Store, workers *worker.Pool, lister Lister) *Manager {
	return &Manager{
		channels: channels,
		sessions: sessions,
		workers:  workers,
		lister:   lister,
	}
}

func (m *Manager) DiscoverAll(ctx context.Context) ([]Result, error) {
	channels, err := m.channels.List(ctx)
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
	channels, err := m.channels.List(ctx)
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
		result := Result{
			ChannelID: item.ChannelID,
			SourceID:  item.SourceID,
			Title:     item.Title,
		}
		createdSession, created, err := m.sessions.CreateDownload(ctx, session.CreateDownloadInput{
			ChannelID: item.ChannelID,
			SourceID:  item.SourceID,
			Title:     item.Title,
			SourceURL: item.SourceURL,
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
		slog.Info("discover execute accepted replay", "channel_id", item.ChannelID, "source_id", item.SourceID, "session_id", createdSession.ID, "task_id", result.TaskID, "title", item.Title)
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

func (m *Manager) DiscoverChannel(ctx context.Context, item channel.Channel) ([]Result, error) {
	entries, err := m.lister.List(ctx, item.ReplaySourceURL, item.DownloadCookieFile)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(entries))
	createdCount := 0
	titlePrefix := strings.TrimSpace(item.TitlePrefix)
	for _, entry := range entries {
		if titlePrefix != "" && !matchAnyPrefix(entry.Title, titlePrefix) {
			slog.Info("discover skipped replay", "channel_id", item.ID, "source_id", entry.ID, "reason", "title_prefix_mismatch", "title", entry.Title, "title_prefix", titlePrefix)
			continue
		}
		if item.DiscoverLimit > 0 && createdCount >= item.DiscoverLimit {
			slog.Info("discover skipped replay", "channel_id", item.ID, "source_id", entry.ID, "reason", "discover_limit_reached", "title", entry.Title, "limit", item.DiscoverLimit)
			break
		}
		createdSession, created, err := m.sessions.CreateDownload(ctx, session.CreateDownloadInput{
			ChannelID: item.ID,
			SourceID:  entry.ID,
			Title:     entry.Title,
			SourceURL: entryURL(entry),
		})
		result := Result{
			ChannelID: item.ID,
			SourceID:  entry.ID,
			Title:     entry.Title,
			Created:   created,
		}
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			slog.Info("discover skipped replay", "channel_id", item.ID, "source_id", entry.ID, "reason", "create_session_failed", "title", entry.Title, "error", err.Error())
			continue
		}
		result.SessionID = createdSession.ID
		if !created {
			slog.Info("discover accepted replay", "channel_id", item.ID, "source_id", entry.ID, "session_id", createdSession.ID, "reason", "already_exists", "title", entry.Title, "created", false)
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
			slog.Info("discover accepted replay", "channel_id", item.ID, "source_id", entry.ID, "session_id", createdSession.ID, "task_id", result.TaskID, "title", entry.Title, "created", true)
		}
	}
	if results == nil {
		return []Result{}, nil
	}
	return results, nil
}

// PreviewChannel lists discovered replays for a channel without creating sessions.
func (m *Manager) PreviewChannel(ctx context.Context, item channel.Channel) ([]Result, error) {
	entries, err := m.lister.List(ctx, item.ReplaySourceURL, item.DownloadCookieFile)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(entries))
	titlePrefix := strings.TrimSpace(item.TitlePrefix)
	for _, entry := range entries {
		if titlePrefix != "" && !matchAnyPrefix(entry.Title, titlePrefix) {
			slog.Info("discover preview skipped replay", "channel_id", item.ID, "source_id", entry.ID, "reason", "title_prefix_mismatch", "title", entry.Title, "title_prefix", titlePrefix)
			continue
		}
		slog.Info("discover preview accepted replay", "channel_id", item.ID, "source_id", entry.ID, "title", entry.Title)
		results = append(results, Result{
			ChannelID: item.ID,
			SourceID:  entry.ID,
			Title:     entry.Title,
			SourceURL: entryURL(entry),
		})
	}
	if results == nil {
		return []Result{}, nil
	}
	return results, nil
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
