package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/db"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	// Use WAL mode and busy timeout so concurrent writers retry instead of failing immediately.
	if _, err := database.Exec("PRAGMA journal_mode = WAL"); err != nil {
		t.Fatalf("set journal_mode: %v", err)
	}
	if _, err := database.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func insertChannel(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO channels (id, name, uid, enabled) VALUES (?, ?, ?, 1)`,
		"test_ch", "Test", 1)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	database := setupDB(t)
	insertChannel(t, database)
	return NewStore(database)
}

// waitForStatus polls the store until the task reaches the desired status or timeout.
func waitForStatus(ctx context.Context, s *Store, id string, want Status, timeout time.Duration) (Task, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := s.Get(ctx, id)
		if err != nil {
			return Task{}, err
		}
		if task.Status == want {
			return task, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := s.Get(ctx, id)
	return task, errors.New("timed out waiting for status " + string(want))
}

// ---------------------------------------------------------------------------
// 1. Store CRUD
// ---------------------------------------------------------------------------

func TestStoreCreateSuccess(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	task, err := s.Create(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "test_type",
		Payload:   `{"key":"val"}`,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == "" {
		t.Fatal("expected non-empty id")
	}
	if task.ID[:5] != "task_" {
		t.Fatalf("expected id prefix 'task_', got %q", task.ID)
	}
	if task.Status != StatusPending {
		t.Fatalf("expected status pending, got %q", task.Status)
	}
	if task.ChannelID != "test_ch" {
		t.Fatalf("expected channel_id test_ch, got %q", task.ChannelID)
	}
	if task.Type != "test_type" {
		t.Fatalf("expected type test_type, got %q", task.Type)
	}
	if task.Attempt != 1 {
		t.Fatalf("expected attempt 1, got %d", task.Attempt)
	}
}

func TestStoreCreateMissingChannelID(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, CreateInput{Type: "test_type"})
	if !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("expected ErrInvalidTask, got %v", err)
	}
}

func TestStoreCreateMissingType(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, CreateInput{ChannelID: "test_ch"})
	if !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("expected ErrInvalidTask, got %v", err)
	}
}

func TestStoreCreateDefaultPayload(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	task, err := s.Create(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "test_type",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.Payload != "{}" {
		t.Fatalf("expected default payload '{}', got %q", task.Payload)
	}
}

func TestStoreGetNotFound(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "task_nonexistent")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestStoreListEmpty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tasks, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if tasks == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestStoreListOrdering(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	t1, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "type_a"})
	if err != nil {
		t.Fatalf("Create t1: %v", err)
	}
	time.Sleep(1 * time.Second) // ensure different timestamps (SQLite datetime has 1s resolution)
	t2, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "type_b"})
	if err != nil {
		t.Fatalf("Create t2: %v", err)
	}

	tasks, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	// DESC ordering: t2 should come first
	if tasks[0].ID != t2.ID {
		t.Fatalf("expected first task id %q, got %q", t2.ID, tasks[0].ID)
	}
	if tasks[1].ID != t1.ID {
		t.Fatalf("expected second task id %q, got %q", t1.ID, tasks[1].ID)
	}
}

func TestStoreLifecycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != StatusPending {
		t.Fatalf("expected pending, got %q", created.Status)
	}

	running, err := s.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	if running.Status != StatusRunning {
		t.Fatalf("expected running, got %q", running.Status)
	}
	if running.StartedAt == "" {
		t.Fatal("expected non-empty started_at")
	}

	succeeded, err := s.MarkSucceeded(ctx, created.ID, "done")
	if err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	if succeeded.Status != StatusSucceeded {
		t.Fatalf("expected succeeded, got %q", succeeded.Status)
	}
	if succeeded.Progress != 100 {
		t.Fatalf("expected progress 100, got %d", succeeded.Progress)
	}
	if succeeded.FinishedAt == "" {
		t.Fatal("expected non-empty finished_at")
	}
}

func TestStoreMarkFailedFromPending(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	failed, err := s.MarkFailed(ctx, created.ID, "oops", errors.New("boom"))
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if failed.Status != StatusFailed {
		t.Fatalf("expected failed, got %q", failed.Status)
	}
	if failed.Error != "boom" {
		t.Fatalf("expected error 'boom', got %q", failed.Error)
	}
}

func TestStoreMarkFailedFromRunning(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	failed, err := s.MarkFailed(ctx, created.ID, "crashed", errors.New("segfault"))
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if failed.Status != StatusFailed {
		t.Fatalf("expected failed, got %q", failed.Status)
	}
}

func TestStoreMarkSucceededFromNonRunning(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.MarkSucceeded(ctx, created.ID, "done")
	if !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("expected ErrTaskConflict, got %v", err)
	}
}

func TestStoreRetryFailedTask(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.MarkFailed(ctx, created.ID, "fail", errors.New("err"))
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	retried, err := s.Retry(ctx, created.ID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if retried.Status != StatusPending {
		t.Fatalf("expected pending, got %q", retried.Status)
	}
	if retried.Attempt != 2 {
		t.Fatalf("expected attempt 2, got %d", retried.Attempt)
	}
	if retried.Progress != 0 {
		t.Fatalf("expected progress 0, got %d", retried.Progress)
	}
}

func TestStoreCancelPendingTask(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cancelled, err := s.Cancel(ctx, created.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("expected cancelled, got %q", cancelled.Status)
	}
}

func TestStoreCancelRunningTask(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	cancelled, err := s.Cancel(ctx, created.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("expected cancelled, got %q", cancelled.Status)
	}
}

// ---------------------------------------------------------------------------
// 2. Store Advanced
// ---------------------------------------------------------------------------

func TestStoreRetryNonFailedFails(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.Retry(ctx, created.ID)
	if !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("expected ErrTaskConflict, got %v", err)
	}
}

func TestStoreUpdateProgress(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	updated, err := s.UpdateProgress(ctx, created.ID, 50, "halfway")
	if err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	if updated.Progress != 50 {
		t.Fatalf("expected progress 50, got %d", updated.Progress)
	}
	if updated.Message != "halfway" {
		t.Fatalf("expected message 'halfway', got %q", updated.Message)
	}
}

func TestStoreUpdateProgressOutOfRange(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	_, err = s.UpdateProgress(ctx, created.ID, -1, "negative")
	if !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("expected ErrInvalidTask for -1, got %v", err)
	}

	_, err = s.UpdateProgress(ctx, created.ID, 101, "over")
	if !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("expected ErrInvalidTask for 101, got %v", err)
	}
}

func TestStoreActiveBySessionAndType(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Insert a session row since tasks.session_id has a FK.
	database := s.db
	_, err := database.Exec(
		`INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sess_1", "slug1", "test_ch", "live_record", "src1", "Test Session", "media_ready",
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	_, err = s.Create(ctx, CreateInput{
		ChannelID: "test_ch",
		SessionID: "sess_1",
		Type:      "test_type",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	task, found, err := s.ActiveBySessionAndType(ctx, "sess_1", "test_type")
	if err != nil {
		t.Fatalf("ActiveBySessionAndType: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if task.ID == "" {
		t.Fatal("expected non-empty task id")
	}
}

func TestStoreActiveBySessionAndTypeEmpty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	_, found, err := s.ActiveBySessionAndType(ctx, "nonexistent", "test_type")
	if err != nil {
		t.Fatalf("ActiveBySessionAndType: %v", err)
	}
	if found {
		t.Fatal("expected found=false")
	}
}

func TestStoreResetToPending(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	reset, err := s.ResetToPending(ctx, created.ID)
	if err != nil {
		t.Fatalf("ResetToPending: %v", err)
	}
	if reset.Status != StatusPending {
		t.Fatalf("expected pending, got %q", reset.Status)
	}
	if reset.Attempt != 2 {
		t.Fatalf("expected attempt 2, got %d", reset.Attempt)
	}
	if reset.StartedAt != "" {
		t.Fatalf("expected empty started_at, got %q", reset.StartedAt)
	}
}

func TestRecoverRunningLiveRecordAdopts(t *testing.T) {
	// 存活子进程模拟重启后残留 ffmpeg（ISS-6）
	sleepCmd := exec.Command("sleep", "10")
	if err := sleepCmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := sleepCmd.Process.Pid
	defer func() { _ = sleepCmd.Process.Kill() }()

	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	hub := NewHub()
	ctx := context.Background()

	created, err := store.Create(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "live_record",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.MarkRunning(ctx, created.ID); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	// 写入 pid 到 task.message（模拟 reportRecording 写入 "recording audio (pid:%d)"）
	if _, err := database.ExecContext(ctx, "UPDATE tasks SET message = ? WHERE id = ?",
		fmt.Sprintf("recording audio (pid:%d)", pid), created.ID); err != nil {
		t.Fatalf("update message: %v", err)
	}

	pool := NewPool(store, hub, 1, nil)
	go hub.Run()
	defer hub.Stop()

	var adoptedPID int
	var adoptedTaskID string
	pool.SetAdoptLiveRecordFn(func(ctx context.Context, task Task, p int) {
		adoptedPID = p
		adoptedTaskID = task.ID
	})

	if err := pool.recoverRunning(ctx); err != nil {
		t.Fatalf("recoverRunning: %v", err)
	}
	if adoptedTaskID != created.ID {
		t.Errorf("adopt task id = %q, want %q", adoptedTaskID, created.ID)
	}
	if adoptedPID != pid {
		t.Errorf("adopt pid = %d, want %d", adoptedPID, pid)
	}

	// 存活分支应保留 running 状态
	task, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Status != StatusRunning {
		t.Errorf("task status = %q, want running（存活进程应保留）", task.Status)
	}
}

func TestStoreRecoverRunning(t *testing.T) {
	// Tests Pool.recoverRunning with a running task of type "other_task"
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	hub := NewHub()
	ctx := context.Background()

	// Create and mark a task as running.
	created, err := store.Create(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "other_task",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = store.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	pool := NewPool(store, hub, 1, nil)
	// recoverRunning requires hub.Run() to be active for broadcasts.
	go hub.Run()
	defer hub.Stop()

	err = pool.recoverRunning(ctx)
	if err != nil {
		t.Fatalf("recoverRunning: %v", err)
	}

	// The "other_task" type falls into the default case: marked failed.
	task, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Status != StatusFailed {
		t.Fatalf("expected failed, got %q", task.Status)
	}
}

func TestStoreRecoverRunningMethod(t *testing.T) {
	// Tests Store.RecoverRunning — the generic method that marks all running → failed.
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, CreateInput{ChannelID: "test_ch", Type: "test_type"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	err = s.RecoverRunning(ctx)
	if err != nil {
		t.Fatalf("RecoverRunning: %v", err)
	}

	task, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Status != StatusFailed {
		t.Fatalf("expected failed, got %q", task.Status)
	}
}

// TestRecoverRunningReEnqueuesOrphanPending 验证 recoverRunning 恢复 pending 孤儿任务:
// 重启后内存队列清空,DB 里 pending 的 task 必须被重新入队执行(异常 #6)。
// pending task 从未被 worker 消费,重新入队不应递增 attempt、不应改 DB 状态。
func TestRecoverRunningReEnqueuesOrphanPending(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	hub := NewHub()
	ctx := context.Background()

	// 创建一个 pending task(直接 Create 即 pending,从未 MarkRunning)。
	created, err := store.Create(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "test_type",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 记录原始 attempt(Create 默认 0 或 1,不动它)。
	before, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}

	pool := NewPool(store, hub, 1, nil)
	// 注册 handler:执行时把 task id 记下,证明被重新入队并执行。
	var executed atomic.Value // string
	pool.Register("test_type", func(ctx context.Context, task Task, reporter Reporter) error {
		executed.Store(task.ID)
		return nil
	})

	// Start 会先 recoverRunning(重新入队 pending),再起 worker goroutines 消费队列。
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := pool.Start(ctx, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer pool.Stop()

	// 等待 worker 消费(最多 1s)。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if executed.Load() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if executed.Load() != created.ID {
		t.Fatalf("orphan pending task not executed: got %v, want %q", executed.Load(), created.ID)
	}

	// 执行后状态应为 succeeded(handler 返回 nil)。
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSucceeded {
		t.Errorf("task status = %q, want succeeded", got.Status)
	}
	if got.Attempt != before.Attempt {
		t.Errorf("attempt = %d, want %d(重新入队不应递增 attempt)", got.Attempt, before.Attempt)
	}
}

// TestRecoverRunningOrphanPendingAttemptsExhausted 验证 attempt 超限的 pending 孤儿
// 被标记 failed 并触发 syncSessionState(否则 session 永久卡 discovered)。
// 用 live_record 类型 + 带 SessionID,模拟真实孤儿(session 卡 discovered 的场景)。
func TestRecoverRunningOrphanPendingAttemptsExhausted(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	hub := NewHub()
	ctx := context.Background()

	// tasks.session_id 有 FK,先插 session(状态 discovered 模拟卡死的孤儿)。
	if _, err := database.ExecContext(ctx,
		`INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sess_orphan_1", "slug1", "test_ch", "live_record", "src1", "Test", "discovered"); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	created, err := store.Create(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "live_record",
		SessionID: "sess_orphan_1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 手动把 attempt 调高到超限(模拟重启循环下累积的重试次数)。
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET attempt = 100 WHERE id = ?`, created.ID); err != nil {
		t.Fatalf("bump attempt: %v", err)
	}

	pool := NewPool(store, hub, 1, &config.Config{Worker: config.WorkerConfig{MaxRetryAttempts: 3}})
	// 注册 live_record handler(否则 syncSessionState 的 bypassFailState 检查无意义)。
	pool.Register("live_record", func(ctx context.Context, task Task, reporter Reporter) error {
		return nil
	})
	go hub.Run()
	defer hub.Stop()

	// 注入 failSessionState 捕获,验证 session 状态被同步(死锁解除的关键)。
	var syncCalled atomic.Value // bool
	pool.SetFailSessionStateFn(func(ctx context.Context, task Task, event, taskID, msg string, bypass bool) error {
		syncCalled.Store(true)
		return nil
	})

	if err := pool.recoverRunning(ctx); err != nil {
		t.Fatalf("recoverRunning: %v", err)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("task status = %q, want failed(attempts exhausted)", got.Status)
	}
	if !syncCalled.Load().(bool) {
		t.Errorf("syncSessionState not called: orphan session would stay stuck in discovered")
	}
}

// ---------------------------------------------------------------------------
// 3. Pool
// ---------------------------------------------------------------------------

func TestPoolRegisterAndRun(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	hub := NewHub()

	pool := NewPool(store, hub, 1, nil)
	ctx := context.Background()

	pool.Register("success_type", func(ctx context.Context, task Task, reporter Reporter) error {
		return reporter.Progress(ctx, 50, "working")
	})

	if err := pool.Start(ctx, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer pool.Stop()

	task, err := pool.Enqueue(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "success_type",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Wait for the pool to process the task through to succeeded.
	result, err := waitForStatus(ctx, store, task.ID, StatusSucceeded, 5*time.Second)
	if err != nil {
		t.Fatalf("waitForStatus succeeded: %v (last status=%q)", err, result.Status)
	}
	if result.Progress != 100 {
		t.Fatalf("expected progress 100, got %d", result.Progress)
	}
}

func TestPoolRetry(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	hub := NewHub()

	pool := NewPool(store, hub, 1, nil)
	ctx := context.Background()

	var callCount int
	var mu sync.Mutex
	pool.Register("flaky_type", func(ctx context.Context, task Task, reporter Reporter) error {
		mu.Lock()
		callCount++
		count := callCount
		mu.Unlock()
		if count == 1 {
			return errors.New("first attempt fails")
		}
		return reporter.Progress(ctx, 100, "done")
	})

	if err := pool.Start(ctx, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer pool.Stop()

	task, err := pool.Enqueue(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "flaky_type",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Wait for the first attempt to fail.
	failed, err := waitForStatus(ctx, store, task.ID, StatusFailed, 5*time.Second)
	if err != nil {
		t.Fatalf("waitForStatus failed: %v (last status=%q)", err, failed.Status)
	}

	// Retry via pool.
	_, err = pool.Retry(ctx, task.ID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}

	// Wait for the second attempt to succeed.
	result, err := waitForStatus(ctx, store, task.ID, StatusSucceeded, 5*time.Second)
	if err != nil {
		t.Fatalf("waitForStatus succeeded: %v (last status=%q)", err, result.Status)
	}
}

func TestPoolCancel(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	hub := NewHub()

	pool := NewPool(store, hub, 1, nil)
	ctx := context.Background()

	executed := make(chan struct{})
	pool.Register("cancel_type", func(ctx context.Context, task Task, reporter Reporter) error {
		close(executed)
		return nil
	})

	if err := pool.Start(ctx, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer pool.Stop()

	task, err := pool.Enqueue(ctx, CreateInput{
		ChannelID: "test_ch",
		Type:      "cancel_type",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Cancel immediately via store (before worker picks it up).
	// The task is in pending state, so Cancel should succeed.
	cancelled, err := pool.Cancel(ctx, task.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("expected cancelled, got %q", cancelled.Status)
	}

	// Give the worker a moment — if it did pick up the task before cancel,
	// MarkRunning would have returned ErrTaskConflict and the handler would not run.
	select {
	case <-executed:
		t.Fatal("handler should not have executed for a cancelled task")
	case <-time.After(200 * time.Millisecond):
		// Expected: handler did not run.
	}

	result, err := store.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if result.Status != StatusCancelled {
		t.Fatalf("expected cancelled, got %q", result.Status)
	}
}

// ---------------------------------------------------------------------------
// 4. Hub
// ---------------------------------------------------------------------------

func TestHubSubscribeBroadcast(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	// Use a buffered subscriber to avoid blocking.
	sub := make(chan Event, 16)
	hub.subscribe <- sub

	task := Task{
		ID:        "task_abc",
		ChannelID: "ch1",
		Status:    StatusRunning,
		Progress:  42,
		Message:   "processing",
	}
	hub.Broadcast(task)

	select {
	case event := <-sub:
		if event.TaskID != "task_abc" {
			t.Fatalf("expected task_id 'task_abc', got %q", event.TaskID)
		}
		if event.Status != StatusRunning {
			t.Fatalf("expected status running, got %q", event.Status)
		}
		if event.Progress != 42 {
			t.Fatalf("expected progress 42, got %d", event.Progress)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestHubUnsubscribe(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	sub := make(chan Event, 16)
	hub.subscribe <- sub
	hub.unsubscribe <- sub

	// Broadcast after unsubscribe — sub channel should be closed (by Unsubscribe),
	// so we should not receive the event on it.
	task := Task{ID: "task_unsub", ChannelID: "ch1", Status: StatusPending}
	hub.Broadcast(task)

	select {
	case event, ok := <-sub:
		if !ok {
			// Channel was closed — correct behavior since Unsubscribe closes it.
		} else {
			t.Fatalf("should not have received event after unsubscribe, got %+v", event)
		}
	case <-time.After(200 * time.Millisecond):
		// Also acceptable: no event received.
	}
}

func TestHubStopClosesChannels(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	sub := make(chan Event, 16)
	hub.subscribe <- sub

	// Give subscribe time to be processed.
	time.Sleep(50 * time.Millisecond)

	hub.Stop()

	// After Stop, subscriber channel should be closed.
	_, ok := <-sub
	if ok {
		t.Fatal("expected subscriber channel to be closed after Stop")
	}
}

// ---------------------------------------------------------------------------
// 5. Helpers
// ---------------------------------------------------------------------------

func TestParsePIDFromMessage(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"12345", 12345},
		{"abc", 0},
		{"", 0},
		{"  99  ", 99},
		{"-5", 0},
		{"0", 0},
	}

	for _, tt := range tests {
		got := parsePIDFromMessage(tt.input)
		if got != tt.expected {
			t.Errorf("parsePIDFromMessage(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestIsProcessAlive(t *testing.T) {
	// Current process should be alive.
	if !isProcessAlive(os.Getpid()) {
		t.Fatal("expected current process to be alive")
	}

	// PID 0 is invalid.
	if isProcessAlive(0) {
		t.Fatal("expected PID 0 to be not alive")
	}

	// PID -1 is invalid.
	if isProcessAlive(-1) {
		t.Fatal("expected PID -1 to be not alive")
	}
}

// TestBypassFailStateRegistration 验证设计 4.3：WithBypassFailState 注册的任务类型
// 在 bypassFailState 查询时返回 true，普通任务返回 false；未注册类型返回 false。
func TestBypassFailStateRegistration(t *testing.T) {
	database := setupDB(t)
	store := NewStore(database)
	hub := NewHub()
	pool := NewPool(store, hub, 1, nil)

	pool.Register("normal_type", func(ctx context.Context, task Task, reporter Reporter) error {
		return nil
	})
	pool.Register("bypass_type", func(ctx context.Context, task Task, reporter Reporter) error {
		return nil
	}, WithBypassFailState())

	if pool.bypassFailState("normal_type") {
		t.Fatalf("normal_type should not bypass fail state")
	}
	if !pool.bypassFailState("bypass_type") {
		t.Fatalf("bypass_type should bypass fail state")
	}
	if pool.bypassFailState("unregistered") {
		t.Fatalf("unregistered type should not bypass fail state")
	}
}

// TestSyncSessionStateBypassFlag 验证 syncSessionState 把 bypass 标志透传给 failSessionState：
// 旁路任务失败时 bypass=true（cmd/hikami 据此仅写 last_error 不降级），普通任务 bypass=false。
// syncSessionState 只读 task.Type 和 task.SessionID，故用字面 Task 值即可，无需持久化。
func TestSyncSessionStateBypassFlag(t *testing.T) {
	database := setupDB(t)
	store := NewStore(database)
	hub := NewHub()
	pool := NewPool(store, hub, 1, nil)

	pool.Register("normal_type", func(ctx context.Context, task Task, reporter Reporter) error {
		return nil
	})
	pool.Register("bypass_type", func(ctx context.Context, task Task, reporter Reporter) error {
		return nil
	}, WithBypassFailState())

	var gotBypass *bool
	pool.SetFailSessionStateFn(func(ctx context.Context, task Task, event, taskID, msg string, bypass bool) error {
		b := bypass
		gotBypass = &b
		return nil
	})

	// 普通任务：bypass 应为 false
	pool.syncSessionState(context.Background(), Task{Type: "normal_type", SessionID: "sess1"}, "boom")
	if gotBypass == nil || *gotBypass {
		t.Fatalf("normal task bypass flag = %v, want false", gotBypass)
	}

	// 旁路任务：bypass 应为 true
	gotBypass = nil
	pool.syncSessionState(context.Background(), Task{Type: "bypass_type", SessionID: "sess1"}, "boom")
	if gotBypass == nil || !*gotBypass {
		t.Fatalf("bypass task bypass flag = %v, want true", gotBypass)
	}

	// 实例级 bypass：普通任务类型 + Task.BypassFailState=true（重新生成回顾场景）→ OR 逻辑透传 true
	// 这是本次新增的实例级标志核心路径，与类型级（WithBypassFailState）取 OR。
	gotBypass = nil
	pool.syncSessionState(context.Background(), Task{Type: "normal_type", SessionID: "sess1", BypassFailState: true}, "boom")
	if gotBypass == nil || !*gotBypass {
		t.Fatalf("instance-level bypass (normal type + BypassFailState=true) = %v, want true (OR logic)", gotBypass)
	}
}

// ---------------------------------------------------------------------------
// v6 新增:TestSyncSessionState_StaleAttempt_Discarded 验证 attempt 校验
// ---------------------------------------------------------------------------

// TestSyncSessionState_StaleAttempt_Discarded 验证 retry 后旧 attempt 的 callback 被丢弃。
// v6 r19e HIGH #2:Retry 复用同一 task ID 只递增 attempt,如果旧 attempt 的失败 callback
// 延迟到新 attempt 已启动后才到达,taskID 相同但 attempt 不同。
// worker 层在 syncSessionState 开头重查 task 当前 attempt,不匹配则丢弃。
func TestSyncSessionState_StaleAttempt_Discarded(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	// 插入 session + task(attempt=2,模拟已被 retry 过)
	_, err := database.Exec(`
		INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status, current_task_id, local_available)
		VALUES ('sess_retry', 'slug', 'test_ch', 'live_record', 'src', 'Test', 'asr_submitted', 'task_retry_1', 1)
	`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO tasks (id, channel_id, session_id, type, status, attempt, payload, progress, message, created_at, updated_at)
		VALUES ('task_retry_1', 'test_ch', 'sess_retry', 'asr', 'running', 2, '{}', 0, '', '2026-07-20T00:00:00+08:00', '2026-07-20T00:00:00+08:00')
	`)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	pool := NewPool(store, NewHub(), 1, nil)
	pool.Register("asr", func(ctx context.Context, task Task, reporter Reporter) error { return nil })

	failSessionCalled := false
	pool.SetFailSessionStateFn(func(ctx context.Context, task Task, event, taskID, msg string, bypass bool) error {
		failSessionCalled = true
		return nil
	})

	// 模拟旧 attempt=1 的 callback(taskID 相同但 attempt 不同)
	pool.syncSessionState(context.Background(), Task{
		ID:        "task_retry_1",
		Type:      "asr",
		SessionID: "sess_retry",
		Attempt:   1, // 旧 attempt(DB 里已是 2)
	}, "old failure")

	// 关键断言:failSessionState 不应该被调用(callback 被丢弃)
	if failSessionCalled {
		t.Fatal("failSessionState should NOT be called for stale attempt callback (attempt mismatch)")
	}
}

// TestSyncSessionState_FreshAttempt_Proceeds 验证 attempt 匹配时正常调用 failSessionState。
func TestSyncSessionState_FreshAttempt_Proceeds(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database)
	store := NewStore(database)
	_, err := database.Exec(`
		INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status, current_task_id, local_available)
		VALUES ('sess_fresh', 'slug', 'test_ch', 'live_record', 'src', 'Test', 'asr_submitted', 'task_fresh_1', 1)
	`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO tasks (id, channel_id, session_id, type, status, attempt, payload, progress, message, created_at, updated_at)
		VALUES ('task_fresh_1', 'test_ch', 'sess_fresh', 'asr', 'failed', 1, '{}', 0, '', '2026-07-20T00:00:00+08:00', '2026-07-20T00:00:00+08:00')
	`)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	pool := NewPool(store, NewHub(), 1, nil)
	pool.Register("asr", func(ctx context.Context, task Task, reporter Reporter) error { return nil })

	failSessionCalled := false
	pool.SetFailSessionStateFn(func(ctx context.Context, task Task, event, taskID, msg string, bypass bool) error {
		failSessionCalled = true
		return nil
	})

	// attempt=1 匹配 DB,应该正常调用 failSessionState
	pool.syncSessionState(context.Background(), Task{
		ID:        "task_fresh_1",
		Type:      "asr",
		SessionID: "sess_fresh",
		Attempt:   1, // 匹配 DB
	}, "fresh failure")

	if !failSessionCalled {
		t.Fatal("failSessionState should be called for fresh attempt callback (attempt matches)")
	}
}
