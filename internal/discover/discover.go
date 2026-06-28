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
