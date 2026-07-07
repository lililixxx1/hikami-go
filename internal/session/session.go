package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"hikami-go/internal/state"
)

var (
	ErrNotFound    = errors.New("session not found")
	ErrInvalid     = errors.New("invalid session")
	ErrAlreadyLive = errors.New("live session already exists for this slot")
)

// isConstraintViolation 判断 SQLite 是否抛出约束冲突（UNIQUE / PRIMARY KEY / FOREIGN KEY 等）。
// modernc/sqlite 的错误消息形如 "constraint failed: UNIQUE ..." / "FOREIGN KEY constraint failed"。
// 仅用于决定是否进一步查 session 存在性来区分 UNIQUE 与其它约束，不做最终判定。
func isConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "constraint failed") || strings.Contains(msg, "UNIQUE constraint")
}

type Session struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	ChannelID      string `json:"channel_id"`
	SourceType     string `json:"source_type"`
	SourceID       string `json:"source_id"`
	Title          string `json:"title"`
	StartedAt      string `json:"started_at,omitempty"`
	EndedAt        string `json:"ended_at,omitempty"`
	SourceURL      string `json:"source_url"`
	Status         string `json:"status"`
	CurrentTaskID  string `json:"current_task_id,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	LocalAvailable bool   `json:"local_available"`
	UploadedAt     string `json:"uploaded_at,omitempty"`
	PublishedAt    string `json:"published_at,omitempty"`
	ArchivedAt     string `json:"archived_at,omitempty"`
	PublishTarget  string `json:"publish_target,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	ChannelName    string `json:"channel_name,omitempty"`
}

type CreateLiveInput struct {
	ChannelID string
	Title     string
	RoomID    int64
	StartedAt time.Time
}

type CreateDownloadInput struct {
	ChannelID string
	SourceID  string
	Title     string
	SourceURL string
	StartedAt time.Time
}

type CreateImportInput struct {
	ChannelID string
	Title     string
	StartedAt time.Time
	EndedAt   time.Time
	SourceURL string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) CreateLive(ctx context.Context, input CreateLiveInput) (Session, error) {
	if strings.TrimSpace(input.ChannelID) == "" {
		return Session{}, fmt.Errorf("%w: channel_id is required", ErrInvalid)
	}
	if strings.TrimSpace(input.Title) == "" {
		input.Title = "B站直播"
	}
	if input.RoomID <= 0 {
		return Session{}, fmt.Errorf("%w: room_id must be greater than 0", ErrInvalid)
	}
	if input.StartedAt.IsZero() {
		input.StartedAt = time.Now()
	}

	slugTime := input.StartedAt.Format("20060102_150405")
	id := fmt.Sprintf("%s_live_%s", input.ChannelID, slugTime)
	slug := "live_" + slugTime
	sourceID := fmt.Sprintf("live-%d-%s", input.RoomID, slugTime)
	sourceURL := fmt.Sprintf("https://live.bilibili.com/%d", input.RoomID)
	startedAt := input.StartedAt.Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id,
			slug,
			channel_id,
			source_type,
			source_id,
			title,
			started_at,
			source_url,
			status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, slug, input.ChannelID, "live_record", sourceID, input.Title, startedAt, sourceURL, state.StatusDiscovered)
	if err != nil {
		// 约束冲突：可能是 UNIQUE（同分钟槽 session 已存在）或 FK（channel 不存在）等。
		// 用字符串匹配 constraint failed 会把 FK 错误也误判为“已存在”（codex 审核），
		// 这里改为查目标 session 是否真实存在来区分：存在才是同槽重复，否则原样返回底层错误。
		if isConstraintViolation(err) {
			if existing, getErr := s.Get(ctx, id); getErr == nil && existing.ID == id {
				// 同 (channel, 分钟槽) UNIQUE 命中。
				// 不再自动复用/重置——历史上 failed 自动重置是 live_check 误触发复用旧 session
				// 的放大器（把 failed 拉回 discovered 后，新的录制任务把状态污染到 recording）。
				return Session{}, fmt.Errorf("%w: %s", ErrAlreadyLive, id)
			}
		}
		return Session{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) CreateDownload(ctx context.Context, input CreateDownloadInput) (Session, bool, error) {
	if strings.TrimSpace(input.ChannelID) == "" {
		return Session{}, false, fmt.Errorf("%w: channel_id is required", ErrInvalid)
	}
	if strings.TrimSpace(input.SourceID) == "" {
		return Session{}, false, fmt.Errorf("%w: source_id is required", ErrInvalid)
	}
	if strings.TrimSpace(input.Title) == "" {
		input.Title = input.SourceID
	}
	if input.StartedAt.IsZero() {
		input.StartedAt = time.Now()
	}

	slug := sanitizeSlug(input.SourceID)
	if slug == "" {
		slug = "download_" + input.StartedAt.Format("20060102_150405")
	}
	id := fmt.Sprintf("%s_download_%s", input.ChannelID, slug)
	startedAt := input.StartedAt.Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id,
			slug,
			channel_id,
			source_type,
			source_id,
			title,
			started_at,
			source_url,
			status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, slug, input.ChannelID, "download", input.SourceID, input.Title, startedAt, input.SourceURL, state.StatusDiscovered)
	if err != nil {
		if strings.Contains(err.Error(), "constraint failed") {
			existing, getErr := s.GetBySource(ctx, input.ChannelID, "download", input.SourceID)
			return existing, false, getErr
		}
		return Session{}, false, err
	}
	created, err := s.Get(ctx, id)
	return created, true, err
}

func (s *Store) CreateImport(ctx context.Context, input CreateImportInput) (Session, error) {
	if strings.TrimSpace(input.ChannelID) == "" {
		return Session{}, fmt.Errorf("%w: channel_id is required", ErrInvalid)
	}
	if strings.TrimSpace(input.Title) == "" {
		input.Title = "手动导入"
	}
	if input.StartedAt.IsZero() {
		input.StartedAt = time.Now()
	}

	slugTime := input.StartedAt.Format("20060102_150405")
	slug := "import_" + slugTime
	id := fmt.Sprintf("%s_import_%s", input.ChannelID, slugTime)
	sourceID := "import-" + slugTime
	startedAt := input.StartedAt.Format(time.RFC3339)
	endedAt := any(nil)
	if !input.EndedAt.IsZero() {
		endedAt = input.EndedAt.Format(time.RFC3339)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id,
			slug,
			channel_id,
			source_type,
			source_id,
			title,
			started_at,
			ended_at,
			source_url,
			status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, slug, input.ChannelID, "import", sourceID, input.Title, startedAt, endedAt, input.SourceURL, state.StatusDiscovered)
	if err != nil {
		return Session{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) List(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, listWithChannelSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		session, err := scanSessionWithChannel(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if sessions == nil {
		return []Session{}, nil
	}
	return sessions, nil
}

func (s *Store) Get(ctx context.Context, id string) (Session, error) {
	session, err := scanSessionCore(s.db.QueryRowContext(ctx, getSQL, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

func (s *Store) GetBySource(ctx context.Context, channelID string, sourceType string, sourceID string) (Session, error) {
	session, err := scanSessionCore(s.db.QueryRowContext(ctx, getBySourceSQL, channelID, sourceType, sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

func (s *Store) UpdateEndedAt(ctx context.Context, sessionID string, endedAt time.Time) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalid)
	}
	if endedAt.IsZero() {
		return fmt.Errorf("%w: ended_at is required", ErrInvalid)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET ended_at = ?, updated_at = ? WHERE id = ?
	`, endedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), sessionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetLocalAvailable 标记场次本地产物是否可用。上传清理策略删除本地目录后置为 false，
// 从 WebDAV 取回（Fetch）成功后置回 true。该标记驱动 glossary/recap/publisher 等读取
// 本地文件的操作是否需要先取回。
func (s *Store) SetLocalAvailable(ctx context.Context, sessionID string, available bool) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalid)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET local_available = ?, updated_at = ? WHERE id = ?
	`, available, time.Now().Format(time.RFC3339), sessionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetArchivedAt 标记场次已归档到 WebDAV 的时间戳。归档任务不推进 session 主状态（保持
// published），仅通过该时间戳体现「已归档」。同一 UPDATE 内清空 last_error：归档失败会经
// worker 特判写入 last_error，用户手动重试成功后必须清掉旧的归档错误，避免 UI 误导。
func (s *Store) SetArchivedAt(ctx context.Context, sessionID string, archivedAt time.Time) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalid)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET archived_at = ?, last_error = NULL, updated_at = ?
		WHERE id = ?
	`, archivedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), sessionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ActiveLiveForChannel(ctx context.Context, channelID string) (Session, bool, error) {
	row := s.db.QueryRowContext(ctx, activeLiveSQL, channelID, state.StatusRecording, state.StatusDiscovered, state.StatusDownloading, state.StatusImporting)
	session, err := scanSessionCore(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	return session, true, nil
}

// FindLiveSessionByTimeWindow looks for a live_record session for the same channel
// within the specified time window around the given startedAt time.
// Returns ErrNotFound if no matching session exists.
func (s *Store) FindLiveSessionByTimeWindow(ctx context.Context, channelID string, startedAt time.Time, window time.Duration) (Session, error) {
	windowSecs := int64(window.Seconds())
	startedAtEpoch := startedAt.Unix()
	row := s.db.QueryRowContext(ctx, findLiveByTimeWindowSQL, channelID, startedAtEpoch, windowSecs, startedAtEpoch)
	session, err := scanSessionCore(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

// FindDownloadByTimeWindow looks for a download session for the same channel
// within the specified time window around the given startedAt time.
// Returns ErrNotFound if no matching session exists.
func (s *Store) FindDownloadByTimeWindow(ctx context.Context, channelID string, startedAt time.Time, window time.Duration) (Session, error) {
	windowSecs := int64(window.Seconds())
	startedAtEpoch := startedAt.Unix()
	row := s.db.QueryRowContext(ctx, findDownloadByTimeWindowSQL, channelID, startedAtEpoch, windowSecs, startedAtEpoch)
	session, err := scanSessionCore(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSessionCore(row scanner) (Session, error) {
	var session Session
	var startedAt sql.NullString
	var endedAt sql.NullString
	var currentTaskID sql.NullString
	var lastError sql.NullString
	var uploadedAt sql.NullString
	var publishedAt sql.NullString
	var archivedAt sql.NullString
	var publishTarget sql.NullString
	var localAvailable int
	err := row.Scan(
		&session.ID,
		&session.Slug,
		&session.ChannelID,
		&session.SourceType,
		&session.SourceID,
		&session.Title,
		&startedAt,
		&endedAt,
		&session.SourceURL,
		&session.Status,
		&currentTaskID,
		&lastError,
		&localAvailable,
		&uploadedAt,
		&publishedAt,
		&archivedAt,
		&publishTarget,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	session.StartedAt = startedAt.String
	session.EndedAt = endedAt.String
	session.CurrentTaskID = currentTaskID.String
	session.LastError = lastError.String
	session.LocalAvailable = localAvailable != 0
	session.UploadedAt = uploadedAt.String
	session.PublishedAt = publishedAt.String
	session.ArchivedAt = archivedAt.String
	session.PublishTarget = publishTarget.String
	return session, err
}

func scanSessionWithChannel(row scanner) (Session, error) {
	var session Session
	var startedAt sql.NullString
	var endedAt sql.NullString
	var currentTaskID sql.NullString
	var lastError sql.NullString
	var uploadedAt sql.NullString
	var publishedAt sql.NullString
	var archivedAt sql.NullString
	var publishTarget sql.NullString
	var localAvailable int
	// channel_name is inserted right after channel_id (19 columns total).
	err := row.Scan(
		&session.ID,
		&session.Slug,
		&session.ChannelID,
		&session.ChannelName,
		&session.SourceType,
		&session.SourceID,
		&session.Title,
		&startedAt,
		&endedAt,
		&session.SourceURL,
		&session.Status,
		&currentTaskID,
		&lastError,
		&localAvailable,
		&uploadedAt,
		&publishedAt,
		&archivedAt,
		&publishTarget,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	session.StartedAt = startedAt.String
	session.EndedAt = endedAt.String
	session.CurrentTaskID = currentTaskID.String
	session.LastError = lastError.String
	session.LocalAvailable = localAvailable != 0
	session.UploadedAt = uploadedAt.String
	session.PublishedAt = publishedAt.String
	session.ArchivedAt = archivedAt.String
	session.PublishTarget = publishTarget.String
	return session, err
}

func sanitizeSlug(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

const selectColumns = `
	id,
	slug,
	channel_id,
	source_type,
	source_id,
	title,
	started_at,
	ended_at,
	source_url,
	status,
	current_task_id,
	last_error,
		local_available,
		uploaded_at,
		published_at,
		archived_at,
		publish_target,
		created_at,
		updated_at
`

const getSQL = `SELECT ` + selectColumns + ` FROM sessions WHERE id = ?`
const listWithChannelSQL = `SELECT
		s.id,
		s.slug,
		s.channel_id,
		COALESCE(c.name, '') AS channel_name,
		s.source_type,
		s.source_id,
		s.title,
		s.started_at,
		s.ended_at,
		s.source_url,
		s.status,
		s.current_task_id,
		s.last_error,
		s.local_available,
		s.uploaded_at,
		s.published_at,
		s.archived_at,
		s.publish_target,
		s.created_at,
		s.updated_at
	FROM sessions s
	LEFT JOIN channels c ON s.channel_id = c.id
	ORDER BY s.created_at DESC, s.id DESC`
const getBySourceSQL = `SELECT ` + selectColumns + ` FROM sessions WHERE channel_id = ? AND source_type = ? AND source_id = ?`
const activeLiveSQL = `SELECT ` + selectColumns + `
FROM sessions
WHERE channel_id = ?
	AND source_type = 'live_record'
	AND status IN (?, ?, ?, ?)
ORDER BY created_at DESC
LIMIT 1`

const findLiveByTimeWindowSQL = `SELECT  ` + selectColumns + ` 
FROM sessions
WHERE channel_id = ?
	AND source_type = 'live_record'
	AND started_at IS NOT NULL
	AND ABS(strftime('%s', started_at) - ?) < ?
ORDER BY ABS(strftime('%s', started_at) - ?) ASC
LIMIT 1`

const findDownloadByTimeWindowSQL = `SELECT ` + selectColumns + `
FROM sessions
WHERE channel_id = ?
	AND source_type = 'download'
	AND started_at IS NOT NULL
	AND ABS(strftime('%s', started_at) - ?) < ?
ORDER BY ABS(strftime('%s', started_at) - ?) ASC
LIMIT 1`

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteFailed(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE status = ?`, state.StatusFailed)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CountByChannel 返回指定主播关联的场次数量。
func (s *Store) CountByChannel(ctx context.Context, channelID string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE channel_id = ?`, channelID).Scan(&count)
	return count, err
}

// SessionStats holds aggregate statistics for the dashboard.
type SessionStats struct {
	TotalSessions   int              `json:"total_sessions"`
	TotalRecaps     int              `json:"total_recaps"`
	SessionsByMonth []MonthCount     `json:"sessions_by_month"`
	TopChannels     []ChannelRanking `json:"top_channels"`
}

type MonthCount struct {
	Month string `json:"month"`
	Count int    `json:"count"`
}

type ChannelRanking struct {
	ChannelID    string `json:"channel_id"`
	ChannelName  string `json:"name"`
	SessionCount int    `json:"session_count"`
	RecapCount   int    `json:"recap_count"`
}

type DashboardData struct {
	SessionsByMonth   []DashboardMonth   `json:"sessions_by_month"`
	SessionsByChannel []DashboardChannel `json:"sessions_by_channel"`
	CostTrend         []DashboardCost    `json:"cost_trend"`
	DanmakuTop        []DashboardDanmaku `json:"danmaku_top"`
	RecapCount        int                `json:"recap_count"`
	PublishCount      int                `json:"publish_count"`
}

type DashboardMonth struct {
	Month        string  `json:"month"`
	SessionCount int     `json:"session_count"`
	ASRHours     float64 `json:"asr_hours"`
}

type DashboardChannel struct {
	ChannelID    string `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	SessionCount int    `json:"session_count"`
	RecapCount   int    `json:"recap_count"`
	PublishCount int    `json:"publish_count"`
}

type DashboardCost struct {
	Month     string  `json:"month"`
	ASRHours  float64 `json:"asr_hours"`
	ASRCost   float64 `json:"asr_cost"`
	AICost    float64 `json:"ai_cost"`
	TotalCost float64 `json:"total_cost"`
}

type DashboardDanmaku struct {
	ChannelID    string `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	DanmakuCount int    `json:"danmaku_count"`
}

func (s *Store) GetStats(ctx context.Context) (*SessionStats, error) {
	stats := &SessionStats{}

	// Total sessions
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&stats.TotalSessions); err != nil {
		return nil, fmt.Errorf("count sessions: %w", err)
	}

	// Total recaps (sessions that reached recap_done or later)
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sessions
		WHERE status IN ('recap_done', 'uploaded', 'published')
	`).Scan(&stats.TotalRecaps); err != nil {
		return nil, fmt.Errorf("count recaps: %w", err)
	}

	// Sessions by month
	rows, err := s.db.QueryContext(ctx, `
		SELECT strftime('%Y-%m', created_at) AS month, COUNT(*) AS cnt
		FROM sessions
		GROUP BY month ORDER BY month DESC LIMIT 12
	`)
	if err != nil {
		return nil, fmt.Errorf("query sessions by month: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mc MonthCount
		if err := rows.Scan(&mc.Month, &mc.Count); err != nil {
			return nil, fmt.Errorf("scan sessions by month: %w", err)
		}
		stats.SessionsByMonth = append(stats.SessionsByMonth, mc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions by month: %w", err)
	}

	// Top channels by session count
	rows2, err := s.db.QueryContext(ctx, `
		SELECT s.channel_id, COALESCE(c.name, s.channel_id) AS name,
			COUNT(*) AS session_count,
			SUM(CASE WHEN s.status IN ('recap_done','uploaded','published') THEN 1 ELSE 0 END) AS recap_count
		FROM sessions s
		LEFT JOIN channels c ON s.channel_id = c.id
		GROUP BY s.channel_id
		ORDER BY session_count DESC
		LIMIT 10
	`)
	if err != nil {
		return nil, fmt.Errorf("query top channels: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var cr ChannelRanking
		if err := rows2.Scan(&cr.ChannelID, &cr.ChannelName, &cr.SessionCount, &cr.RecapCount); err != nil {
			return nil, fmt.Errorf("scan top channels: %w", err)
		}
		stats.TopChannels = append(stats.TopChannels, cr)
	}
	if err := rows2.Err(); err != nil {
		return nil, fmt.Errorf("iterate top channels: %w", err)
	}

	return stats, nil
}

func (s *Store) GetDashboardStats(ctx context.Context) (*DashboardData, error) {
	data := &DashboardData{
		SessionsByMonth:   []DashboardMonth{},
		SessionsByChannel: []DashboardChannel{},
		CostTrend:         []DashboardCost{},
		DanmakuTop:        []DashboardDanmaku{},
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(CASE WHEN status IN ('recap_done', 'uploaded', 'published') THEN 1 END),
			COUNT(CASE WHEN status = 'published' OR published_at IS NOT NULL THEN 1 END)
		FROM sessions
	`).Scan(&data.RecapCount, &data.PublishCount); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			strftime('%Y-%m', COALESCE(started_at, created_at)) AS month,
			COUNT(*) AS session_count,
			SUM(CASE
				WHEN status IN ('asr_done', 'recap_done', 'uploaded', 'published') THEN
					CASE
						WHEN started_at IS NOT NULL AND ended_at IS NOT NULL
							AND strftime('%s', ended_at) > strftime('%s', started_at)
						THEN (strftime('%s', ended_at) - strftime('%s', started_at)) / 3600.0
						ELSE 2.0
					END
				ELSE 0
			END) AS asr_hours
		FROM sessions
		GROUP BY month
		ORDER BY month DESC
		LIMIT 12
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item DashboardMonth
		if err := rows.Scan(&item.Month, &item.SessionCount, &item.ASRHours); err != nil {
			return nil, err
		}
		data.SessionsByMonth = append(data.SessionsByMonth, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT
			s.channel_id,
			COALESCE(c.name, s.channel_id) AS channel_name,
			COUNT(*) AS session_count,
			COUNT(CASE WHEN s.status IN ('recap_done', 'uploaded', 'published') THEN 1 END) AS recap_count,
			COUNT(CASE WHEN s.status = 'published' OR s.published_at IS NOT NULL THEN 1 END) AS publish_count
		FROM sessions s
		LEFT JOIN channels c ON c.id = s.channel_id
		GROUP BY s.channel_id
		ORDER BY session_count DESC, s.channel_id ASC
		LIMIT 20
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item DashboardChannel
		if err := rows.Scan(&item.ChannelID, &item.ChannelName, &item.SessionCount, &item.RecapCount, &item.PublishCount); err != nil {
			return nil, err
		}
		data.SessionsByChannel = append(data.SessionsByChannel, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
		WITH monthly AS (
			SELECT
				strftime('%Y-%m', COALESCE(started_at, created_at)) AS month,
				SUM(CASE
					WHEN status IN ('asr_done', 'recap_done', 'uploaded', 'published') THEN
						CASE
							WHEN started_at IS NOT NULL AND ended_at IS NOT NULL
								AND strftime('%s', ended_at) > strftime('%s', started_at)
							THEN (strftime('%s', ended_at) - strftime('%s', started_at)) / 3600.0
							ELSE 2.0
						END
					ELSE 0
				END) AS asr_hours,
				COUNT(CASE WHEN status IN ('recap_done', 'uploaded', 'published') THEN 1 END) AS recap_count
			FROM sessions
			GROUP BY month
		)
		SELECT
			month,
			asr_hours,
			asr_hours * 36.0 AS asr_cost,
			recap_count * 0.1 AS ai_cost,
			asr_hours * 36.0 + recap_count * 0.1 AS total_cost
		FROM monthly
		ORDER BY month DESC
		LIMIT 12
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item DashboardCost
		if err := rows.Scan(&item.Month, &item.ASRHours, &item.ASRCost, &item.AICost, &item.TotalCost); err != nil {
			return nil, err
		}
		data.CostTrend = append(data.CostTrend, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 当前 schema 未持久化弹幕数量，保留空结果，避免用场次量伪造弹幕排行。
	rows, err = s.db.QueryContext(ctx, `
		SELECT c.id, c.name, 0 AS danmaku_count
		FROM channels c
		WHERE 1 = 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item DashboardDanmaku
		if err := rows.Scan(&item.ChannelID, &item.ChannelName, &item.DanmakuCount); err != nil {
			return nil, err
		}
		data.DanmakuTop = append(data.DanmakuTop, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return data, nil
}
