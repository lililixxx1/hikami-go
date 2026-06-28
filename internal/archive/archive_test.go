package archive

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/upload"
	"hikami-go/internal/worker"
)

// --- fakes ---

type fakeCopier struct {
	copyCalls []copyCall
	copyErr   error
	copyFunc  func(ctx context.Context, source, target string) error
}

type copyCall struct {
	source, target string
}

func (f *fakeCopier) Copy(ctx context.Context, source, target string) error {
	f.copyCalls = append(f.copyCalls, copyCall{source: source, target: target})
	if f.copyFunc != nil {
		return f.copyFunc(ctx, source, target)
	}
	return f.copyErr
}

type fakeDeleter struct {
	deleteCalls []string
	deleteErr   error
}

func (f *fakeDeleter) Delete(ctx context.Context, target string) error {
	f.deleteCalls = append(f.deleteCalls, target)
	return f.deleteErr
}

// --- helpers ---

type archiveTestFixture struct {
	cfg      *config.Config
	sessions *session.Store
	states   *state.Store
	pool     *worker.Pool
	database *sql.DB
	copier   *fakeCopier
	deleter  *fakeDeleter
	handler  *Handler
}

func defaultArchiveConfig(outputRoot string) *config.Config {
	return &config.Config{
		OutputRoot: outputRoot,
		WebDAV: config.WebDAVConfig{
			Remote:   "remote:",
			BasePath: "/base",
		},
		Archive: config.ArchiveConfig{
			AutoAfterPublish: false,
			CleanupPolicy:    "none",
		},
		ASRTemp: config.ASRTempConfig{
			RcloneRemote: "temp:",
			BasePath:     "/tmp",
		},
	}
}

func setupArchiveTest(t *testing.T) *archiveTestFixture {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := defaultArchiveConfig(t.TempDir())
	sessions := session.NewStore(database)
	states := state.NewStore(database)
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	pool := worker.NewPool(taskStore, hub, 1, nil)
	copier := &fakeCopier{}
	deleter := &fakeDeleter{}
	handler := NewHandler(cfg, sessions, states, copier, deleter)
	return &archiveTestFixture{cfg: cfg, sessions: sessions, states: states, pool: pool, database: database, copier: copier, deleter: deleter, handler: handler}
}

func (f *archiveTestFixture) insertChannel(t *testing.T, id string) {
	t.Helper()
	_, err := f.database.Exec(`INSERT OR IGNORE INTO channels (id, name, uid, live_room_id) VALUES (?, ?, ?, ?)`,
		id, "test-channel", 10001, 1234)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func (f *archiveTestFixture) insertSession(t *testing.T, id, slug, channelID, status string) {
	t.Helper()
	_, err := f.database.Exec(`INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, slug, channelID, "live_record", "source-1", "Test Session", status)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func (f *archiveTestFixture) createSessionDir(t *testing.T, channelID, slug string) string {
	t.Helper()
	dir := filepath.Join(f.cfg.OutputRoot, channelID, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	return dir
}

type noopReporter struct{}

func (noopReporter) Progress(ctx context.Context, percent int, message string) error { return nil }

// --- CreateTask tests ---

func TestCreateTaskSuccess(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	fix.createSessionDir(t, "ch1", "live_1")

	task, err := fix.handler.CreateTask(context.Background(), fix.pool, "ch1_live_1")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.SessionID != "ch1_live_1" {
		t.Errorf("task.SessionID = %q, want ch1_live_1", task.SessionID)
	}
	if task.Type != TaskType {
		t.Errorf("task.Type = %q, want %q", task.Type, TaskType)
	}
}

func TestCreateTaskWrongStatus(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.insertChannel(t, "ch1")
	// recap_done 不是归档的合法起点（应 published）
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusRecapDone))
	fix.createSessionDir(t, "ch1", "live_1")

	_, err := fix.handler.CreateTask(context.Background(), fix.pool, "ch1_live_1")
	if !errors.Is(err, ErrSessionNotReady) {
		t.Fatalf("err = %v, want ErrSessionNotReady", err)
	}
}

func TestCreateTaskDirMissing(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	// 不创建目录

	_, err := fix.handler.CreateTask(context.Background(), fix.pool, "ch1_live_1")
	if !errors.Is(err, ErrArchiveMissing) {
		t.Fatalf("err = %v, want ErrArchiveMissing", err)
	}
}

func TestCreateTaskWebDAVNotConfigured(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.cfg.WebDAV = config.WebDAVConfig{} // 既无 remote 也无 url
	fix.handler = NewHandler(fix.cfg, fix.sessions, fix.states, fix.copier, fix.deleter)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	fix.createSessionDir(t, "ch1", "live_1")

	_, err := fix.handler.CreateTask(context.Background(), fix.pool, "ch1_live_1")
	if !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("err = %v, want ErrConfigMissing", err)
	}
}

func TestCreateTaskActiveArchiveConflict(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	fix.createSessionDir(t, "ch1", "live_1")

	if _, err := fix.handler.CreateTask(context.Background(), fix.pool, "ch1_live_1"); err != nil {
		t.Fatalf("first CreateTask: %v", err)
	}
	// 同 session 已有活跃 archive 任务
	_, err := fix.handler.CreateTask(context.Background(), fix.pool, "ch1_live_1")
	if !errors.Is(err, worker.ErrTaskConflict) {
		t.Fatalf("err = %v, want ErrTaskConflict", err)
	}
}

// TestCreateTaskActiveUploadConflict 验证 upload/archive 互斥 gate：同 session 活跃 upload 时拒绝归档。
func TestCreateTaskActiveUploadConflict(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	fix.createSessionDir(t, "ch1", "live_1")

	// 插入一个活跃 upload 任务（绕过 upload handler 的状态校验，直接入队 store）
	uploadStore := worker.NewStore(fix.database)
	_, err := uploadStore.Create(context.Background(), worker.CreateInput{
		ChannelID: "ch1", SessionID: "ch1_live_1", Type: upload.TaskType, Payload: "{}",
	})
	if err != nil {
		t.Fatalf("seed upload task: %v", err)
	}

	_, err = fix.handler.CreateTask(context.Background(), fix.pool, "ch1_live_1")
	if !errors.Is(err, worker.ErrTaskConflict) {
		t.Fatalf("err = %v, want ErrTaskConflict (upload/archive mutex)", err)
	}
}

// --- HandleTask tests ---

func TestHandleTaskSuccessDoesNotAdvanceState(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	fix.createSessionDir(t, "ch1", "live_1")

	task := worker.Task{ID: "t1", ChannelID: "ch1", SessionID: "ch1_live_1", Type: TaskType}
	if err := fix.handler.HandleTask(context.Background(), task, noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	// 核心断言：session 状态仍是 published，未被降级
	got, err := fix.sessions.Get(context.Background(), "ch1_live_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(state.StatusPublished) {
		t.Errorf("session.Status = %q, want published (archive must not advance state)", got.Status)
	}
	// archived_at 已写入
	if got.ArchivedAt == "" {
		t.Errorf("session.ArchivedAt empty, want timestamp")
	}
	// copier 被调用了一次
	if len(fix.copier.copyCalls) != 1 {
		t.Errorf("copier called %d times, want 1", len(fix.copier.copyCalls))
	}
}

func TestHandleTaskCopyFailureReturnsErr(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.copier.copyErr = errors.New("webdav unreachable")
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	fix.createSessionDir(t, "ch1", "live_1")

	task := worker.Task{ID: "t1", ChannelID: "ch1", SessionID: "ch1_live_1", Type: TaskType}
	err := fix.handler.HandleTask(context.Background(), task, noopReporter{})
	if err == nil {
		t.Fatal("HandleTask err = nil, want copy error")
	}
	// 复制失败：archived_at 不应写入
	got, _ := fix.sessions.Get(context.Background(), "ch1_live_1")
	if got.ArchivedAt != "" {
		t.Errorf("ArchivedAt = %q, want empty on copy failure", got.ArchivedAt)
	}
}

func TestHandleTaskWrongStatus(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusUploaded))
	fix.createSessionDir(t, "ch1", "live_1")

	task := worker.Task{ID: "t1", ChannelID: "ch1", SessionID: "ch1_live_1", Type: TaskType}
	err := fix.handler.HandleTask(context.Background(), task, noopReporter{})
	if err == nil {
		t.Fatal("HandleTask err = nil, want status error")
	}
	if len(fix.copier.copyCalls) != 0 {
		t.Errorf("copier called on invalid status, want 0 calls")
	}
}

// --- Cleanup tests ---

func TestHandleTaskCleanupAllRemovesDirAndSetsLocalAvailable(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.cfg.Archive.CleanupPolicy = "all"
	fix.handler = NewHandler(fix.cfg, fix.sessions, fix.states, fix.copier, fix.deleter)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	dir := fix.createSessionDir(t, "ch1", "live_1")

	task := worker.Task{ID: "t1", ChannelID: "ch1", SessionID: "ch1_live_1", Type: TaskType}
	if err := fix.handler.HandleTask(context.Background(), task, noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("session dir still exists after cleanup=all, err=%v", err)
	}
	got, _ := fix.sessions.Get(context.Background(), "ch1_live_1")
	if got.LocalAvailable {
		t.Errorf("LocalAvailable = true, want false after cleanup=all")
	}
}

// TestHandleTaskCleanupAllSkipsWhenStatusReverted 验证 cleanupAll 的 published 守卫：
// 归档期间状态被并发回退（published→uploaded）时，不删除本地目录。
func TestHandleTaskCleanupAllSkipsWhenStatusReverted(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.cfg.Archive.CleanupPolicy = "all"
	// copy 成功后、cleanup 前把状态改成 uploaded，模拟并发「删除专栏」
	fix.copier.copyFunc = func(ctx context.Context, source, target string) error {
		_, _ = fix.states.Apply(ctx, "ch1_live_1", state.EventPublishReverted, "t1", "")
		return nil
	}
	fix.handler = NewHandler(fix.cfg, fix.sessions, fix.states, fix.copier, fix.deleter)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	dir := fix.createSessionDir(t, "ch1", "live_1")

	task := worker.Task{ID: "t1", ChannelID: "ch1", SessionID: "ch1_live_1", Type: TaskType}
	if err := fix.handler.HandleTask(context.Background(), task, noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	// 守卫生效：目录仍在（状态已回退到 uploaded，不该删）
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("session dir removed despite status revert, err=%v", err)
	}
}

func TestHandleTaskCleanupGeneratedRemovesAsrOnly(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.cfg.Archive.CleanupPolicy = "generated"
	fix.handler = NewHandler(fix.cfg, fix.sessions, fix.states, fix.copier, fix.deleter)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_1", "live_1", "ch1", string(state.StatusPublished))
	dir := fix.createSessionDir(t, "ch1", "live_1")
	asrDir := filepath.Join(dir, "asr")
	if err := os.MkdirAll(asrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recapDir := filepath.Join(dir, "recap")
	if err := os.MkdirAll(recapDir, 0o755); err != nil {
		t.Fatal(err)
	}

	task := worker.Task{ID: "t1", ChannelID: "ch1", SessionID: "ch1_live_1", Type: TaskType}
	if err := fix.handler.HandleTask(context.Background(), task, noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if _, err := os.Stat(asrDir); !os.IsNotExist(err) {
		t.Errorf("asr/ still exists, want removed")
	}
	if _, err := os.Stat(recapDir); err != nil {
		t.Errorf("recap/ removed, want retained: %v", err)
	}
}

// --- archiveTarget native/rclone tests ---

func TestArchiveTargetRclone(t *testing.T) {
	fix := setupArchiveTest(t) // 默认 remote + base_path，nativeWebDAV=false
	got := fix.handler.archiveTarget(session.Session{ChannelID: "ch1", Slug: "live_1"})
	want := "remote:/base/ch1/live_1"
	if got != want {
		t.Errorf("archiveTarget (rclone) = %q, want %q", got, want)
	}
}

func TestArchiveTargetNative(t *testing.T) {
	fix := setupArchiveTest(t)
	fix.cfg.WebDAV = config.WebDAVConfig{URL: "https://webdav.example.com", BasePath: "/hikami"}
	fix.handler = NewHandler(fix.cfg, fix.sessions, fix.states, fix.copier, fix.deleter)
	got := fix.handler.archiveTarget(session.Session{ChannelID: "ch1", Slug: "live_1"})
	want := "ch1/live_1" // native 模式下相对路径
	if got != want {
		t.Errorf("archiveTarget (native) = %q, want %q", got, want)
	}
}
