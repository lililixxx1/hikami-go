package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"hikami-go/internal/config"
)

// nowRFC3339 返回本地时区的 RFC3339 时间字符串，与 sessions/tasks 表的时间字段
// （time.Now().Format(time.RFC3339)）保持一致。避免 SQLite datetime('now') 返回 UTC，
// 导致前端展示与其它表时间字段相差一个时区。
func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

var (
	ErrNotFound  = errors.New("channel not found")
	ErrDuplicate = errors.New("channel already exists")
	ErrInUse     = errors.New("channel is in use")
	ErrInvalid   = errors.New("invalid channel")
)

type Channel struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	UID                 int64  `json:"uid"`
	LiveRoomID          int64  `json:"live_room_id"`
	ReplaySourceURL     string `json:"replay_source_url"`
	SpaceURL            string `json:"space_url"`
	TitlePrefix         string `json:"title_prefix"`
	CookieFile          string `json:"cookie_file"`
	DownloadCookieFile  string `json:"download_cookie_file"`
	DownloadAccountID   *int64 `json:"download_account_id,omitempty"`
	Enabled             bool   `json:"enabled"`
	AutoRecord          bool   `json:"auto_record"`
	AutoASR             bool   `json:"auto_asr"`
	AutoRecap           bool   `json:"auto_recap"`
	RecordDanmaku       bool   `json:"record_danmaku"`
	SourceMode          string `json:"source_mode"`
	DiscoverLimit       int    `json:"discover_limit"`
	PublishEnabled      bool   `json:"publish_enabled"`
	PublishMode         string `json:"publish_mode"`
	PublishCategoryID   int    `json:"publish_category_id"`
	PublishListID       int    `json:"publish_list_id"`
	PublishPrivatePub   int    `json:"publish_private_pub"`
	PublishOriginal     int    `json:"publish_original"`
	AutoPublish         bool   `json:"auto_publish"`
	PublishAigc         int    `json:"publish_aigc"`
	PublishTimerPubTime int64  `json:"publish_timer_pub_time"`
	PublishCoverURL     string `json:"publish_cover_url"`
	PublishTopics       string `json:"publish_topics"`
	RecapModel          string `json:"recap_model"`
	MaxContinuations    int    `json:"max_continuations"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

type UpsertInput struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	UID                 int64  `json:"uid"`
	LiveRoomID          int64  `json:"live_room_id"`
	ReplaySourceURL     string `json:"replay_source_url"`
	SpaceURL            string `json:"space_url"`
	TitlePrefix         string `json:"title_prefix"`
	CookieFile          string `json:"cookie_file"`
	DownloadCookieFile  string `json:"download_cookie_file"`
	DownloadAccountID   *int64 `json:"download_account_id"`
	Enabled             bool   `json:"enabled"`
	AutoRecord          bool   `json:"auto_record"`
	AutoASR             bool   `json:"auto_asr"`
	AutoRecap           *bool  `json:"auto_recap"`
	RecordDanmaku       bool   `json:"record_danmaku"`
	SourceMode          string `json:"source_mode"`
	DiscoverLimit       int    `json:"discover_limit"`
	PublishEnabled      bool   `json:"publish_enabled"`
	PublishMode         string `json:"publish_mode"`
	PublishCategoryID   int    `json:"publish_category_id"`
	PublishListID       int    `json:"publish_list_id"`
	PublishPrivatePub   int    `json:"publish_private_pub"`
	PublishOriginal     int    `json:"publish_original"`
	AutoPublish         bool   `json:"auto_publish"`
	PublishAigc         int    `json:"publish_aigc"`
	PublishTimerPubTime int64  `json:"publish_timer_pub_time"`
	PublishCoverURL     string `json:"publish_cover_url"`
	PublishTopics       string `json:"publish_topics"`
	RecapModel          string `json:"recap_model"`
	MaxContinuations    int    `json:"max_continuations"`
}

type Store struct {
	db *sql.DB
}

type CookieUsage string

const (
	CookieUsageDownload CookieUsage = "download"
	CookieUsagePublish  CookieUsage = "publish"
)

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Bootstrap(ctx context.Context, channels []config.BootstrapChannel) error {
	if len(channels) == 0 {
		return nil
	}

	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM channels").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, item := range channels {
		input := UpsertInput{
			ID:                  item.ID,
			Name:                item.Name,
			UID:                 item.UID,
			LiveRoomID:          item.LiveRoomID,
			ReplaySourceURL:     item.ReplaySourceURL,
			SpaceURL:            item.SpaceURL,
			TitlePrefix:         item.TitlePrefix,
			CookieFile:          item.CookieFile,
			DownloadCookieFile:  item.DownloadCookieFile,
			Enabled:             item.Enabled,
			AutoRecord:          item.AutoRecord,
			AutoASR:             item.AutoASR,
			AutoRecap:           item.AutoRecap,
			RecordDanmaku:       true,
			SourceMode:          item.SourceMode,
			DiscoverLimit:       item.DiscoverLimit,
			PublishEnabled:      item.PublishEnabled,
			PublishMode:         item.PublishMode,
			PublishCategoryID:   item.PublishCategoryID,
			PublishListID:       item.PublishListID,
			PublishPrivatePub:   item.PublishPrivatePub,
			PublishOriginal:     item.PublishOriginal,
			AutoPublish:         item.AutoPublish,
			PublishAigc:         item.PublishAigc,
			PublishTimerPubTime: item.PublishTimerPubTime,
			PublishCoverURL:     item.PublishCoverURL,
			PublishTopics:       item.PublishTopics,
			MaxContinuations:    -1,
		}
		if err := validate(input, true); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createSQL,
			input.ID,
			input.Name,
			input.UID,
			input.LiveRoomID,
			input.ReplaySourceURL,
			input.SpaceURL,
			input.TitlePrefix,
			input.CookieFile,
			input.DownloadCookieFile,
			nullInt64PtrValue(input.DownloadAccountID),
			boolToInt(input.Enabled),
			boolToInt(input.AutoRecord),
			boolToInt(input.AutoASR),
			boolToInt(resolveAutoRecap(input.AutoRecap, false)),
			boolToInt(input.RecordDanmaku),
			input.SourceMode,
			input.DiscoverLimit,
			boolToInt(input.PublishEnabled),
			input.PublishMode,
			input.PublishCategoryID,
			input.PublishListID,
			input.PublishPrivatePub,
			input.PublishOriginal,
			boolToInt(input.AutoPublish),
			input.PublishAigc,
			input.PublishTimerPubTime,
			input.PublishCoverURL,
			input.PublishTopics,
			input.RecapModel,
			input.MaxContinuations,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) List(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, listSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if channels == nil {
		return []Channel{}, nil
	}
	return channels, nil
}

func (s *Store) Get(ctx context.Context, id string) (Channel, error) {
	row := s.db.QueryRowContext(ctx, getSQL, id)
	channel, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	return channel, err
}

func (s *Store) Create(ctx context.Context, input UpsertInput) (Channel, error) {
	if input.SourceMode == "" {
		input.SourceMode = "both"
	}
	if err := validate(input, true); err != nil {
		return Channel{}, err
	}
	_, err := s.db.ExecContext(ctx, createSQL,
		input.ID,
		input.Name,
		input.UID,
		input.LiveRoomID,
		input.ReplaySourceURL,
		input.SpaceURL,
		input.TitlePrefix,
		input.CookieFile,
		input.DownloadCookieFile,
		nullInt64PtrValue(input.DownloadAccountID),
		boolToInt(input.Enabled),
		boolToInt(input.AutoRecord),
		boolToInt(input.AutoASR),
		boolToInt(resolveAutoRecap(input.AutoRecap, false)),
		boolToInt(input.RecordDanmaku),
		input.SourceMode,
		input.DiscoverLimit,
		boolToInt(input.PublishEnabled),
		input.PublishMode,
		input.PublishCategoryID,
		input.PublishListID,
		input.PublishPrivatePub,
		input.PublishOriginal,
		boolToInt(input.AutoPublish),
		input.PublishAigc,
		input.PublishTimerPubTime,
		input.PublishCoverURL,
		input.PublishTopics,
		input.RecapModel,
		input.MaxContinuations,
	)
	if err != nil {
		if strings.Contains(err.Error(), "constraint failed") {
			return Channel{}, ErrDuplicate
		}
		return Channel{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *Store) SaveIdentified(ctx context.Context, input UpsertInput) (Channel, bool, error) {
	if input.SourceMode == "" {
		input.SourceMode = "both"
	}
	if err := validate(input, true); err != nil {
		return Channel{}, false, err
	}

	existing, err := s.Get(ctx, input.ID)
	if errors.Is(err, ErrNotFound) {
		created, err := s.Create(ctx, input)
		return created, true, err
	}
	if err != nil {
		return Channel{}, false, err
	}

	updated, err := s.Update(ctx, input.ID, mergeIdentified(existing, input))
	return updated, false, err
}

func (s *Store) Update(ctx context.Context, id string, input UpsertInput) (Channel, error) {
	input.ID = id
	if input.SourceMode == "" {
		input.SourceMode = "both"
	}
	if err := validate(input, false); err != nil {
		return Channel{}, err
	}

	// auto_recap 为三态：调用方未提供（nil）时保留现有值，避免完整 UpsertInput 更新时
	// 把该字段意外写成 false（与既有 bool 字段零值机制的差异点，详见 resolveAutoRecap 注释）。
	// 注：若 Get 返回非 ErrNotFound 错误（DB 故障等），必须向上传播，不可静默 fallback false。
	var existingAutoRecap bool
	if input.AutoRecap == nil {
		existing, getErr := s.Get(ctx, id)
		if getErr == nil {
			existingAutoRecap = existing.AutoRecap
		} else if !errors.Is(getErr, ErrNotFound) {
			return Channel{}, getErr
		}
		// ErrNotFound 时保持 existingAutoRecap=false；后续 Update 的 RowsAffected==0 会再返回 ErrNotFound。
	}

	result, err := s.db.ExecContext(ctx, updateSQL,
		input.Name,
		input.UID,
		input.LiveRoomID,
		input.ReplaySourceURL,
		input.SpaceURL,
		input.TitlePrefix,
		input.CookieFile,
		input.DownloadCookieFile,
		nullInt64PtrValue(input.DownloadAccountID),
		boolToInt(input.Enabled),
		boolToInt(input.AutoRecord),
		boolToInt(input.AutoASR),
		boolToInt(resolveAutoRecap(input.AutoRecap, existingAutoRecap)),
		boolToInt(input.RecordDanmaku),
		input.SourceMode,
		input.DiscoverLimit,
		boolToInt(input.PublishEnabled),
		input.PublishMode,
		input.PublishCategoryID,
		input.PublishListID,
		input.PublishPrivatePub,
		input.PublishOriginal,
		boolToInt(input.AutoPublish),
		input.PublishAigc,
		input.PublishTimerPubTime,
		input.PublishCoverURL,
		input.PublishTopics,
		input.RecapModel,
		input.MaxContinuations,
		nowRFC3339(),
		id,
	)
	if err != nil {
		return Channel{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Channel{}, err
	}
	if affected == 0 {
		return Channel{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Store) UpdateCookieFile(ctx context.Context, id string, usage CookieUsage, cookiePath string) (Channel, error) {
	var query string
	switch usage {
	case CookieUsageDownload:
		query = "UPDATE channels SET download_cookie_file = ?, updated_at = ? WHERE id = ?"
	case CookieUsagePublish:
		query = "UPDATE channels SET cookie_file = ?, updated_at = ? WHERE id = ?"
	default:
		return Channel{}, fmt.Errorf("%w: invalid cookie usage", ErrInvalid)
	}
	result, err := s.db.ExecContext(ctx, query, cookiePath, nowRFC3339(), id)
	if err != nil {
		return Channel{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Channel{}, err
	}
	if affected == 0 {
		return Channel{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM channels WHERE id = ?", id)
	if err != nil {
		if strings.Contains(err.Error(), "constraint failed") {
			return ErrInUse
		}
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

func mergeIdentified(existing Channel, identified UpsertInput) UpsertInput {
	if existing.TitlePrefix != "" {
		identified.TitlePrefix = existing.TitlePrefix
	}
	if identified.CookieFile == "" {
		identified.CookieFile = existing.CookieFile
	}
	if identified.DownloadCookieFile == "" {
		identified.DownloadCookieFile = existing.DownloadCookieFile
	}
	if identified.DownloadAccountID == nil {
		identified.DownloadAccountID = existing.DownloadAccountID
	}
	identified.RecapModel = existing.RecapModel
	identified.MaxContinuations = existing.MaxContinuations
	identified.Enabled = existing.Enabled
	identified.AutoRecord = existing.AutoRecord
	identified.AutoASR = existing.AutoASR
	existingAutoRecap := existing.AutoRecap
	identified.AutoRecap = &existingAutoRecap
	identified.RecordDanmaku = existing.RecordDanmaku
	identified.SourceMode = existing.SourceMode
	identified.DiscoverLimit = existing.DiscoverLimit
	identified.PublishEnabled = existing.PublishEnabled
	identified.PublishMode = existing.PublishMode
	identified.PublishCategoryID = existing.PublishCategoryID
	identified.PublishListID = existing.PublishListID
	identified.PublishPrivatePub = existing.PublishPrivatePub
	identified.PublishOriginal = existing.PublishOriginal
	identified.AutoPublish = existing.AutoPublish
	identified.PublishAigc = existing.PublishAigc
	identified.PublishTimerPubTime = existing.PublishTimerPubTime
	identified.PublishCoverURL = existing.PublishCoverURL
	identified.PublishTopics = existing.PublishTopics
	return identified
}

type scanner interface {
	Scan(dest ...any) error
}

func scanChannel(row scanner) (Channel, error) {
	var channel Channel
	var enabled int
	var autoRecord int
	var autoASR int
	var autoRecap int
	var recordDanmaku int
	var publishEnabled int
	var autoPublish int
	var downloadAccountID sql.NullInt64
	err := row.Scan(
		&channel.ID,
		&channel.Name,
		&channel.UID,
		&channel.LiveRoomID,
		&channel.ReplaySourceURL,
		&channel.SpaceURL,
		&channel.TitlePrefix,
		&channel.CookieFile,
		&channel.DownloadCookieFile,
		&downloadAccountID,
		&enabled,
		&autoRecord,
		&autoASR,
		&autoRecap,
		&recordDanmaku,
		&channel.SourceMode,
		&channel.DiscoverLimit,
		&publishEnabled,
		&channel.PublishMode,
		&channel.PublishCategoryID,
		&channel.PublishListID,
		&channel.PublishPrivatePub,
		&channel.PublishOriginal,
		&autoPublish,
		&channel.PublishAigc,
		&channel.PublishTimerPubTime,
		&channel.PublishCoverURL,
		&channel.PublishTopics,
		&channel.RecapModel,
		&channel.MaxContinuations,
		&channel.CreatedAt,
		&channel.UpdatedAt,
	)
	channel.Enabled = enabled != 0
	channel.AutoRecord = autoRecord != 0
	channel.AutoASR = autoASR != 0
	channel.AutoRecap = autoRecap != 0
	channel.RecordDanmaku = recordDanmaku != 0
	channel.PublishEnabled = publishEnabled != 0
	channel.AutoPublish = autoPublish != 0
	if downloadAccountID.Valid {
		channel.DownloadAccountID = &downloadAccountID.Int64
	}
	return channel, err
}

func validate(input UpsertInput, requireID bool) error {
	if requireID && strings.TrimSpace(input.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalid)
	}
	if strings.Contains(input.ID, "/") || strings.Contains(input.ID, "\\") {
		return fmt.Errorf("%w: id must not contain path separators", ErrInvalid)
	}
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if input.UID <= 0 {
		return fmt.Errorf("%w: uid must be greater than 0", ErrInvalid)
	}
	if input.LiveRoomID < 0 {
		return fmt.Errorf("%w: live_room_id must be greater than or equal to 0", ErrInvalid)
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// resolveAutoRecap 解析 UpsertInput.AutoRecap 的三态语义：
//   - nil（调用方未提供）：Create/Bootstrap 默认 false（2026-07-06 反转,新建主播默认不自动回顾）;
//     Update 默认保留现有值（fallback）。
//   - 非 nil：取其显式值。
//
// 其余 bool 字段（auto_record/auto_asr/...）沿用既有零值机制，唯独 auto_recap 需三态（其余 bool 字段零值即 false,无需三态）。
func resolveAutoRecap(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func nullInt64PtrValue(value *int64) any {
	if value != nil {
		return *value
	}
	return nil
}

const selectColumns = `
	id,
	name,
	uid,
	live_room_id,
	replay_source_url,
	space_url,
	title_prefix,
	cookie_file,
	download_cookie_file,
	download_account_id,
	enabled,
	auto_record,
	auto_asr,
	auto_recap,
	record_danmaku,
	source_mode,
	discover_limit,
	publish_enabled,
	publish_mode,
	publish_category_id,
	publish_list_id,
	publish_private_pub,
	publish_original,
	auto_publish,
	publish_aigc,
		publish_timer_pub_time,
		publish_cover_url,
		publish_topics,
		recap_model,
		max_continuations,
		created_at,
		updated_at
`

const listSQL = `SELECT ` + selectColumns + ` FROM channels ORDER BY id`
const getSQL = `SELECT ` + selectColumns + ` FROM channels WHERE id = ?`

const createSQL = `
INSERT INTO channels (
	id,
	name,
	uid,
	live_room_id,
	replay_source_url,
	space_url,
	title_prefix,
	cookie_file,
	download_cookie_file,
	download_account_id,
		enabled,
		auto_record,
		auto_asr,
		auto_recap,
		record_danmaku,
		source_mode,
		discover_limit,
		publish_enabled,
		publish_mode,
		publish_category_id,
		publish_list_id,
		publish_private_pub,
		publish_original,
		auto_publish,
			publish_aigc,
			publish_timer_pub_time,
			publish_cover_url,
			publish_topics,
			recap_model,
			max_continuations
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

const updateSQL = `
UPDATE channels
SET
	name = ?,
	uid = ?,
	live_room_id = ?,
	replay_source_url = ?,
	space_url = ?,
	title_prefix = ?,
	cookie_file = ?,
	download_cookie_file = ?,
	download_account_id = ?,
		enabled = ?,
		auto_record = ?,
		auto_asr = ?,
		auto_recap = ?,
		record_danmaku = ?,
	source_mode = ?,
	discover_limit = ?,
	publish_enabled = ?,
	publish_mode = ?,
	publish_category_id = ?,
	publish_list_id = ?,
	publish_private_pub = ?,
	publish_original = ?,
	auto_publish = ?,
	publish_aigc = ?,
		publish_timer_pub_time = ?,
		publish_cover_url = ?,
		publish_topics = ?,
		recap_model = ?,
		max_continuations = ?,
		updated_at = ?
WHERE id = ?
`
