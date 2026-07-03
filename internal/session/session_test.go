package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/db"
	"hikami-go/internal/state"
)

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func insertChannel(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO channels (id, name, uid, enabled) VALUES (?, ?, ?, 1)`, "test_ch", "Test", 1)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func TestCreateLiveSuccess(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		Title:     "测试直播",
		RoomID:    12345,
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if sess.Slug != "live_20260501_100000" {
		t.Fatalf("unexpected slug: %s", sess.Slug)
	}
	if sess.SourceType != "live_record" {
		t.Fatalf("unexpected source_type: %s", sess.SourceType)
	}
	if sess.Title != "测试直播" {
		t.Fatalf("unexpected title: %s", sess.Title)
	}
	if sess.ChannelID != "test_ch" {
		t.Fatalf("unexpected channel_id: %s", sess.ChannelID)
	}
}

func TestCreateLiveDefaultTitle(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		Title:     "",
		RoomID:    12345,
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}
	if sess.Title != "B站直播" {
		t.Fatalf("expected default title 'B站直播', got %q", sess.Title)
	}
}

func TestCreateLiveDefaultTime(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		Title:     "测试",
		RoomID:    12345,
		StartedAt: time.Time{},
	})
	after := time.Now()
	before := after.Add(-5 * time.Second)
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}

	parsed, err := time.Parse(time.RFC3339, sess.StartedAt)
	if err != nil {
		t.Fatalf("parse started_at: %v", err)
	}
	if parsed.Before(before) || parsed.After(after) {
		t.Fatalf("started_at %s should be between %s and %s", sess.StartedAt, before, after)
	}
}

func TestCreateLiveMissingChannelID(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	_, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "",
		RoomID:    12345,
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err == nil {
		t.Fatal("expected error for missing channel_id")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestCreateLiveInvalidRoomID(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	for _, roomID := range []int64{0, -1, -100} {
		_, err := store.CreateLive(context.Background(), CreateLiveInput{
			ChannelID: "test_ch",
			RoomID:    roomID,
			StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
		})
		if err == nil {
			t.Fatalf("expected error for room_id=%d", roomID)
		}
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid for room_id=%d, got %v", roomID, err)
		}
	}
}

func TestCreateLiveDuplicateRejected(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	input := CreateLiveInput{
		ChannelID: "test_ch",
		Title:     "幂等测试",
		RoomID:    99999,
		StartedAt: time.Date(2026, 5, 1, 12, 30, 45, 0, time.Local),
	}

	if _, err := store.CreateLive(context.Background(), input); err != nil {
		t.Fatalf("first CreateLive: %v", err)
	}
	// 第二次（同一分钟槽）必须被拒绝：历史静默复用是 live_check 误触发复用旧 session 的入口。
	_, err := store.CreateLive(context.Background(), input)
	if !errors.Is(err, ErrAlreadyLive) {
		t.Fatalf("second CreateLive: expected ErrAlreadyLive, got %v", err)
	}
}

func TestCreateLiveDoesNotResetFailedSession(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	input := CreateLiveInput{
		ChannelID: "test_ch",
		Title:     "失败重试",
		RoomID:    99999,
		StartedAt: time.Date(2026, 5, 1, 12, 30, 45, 0, time.Local),
	}
	sess, err := store.CreateLive(context.Background(), input)
	if err != nil {
		t.Fatalf("first CreateLive: %v", err)
	}
	if _, err := database.Exec(`
		UPDATE sessions
		SET status = ?, current_task_id = 'task_old', last_error = 'old error'
		WHERE id = ?
	`, state.StatusFailed, sess.ID); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	// CreateLive 不再自动重置 failed session：恢复应由显式的人工路径触发。
	// 否则 live_check 会在 ASR 进行中把 failed 拉回 discovered，污染状态机。
	_, err = store.CreateLive(context.Background(), input)
	if !errors.Is(err, ErrAlreadyLive) {
		t.Fatalf("retry CreateLive: expected ErrAlreadyLive, got %v", err)
	}

	// 旧 session 必须保持 failed 原状，未被静默重置。
	kept, err := store.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if kept.Status != string(state.StatusFailed) {
		t.Fatalf("session status = %q, want failed (must not be auto-reset)", kept.Status)
	}
	if kept.CurrentTaskID != "task_old" || kept.LastError != "old error" {
		t.Fatalf("session was mutated: task=%q error=%q, want unchanged", kept.CurrentTaskID, kept.LastError)
	}
}

func TestCreateLiveForeignKeyViolation(t *testing.T) {
	database := setupDB(t)
	store := NewStore(database)

	_, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "nonexistent_ch",
		Title:     "测试",
		RoomID:    12345,
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err == nil {
		t.Fatal("expected foreign key error for nonexistent channel")
	}
	// 回归（codex 审核）：FK 约束错误不得被误判为 ErrAlreadyLive，
	// 否则不存在的 channel 会被误报为"直播 session 已存在"，污染调用方分支。
	if errors.Is(err, ErrAlreadyLive) {
		t.Fatalf("FK 错误不应映射为 ErrAlreadyLive, got: %v", err)
	}
}

func TestCreateLiveIDFormat(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	ts := time.Date(2026, 5, 1, 10, 30, 45, 0, time.Local)
	sess, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		RoomID:    12345,
		StartedAt: ts,
	})
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}

	expected := "test_ch_live_20260501_103045"
	if sess.ID != expected {
		t.Fatalf("expected ID %q, got %q", expected, sess.ID)
	}
}

func TestCreateDownloadSuccess(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, created, err := store.CreateDownload(context.Background(), CreateDownloadInput{
		ChannelID: "test_ch",
		SourceID:  "BV1xx411c7mD",
		Title:     "回放标题",
		SourceURL: "https://www.bilibili.com/video/BV1xx411c7mD",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	if !created {
		t.Fatal("expected created=true")
	}
	if sess.SourceType != "download" {
		t.Fatalf("unexpected source_type: %s", sess.SourceType)
	}
	if sess.Title != "回放标题" {
		t.Fatalf("unexpected title: %s", sess.Title)
	}
}

func TestCreateDownloadDuplicate(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	input := CreateDownloadInput{
		ChannelID: "test_ch",
		SourceID:  "BV1dup",
		Title:     "回放",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	}

	sess1, created1, err := store.CreateDownload(context.Background(), input)
	if err != nil {
		t.Fatalf("first CreateDownload: %v", err)
	}
	if !created1 {
		t.Fatal("expected created=true on first call")
	}

	sess2, created2, err := store.CreateDownload(context.Background(), input)
	if err != nil {
		t.Fatalf("second CreateDownload: %v", err)
	}
	if created2 {
		t.Fatal("expected created=false on duplicate")
	}
	if sess1.ID != sess2.ID {
		t.Fatalf("expected same ID, got %q and %q", sess1.ID, sess2.ID)
	}
}

func TestCreateDownloadMissingSourceID(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	_, _, err := store.CreateDownload(context.Background(), CreateDownloadInput{
		ChannelID: "test_ch",
		SourceID:  "",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err == nil {
		t.Fatal("expected error for missing source_id")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestCreateDownloadDefaultTitle(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, _, err := store.CreateDownload(context.Background(), CreateDownloadInput{
		ChannelID: "test_ch",
		SourceID:  "BV1title",
		Title:     "",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	if sess.Title != "BV1title" {
		t.Fatalf("expected title 'BV1title', got %q", sess.Title)
	}
}

func TestCreateDownloadSlugFallback(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	ts := time.Date(2026, 5, 1, 10, 30, 0, 0, time.Local)
	sess, _, err := store.CreateDownload(context.Background(), CreateDownloadInput{
		ChannelID: "test_ch",
		SourceID:  "!!!",
		Title:     "测试",
		StartedAt: ts,
	})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}

	slug := sanitizeSlug("!!!")
	if slug != "" {
		t.Fatalf("expected sanitizeSlug to return empty for '!!!', got %q", slug)
	}
	if !strings.HasPrefix(sess.Slug, "download_") {
		t.Fatalf("expected slug to start with 'download_', got %q", sess.Slug)
	}
	if sess.Slug != "download_20260501_103000" {
		t.Fatalf("expected slug 'download_20260501_103000', got %q", sess.Slug)
	}
}

func TestCreateImportSuccess(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateImport(context.Background(), CreateImportInput{
		ChannelID: "test_ch",
		Title:     "导入测试",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateImport: %v", err)
	}
	if sess.SourceType != "import" {
		t.Fatalf("unexpected source_type: %s", sess.SourceType)
	}
	if sess.Title != "导入测试" {
		t.Fatalf("unexpected title: %s", sess.Title)
	}
}

func TestCreateImportDefaultTitle(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateImport(context.Background(), CreateImportInput{
		ChannelID: "test_ch",
		Title:     "",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateImport: %v", err)
	}
	if sess.Title != "手动导入" {
		t.Fatalf("expected default title '手动导入', got %q", sess.Title)
	}
}

func TestCreateImportWithEndedAt(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	endedAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.Local)
	sess, err := store.CreateImport(context.Background(), CreateImportInput{
		ChannelID: "test_ch",
		Title:     "有结束时间",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
		EndedAt:   endedAt,
	})
	if err != nil {
		t.Fatalf("CreateImport: %v", err)
	}
	if sess.EndedAt == "" {
		t.Fatal("expected non-empty ended_at")
	}
	parsed, err := time.Parse(time.RFC3339, sess.EndedAt)
	if err != nil {
		t.Fatalf("parse ended_at: %v", err)
	}
	if !parsed.Equal(endedAt) {
		t.Fatalf("ended_at mismatch: got %s, want %s", parsed, endedAt)
	}
}

func TestCreateImportWithoutEndedAt(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateImport(context.Background(), CreateImportInput{
		ChannelID: "test_ch",
		Title:     "无结束时间",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
		EndedAt:   time.Time{},
	})
	if err != nil {
		t.Fatalf("CreateImport: %v", err)
	}
	if sess.EndedAt != "" {
		t.Fatalf("expected empty ended_at, got %q", sess.EndedAt)
	}
}

func TestGetNotFound(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	_, err := store.Get(context.Background(), "nonexistent_id")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetBySourceSuccess(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, _, err := store.CreateDownload(context.Background(), CreateDownloadInput{
		ChannelID: "test_ch",
		SourceID:  "BV1get",
		Title:     "测试",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}

	found, err := store.GetBySource(context.Background(), "test_ch", "download", "BV1get")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if found.ID != sess.ID {
		t.Fatalf("expected ID %q, got %q", sess.ID, found.ID)
	}
}

func TestGetBySourceNotFound(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	_, err := store.GetBySource(context.Background(), "test_ch", "download", "BV1nope")
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateEndedAt_Success(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		Title:     "结束时间测试",
		RoomID:    12345,
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}

	endedAt := time.Date(2026, 5, 1, 12, 30, 0, 0, time.Local)
	if err := store.UpdateEndedAt(context.Background(), sess.ID, endedAt); err != nil {
		t.Fatalf("UpdateEndedAt: %v", err)
	}

	updated, err := store.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.EndedAt == "" {
		t.Fatal("expected non-empty ended_at")
	}
	parsed, err := time.Parse(time.RFC3339, updated.EndedAt)
	if err != nil {
		t.Fatalf("parse ended_at: %v", err)
	}
	if !parsed.Equal(endedAt) {
		t.Fatalf("ended_at mismatch: got %s, want %s", parsed, endedAt)
	}
}

func TestUpdateEndedAt_NotFound(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	err := store.UpdateEndedAt(context.Background(), "missing_session", time.Date(2026, 5, 1, 12, 30, 0, 0, time.Local))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateEndedAt_ZeroTime(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	err := store.UpdateEndedAt(context.Background(), "session_1", time.Time{})
	if err == nil {
		t.Fatal("expected error for zero ended_at")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestSetLocalAvailable_Success(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		Title:     "本地可用测试",
		RoomID:    12345,
		StartedAt: time.Date(2026, 6, 17, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}
	if !sess.LocalAvailable {
		t.Fatal("expected LocalAvailable=true by default after create")
	}

	if err := store.SetLocalAvailable(context.Background(), sess.ID, false); err != nil {
		t.Fatalf("SetLocalAvailable false: %v", err)
	}
	got, err := store.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("Get after set false: %v", err)
	}
	if got.LocalAvailable {
		t.Fatal("LocalAvailable = true, want false")
	}

	if err := store.SetLocalAvailable(context.Background(), sess.ID, true); err != nil {
		t.Fatalf("SetLocalAvailable true: %v", err)
	}
	got, err = store.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("Get after set true: %v", err)
	}
	if !got.LocalAvailable {
		t.Fatal("LocalAvailable = false, want true")
	}
}

func TestSetLocalAvailable_NotFound(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	err := store.SetLocalAvailable(context.Background(), "missing_session", false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSetLocalAvailable_EmptyID(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	err := store.SetLocalAvailable(context.Background(), "  ", false)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestListEmpty(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sessions, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if sessions == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestListOrderedByCreatedDesc(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	// Insert sessions with a small delay to ensure ordering
	sess1, err := store.CreateImport(context.Background(), CreateImportInput{
		ChannelID: "test_ch",
		Title:     "第一条",
		StartedAt: time.Date(2026, 5, 1, 8, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	sess2, err := store.CreateImport(context.Background(), CreateImportInput{
		ChannelID: "test_ch",
		Title:     "第二条",
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}

	sessions, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) < 2 {
		t.Fatalf("expected at least 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ID != sess2.ID {
		t.Fatalf("expected first session to be %q (most recent), got %q", sess2.ID, sessions[0].ID)
	}
	if sessions[1].ID != sess1.ID {
		t.Fatalf("expected second session to be %q (earlier), got %q", sess1.ID, sessions[1].ID)
	}
}

func TestActiveLiveForChannel(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	// Create a live session
	sess, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		RoomID:    12345,
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}

	// Update status to "recording" so ActiveLiveForChannel picks it up
	_, err = database.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, "recording", sess.ID)
	if err != nil {
		t.Fatalf("update status: %v", err)
	}

	found, ok, err := store.ActiveLiveForChannel(context.Background(), "test_ch")
	if err != nil {
		t.Fatalf("ActiveLiveForChannel: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if found.ID != sess.ID {
		t.Fatalf("expected ID %q, got %q", sess.ID, found.ID)
	}
}

func TestActiveLiveForChannelNone(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, ok, err := store.ActiveLiveForChannel(context.Background(), "test_ch")
	if err != nil {
		t.Fatalf("ActiveLiveForChannel: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false")
	}
	if sess.ID != "" {
		t.Fatalf("expected empty session, got ID %q", sess.ID)
	}
}

// TestActiveLiveForChannelScope 验证频道级活跃录制判断的范围：
// 只拦截正在录制/排队中的状态（recording/discovered/downloading/importing），
// 不拦截历史或后期状态——否则一旦某场直播发布过（published 等终态），
// 该频道将永久无法再录制新直播（高严重度回归，由 codex 审核指出）。
// 原始下播竞态的防护由 CreateLive 的同分钟槽 UNIQUE 约束承担，而非频道级白名单。
func TestActiveLiveForChannelScope(t *testing.T) {
	blocking := []state.Status{
		state.StatusRecording,
		state.StatusDiscovered,
		state.StatusDownloading,
		state.StatusImporting,
	}
	// 后期/终态不应阻止新录制（含 published 这个关键回归点）。
	allowing := []state.Status{
		state.StatusMediaReady,
		state.StatusASRSubmitted,
		state.StatusASRDone,
		state.StatusRecapDone,
		state.StatusUploaded,
		state.StatusPublished,
		state.StatusFailed,
	}

	for _, st := range blocking {
		st := st
		t.Run(string("blocks_"+st), func(t *testing.T) {
			database := setupDB(t)
			insertChannel(t, database)
			store := NewStore(database)

			sess, err := store.CreateLive(context.Background(), CreateLiveInput{
				ChannelID: "test_ch",
				RoomID:    12345,
				StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
			})
			if err != nil {
				t.Fatalf("CreateLive: %v", err)
			}
			if _, err := database.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, st, sess.ID); err != nil {
				t.Fatalf("set status %s: %v", st, err)
			}

			_, ok, err := store.ActiveLiveForChannel(context.Background(), "test_ch")
			if err != nil {
				t.Fatalf("ActiveLiveForChannel status=%s: %v", st, err)
			}
			if !ok {
				t.Fatalf("status=%s: 录制中状态应被拦截 (ok=true)", st)
			}
		})
	}

	for _, st := range allowing {
		st := st
		t.Run(string("allows_"+st), func(t *testing.T) {
			database := setupDB(t)
			insertChannel(t, database)
			store := NewStore(database)

			sess, err := store.CreateLive(context.Background(), CreateLiveInput{
				ChannelID: "test_ch",
				RoomID:    12345,
				StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
			})
			if err != nil {
				t.Fatalf("CreateLive: %v", err)
			}
			if _, err := database.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, st, sess.ID); err != nil {
				t.Fatalf("set status %s: %v", st, err)
			}

			_, ok, err := store.ActiveLiveForChannel(context.Background(), "test_ch")
			if err != nil {
				t.Fatalf("ActiveLiveForChannel status=%s: %v", st, err)
			}
			if ok {
				t.Fatalf("status=%s: 后期/终态不得阻止新录制", st)
			}
		})
	}
}

// TestCreateLiveNewSessionAfterTerminal 验证历史终态 session 不阻止同频道创建新 session。
// 回归：曾因 ActiveLiveForChannel 白名单误扩到 published 等终态，导致该频道发布过一场后
// 永久无法再录。修复后白名单只覆盖录制中状态，新直播用不同分钟槽创建新 session。
func TestCreateLiveNewSessionAfterTerminal(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	// 第一场：已发布（终态）
	first, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		RoomID:    12345,
		StartedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("first CreateLive: %v", err)
	}
	if _, err := database.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, state.StatusPublished, first.ID); err != nil {
		t.Fatalf("mark published: %v", err)
	}
	// 频道级活跃检查不应拦截
	if _, ok, err := store.ActiveLiveForChannel(context.Background(), "test_ch"); ok || err != nil {
		t.Fatalf("published 终态后不应有活跃录制, got ok=%v err=%v", ok, err)
	}

	// 第二场：不同分钟槽，应能成功创建
	second, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		RoomID:    12345,
		StartedAt: time.Date(2026, 5, 8, 20, 30, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("second CreateLive 应成功（终态后允许新直播）, got: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("第二场应是新 session, 复用了第一场 id=%s", first.ID)
	}
}

func TestSanitizeSlug(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"HelloWorld", "helloworld"},
		{"hello-world_test", "hello-world_test"},
		{"BV1xx411c7mD", "bv1xx411c7md"},
		{"  spaces  ", "spaces"},
		{"特殊字符!!!", ""},
		{"a  b  c", "a-b-c"},
		{"UPPER-lower", "upper-lower"},
		{"", ""},
		{"---", ""},
		{"123abc", "123abc"},
		{"abc-123", "abc-123"},
	}

	for _, tc := range tests {
		got := sanitizeSlug(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestFindLiveSessionByTimeWindow(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	// Create a live session at a known time
	liveTime := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	_, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		RoomID:    12345,
		StartedAt: liveTime,
	})
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}

	// Should find within 4-hour window
	found, err := store.FindLiveSessionByTimeWindow(context.Background(), "test_ch", liveTime, 4*time.Hour)
	if err != nil {
		t.Fatalf("FindLiveSessionByTimeWindow: %v", err)
	}
	if found.SourceType != "live_record" {
		t.Fatalf("expected source_type live_record, got %s", found.SourceType)
	}

	// Should not find for different channel
	_, err = store.FindLiveSessionByTimeWindow(context.Background(), "other_ch", liveTime, 4*time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for different channel, got %v", err)
	}

	// Should not find outside window
	outsideTime := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)
	_, err = store.FindLiveSessionByTimeWindow(context.Background(), "test_ch", outsideTime, 4*time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound outside window, got %v", err)
	}
}

func TestFindDownloadByTimeWindow(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	// Create a download session at a known time
	dlTime := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	_, _, err := store.CreateDownload(context.Background(), CreateDownloadInput{
		ChannelID: "test_ch",
		SourceID:  "BV1window",
		Title:     "回放",
		SourceURL: "https://www.bilibili.com/video/BV1window",
		StartedAt: dlTime,
	})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}

	// Should find within 4-hour window
	found, err := store.FindDownloadByTimeWindow(context.Background(), "test_ch", dlTime, 4*time.Hour)
	if err != nil {
		t.Fatalf("FindDownloadByTimeWindow: %v", err)
	}
	if found.SourceType != "download" {
		t.Fatalf("expected source_type download, got %s", found.SourceType)
	}

	// Should not find for different channel
	_, err = store.FindDownloadByTimeWindow(context.Background(), "other_ch", dlTime, 4*time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for different channel, got %v", err)
	}

	// Should not find outside window
	outsideTime := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)
	_, err = store.FindDownloadByTimeWindow(context.Background(), "test_ch", outsideTime, 4*time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound outside window, got %v", err)
	}
}

// --- SetArchivedAt 测试（归档任务用，验证写时间戳 + 清 last_error）---

func TestSetArchivedAt_SuccessClearsLastError(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	sess, err := store.CreateLive(context.Background(), CreateLiveInput{
		ChannelID: "test_ch",
		Title:     "归档测试",
		RoomID:    12345,
		StartedAt: time.Date(2026, 6, 23, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateLive: %v", err)
	}
	// 模拟归档失败写入 last_error
	_, err = database.Exec(`UPDATE sessions SET last_error = ? WHERE id = ?`, "archive failed once", sess.ID)
	if err != nil {
		t.Fatalf("seed last_error: %v", err)
	}

	if err := store.SetArchivedAt(context.Background(), sess.ID, time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("SetArchivedAt: %v", err)
	}
	got, err := store.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ArchivedAt == "" {
		t.Error("ArchivedAt empty, want timestamp")
	}
	if got.LastError != "" {
		t.Errorf("LastError = %q, want empty (cleared on archive success)", got.LastError)
	}
}

func TestSetArchivedAt_NotFound(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	err := store.SetArchivedAt(context.Background(), "missing_session", time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSetArchivedAt_EmptyID(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)

	err := store.SetArchivedAt(context.Background(), "  ", time.Now())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}
