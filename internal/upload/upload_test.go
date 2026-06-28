package upload

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

// --- fake implementations ---

type fakeCopier struct {
	copyFunc func(ctx context.Context, source, target string) error
}

func (f fakeCopier) Copy(ctx context.Context, source string, target string) error {
	if f.copyFunc != nil {
		return f.copyFunc(ctx, source, target)
	}
	return nil
}

type fakeDeleter struct {
	deleteCalled bool
	deleteTarget string
	deleteErr    error
}

func (f *fakeDeleter) Delete(ctx context.Context, target string) error {
	f.deleteCalled = true
	f.deleteTarget = target
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return nil
}

// --- helpers ---

type uploadTestFixture struct {
	cfg      *config.Config
	sessions *session.Store
	states   *state.Store
	pool     *worker.Pool
	database *sql.DB
}

func defaultUploadConfig(outputRoot string) *config.Config {
	return &config.Config{
		OutputRoot: outputRoot,
		WebDAV: config.WebDAVConfig{
			Remote:   "remote:",
			BasePath: "/base",
		},
		Upload: config.UploadConfig{
			CleanupPolicy: "none",
		},
		ASRTemp: config.ASRTempConfig{
			RcloneRemote: "temp:",
			BasePath:     "/tmp",
		},
	}
}

func setupUploadTest(t *testing.T) *uploadTestFixture {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := defaultUploadConfig(t.TempDir())
	sessions := session.NewStore(database)
	states := state.NewStore(database)
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	// Create pool without starting it to avoid background task execution
	// that would cause SQLITE_BUSY during HandleTask calls
	pool := worker.NewPool(taskStore, hub, 1, nil)
	return &uploadTestFixture{cfg: cfg, sessions: sessions, states: states, pool: pool, database: database}
}

func (f *uploadTestFixture) insertChannel(t *testing.T, id string) {
	t.Helper()
	_, err := f.database.Exec(`INSERT OR IGNORE INTO channels (id, name, uid, live_room_id) VALUES (?, ?, ?, ?)`,
		id, "test-channel", 10001, 1234)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func (f *uploadTestFixture) insertSession(t *testing.T, id, slug, channelID, status string) {
	t.Helper()
	_, err := f.database.Exec(`INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, slug, channelID, "live_record", "source-1", "Test Session", status)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func (f *uploadTestFixture) createSessionDir(t *testing.T, channelID, slug string) string {
	t.Helper()
	dir := filepath.Join(f.cfg.OutputRoot, channelID, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	return dir
}

// --- CreateTask tests ---

func TestCreateTaskSuccess(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	copier := fakeCopier{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	task, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Type != TaskType {
		t.Fatalf("task type = %q, want %q", task.Type, TaskType)
	}
}

func TestCreateTaskRecapDone(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusRecapDone))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	copier := fakeCopier{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	task, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Type != TaskType {
		t.Fatalf("task type = %q, want %q", task.Type, TaskType)
	}
}

func TestCreateTaskWrongStatus(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusMediaReady))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	copier := fakeCopier{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error for wrong status")
	}
	if !strings.Contains(err.Error(), ErrSessionNotReady.Error()) {
		t.Fatalf("error = %v, want ErrSessionNotReady", err)
	}
}

func TestCreateTaskNoRemote(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.WebDAV.Remote = ""

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error for missing remote config")
	}
	if !strings.Contains(err.Error(), ErrConfigMissing.Error()) {
		t.Fatalf("error = %v, want ErrConfigMissing", err)
	}
}

func TestCreateTaskDirNotExist(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	// Do NOT create session dir

	copier := fakeCopier{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error for missing session dir")
	}
	if !strings.Contains(err.Error(), ErrArchiveMissing.Error()) {
		t.Fatalf("error = %v, want ErrArchiveMissing", err)
	}
}

func TestCreateTaskNotDirectory(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))

	// Create a file instead of directory at the session path
	sessionPath := filepath.Join(fix.cfg.OutputRoot, "ch1", "live_20260101_120000")
	if err := os.MkdirAll(filepath.Join(fix.cfg.OutputRoot, "ch1"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	copier := fakeCopier{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error for non-directory session path")
	}
	if !strings.Contains(err.Error(), ErrArchiveMissing.Error()) {
		t.Fatalf("error = %v, want ErrArchiveMissing", err)
	}
}

func TestCreateTaskActiveConflict(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	copier := fakeCopier{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("first CreateTask: %v", err)
	}
	_, err = h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected conflict error")
	}
	if !strings.Contains(err.Error(), worker.ErrTaskConflict.Error()) {
		t.Fatalf("error = %v, want ErrTaskConflict", err)
	}
}

func TestCreateTaskSessionNotFound(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")

	copier := fakeCopier{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	_, err := h.CreateTask(context.Background(), fix.pool, "nonexistent")
	if err == nil {
		t.Fatalf("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), session.ErrNotFound.Error()) {
		t.Fatalf("error = %v, want session.ErrNotFound", err)
	}
}

// --- Fetch tests ---

func TestFetchSuccess(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	var copiedSource, copiedTarget string
	copier := fakeCopier{
		copyFunc: func(ctx context.Context, source, target string) error {
			copiedSource = source
			copiedTarget = target
			return nil
		},
	}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)

	sess, err := h.Fetch(context.Background(), "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if sess.ID != "ch1_live_20260101_120000" {
		t.Fatalf("session id = %q, want ch1_live_20260101_120000", sess.ID)
	}
	if copiedSource == "" || copiedTarget == "" {
		t.Fatalf("Copy was not called")
	}
	if !strings.HasPrefix(copiedSource, "remote:") {
		t.Fatalf("source = %q, want remote: prefix", copiedSource)
	}
}

func TestFetchNoRemote(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.WebDAV.Remote = ""

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	_, err := h.Fetch(context.Background(), "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error for missing remote")
	}
	if !strings.Contains(err.Error(), ErrConfigMissing.Error()) {
		t.Fatalf("error = %v, want ErrConfigMissing", err)
	}
}

func TestFetchSessionNotFound(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")

	copier := fakeCopier{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	_, err := h.Fetch(context.Background(), "nonexistent")
	if err == nil {
		t.Fatalf("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), session.ErrNotFound.Error()) {
		t.Fatalf("error = %v, want session.ErrNotFound", err)
	}
}

func TestFetchCopyFails(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	copyErr := errors.New("copy failed")
	copier := fakeCopier{
		copyFunc: func(ctx context.Context, source, target string) error {
			return copyErr
		},
	}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	_, err := h.Fetch(context.Background(), "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error from Copy")
	}
	if !strings.Contains(err.Error(), copyErr.Error()) {
		t.Fatalf("error = %v, want copy error", err)
	}
}

// --- Cleanup policy tests ---

func TestCleanupPolicyNone(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	// Create asr subdirectory
	asrDir := filepath.Join(sessionDir, "asr")
	if err := os.MkdirAll(asrDir, 0o755); err != nil {
		t.Fatalf("mkdir asr: %v", err)
	}

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "none"

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	if _, err := os.Stat(asrDir); err != nil {
		t.Fatalf("asr dir should still exist after none policy: %v", err)
	}
}

func TestCleanupPolicyNoneEmpty(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	asrDir := filepath.Join(sessionDir, "asr")
	if err := os.MkdirAll(asrDir, 0o755); err != nil {
		t.Fatalf("mkdir asr: %v", err)
	}

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = ""

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	if _, err := os.Stat(asrDir); err != nil {
		t.Fatalf("asr dir should still exist after empty policy: %v", err)
	}
}

func TestCleanupPolicyUnknown(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	asrDir := filepath.Join(sessionDir, "asr")
	if err := os.MkdirAll(asrDir, 0o755); err != nil {
		t.Fatalf("mkdir asr: %v", err)
	}

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "unknown"

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	// Should not panic
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	if _, err := os.Stat(asrDir); err != nil {
		t.Fatalf("asr dir should still exist after unknown policy: %v", err)
	}
}

func TestCleanupGeneratedRemovesASRDir(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	asrDir := filepath.Join(sessionDir, "asr")
	if err := os.MkdirAll(filepath.Join(asrDir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "generated"

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	if _, err := os.Stat(asrDir); !os.IsNotExist(err) {
		t.Fatalf("asr dir should be removed after generated policy")
	}

	// Session dir itself should still exist
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("session dir should still exist: %v", err)
	}
}

func TestCleanupGeneratedDirNotExist(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "generated"

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	// asr/ doesn't exist - should not panic or error
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	// Session dir should still exist
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("session dir should still exist: %v", err)
	}
}

func TestCleanupTempRemovesPublicAudio(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	asrDir := filepath.Join(sessionDir, "asr")
	if err := os.MkdirAll(asrDir, 0o755); err != nil {
		t.Fatalf("mkdir asr: %v", err)
	}
	publicAudio := filepath.Join(asrDir, "audio.public.json")
	if err := os.WriteFile(publicAudio, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "temp"

	deleter := &fakeDeleter{}
	copier := fakeCopier{}
	h := &Handler{cfg: cfg, sessions: fix.sessions, states: fix.states, copier: copier, deleter: deleter}
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	if _, err := os.Stat(publicAudio); !os.IsNotExist(err) {
		t.Fatalf("audio.public.json should be removed after temp policy")
	}
}

func TestCleanupTempDeletesRemote(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	asrDir := filepath.Join(sessionDir, "asr")
	if err := os.MkdirAll(asrDir, 0o755); err != nil {
		t.Fatalf("mkdir asr: %v", err)
	}

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "temp"

	deleter := &fakeDeleter{}
	copier := fakeCopier{}
	h := &Handler{cfg: cfg, sessions: fix.sessions, states: fix.states, copier: copier, deleter: deleter}
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	if !deleter.deleteCalled {
		t.Fatalf("Delete should be called")
	}
	if !strings.Contains(deleter.deleteTarget, "temp:") {
		t.Fatalf("delete target = %q, want to contain temp: remote", deleter.deleteTarget)
	}
	if !strings.Contains(deleter.deleteTarget, "ch1") {
		t.Fatalf("delete target = %q, want to contain channel id", deleter.deleteTarget)
	}
}

func TestCleanupTempLocalFileNotExist(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "temp"

	deleter := &fakeDeleter{}
	copier := fakeCopier{}
	h := &Handler{cfg: cfg, sessions: fix.sessions, states: fix.states, copier: copier, deleter: deleter}
	// No asr/audio.public.json exists - should not panic
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	// Session dir should still exist
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("session dir should still exist: %v", err)
	}
}

// --- CleanupAll tests ---

func TestCleanupAllRemovesSessionDir(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusUploaded))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "all"

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	// cleanupAll 删除整个本地场次目录（raw/asr/package/recap/metadata.json）
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session dir should be removed after all policy, got err=%v", err)
	}
}

func TestCleanupAllSetsLocalAvailableFalse(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusUploaded))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "all"

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	// 删除整个目录后 local_available 应被置为 false，驱动 glossary/recap/publisher 守卫。
	got, err := fix.sessions.Get(context.Background(), "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.LocalAvailable {
		t.Fatalf("LocalAvailable = true, want false after cleanupAll")
	}
}

func TestFetchSetsLocalAvailableTrue(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusUploaded))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	// 模拟上传后已清理：本地目录不存在且 local_available=false
	if err := os.RemoveAll(sessionDir); err != nil {
		t.Fatalf("remove session dir: %v", err)
	}
	if err := fix.sessions.SetLocalAvailable(context.Background(), "ch1_live_20260101_120000", false); err != nil {
		t.Fatalf("set local_available false: %v", err)
	}

	copier := fakeCopier{
		copyFunc: func(ctx context.Context, source, target string) error {
			// 模拟 rclone copy：重建目标目录
			return os.MkdirAll(target, 0o755)
		},
	}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, copier)
	got, err := h.Fetch(context.Background(), "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.LocalAvailable {
		t.Fatalf("LocalAvailable = false, want true after Fetch")
	}
	// 确认 DB 层面也已置回
	persisted, err := fix.sessions.Get(context.Background(), "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("get session after fetch: %v", err)
	}
	if !persisted.LocalAvailable {
		t.Fatalf("persisted LocalAvailable = false, want true after Fetch")
	}
}

func TestCleanupAllSkipsIfNotUploaded(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusRecapDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "all"

	copier := fakeCopier{}
	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("session dir should still exist when status is recap_done: %v", err)
	}
}

func TestCleanupAllSessionQueryFails(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	sessionDir := fix.createSessionDir(t, "ch1", "live_20260101_120000")

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "all"

	copier := fakeCopier{}
	// Use a sessions store backed by a different (closed) DB to make Get fail
	badDB, err := db.Open(filepath.Join(t.TempDir(), "bad.db"))
	if err != nil {
		t.Fatalf("open bad db: %v", err)
	}
	// Close immediately to make queries fail
	_ = badDB.Close()
	badSessions := session.NewStore(badDB)

	h := &Handler{cfg: cfg, sessions: badSessions, states: fix.states, copier: copier}
	// Should not panic
	h.cleanupSession(context.Background(), sessionDir, session.Session{ID: "ch1_live_20260101_120000", ChannelID: "ch1", Slug: "live_20260101_120000"})

	// Session dir should still exist since cleanup couldn't verify status
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("session dir should still exist when query fails: %v", err)
	}
}

// --- HandleTask integration tests ---

func TestHandleTaskSuccess(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	var copyCalled bool
	copier := fakeCopier{
		copyFunc: func(ctx context.Context, source, target string) error {
			copyCalled = true
			return nil
		},
	}

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "none"

	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	task, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	reporter := &noopReporter{}
	err = h.HandleTask(context.Background(), task, reporter)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if !copyCalled {
		t.Fatalf("Copy should have been called")
	}

	// Verify session status changed to uploaded
	sess, err := fix.sessions.Get(context.Background(), "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if sess.Status != string(state.StatusUploaded) {
		t.Fatalf("session status = %q, want %q", sess.Status, state.StatusUploaded)
	}
}

func TestHandleTaskCopyFails(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	fix.createSessionDir(t, "ch1", "live_20260101_120000")

	copyErr := errors.New("rclone copy failed")
	copier := fakeCopier{
		copyFunc: func(ctx context.Context, source, target string) error {
			return copyErr
		},
	}

	cfg := defaultUploadConfig(fix.cfg.OutputRoot)
	cfg.Upload.CleanupPolicy = "none"

	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	task, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	reporter := &noopReporter{}
	err = h.HandleTask(context.Background(), task, reporter)
	if err == nil {
		t.Fatalf("expected error from HandleTask when Copy fails")
	}
	if !strings.Contains(err.Error(), copyErr.Error()) {
		t.Fatalf("error = %v, want copy error", err)
	}

	// upload 失败不再改变 session 主状态，只记录 last_error
	sess, err := fix.sessions.Get(context.Background(), "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if sess.Status != string(state.StatusASRDone) {
		t.Fatalf("session status = %q, want %q (upload failure should not change session state)", sess.Status, state.StatusASRDone)
	}
}

// TestHandleTaskSessionDirMissing 验证 ISS-5/8：状态为 asr_done 但 sessionDir 缺失时，
// HandleTask 应直接失败且不推进到 uploaded（堵住"状态先于产物"的异常推进）。
func TestHandleTaskSessionDirMissing(t *testing.T) {
	fix := setupUploadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	// 故意不创建 sessionDir，模拟状态被异常推进但产物缺失

	copier := fakeCopier{
		copyFunc: func(ctx context.Context, source, target string) error {
			t.Fatalf("Copy 不应在 sessionDir 缺失时被调用")
			return nil
		},
	}
	cfg := defaultUploadConfig(fix.cfg.OutputRoot)

	h := NewHandler(cfg, fix.sessions, fix.states, copier)
	// 直接构造 task（不经 CreateTask——CreateTask 的 validateUploadReady 会先失败）
	task := worker.Task{
		ID:        "task_missing_dir",
		ChannelID: "ch1",
		SessionID: "ch1_live_20260101_120000",
		Type:      TaskType,
		Payload:   "{}",
	}

	reporter := &noopReporter{}
	if err := h.HandleTask(context.Background(), task, reporter); err == nil {
		t.Fatal("期望 sessionDir 缺失时返回错误")
	}

	sess, err := fix.sessions.Get(context.Background(), "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if sess.Status != string(state.StatusASRDone) {
		t.Errorf("session status = %q, want %q（缺产物不应推进）", sess.Status, state.StatusASRDone)
	}
}

// --- noop reporter ---

type noopReporter struct{}

func (noopReporter) Progress(ctx context.Context, progress int, message string) error {
	return nil
}
