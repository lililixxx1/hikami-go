package state

import (
	"context"
	"path/filepath"
	"testing"

	"database/sql"
	"errors"

	"hikami-go/internal/db"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

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
	_, err := database.Exec(
		`INSERT INTO channels (id, name, uid, enabled) VALUES (?, ?, ?, 1)`,
		"test_ch", "Test", 1,
	)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func insertSession(t *testing.T, database *sql.DB, sessionID, status string) {
	t.Helper()
	_, err := database.Exec(
		`INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, source_url, status)
		 VALUES (?, 'test_slug', 'test_ch', 'live_record', 'src_1', 'Test Session', '', ?)`,
		sessionID, status,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 1: TestNextValidTransitions — table-driven, all legal transitions
// ---------------------------------------------------------------------------

func TestNextValidTransitions(t *testing.T) {
	tests := []struct {
		name    string
		current Status
		event   Event
		want    Status
	}{
		// discovered -> downloading / recording / importing
		{"discovered + download_started -> downloading", StatusDiscovered, EventDownloadStarted, StatusDownloading},
		{"discovered + live_record_started -> recording", StatusDiscovered, EventLiveRecordStarted, StatusRecording},
		{"discovered + import_started -> importing", StatusDiscovered, EventImportStarted, StatusImporting},

		// downloading -> downloading / media_ready
		{"downloading + download_succeeded -> downloading", StatusDownloading, EventDownloadSucceeded, StatusDownloading},
		{"downloading + normalize_succeeded -> media_ready", StatusDownloading, EventNormalizeSucceeded, StatusMediaReady},

		// recording -> recording / media_ready
		{"recording + live_record_succeeded -> recording", StatusRecording, EventLiveRecordSucceeded, StatusRecording},
		{"recording + normalize_succeeded -> media_ready", StatusRecording, EventNormalizeSucceeded, StatusMediaReady},

		// importing -> importing / media_ready
		{"importing + import_succeeded -> importing", StatusImporting, EventImportSucceeded, StatusImporting},
		{"importing + normalize_succeeded -> media_ready", StatusImporting, EventNormalizeSucceeded, StatusMediaReady},

		// media_ready -> asr_submitted
		{"media_ready + asr_submitted -> asr_submitted", StatusMediaReady, EventASRSubmitted, StatusASRSubmitted},

		// asr_submitted -> asr_done
		{"asr_submitted + asr_succeeded -> asr_done", StatusASRSubmitted, EventASRSucceeded, StatusASRDone},

		// asr_done -> recap_done / uploaded
		{"asr_done + recap_succeeded -> recap_done", StatusASRDone, EventRecapSucceeded, StatusRecapDone},
		{"asr_done + upload_succeeded -> uploaded", StatusASRDone, EventUploadSucceeded, StatusUploaded},

		// recap_done -> uploaded / published
		{"recap_done + upload_succeeded -> uploaded", StatusRecapDone, EventUploadSucceeded, StatusUploaded},
		{"recap_done + publish_succeeded -> published", StatusRecapDone, EventPublishSucceeded, StatusPublished},

		// uploaded -> published
		{"uploaded + publish_succeeded -> published", StatusUploaded, EventPublishSucceeded, StatusPublished},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Next(tt.current, tt.event)
			if err != nil {
				t.Fatalf("Next(%s, %s) error: %v", tt.current, tt.event, err)
			}
			if got != tt.want {
				t.Fatalf("Next(%s, %s) = %s, want %s", tt.current, tt.event, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 2: TestNextInvalidTransition — illegal transition returns ErrInvalidTransition
// ---------------------------------------------------------------------------

func TestNextInvalidTransition(t *testing.T) {
	tests := []struct {
		name    string
		current Status
		event   Event
	}{
		{"media_ready + download_started", StatusMediaReady, EventDownloadStarted},
		{"discovered + normalize_succeeded", StatusDiscovered, EventNormalizeSucceeded},
		{"asr_submitted + upload_succeeded", StatusASRSubmitted, EventUploadSucceeded},
		{"published + download_started", StatusPublished, EventDownloadStarted},
		{"failed + download_started", StatusFailed, EventDownloadStarted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Next(tt.current, tt.event)
			if err == nil {
				t.Fatalf("Next(%s, %s) expected error, got nil", tt.current, tt.event)
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("Next(%s, %s) error = %v, want ErrInvalidTransition", tt.current, tt.event, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 3: TestTaskFailedFromAnyState — EventTaskFailed from any status -> StatusFailed
// ---------------------------------------------------------------------------

func TestTaskFailedFromAnyState(t *testing.T) {
	allStatuses := []Status{
		StatusDiscovered,
		StatusDownloading,
		StatusRecording,
		StatusImporting,
		StatusMediaReady,
		StatusASRSubmitted,
		StatusASRDone,
		StatusRecapDone,
		StatusUploaded,
		StatusPublished,
		StatusFailed,
	}
	for _, s := range allStatuses {
		t.Run(string(s), func(t *testing.T) {
			got, err := Next(s, EventTaskFailed)
			if err != nil {
				t.Fatalf("Next(%s, task_failed) error: %v", s, err)
			}
			if got != StatusFailed {
				t.Fatalf("Next(%s, task_failed) = %s, want failed", s, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 4: TestNullable — nullable("") -> nil, nullable("x") -> "x"
// ---------------------------------------------------------------------------

func TestNullable(t *testing.T) {
	t.Run("empty string returns nil", func(t *testing.T) {
		got := nullable("")
		if got != nil {
			t.Fatalf("nullable(\"\") = %v, want nil", got)
		}
	})
	t.Run("non-empty string returns value", func(t *testing.T) {
		got := nullable("x")
		if got == nil {
			t.Fatalf("nullable(\"x\") = nil, want non-nil")
		}
		s, ok := got.(string)
		if !ok {
			t.Fatalf("nullable(\"x\") type = %T, want string", got)
		}
		if s != "x" {
			t.Fatalf("nullable(\"x\") = %q, want %q", s, "x")
		}
	})
}

// ---------------------------------------------------------------------------
// Test 5: TestApplySuccess — insert session, Apply(download_started) -> downloading
// ---------------------------------------------------------------------------

func TestApplySuccess(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	insertSession(t, database, "sess_apply_ok", "discovered")

	store := NewStore(database)
	ctx := context.Background()

	next, err := store.Apply(ctx, "sess_apply_ok", EventDownloadStarted, "task_1", "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if next != StatusDownloading {
		t.Fatalf("Apply result = %s, want downloading", next)
	}

	// Verify status persisted in DB
	var status string
	err = database.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = 'sess_apply_ok'").Scan(&status)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "downloading" {
		t.Fatalf("DB status = %s, want downloading", status)
	}
}

// ---------------------------------------------------------------------------
// Test 6: TestApplySessionNotFound — nonexistent sessionID -> ErrSessionNotFound
// ---------------------------------------------------------------------------

func TestApplySessionNotFound(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	// No session inserted

	store := NewStore(database)
	ctx := context.Background()

	_, err := store.Apply(ctx, "nonexistent_id", EventDownloadStarted, "task_1", "")
	if err != ErrSessionNotFound {
		t.Fatalf("Apply error = %v, want ErrSessionNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Test 7: TestApplyInvalidTransition — media_ready + download_started -> ErrInvalidTransition
// ---------------------------------------------------------------------------

func TestApplyInvalidTransition(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	insertSession(t, database, "sess_invalid", "media_ready")

	store := NewStore(database)
	ctx := context.Background()

	_, err := store.Apply(ctx, "sess_invalid", EventDownloadStarted, "task_1", "")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Apply error = %v, want ErrInvalidTransition", err)
	}
}

// ---------------------------------------------------------------------------
// Test 8: TestApplyTaskFailedSetsError — task_failed sets last_error
// v6 修订:测试场景模拟真实流程(task 进入时 Apply 已写 current_task_id),
// 这样 CAS 检查 current_task_id=task_1 才会匹配。
// ---------------------------------------------------------------------------

func TestApplyTaskFailedSetsError(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	insertSession(t, database, "sess_fail", "downloading")

	store := NewStore(database)
	ctx := context.Background()

	// v6: 模拟 task_1 进入流程(写 current_task_id=task_1)
	// downloading + EventDownloadSucceeded → downloading(状态不变,但 current_task_id 写入)
	if _, err := store.Apply(ctx, "sess_fail", EventDownloadSucceeded, "task_1", ""); err != nil {
		t.Fatalf("setup Apply EventDownloadSucceeded: %v", err)
	}

	// task_1 失败:CAS 检查 current_task_id=task_1 匹配 → 写 failed
	next, err := store.Apply(ctx, "sess_fail", EventTaskFailed, "task_1", "network timeout")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if next != StatusFailed {
		t.Fatalf("Apply result = %s, want failed", next)
	}

	var status, lastError string
	err = database.QueryRowContext(ctx,
		"SELECT status, last_error FROM sessions WHERE id = 'sess_fail'",
	).Scan(&status, &lastError)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status = %s, want failed", status)
	}
	if lastError != "network timeout" {
		t.Fatalf("last_error = %q, want %q", lastError, "network timeout")
	}
}

// ---------------------------------------------------------------------------
// Test 9: TestApplyUploadSucceededSetsTimestamp — upload_succeeded sets uploaded_at
// ---------------------------------------------------------------------------

func TestApplyUploadSucceededSetsTimestamp(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	insertSession(t, database, "sess_upload", "asr_done")

	store := NewStore(database)
	ctx := context.Background()

	next, err := store.Apply(ctx, "sess_upload", EventUploadSucceeded, "task_1", "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if next != StatusUploaded {
		t.Fatalf("Apply result = %s, want uploaded", next)
	}

	var uploadedAt string
	err = database.QueryRowContext(ctx,
		"SELECT uploaded_at FROM sessions WHERE id = 'sess_upload'",
	).Scan(&uploadedAt)
	if err != nil {
		t.Fatalf("query uploaded_at: %v", err)
	}
	if uploadedAt == "" {
		t.Fatal("uploaded_at should be set, got empty string")
	}
}

// ---------------------------------------------------------------------------
// Test 10: TestApplyPublishSucceededSetsTimestamp — publish_succeeded sets published_at
// ---------------------------------------------------------------------------

func TestApplyPublishSucceededSetsTimestamp(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	insertSession(t, database, "sess_publish", "uploaded")

	store := NewStore(database)
	ctx := context.Background()

	next, err := store.Apply(ctx, "sess_publish", EventPublishSucceeded, "task_1", "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if next != StatusPublished {
		t.Fatalf("Apply result = %s, want published", next)
	}

	var publishedAt string
	err = database.QueryRowContext(ctx,
		"SELECT published_at FROM sessions WHERE id = 'sess_publish'",
	).Scan(&publishedAt)
	if err != nil {
		t.Fatalf("query published_at: %v", err)
	}
	if publishedAt == "" {
		t.Fatal("published_at should be set, got empty string")
	}
}

// ---------------------------------------------------------------------------
// Test 11: TestApplyWithPublishTarget_WritesTargetInTx — publish_target 与 published_at 同事务落库（ISS-4）
// ---------------------------------------------------------------------------

func TestApplyWithPublishTarget_WritesTargetInTx(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	insertSession(t, database, "sess_pubtarget", "uploaded")

	store := NewStore(database)
	ctx := context.Background()

	next, err := store.ApplyWithPublishTarget(ctx, "sess_pubtarget", "task_1", "draft:abc123")
	if err != nil {
		t.Fatalf("ApplyWithPublishTarget: %v", err)
	}
	if next != StatusPublished {
		t.Fatalf("result = %s, want published", next)
	}

	var status, publishedAt, publishTarget string
	err = database.QueryRowContext(ctx,
		"SELECT status, published_at, publish_target FROM sessions WHERE id = 'sess_pubtarget'",
	).Scan(&status, &publishedAt, &publishTarget)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "published" {
		t.Fatalf("status = %s, want published", status)
	}
	if publishedAt == "" {
		t.Fatal("published_at should be set in the same transaction")
	}
	if publishTarget != "draft:abc123" {
		t.Fatalf("publish_target = %q, want %q", publishTarget, "draft:abc123")
	}
}


// ---------------------------------------------------------------------------
// v6 新增测试:TestApplyTaskFailed_EmptyTaskID_NoCAS — 空 taskID 走原逻辑(不加 CAS)
// ---------------------------------------------------------------------------

// TestApplyTaskFailed_EmptyTaskID_NoCAS 验证空 taskID 时 EventTaskFailed 走原 UPDATE 逻辑
// (不加 CAS),向后兼容 main.go:236/279/322 的"自动任务创建失败"场景。
// 这些场景在 task 入队前就失败,没有 task.id,session.current_task_id 可能是任意值。
// v6 r19e HIGH #1:如果加 CAS 会让这些正常失败 callback 被静默丢弃。
func TestApplyTaskFailed_EmptyTaskID_NoCAS(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	// session.current_task_id 不预设(NULL),模拟"自动任务创建失败"场景
	insertSession(t, database, "sess_empty_taskid", "media_ready")

	store := NewStore(database)
	ctx := context.Background()

	// 空 taskID 调用 EventTaskFailed,应该走原逻辑写入 failed(不被 CAS 拦截)
	next, err := store.Apply(ctx, "sess_empty_taskid", EventTaskFailed, "", "auto asr task creation failed: network error")
	if err != nil {
		t.Fatalf("Apply EventTaskFailed with empty taskID: %v", err)
	}
	if next != StatusFailed {
		t.Fatalf("Apply result = %s, want failed (empty taskID should bypass CAS)", next)
	}

	var status, lastError string
	err = database.QueryRowContext(ctx,
		"SELECT status, last_error FROM sessions WHERE id = 'sess_empty_taskid'",
	).Scan(&status, &lastError)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status = %s, want failed", status)
	}
	if lastError != "auto asr task creation failed: network error" {
		t.Fatalf("last_error = %q, want auto asr task creation failed: network error", lastError)
	}
}

// ---------------------------------------------------------------------------
// v6 新增测试:TestApplyTaskFailed_CASMismatch_Discarded — CAS 不匹配时丢弃 callback
// ---------------------------------------------------------------------------

// TestApplyTaskFailed_CASMismatch_Discarded 验证非空 taskID 的 EventTaskFailed 在
// session.current_task_id 不匹配时被丢弃(模拟 reset 清空 current_task_id 后旧 callback 到达)。
// v6 r19d HIGH #1 + r19e HIGH #2:防止 reset 后延迟 callback 把 session 又写回 failed。
func TestApplyTaskFailed_CASMismatch_Discarded(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	// session.current_task_id="task_A"(模拟 task_A 进入流程时的状态)
	insertSession(t, database, "sess_cas", "media_ready")
	_, err := database.Exec(`UPDATE sessions SET current_task_id = 'task_A' WHERE id = 'sess_cas'`)
	if err != nil {
		t.Fatalf("set current_task_id: %v", err)
	}

	store := NewStore(database)
	ctx := context.Background()

	// 模拟 reset 清空 current_task_id(NULL)
	_, err = database.Exec(`UPDATE sessions SET current_task_id = NULL, status = 'media_ready' WHERE id = 'sess_cas'`)
	if err != nil {
		t.Fatalf("simulate reset: %v", err)
	}

	// 延迟的 task_A callback 到达:session.current_task_id=NULL ≠ taskID=task_A → CAS 失败 → 丢弃
	next, err := store.Apply(ctx, "sess_cas", EventTaskFailed, "task_A", "old failure")
	if err != nil {
		t.Fatalf("Apply EventTaskFailed CAS mismatch: %v (should not error)", err)
	}
	// 关键断言:返回当前状态(media_ready),不是 failed
	if next != StatusMediaReady {
		t.Fatalf("Apply result = %s, want media_ready (CAS mismatch should discard callback)", next)
	}

	// session 状态应该保持 media_ready(未被覆盖)
	var status string
	err = database.QueryRowContext(ctx,
		"SELECT status FROM sessions WHERE id = 'sess_cas'",
	).Scan(&status)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "media_ready" {
		t.Fatalf("status = %s, want media_ready (callback should be discarded)", status)
	}
}

// TestApplyTaskFailed_CASMatch_WritesFailed 验证正常流程 CAS 匹配时写 failed。
// task 进入流程时 Apply 写 current_task_id=task_X,task 失败时 callback taskID=task_X 匹配。
func TestApplyTaskFailed_CASMatch_WritesFailed(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	insertSession(t, database, "sess_match", "media_ready")

	store := NewStore(database)
	ctx := context.Background()

	// task_X 进入流程(media_ready → asr_submitted,写 current_task_id=task_X)
	next, err := store.Apply(ctx, "sess_match", EventASRSubmitted, "task_X", "")
	if err != nil {
		t.Fatalf("Apply EventASRSubmitted: %v", err)
	}
	if next != StatusASRSubmitted {
		t.Fatalf("after EventASRSubmitted: status = %s, want asr_submitted", next)
	}

	// task_X 失败:CAS 检查 current_task_id=task_X 匹配 → 写 failed
	next, err = store.Apply(ctx, "sess_match", EventTaskFailed, "task_X", "asr failed")
	if err != nil {
		t.Fatalf("Apply EventTaskFailed: %v", err)
	}
	if next != StatusFailed {
		t.Fatalf("Apply result = %s, want failed (CAS should match)", next)
	}

	var status string
	err = database.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = 'sess_match'").Scan(&status)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status = %s, want failed", status)
	}
}
