package importer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

// --- findImportSource tests ---

func TestFindImportSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "import.source.mp3"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := findImportSource(dir)
	if err != nil {
		t.Fatalf("findImportSource: %v", err)
	}
	if filepath.Base(got) != "import.source.mp3" {
		t.Fatalf("got %q, want import.source.mp3", got)
	}
}

func TestFindImportSourceDifferentExt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "import.source.wav"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := findImportSource(dir)
	if err != nil {
		t.Fatalf("findImportSource: %v", err)
	}
	if filepath.Base(got) != "import.source.wav" {
		t.Fatalf("got %q, want import.source.wav", got)
	}
}

func TestFindImportSourceNoExt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "import.source"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := findImportSource(dir)
	if err != nil {
		t.Fatalf("findImportSource: %v", err)
	}
	if filepath.Base(got) != "import.source" {
		t.Fatalf("got %q, want import.source", got)
	}
}

func TestFindImportSourceNotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := findImportSource(dir)
	if err == nil {
		t.Fatalf("expected error for no matching file")
	}
}

func TestFindImportSourceSkipsDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "import.source"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := findImportSource(dir)
	if err == nil {
		t.Fatalf("expected error when only a directory named import.source exists")
	}
}

func TestFindImportSourceEmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := findImportSource(dir)
	if err == nil {
		t.Fatalf("expected error for empty dir")
	}
}

func TestFindImportSourceDirNotExist(t *testing.T) {
	_, err := findImportSource("/nonexistent/path/12345")
	if err == nil {
		t.Fatalf("expected error for nonexistent dir")
	}
}

// --- writeJSON tests ---

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	m := map[string]any{"key": "value", "num": 42}
	if err := writeJSON(path, m); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Must be valid JSON
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["key"] != "value" {
		t.Fatalf("key = %v, want 'value'", parsed["key"])
	}
	if parsed["num"] != float64(42) {
		t.Fatalf("num = %v, want 42", parsed["num"])
	}

	// Must end with newline
	if len(data) > 0 && data[len(data)-1] != '\n' {
		t.Fatalf("JSON output should end with newline")
	}
}

// --- 扩展测试 ---

func TestFindImportSource_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "import.source.mp3"), []byte("audio"), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "import.source.wav"), []byte("wav"), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	got, err := findImportSource(dir)
	if err != nil {
		t.Fatalf("findImportSource: %v", err)
	}
	base := filepath.Base(got)
	if !strings.HasPrefix(base, "import.source") {
		t.Fatalf("got %q, want import.source.*", base)
	}
}

func TestWriteJSON_MetadataWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "import.raw.json")
	m := map[string]any{
		"media_file":   "test.mp3",
		"danmaku_file": "danmaku.jsonl",
	}
	if err := writeJSON(path, m); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if parsed["media_file"] != "test.mp3" {
		t.Fatalf("media_file = %v, want test.mp3", parsed["media_file"])
	}
	if parsed["danmaku_file"] != "danmaku.jsonl" {
		t.Fatalf("danmaku_file = %v, want danmaku.jsonl", parsed["danmaku_file"])
	}
}

func TestCreateFromMultipart_Success(t *testing.T) {
	h, pool := newTestImporterHandler(t)
	defer pool.Stop()

	mediaBody := "fake audio data"
	mediaHeader := createMultipartFileHeader(t, "test.mp3", mediaBody)

	task, err := h.CreateFromMultipart(context.Background(), session.CreateImportInput{
		ChannelID: "test_ch",
		Title:     "测试导入",
		StartedAt: time.Now(),
	}, mediaHeader, nil)
	if err != nil {
		t.Fatalf("CreateFromMultipart: %v", err)
	}
	if task.ID == "" {
		t.Fatal("task ID should not be empty")
	}

	// 验证 raw 目录文件存在
	rawDir := filepath.Join(h.cfg.OutputRoot, "test_ch")
	// 查找 slug 目录
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no session directory created")
	}
	slugDir := entries[0].Name()
	importFile := filepath.Join(rawDir, slugDir, "raw", "import.source.mp3")
	if _, err := os.Stat(importFile); err != nil {
		t.Fatalf("import source file missing: %v", err)
	}
	metaFile := filepath.Join(rawDir, slugDir, "raw", "import.raw.json")
	if _, err := os.Stat(metaFile); err != nil {
		t.Fatalf("import metadata file missing: %v", err)
	}
}

func TestCreateFromMultipart_WithDanmaku(t *testing.T) {
	h, pool := newTestImporterHandler(t)
	defer pool.Stop()

	mediaHeader := createMultipartFileHeader(t, "audio.mp3", "audio data")
	danmakuHeader := createMultipartFileHeader(t, "danmaku.jsonl", `{"text":"弹幕"}`)

	task, err := h.CreateFromMultipart(context.Background(), session.CreateImportInput{
		ChannelID: "test_ch",
		Title:     "带弹幕导入",
		StartedAt: time.Now(),
	}, mediaHeader, danmakuHeader)
	if err != nil {
		t.Fatalf("CreateFromMultipart: %v", err)
	}
	if task.ID == "" {
		t.Fatal("task ID should not be empty")
	}

	// 查找弹幕文件
	rawDir := filepath.Join(h.cfg.OutputRoot, "test_ch")
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	slugDir := entries[0].Name()
	danmakuFile := filepath.Join(rawDir, slugDir, "raw", "danmaku.jsonl")
	if _, err := os.Stat(danmakuFile); err != nil {
		t.Fatalf("danmaku file missing: %v", err)
	}
}

func TestHandleTask_FFmpegConvert(t *testing.T) {
	h, pool, sessions, _, taskStore := newTestImporterHandlerFull(t)
	defer pool.Stop()

	sess, err := sessions.CreateImport(context.Background(), session.CreateImportInput{
		ChannelID: "test_ch",
		Title:     "转换测试",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create import session: %v", err)
	}

	// 写入 import source 文件
	rawDir := filepath.Join(h.cfg.OutputRoot, "test_ch", sess.Slug, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatalf("mkdir raw: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rawDir, "import.source.mp3"), []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	task, err := taskStore.Create(context.Background(), worker.CreateInput{
		ChannelID: "test_ch",
		SessionID: sess.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task, err = taskStore.MarkRunning(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}

	err = h.HandleTask(context.Background(), task, noopReporter{})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	audioPath := filepath.Join(rawDir, "audio.m4a")
	if _, err := os.Stat(audioPath); err != nil {
		t.Fatalf("converted audio file missing: %v", err)
	}
}

func TestHandleTask_ConvertFail(t *testing.T) {
	cfg := &config.Config{
		OutputRoot: filepath.Join(t.TempDir(), "output"),
	}
	database := newTestDB(t)
	sessions := session.NewStore(database)
	states := state.NewStore(database)
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	pool := worker.NewPool(taskStore, hub, 1, nil)
	if err := pool.Start(context.Background(), 1); err != nil {
		t.Fatalf("start pool: %v", err)
	}
	defer pool.Stop()
	if _, err := database.Exec("INSERT INTO channels(id, name, uid, live_room_id, enabled) VALUES ('test_ch', 'Test', 1, 0, 1)"); err != nil {
		t.Fatalf("insert test channel: %v", err)
	}
	converter := &mockConverter{err: errors.New("ffmpeg convert error")}
	handler := NewHandler(cfg, sessions, states, pool, converter)

	sess, err := sessions.CreateImport(context.Background(), session.CreateImportInput{
		ChannelID: "test_ch",
		Title:     "转换失败测试",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create import session: %v", err)
	}

	rawDir := filepath.Join(cfg.OutputRoot, "test_ch", sess.Slug, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatalf("mkdir raw: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rawDir, "import.source.mp3"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	task, err := taskStore.Create(context.Background(), worker.CreateInput{
		ChannelID: "test_ch",
		SessionID: sess.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task, err = taskStore.MarkRunning(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}

	err = handler.HandleTask(context.Background(), task, noopReporter{})
	if err == nil {
		t.Fatal("expected error from convert failure")
	}
	if !strings.Contains(err.Error(), "ffmpeg convert error") {
		t.Fatalf("error = %q, want containing 'ffmpeg convert error'", err.Error())
	}
}

func TestHandleTask_SourceMissing(t *testing.T) {
	h, pool, sessions, _, taskStore := newTestImporterHandlerFull(t)
	defer pool.Stop()

	sess, err := sessions.CreateImport(context.Background(), session.CreateImportInput{
		ChannelID: "test_ch",
		Title:     "源文件缺失测试",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create import session: %v", err)
	}

	// 创建空 raw 目录，不放置 import source 文件
	rawDir := filepath.Join(h.cfg.OutputRoot, "test_ch", sess.Slug, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatalf("mkdir raw: %v", err)
	}

	task, err := taskStore.Create(context.Background(), worker.CreateInput{
		ChannelID: "test_ch",
		SessionID: sess.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task, err = taskStore.MarkRunning(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}

	err = h.HandleTask(context.Background(), task, noopReporter{})
	if err == nil {
		t.Fatal("expected error for missing source file")
	}
}

// --- 辅助类型和函数 ---

type mockConverter struct {
	err error
}

func (m *mockConverter) Convert(ctx context.Context, inputPath string, outputPath string) error {
	if m.err != nil {
		return m.err
	}
	return os.WriteFile(outputPath, []byte("converted"), 0o644)
}

type noopReporter struct{}

func (noopReporter) Progress(ctx context.Context, progress int, message string) error { return nil }

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func newTestImporterHandler(t *testing.T) (*Handler, *worker.Pool) {
	t.Helper()
	h, pool, _, _, _ := newTestImporterHandlerFull(t)
	return h, pool
}

func newTestImporterHandlerFull(t *testing.T) (*Handler, *worker.Pool, *session.Store, *state.Store, *worker.Store) {
	t.Helper()
	database := newTestDB(t)
	cfg := &config.Config{
		OutputRoot: filepath.Join(t.TempDir(), "output"),
	}
	sessions := session.NewStore(database)
	states := state.NewStore(database)
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	pool := worker.NewPool(taskStore, hub, 1, nil)
	if err := pool.Start(context.Background(), 1); err != nil {
		t.Fatalf("start pool: %v", err)
	}
	// 创建测试用主播
	if _, err := database.Exec("INSERT INTO channels(id, name, uid, live_room_id, enabled) VALUES ('test_ch', 'Test', 1, 0, 1)"); err != nil {
		t.Fatalf("insert test channel: %v", err)
	}
	handler := NewHandler(cfg, sessions, states, pool, &mockConverter{})
	return handler, pool, sessions, states, taskStore
}

func createMultipartFileHeader(t *testing.T, filename, content string) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	reader := multipart.NewReader(bytes.NewReader(buf.Bytes()), writer.Boundary())
	form, err := reader.ReadForm(0)
	if err != nil {
		t.Fatalf("read form: %v", err)
	}
	// 注意：不调用 form.RemoveAll()，让文件保持存在供 handler 使用
	if fh := form.File["file"]; len(fh) > 0 {
		return fh[0]
	}
	t.Fatal("no file in multipart form")
	return nil
}
