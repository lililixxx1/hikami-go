package recap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/glossary"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

// --- helpers ---

type recapTestFixture struct {
	cfg           *config.Config
	sessions      *session.Store
	states        *state.Store
	pool          *worker.Pool
	database      *sql.DB
	taskStore     *worker.Store
	hub           *worker.Hub
	glossaryStore *glossary.Store
	channelStore  *channel.Store
}

func speakerID(id int64) *int64 {
	return &id
}

func setupRecapTest(t *testing.T) *recapTestFixture {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := &config.Config{OutputRoot: t.TempDir()}
	sessions := session.NewStore(database)
	states := state.NewStore(database)
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	// Create pool with 1 worker but do NOT start it to avoid background task execution
	// that would cause SQLITE_BUSY. For CreateTask we only need the pool to enqueue.
	pool := worker.NewPool(taskStore, hub, 1, nil)
	// No pool.Start() - tasks won't be executed in background
	gs := glossary.NewStore(database)
	cs := channel.NewStore(database)
	return &recapTestFixture{cfg: cfg, sessions: sessions, states: states, pool: pool, database: database, taskStore: taskStore, hub: hub, glossaryStore: gs, channelStore: cs}
}

func (f *recapTestFixture) insertChannel(t *testing.T, id string) {
	t.Helper()
	_, err := f.database.Exec(`INSERT OR IGNORE INTO channels (id, name, uid, live_room_id) VALUES (?, ?, ?, ?)`,
		id, "test-channel", 10001, 1234)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func (f *recapTestFixture) insertSession(t *testing.T, id, slug, channelID, status string) {
	t.Helper()
	_, err := f.database.Exec(`INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, slug, channelID, "live_record", "source-1", "Test Session", status)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// --- Handler CreateTask tests ---

func TestCreateTaskSuccess(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))

	// Create transcript.txt
	sessionDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "live_20260101_120000")
	pkgDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	task, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Type != TaskType {
		t.Fatalf("task type = %q, want %q", task.Type, TaskType)
	}
	if task.Status != worker.StatusPending {
		t.Fatalf("task status = %q, want %q", task.Status, worker.StatusPending)
	}
}

func TestCreateTaskWrongStatus(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusMediaReady))

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error for wrong status")
	}
	if !strings.Contains(err.Error(), ErrSessionNotReady.Error()) {
		t.Fatalf("error = %v, want ErrSessionNotReady", err)
	}
}

func TestCreateTaskTranscriptMissing(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))

	// Do NOT create transcript.txt
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error for missing transcript")
	}
	if !strings.Contains(err.Error(), ErrTranscriptMissing.Error()) {
		t.Fatalf("error = %v, want ErrTranscriptMissing", err)
	}
}

func TestCreateTaskLocalUnavailable(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusUploaded))
	// 模拟上传 all 策略清理后：本地目录已删且 local_available=false
	if err := fix.sessions.SetLocalAvailable(context.Background(), "ch1_live_20260101_120000", false); err != nil {
		t.Fatalf("set local_available false: %v", err)
	}

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected error when local files unavailable")
	}
	if !strings.Contains(err.Error(), ErrTranscriptMissing.Error()) {
		t.Fatalf("error = %v, want ErrTranscriptMissing", err)
	}
	if !strings.Contains(err.Error(), "fetch from webdav first") {
		t.Fatalf("error = %v, want hint to fetch from webdav", err)
	}
}

func TestCreateTaskWithRangeLocalUnavailable(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusUploaded))
	if err := fix.sessions.SetLocalAvailable(context.Background(), "ch1_live_20260101_120000", false); err != nil {
		t.Fatalf("set local_available false: %v", err)
	}

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	_, err := h.CreateTaskWithRange(context.Background(), fix.pool, "ch1_live_20260101_120000", 10, 100)
	if err == nil {
		t.Fatalf("expected error when local files unavailable")
	}
	if !strings.Contains(err.Error(), ErrTranscriptMissing.Error()) {
		t.Fatalf("error = %v, want ErrTranscriptMissing", err)
	}
}

func TestCreateTaskActiveConflict(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))

	// Create transcript.txt
	sessionDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "live_20260101_120000")
	pkgDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	// First task succeeds
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("first CreateTask: %v", err)
	}
	// Second task should conflict
	_, err = h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err == nil {
		t.Fatalf("expected conflict error")
	}
	if !strings.Contains(err.Error(), worker.ErrTaskConflict.Error()) {
		t.Fatalf("error = %v, want ErrTaskConflict", err)
	}
}

func TestCreateTaskSessionNotFound(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	_, err := h.CreateTask(context.Background(), fix.pool, "nonexistent")
	if err == nil {
		t.Fatalf("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), session.ErrNotFound.Error()) {
		t.Fatalf("error = %v, want session.ErrNotFound", err)
	}
}

// --- Provider tests ---

// fakeCapabilityChecker 实现 CapabilityChecker 用于测试。
type fakeCapabilityChecker struct{ available bool }

func (f fakeCapabilityChecker) RecapGenerate() bool { return f.available }

// TestCreateTaskCapabilityUnavailable 验证设计 4.5：注入的 CapabilityChecker 判定回顾能力
// 不可用时，CreateTask 返回 ErrRecapUnavailable（即便状态/产物都就绪）。
func TestCreateTaskCapabilityUnavailable(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))

	// 产物就绪（否则会先被 ErrTranscriptMissing 挡住，无法验证能力 gate）
	pkgDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "live_20260101_120000", "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	h.SetCapabilityChecker(fakeCapabilityChecker{available: false})
	_, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if !errors.Is(err, ErrRecapUnavailable) {
		t.Fatalf("error = %v, want ErrRecapUnavailable", err)
	}

	// 能力恢复后应能正常入队
	h.SetCapabilityChecker(fakeCapabilityChecker{available: true})
	task, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("CreateTask after capability restored: %v", err)
	}
	if task.Type != TaskType {
		t.Fatalf("task type = %q, want %q", task.Type, TaskType)
	}
}

// TestCreateTaskCapabilityNotSet 验证未注入 CapabilityChecker 时不做能力校验（向后兼容）。
func TestCreateTaskCapabilityNotSet(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))
	pkgDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "live_20260101_120000", "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	// 不调用 SetCapabilityChecker → 不应因能力拒绝
	if _, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000"); err != nil {
		t.Fatalf("CreateTask without capability checker: %v", err)
	}
}

func TestLocalProvider(t *testing.T) {
	provider := LocalProvider{}
	result, err := provider.Generate(context.Background(), "", "hello transcript", session.Session{
		ID:    "s1",
		Title: "Test Title",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Raw == "" {
		t.Fatalf("raw is empty")
	}
	if !strings.Contains(result.Content, "Test Title") {
		t.Fatalf("content missing title: %s", result.Content)
	}
	if !strings.Contains(result.Content, "hello transcript") {
		t.Fatalf("content missing transcript content: %s", result.Content)
	}
}

func TestLocalProviderWithSystemPrompt(t *testing.T) {
	provider := LocalProvider{}
	result, err := provider.Generate(context.Background(), defaultSystemPrompt, "hello transcript", session.Session{
		ID:    "s1",
		Title: "Test Title",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(result.Content, "Test Title") {
		t.Fatalf("content missing title: %s", result.Content)
	}
}

func TestLocalProviderEmptyPrompt(t *testing.T) {
	provider := LocalProvider{}
	result, err := provider.Generate(context.Background(), "", "", session.Session{
		ID:    "s1",
		Title: "Test",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(result.Content, "暂无转写内容。") {
		t.Fatalf("content should contain '暂无转写内容。' for empty prompt, got: %s", result.Content)
	}
}

// --- Helper function tests ---

func TestFirstParagraph(t *testing.T) {
	got := firstParagraph("hello\n\nworld")
	if got != "hello" {
		t.Fatalf("firstParagraph = %q, want %q", got, "hello")
	}
}

func TestFirstParagraphEmpty(t *testing.T) {
	got := firstParagraph("")
	if got != "暂无转写内容。" {
		t.Fatalf("firstParagraph = %q, want %q", got, "暂无转写内容。")
	}
}

func TestFirstParagraphNoDoubleNewline(t *testing.T) {
	got := firstParagraph("single")
	if got != "single" {
		t.Fatalf("firstParagraph = %q, want %q", got, "single")
	}
}

func TestFirstParagraphLeadingNewlines(t *testing.T) {
	got := firstParagraph("\n\nhello")
	if got != "hello" {
		t.Fatalf("firstParagraph = %q, want %q (TrimSpace removes leading newlines before split)", got, "hello")
	}
}

func TestSafeName(t *testing.T) {
	got := safeName("a/b\\c d")
	if got != "a_b_c_d" {
		t.Fatalf("safeName = %q, want %q", got, "a_b_c_d")
	}
}

func TestParseChatCompletionContent(t *testing.T) {
	got := parseChatCompletionContent([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	if got != "hello" {
		t.Fatalf("parseChatCompletionContent = %q, want %q", got, "hello")
	}
}

func TestParseChatCompletionContentEmpty(t *testing.T) {
	got := parseChatCompletionContent([]byte(`{"choices":[]}`))
	if got != "" {
		t.Fatalf("parseChatCompletionContent = %q, want empty", got)
	}
}

func TestParseChatCompletionContentInvalidJSON(t *testing.T) {
	got := parseChatCompletionContent([]byte("not json"))
	if got != "" {
		t.Fatalf("parseChatCompletionContent = %q, want empty", got)
	}
}

func TestParseChatCompletionContentEmptyContent(t *testing.T) {
	got := parseChatCompletionContent([]byte(`{"choices":[{"message":{"content":""}}]}`))
	if got != "" {
		t.Fatalf("parseChatCompletionContent = %q, want empty", got)
	}
}

func TestParseChatCompletionResult_FinishReason(t *testing.T) {
	got := parseChatCompletionResult([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"hello"}}]}`))
	if got.Content != "hello" {
		t.Fatalf("Content = %q, want %q", got.Content, "hello")
	}
	if got.FinishReason != "length" {
		t.Fatalf("FinishReason = %q, want %q", got.FinishReason, "length")
	}
}

func TestParseAnthropicResult_StopReason(t *testing.T) {
	got := parseAnthropicResult([]byte(`{"stop_reason":"max_tokens","content":[{"type":"text","text":"hello"}]}`))
	if got.Content != "hello" {
		t.Fatalf("Content = %q, want %q", got.Content, "hello")
	}
	if got.FinishReason != "max_tokens" {
		t.Fatalf("FinishReason = %q, want %q", got.FinishReason, "max_tokens")
	}
}

func TestShouldContinueRecap(t *testing.T) {
	tests := []struct {
		name         string
		finishReason string
		want         bool
	}{
		{name: "openai length", finishReason: "length", want: true},
		{name: "anthropic max tokens", finishReason: "max_tokens", want: true},
		{name: "stop", finishReason: "stop", want: false},
		{name: "empty", finishReason: "", want: false},
		{name: "unknown", finishReason: "end_turn", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldContinueRecap(tt.finishReason); got != tt.want {
				t.Fatalf("shouldContinueRecap(%q) = %v, want %v", tt.finishReason, got, tt.want)
			}
		})
	}
}

func TestAppendContinuation(t *testing.T) {
	base := "# Test\n\n## 详细内容回顾\n\n前文"
	continuation := "## 详细内容回顾\n\n后文"
	got := appendContinuation(base, continuation)
	if strings.Count(got, "## 详细内容回顾") != 1 {
		t.Fatalf("expected duplicate heading to be removed, got: %s", got)
	}
	if !strings.Contains(got, "前文") || !strings.Contains(got, "后文") {
		t.Fatalf("expected base and continuation content, got: %s", got)
	}
}

type staticProvider struct {
	content      string
	raw          string
	finishReason string
}

func (p staticProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	return aiprovider.GenerateResult{
		Content:      p.content,
		Raw:          p.raw,
		FinishReason: p.finishReason,
	}, nil
}

type sequenceProvider struct {
	results []aiprovider.GenerateResult
	calls   int
}

func (p *sequenceProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	if p.calls >= len(p.results) {
		return aiprovider.GenerateResult{}, nil
	}
	result := p.results[p.calls]
	p.calls++
	return result, nil
}

type recordingProvider struct {
	models []string
}

func (p *recordingProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	p.models = append(p.models, recapModelFromContext(ctx, ""))
	return aiprovider.GenerateResult{
		Content:      "# Test\n\ncontent",
		Raw:          `{}`,
		FinishReason: "stop",
	}, nil
}

// --- HandleTask integration tests ---

func TestHandleTaskWithLocalProvider(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))

	sessionDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "live_20260101_120000")
	pkgDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello world transcript"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	task, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	reporter := &noopReporter{}
	err = h.HandleTask(context.Background(), task, reporter)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	// Verify recap files exist
	recapDir := filepath.Join(sessionDir, "recap")
	entries, err := os.ReadDir(recapDir)
	if err != nil {
		t.Fatalf("read recap dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("recap dir is empty, expected files")
	}

	// Check for the .md file
	foundMD := false
	foundPrompt := false
	foundRaw := false
	foundCorrected := false
	foundCorrectionReport := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			foundMD = true
		}
		if e.Name() == "live-recap.prompt.md" {
			foundPrompt = true
		}
		if e.Name() == "live-recap.raw.json" {
			foundRaw = true
		}
		if e.Name() == "transcript.corrected.txt" {
			foundCorrected = true
		}
		if e.Name() == "transcript.correction.json" {
			foundCorrectionReport = true
		}
	}
	if !foundMD {
		t.Fatalf("missing .md recap file")
	}
	if !foundPrompt {
		t.Fatalf("missing prompt file")
	}
	if !foundRaw {
		t.Fatalf("missing raw json file")
	}
	if !foundCorrected {
		t.Fatalf("missing corrected transcript file")
	}
	if !foundCorrectionReport {
		t.Fatalf("missing correction report file")
	}
}

func TestHandleTaskWithLocalProviderEmptyTranscript(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_live_20260101_120000", "live_20260101_120000", "ch1", string(state.StatusASRDone))

	sessionDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "live_20260101_120000")
	pkgDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Empty transcript
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte(""), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	task, err := h.CreateTask(context.Background(), fix.pool, "ch1_live_20260101_120000")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	reporter := &noopReporter{}
	err = h.HandleTask(context.Background(), task, reporter)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	recapDir := filepath.Join(sessionDir, "recap")
	entries, err := os.ReadDir(recapDir)
	if err != nil {
		t.Fatalf("read recap dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("recap dir should still have files for empty transcript")
	}
}

func TestHandleTaskExtractsSuggestedTermsBeforeCleaning(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "suggested_terms", "suggested_terms", "ch1", string(state.StatusASRDone))

	sessionDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "suggested_terms")
	pkgDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello world transcript"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	provider := staticProvider{
		content: "# Test\n\n正文里有律动 [应为：绿冻]。\n\n## 致大家\n谢谢。",
		raw:     `{"provider":"static"}`,
	}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, provider, fix.glossaryStore, nil, nil)
	task, err := h.CreateTask(context.Background(), fix.pool, "suggested_terms")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := h.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	recapDir := filepath.Join(sessionDir, "recap")
	data, err := os.ReadFile(filepath.Join(recapDir, "suggested_terms.json"))
	if err != nil {
		t.Fatalf("read suggested terms: %v", err)
	}
	var terms []string
	if err := json.Unmarshal(data, &terms); err != nil {
		t.Fatalf("unmarshal suggested terms: %v", err)
	}
	if len(terms) != 1 || terms[0] != "绿冻" {
		t.Fatalf("unexpected suggested terms: %#v", terms)
	}

	md, err := os.ReadFile(filepath.Join(recapDir, "直播回顾_suggested_terms.md"))
	if err != nil {
		t.Fatalf("read recap markdown: %v", err)
	}
	if strings.Contains(string(md), "[应为：绿冻]") {
		t.Fatalf("recap markdown should not contain suggested term marker: %s", string(md))
	}
}

func TestHandleTaskContinuesOnLengthFinishReason(t *testing.T) {
	fix := setupRecapTest(t)
	fix.cfg.RecapAI.MaxContinuations = 2
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "continuation_length", "continuation_length", "ch1", string(state.StatusASRDone))

	sessionDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "continuation_length")
	pkgDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello world transcript"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	provider := &sequenceProvider{results: []aiprovider.GenerateResult{
		{Content: "# Test\n\n## A\npart1", Raw: `{"index":0}`, FinishReason: "length"},
		{Content: "part2\n\n## 致大家\n谢谢", Raw: `{"index":1}`, FinishReason: "stop"},
	}}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, provider, fix.glossaryStore, nil, nil)
	task, err := h.CreateTask(context.Background(), fix.pool, "continuation_length")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := h.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}

	md, err := os.ReadFile(filepath.Join(sessionDir, "recap", "直播回顾_continuation_length.md"))
	if err != nil {
		t.Fatalf("read recap markdown: %v", err)
	}
	if !strings.Contains(string(md), "part1") || !strings.Contains(string(md), "part2") {
		t.Fatalf("recap should contain initial and continuation content: %s", string(md))
	}
	raw, err := os.ReadFile(filepath.Join(sessionDir, "recap", "live-recap.raw.json"))
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	if !strings.Contains(string(raw), `"responses"`) {
		t.Fatalf("raw response should combine multiple responses: %s", string(raw))
	}
}

func TestHandleTaskStopsAtMaxContinuations(t *testing.T) {
	fix := setupRecapTest(t)
	fix.cfg.RecapAI.MaxContinuations = 1
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "continuation_limit", "continuation_limit", "ch1", string(state.StatusASRDone))

	sessionDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "continuation_limit")
	pkgDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello world transcript"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	provider := &sequenceProvider{results: []aiprovider.GenerateResult{
		{Content: "# Test\n\npart1", Raw: `{"index":0}`, FinishReason: "length"},
		{Content: "part2", Raw: `{"index":1}`, FinishReason: "length"},
		{Content: "part3", Raw: `{"index":2}`, FinishReason: "stop"},
	}}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, provider, fix.glossaryStore, nil, nil)
	task, err := h.CreateTask(context.Background(), fix.pool, "continuation_limit")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := h.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}

	md, err := os.ReadFile(filepath.Join(sessionDir, "recap", "直播回顾_continuation_limit.md"))
	if err != nil {
		t.Fatalf("read recap markdown: %v", err)
	}
	if strings.Contains(string(md), "part3") {
		t.Fatalf("recap should stop at max continuations: %s", string(md))
	}
}

func TestHandleTaskUsesChannelRecapOptions(t *testing.T) {
	fix := setupRecapTest(t)
	fix.cfg.RecapAI.Model = "v4-flash"
	fix.cfg.RecapAI.MaxContinuations = 2
	fix.insertChannel(t, "ch1")
	if _, err := fix.database.Exec(`UPDATE channels SET recap_model = ?, max_continuations = ? WHERE id = ?`, "v4-pro", 0, "ch1"); err != nil {
		t.Fatalf("update channel recap options: %v", err)
	}
	fix.insertSession(t, "channel_recap_options", "channel_recap_options", "ch1", string(state.StatusASRDone))

	sessionDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "channel_recap_options")
	pkgDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "transcript.txt"), []byte("hello world transcript"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	provider := &recordingProvider{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, provider, fix.glossaryStore, nil, fix.channelStore)
	task, err := h.CreateTask(context.Background(), fix.pool, "channel_recap_options")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := h.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if len(provider.models) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(provider.models))
	}
	if provider.models[0] != "v4-pro" {
		t.Fatalf("model = %q, want v4-pro", provider.models[0])
	}
}

func TestHandleTaskSessionNotFound(t *testing.T) {
	fix := setupRecapTest(t)
	fix.insertChannel(t, "ch1")

	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	task := worker.Task{ID: "task_1", ChannelID: "ch1", SessionID: "nonexistent", Type: TaskType}
	reporter := &noopReporter{}
	err := h.HandleTask(context.Background(), task, reporter)
	if err == nil {
		t.Fatalf("expected error for nonexistent session")
	}
}

// --- noop reporter ---

type noopReporter struct{}

func (noopReporter) Progress(ctx context.Context, progress int, message string) error {
	return nil
}

// --- Danmaku analysis tests ---

func TestAnalyzeDanmaku(t *testing.T) {
	data := []byte(`[
		{"time_ms": 1000, "text": "你好", "user_id": "u1"},
		{"time_ms": 5000, "text": "哈哈哈", "user_id": "u2"},
		{"time_ms": 6000, "text": "加油", "user_id": "u1"},
		{"time_ms": 35000, "text": "好感动", "user_id": "u3"},
		{"time_ms": 36000, "text": "草", "user_id": "u2"},
		{"time_ms": 37000, "text": "太棒了", "user_id": "u4"}
	]`)
	stats, err := analyzeDanmaku(data, 120000) // 2 minutes
	if err != nil {
		t.Fatal(err)
	}
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if stats.TotalCount != 6 {
		t.Fatalf("expected 6 total, got %d", stats.TotalCount)
	}
	if stats.UniqueUsers != 4 {
		t.Fatalf("expected 4 unique users, got %d", stats.UniqueUsers)
	}
	if stats.DurationMin != 2.0 {
		t.Fatalf("expected 2.0 minutes, got %f", stats.DurationMin)
	}
	if len(stats.PeakMoments) == 0 {
		t.Fatal("expected peak moments")
	}
	if len(stats.TopDanmaku) == 0 {
		t.Fatal("expected top danmaku")
	}
	if len(stats.Keywords) == 0 {
		t.Fatal("expected keywords")
	}
}

func TestAnalyzeDanmakuEmpty(t *testing.T) {
	stats, err := analyzeDanmaku([]byte("[]"), 60000)
	if err != nil {
		t.Fatal(err)
	}
	if stats != nil {
		t.Fatalf("expected nil for empty data, got %+v", stats)
	}
}

func TestBuildPromptWithDanmaku(t *testing.T) {
	fix := setupRecapTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)

	sess := session.Session{
		ID:        "test-1",
		ChannelID: "ch1",
		Title:     "测试直播",
		StartedAt: "2026-05-06T20:00:00+08:00",
	}

	stats := &danmakuStats{
		TotalCount:  100,
		UniqueUsers: 50,
		DurationMin: 60.0,
		AvgPerMin:   1.67,
		PeakMoments: []peakMoment{{TimeMinSec: "05:00", Count: 20}},
		TopDanmaku:  []danmakuEntry{{TimeMinSec: "05:30", Text: "太棒了"}},
		Keywords:    []keywordCount{{Word: "哈哈", Count: 10}},
	}
	meta := &sessionMetadata{DurationMs: 3600000}

	resolved := &ResolvedTemplate{
		SystemPrompt: defaultSystemPrompt,
		UserFormat:   defaultUserFormat,
	}
	vars := &TemplateVars{}

	prompt := h.buildPrompt(context.Background(), sess, []byte("转写内容测试"), stats, meta, resolved, vars, nil)

	if !strings.Contains(prompt, "弹幕数：100") {
		t.Fatal("prompt should contain danmaku count")
	}
	if !strings.Contains(prompt, "代表性弹幕") {
		t.Fatal("prompt should contain top danmaku section")
	}
	if strings.Contains(prompt, "弹幕密度峰值") {
		t.Fatal("prompt should not contain removed density peak section")
	}
	if !strings.Contains(prompt, "1小时0分钟") {
		t.Fatal("prompt should contain duration")
	}
}

func TestBuildPromptWithoutDanmaku(t *testing.T) {
	fix := setupRecapTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)

	sess := session.Session{
		ID:        "test-2",
		ChannelID: "ch1",
		Title:     "无弹幕直播",
	}

	resolved := &ResolvedTemplate{
		SystemPrompt: defaultSystemPrompt,
		UserFormat:   defaultUserFormat,
	}
	vars := &TemplateVars{}

	prompt := h.buildPrompt(context.Background(), sess, []byte("转写内容"), nil, nil, resolved, vars, nil)

	if strings.Contains(prompt, "弹幕分析数据") {
		t.Fatal("prompt should NOT contain danmaku section when stats is nil")
	}
	if !strings.Contains(prompt, "转写原文") {
		t.Fatal("prompt should contain transcript section")
	}
}

func TestReadSessionMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	meta := readSessionMetadata(tmpDir)
	if meta != nil {
		t.Fatal("expected nil when metadata.json doesn't exist")
	}

	pkgDir := filepath.Join(tmpDir, "package")
	os.MkdirAll(pkgDir, 0o755)

	metaJSON := `{"duration_ms": 3600000, "source_audio_name": "test.mp3", "danmaku_count": 100}`
	os.WriteFile(filepath.Join(pkgDir, "metadata.json"), []byte(metaJSON), 0o644)

	meta = readSessionMetadata(tmpDir)
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
	if meta.DurationMs != 3600000 {
		t.Fatalf("expected 3600000, got %d", meta.DurationMs)
	}
	if meta.DanmakuCount != 100 {
		t.Fatalf("expected 100, got %d", meta.DanmakuCount)
	}
}

// --- FormatDanmakuStats tests ---

func TestFormatDanmakuStats(t *testing.T) {
	stats := &danmakuStats{
		TotalCount:  500,
		UniqueUsers: 200,
		DurationMin: 120.0,
		AvgPerMin:   4.2,
		PeakMoments: []peakMoment{{TimeMinSec: "15:30", Count: 50}},
	}
	result := FormatDanmakuStats(stats, &TemplateVars{})
	if !strings.Contains(result, "总弹幕数：500 条") {
		t.Fatalf("expected total count, got: %s", result)
	}
	if !strings.Contains(result, "独立用户：200 人") {
		t.Fatalf("expected unique users, got: %s", result)
	}
	if !strings.Contains(result, "人均弹幕：2.5 条") {
		t.Fatalf("expected per-user average, got: %s", result)
	}
	if strings.Contains(result, "弹幕密度峰值") {
		t.Fatalf("stats should not contain removed density peak section, got: %s", result)
	}
}

func TestFormatDanmakuStatsNil(t *testing.T) {
	result := FormatDanmakuStats(nil, &TemplateVars{})
	if result != "" {
		t.Fatalf("expected empty string for nil stats, got: %s", result)
	}
}

func TestFormatDanmakuStatsZeroCount(t *testing.T) {
	stats := &danmakuStats{TotalCount: 0}
	result := FormatDanmakuStats(stats, &TemplateVars{})
	if result != "" {
		t.Fatalf("expected empty string for zero count, got: %s", result)
	}
}

// --- appendDanmakuStats tests ---

func TestAppendDanmakuStatsExistingSection(t *testing.T) {
	recap := "## 弹幕互动精选\n\nsome content\n\n## 观看建议\n\n建议内容"
	statsSection := "### 弹幕统计\n\n| 指标 | 数值 |\n|------|------|\n| 弹幕总数 | 500 条 |"
	result := appendDanmakuStats(recap, statsSection)
	if !strings.Contains(result, "### 弹幕统计") {
		t.Fatalf("stats section not inserted, got: %s", result)
	}
	// Should be inserted between 弹幕互动精选 and 观看建议
	danmakuIdx := strings.Index(result, "## 弹幕互动精选")
	statsIdx := strings.Index(result, "### 弹幕统计")
	watchIdx := strings.Index(result, "## 观看建议")
	if statsIdx < danmakuIdx {
		t.Fatalf("stats should be after 弹幕互动精选 header")
	}
	if statsIdx > watchIdx {
		t.Fatalf("stats should be before 观看建议")
	}
}

func TestAppendDanmakuStatsNoSection(t *testing.T) {
	recap := "# Title\n\n## 直播概要\n\n概要内容\n\n## 观看建议\n\n建议内容"
	statsSection := "### 弹幕统计\n\n| 指标 | 数值 |\n|------|------|\n| 弹幕总数 | 500 条 |"
	result := appendDanmakuStats(recap, statsSection)
	if !strings.Contains(result, "## 弹幕互动精选") {
		t.Fatalf("should create 弹幕互动精选 section, got: %s", result)
	}
	if !strings.Contains(result, "### 弹幕统计") {
		t.Fatalf("should contain stats, got: %s", result)
	}
}

func TestAppendDanmakuStatsEmojiSection(t *testing.T) {
	recap := "# Title\n\n## 💬 弹幕互动精选\n\n| 弹幕内容 | 上下文说明 |\n|---|---|\n| test | context |\n\n## 🎯 观看建议\n\n建议内容"
	statsSection := "### 弹幕统计\n\n| 指标 | 数值 |\n|------|------|\n| 弹幕总数 | 500 条 |"
	result := appendDanmakuStats(recap, statsSection)
	if strings.Count(result, "弹幕互动精选") != 1 {
		t.Fatalf("should not duplicate danmaku section, got: %s", result)
	}
	statsIdx := strings.Index(result, "### 弹幕统计")
	tableIdx := strings.Index(result, "| 弹幕内容 | 上下文说明 |")
	watchIdx := strings.Index(result, "## 🎯 观看建议")
	if statsIdx < 0 || tableIdx < 0 || watchIdx < 0 {
		t.Fatalf("missing expected sections, got: %s", result)
	}
	if !(statsIdx < tableIdx && tableIdx < watchIdx) {
		t.Fatalf("stats should be inserted inside danmaku section before existing content, got: %s", result)
	}
}

func TestAppendDanmakuStatsEmptySection(t *testing.T) {
	recap := "some content"
	result := appendDanmakuStats(recap, "")
	if result != recap {
		t.Fatalf("should return unchanged when statsSection is empty")
	}
}

func TestFormatDanmakuStatsOmitsTopicClusters(t *testing.T) {
	stats := &danmakuStats{
		TotalCount:  500,
		UniqueUsers: 200,
		Topics: []danmakuTopic{
			{TimeRange: "120:00-28:00", Keyword: "爆了", Count: 99},
			{TimeRange: "00:00-04:00", Keyword: "|柜子歌呢？不放柜子歌就", Count: 92},
			{TimeRange: "45:00-47:00", Keyword: "好姐妹", Count: 42},
		},
	}
	result := FormatDanmakuStats(stats, &TemplateVars{})
	if strings.Contains(result, "话题聚类") || strings.Contains(result, "好姐妹") || strings.Contains(result, "爆了") {
		t.Fatalf("topic clusters should be omitted from publishable stats, got: %s", result)
	}
}

// --- NewHandler with templateStore ---

func TestNewHandlerNilTemplateStore(t *testing.T) {
	fix := setupRecapTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, nil, fix.glossaryStore, nil, nil)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.templateStore == nil {
		t.Fatal("templateStore should be initialized to non-nil even when nil is passed")
	}
}

// --- Template rendering in buildPrompt ---

func TestBuildPromptTemplateRendering(t *testing.T) {
	fix := setupRecapTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)

	sess := session.Session{
		ID:        "test-3",
		ChannelID: "ch1",
		Title:     "测试直播",
		Slug:      "test_slug",
		StartedAt: "2026-01-15T20:00:00+08:00",
	}

	resolved := &ResolvedTemplate{
		SystemPrompt: "Custom system prompt",
		UserFormat:   "章节：{{channel_name}} 日期：{{date}} 弹幕：{{danmaku_count}}",
		ExtraVars:    map[string]string{},
	}
	vars := &TemplateVars{
		ChannelName:  sess.Title,
		ChannelID:    sess.ChannelID,
		Slug:         sess.Slug,
		Date:         "2026.01.15",
		DateTime:     sess.StartedAt,
		DanmakuCount: 42,
	}

	prompt := h.buildPrompt(context.Background(), sess, []byte("test transcript"), nil, nil, resolved, vars, nil)

	// Output format section should have rendered template variables
	if !strings.Contains(prompt, "章节：测试直播") {
		t.Fatalf("prompt should contain rendered channel_name, got: %s", prompt)
	}
	if !strings.Contains(prompt, "日期：2026.01.15") {
		t.Fatalf("prompt should contain rendered date, got: %s", prompt)
	}
	if !strings.Contains(prompt, "弹幕：42") {
		t.Fatalf("prompt should contain rendered danmaku_count, got: %s", prompt)
	}
}

func TestCalculateSpeakerStats_MultipleSpeakers(t *testing.T) {
	segments := []transcriptSegment{
		{StartMS: 0, EndMS: 10000, SpeakerID: speakerID(0), Text: "a"},
		{StartMS: 10000, EndMS: 20000, SpeakerID: speakerID(1), Text: "b"},
		{StartMS: 20000, EndMS: 30000, SpeakerID: speakerID(0), Text: "c"},
		{StartMS: 30000, EndMS: 40000, SpeakerID: speakerID(1), Text: "d"},
	}

	stats := calculateSpeakerStats(segments, nil)
	if stats == nil {
		t.Fatal("expected speaker stats")
	}
	if stats.EffectiveCount != 2 {
		t.Fatalf("unexpected effective speaker count: %d", stats.EffectiveCount)
	}
	if len(stats.Speakers) != 2 {
		t.Fatalf("unexpected speakers: %#v", stats.Speakers)
	}
	if stats.Coverage != 1 {
		t.Fatalf("unexpected coverage: %f", stats.Coverage)
	}
	if math.Abs(stats.SwitchesPerMinute-4.5) > 0.001 {
		t.Fatalf("unexpected switch frequency: %f", stats.SwitchesPerMinute)
	}
}

func TestCalculateSpeakerStats_SingleSpeakerReturnsNil(t *testing.T) {
	segments := []transcriptSegment{
		{StartMS: 0, EndMS: 10000, SpeakerID: speakerID(0), Text: "a"},
		{StartMS: 10000, EndMS: 20000, SpeakerID: speakerID(0), Text: "b"},
	}

	if stats := calculateSpeakerStats(segments, nil); stats != nil {
		t.Fatalf("expected nil stats, got: %#v", stats)
	}
}

func TestCalculateSpeakerStats_NoiseFiltered(t *testing.T) {
	segments := []transcriptSegment{
		{StartMS: 0, EndMS: 10000, SpeakerID: speakerID(0), Text: "a"},
		{StartMS: 10000, EndMS: 20000, SpeakerID: speakerID(1), Text: "b"},
		{StartMS: 20000, EndMS: 30000, SpeakerID: speakerID(0), Text: "c"},
		{StartMS: 30000, EndMS: 40000, SpeakerID: speakerID(1), Text: "d"},
		{StartMS: 40000, EndMS: 50000, SpeakerID: speakerID(2), Text: "noise"},
	}

	stats := calculateSpeakerStats(segments, nil)
	if stats == nil {
		t.Fatal("expected speaker stats")
	}
	if stats.EffectiveCount != 2 {
		t.Fatalf("unexpected effective speaker count: %d", stats.EffectiveCount)
	}
	for _, speaker := range stats.Speakers {
		if speaker.ID == 2 {
			t.Fatalf("noise speaker should be filtered: %#v", stats.Speakers)
		}
	}
}

func TestCalculateSpeakerStats_LowCoverageSkips(t *testing.T) {
	segments := []transcriptSegment{
		{StartMS: 0, EndMS: 100000, Text: "missing speaker"},
		{StartMS: 100000, EndMS: 110000, SpeakerID: speakerID(0), Text: "a"},
		{StartMS: 110000, EndMS: 120000, SpeakerID: speakerID(1), Text: "b"},
	}

	if stats := calculateSpeakerStats(segments, nil); stats != nil {
		t.Fatalf("expected nil stats for low coverage, got: %#v", stats)
	}
}

func TestCalculateSpeakerStats_RangeCrop(t *testing.T) {
	segments := []transcriptSegment{
		{StartMS: 0, EndMS: 15000, SpeakerID: speakerID(0), Text: "a"},
		{StartMS: 15000, EndMS: 20000, SpeakerID: speakerID(1), Text: "b"},
		{StartMS: 20000, EndMS: 25000, SpeakerID: speakerID(0), Text: "c"},
		{StartMS: 25000, EndMS: 35000, SpeakerID: speakerID(1), Text: "d"},
	}
	r := &timeRange{StartSec: 10, EndSec: 30}

	stats := calculateSpeakerStats(segments, r)
	if stats == nil {
		t.Fatal("expected speaker stats")
	}
	if stats.EffectiveCount != 2 {
		t.Fatalf("unexpected effective speaker count: %d", stats.EffectiveCount)
	}
	var speaker0, speaker1 speakerStat
	for _, speaker := range stats.Speakers {
		if speaker.ID == 0 {
			speaker0 = speaker
		}
		if speaker.ID == 1 {
			speaker1 = speaker
		}
	}
	if speaker0.DurationMS != 10000 || speaker1.DurationMS != 10000 {
		t.Fatalf("unexpected cropped durations: speaker0=%d speaker1=%d", speaker0.DurationMS, speaker1.DurationMS)
	}
}

func TestPromptIncludesSpeakerInfo(t *testing.T) {
	fix := setupRecapTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	sess := session.Session{ID: "speaker-prompt", ChannelID: "ch1", Title: "测试直播", Slug: "speaker_prompt"}
	resolved := &ResolvedTemplate{SystemPrompt: "system", UserFormat: "format", ExtraVars: map[string]string{}}
	stats := &speakerStats{
		EffectiveCount:    2,
		Coverage:          1,
		SwitchesPerMinute: 1.5,
		Speakers: []speakerStat{
			{ID: 0, DurationMS: 120000, SegmentCount: 3, DurationRatio: 0.6},
			{ID: 1, DurationMS: 80000, SegmentCount: 2, DurationRatio: 0.4},
		},
	}

	prompt := h.buildPromptWithKnowledgeAndSpeakers(context.Background(), sess, []byte("转写"), nil, nil, resolved, &TemplateVars{}, nil, nil, stats)

	speakerIndex := strings.Index(prompt, "## 说话人统计")
	if speakerIndex < 0 {
		t.Fatalf("prompt should include speaker info, got: %s", prompt)
	}
	basicIndex := strings.Index(prompt, "## 基本信息")
	formatIndex := strings.Index(prompt, "## 输出格式要求")
	if !(basicIndex >= 0 && basicIndex < speakerIndex && speakerIndex < formatIndex) {
		t.Fatalf("speaker info should be after basic info and before format, got: %s", prompt)
	}
	for _, want := range []string{"ASR 自动检测", "不能确认真实身份", "不要自行命名说话人"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q, got: %s", want, prompt)
		}
	}
}

func TestPromptSkipsSpeakerInfoWhenNil(t *testing.T) {
	fix := setupRecapTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	sess := session.Session{ID: "speaker-prompt-nil", ChannelID: "ch1", Title: "测试直播", Slug: "speaker_prompt_nil"}
	resolved := &ResolvedTemplate{SystemPrompt: "system", UserFormat: "format", ExtraVars: map[string]string{}}

	prompt := h.buildPromptWithKnowledgeAndSpeakers(context.Background(), sess, []byte("转写"), nil, nil, resolved, &TemplateVars{}, nil, nil, nil)

	if strings.Contains(prompt, "## 说话人统计") {
		t.Fatalf("prompt should skip speaker info, got: %s", prompt)
	}
}

func TestTimedTranscriptFromSegments(t *testing.T) {
	fix := setupRecapTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "timed", "timed_slug", "ch1", string(state.StatusASRDone))
	packageDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "timed_slug", "package")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	segments := `[{"start_ms":29084,"end_ms":31164,"text":"好，我们今天怎么没有柜子歌是吧？"},{"start_ms":3661000,"end_ms":3663000,"text":"一小时后的内容"}]`
	if err := os.WriteFile(filepath.Join(packageDir, "segments.json"), []byte(segments), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := fix.sessions.Get(context.Background(), "timed")
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.timedTranscript(sess)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "[00:29] 好，我们今天怎么没有柜子歌是吧？") {
		t.Fatalf("missing minute timestamp, got: %s", got)
	}
	if !strings.Contains(got, "[01:01:01] 一小时后的内容") {
		t.Fatalf("missing hour timestamp, got: %s", got)
	}
}

func TestApplyGlossaryCorrections(t *testing.T) {
	fix := setupRecapTest(t)
	ctx := context.Background()
	if err := fix.glossaryStore.Upsert(ctx, "ch1", "律动", "绿冻", "粉丝称呼"); err != nil {
		t.Fatal(err)
	}
	if err := fix.glossaryStore.Upsert(ctx, "ch1", "柜子哥", "柜子歌", "梗"); err != nil {
		t.Fatal(err)
	}
	got := applyGlossaryCorrections(ctx, fix.glossaryStore, "ch1", "律动文学和柜子哥都出现了")
	if got != "绿冻文学和柜子歌都出现了" {
		t.Fatalf("unexpected corrected content: %s", got)
	}
}

func TestApplyGlossaryCorrectionsSkipsMarkdownQuotes(t *testing.T) {
	fix := setupRecapTest(t)
	ctx := context.Background()
	if err := fix.glossaryStore.Upsert(ctx, "ch1", "柜子哥", "柜子歌", "梗"); err != nil {
		t.Fatal(err)
	}

	input := "正文柜子哥\n> 主播说柜子哥\n  > 缩进引用柜子哥\n- 列表柜子哥"
	got := applyGlossaryCorrections(ctx, fix.glossaryStore, "ch1", input)
	want := "正文柜子歌\n> 主播说柜子哥\n  > 缩进引用柜子哥\n- 列表柜子歌"
	if got != want {
		t.Fatalf("unexpected corrected content:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestCleanSuggestedTermMarkers(t *testing.T) {
	input := "律动 [应为：绿冻] 和做题龙 [应为:做题龙] 都在正文里"
	got := cleanSuggestedTermMarkers(input)
	if strings.Contains(got, "[应为：绿冻]") || strings.Contains(got, "[应为:做题龙]") {
		t.Fatalf("markers should be removed, got: %s", got)
	}
	if !strings.Contains(got, "律动 ") || !strings.Contains(got, " 和做题龙 ") || !strings.Contains(got, " 都在正文里") {
		t.Fatalf("non-marker content should remain, got: %s", got)
	}
}

func TestBuildPromptGlossaryQuoteRule(t *testing.T) {
	fix := setupRecapTest(t)
	ctx := context.Background()
	if err := fix.glossaryStore.Upsert(ctx, "ch1", "柜子哥", "柜子歌", "梗"); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	resolved := &ResolvedTemplate{
		SystemPrompt: defaultSystemPrompt,
		UserFormat:   defaultUserFormat,
	}
	prompt := h.buildPrompt(ctx, session.Session{ChannelID: "ch1", Title: "Test"}, []byte("柜子哥"), nil, nil, resolved, &TemplateVars{}, nil)
	if !strings.Contains(prompt, "Markdown 引用块（>）保留主播原始说法") {
		t.Fatalf("prompt should contain quote preservation rule, got: %s", prompt)
	}
}

func TestBuiltinPresetsPromptLayering(t *testing.T) {
	var concise *TemplatePreset
	longPresets := make(map[string]string)
	for i := range BuiltinPresets {
		preset := &BuiltinPresets[i]
		if preset.Name == "简洁摘要" {
			concise = preset
			continue
		}
		longPresets[preset.Name] = preset.SystemPrompt
	}
	if concise == nil {
		t.Fatal("missing concise preset")
	}
	if !strings.Contains(concise.SystemPrompt, "不超过 800 字") && !strings.Contains(concise.UserFormat, "不超过 800 字") {
		t.Fatal("concise preset should keep 800 character limit")
	}
	for _, forbidden := range []string{"最低长度", "500-800 字", "8-10 段"} {
		if strings.Contains(concise.SystemPrompt, forbidden) {
			t.Fatalf("concise preset should not contain long-form constraint %q", forbidden)
		}
	}
	if !strings.Contains(concise.SystemPrompt, "避免空泛表述") {
		t.Fatal("concise preset should contain anti-vague requirement")
	}

	for _, name := range []string{"粉丝向精修", "正式详实", "粉丝向", "弹幕聚焦"} {
		prompt, ok := longPresets[name]
		if !ok {
			t.Fatalf("missing long preset %q", name)
		}
		if !strings.Contains(prompt, "空泛") && !strings.Contains(prompt, "具体") {
			t.Fatalf("long preset %q should contain anti-vague or concrete-detail requirement", name)
		}
	}
}

func TestDefaultSystemPromptHasDetailConstraints(t *testing.T) {
	for _, want := range []string{"15 分钟", "禁止只做高度概括", "生成后自检清单"} {
		if !strings.Contains(defaultSystemPrompt, want) {
			t.Fatalf("defaultSystemPrompt should contain %q", want)
		}
	}
}

func TestBuildCorrectionRulesSortsByLength(t *testing.T) {
	fix := setupRecapTest(t)
	ctx := context.Background()
	if err := fix.glossaryStore.Upsert(ctx, "ch1", "律动", "绿冻", "粉丝称呼"); err != nil {
		t.Fatal(err)
	}
	if err := fix.glossaryStore.Upsert(ctx, "ch1", "律动文学", "绿冻文学", "粉丝称呼"); err != nil {
		t.Fatal(err)
	}
	rules, err := buildCorrectionRules(ctx, fix.glossaryStore, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if rules[0].Term != "律动文学" {
		t.Fatalf("longer term should come first, got %#v", rules)
	}
}

func TestCorrectTextWithRules(t *testing.T) {
	rules := []correctionRule{
		{Term: "律动文学", Canonical: "绿冻文学"},
		{Term: "律动", Canonical: "绿冻"},
	}
	got, applied := correctTextWithRules("律动文学鉴赏会和律动", rules)
	if got != "绿冻文学鉴赏会和绿冻" {
		t.Fatalf("unexpected corrected text: %s", got)
	}
	if strings.Join(applied, ",") != "律动,律动文学" {
		t.Fatalf("unexpected applied terms: %#v", applied)
	}
}

func TestCorrectedTranscriptForPromptSegments(t *testing.T) {
	fix := setupRecapTest(t)
	ctx := context.Background()
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "timed-corrected", "timed_corrected", "ch1", string(state.StatusASRDone))
	if err := fix.glossaryStore.Upsert(ctx, "ch1", "律动", "绿冻", "粉丝称呼"); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "timed_corrected", "package")
	recapDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "timed_corrected", "recap")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	segments := `[{"start_ms":29084,"end_ms":31164,"text":"律动文学来了"}]`
	if err := os.WriteFile(filepath.Join(packageDir, "segments.json"), []byte(segments), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := fix.sessions.Get(ctx, "timed-corrected")
	if err != nil {
		t.Fatal(err)
	}
	out, report, err := h.correctedTranscriptForPrompt(ctx, sess, nil, []byte("fallback 律动"), recapDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "[00:29] 绿冻文学来了") {
		t.Fatalf("expected corrected timed transcript, got: %s", out)
	}
	if report.Source != "segments.json" || report.AppliedCount != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if _, err := os.Stat(filepath.Join(recapDir, "transcript.corrected.txt")); err != nil {
		t.Fatalf("missing corrected transcript artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recapDir, "transcript.correction.json")); err != nil {
		t.Fatalf("missing correction report artifact: %v", err)
	}
}

func TestCorrectedTranscriptForPromptFallbackAndRange(t *testing.T) {
	fix := setupRecapTest(t)
	ctx := context.Background()
	h := NewHandler(fix.cfg, fix.sessions, fix.states, LocalProvider{}, fix.glossaryStore, nil, nil)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "fallback-corrected", "fallback_corrected", "ch1", string(state.StatusASRDone))
	if err := fix.glossaryStore.Upsert(ctx, "ch1", "柜子哥", "柜子歌", "梗"); err != nil {
		t.Fatal(err)
	}
	sess, err := fix.sessions.Get(ctx, "fallback-corrected")
	if err != nil {
		t.Fatal(err)
	}
	recapDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "fallback_corrected", "recap")
	r := &timeRange{StartSec: 10, EndSec: 20}
	out, report, err := h.correctedTranscriptForPrompt(ctx, sess, r, []byte("这里有柜子哥"), recapDir)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "这里有柜子歌" {
		t.Fatalf("unexpected corrected transcript: %s", out)
	}
	if report.Source != "filtered_transcript" || report.AppliedCount != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if _, err := os.Stat(filepath.Join(recapDir, "transcript_0010-0020.corrected.txt")); err != nil {
		t.Fatalf("missing ranged corrected transcript artifact: %v", err)
	}
}

func TestEnsureFinalAddressSectionTrimsNotice(t *testing.T) {
	input := "# Title\n\n## 正文\n\n内容\n\n## 💌 致绿冻们\n\n结尾内容\n\n---\n\n> 本文由 Hikami-Go 自动生成，基于直播转写和弹幕数据分析。"
	got := ensureFinalAddressSection(input)
	if !strings.HasSuffix(strings.TrimSpace(got), "结尾内容") {
		t.Fatalf("final address section should be last, got: %s", got)
	}
	if strings.Contains(got, "本文由 Hikami-Go 自动生成") {
		t.Fatalf("generated notice after final section should be removed, got: %s", got)
	}
}

// TestHasGeneratedNotice 兼容历史与变体署名：改名过渡期 AI 可能吐回旧 Hazel 签名或泛化
// 的「AI 自动生成」文案。去重逻辑用结构匹配，不绑定品牌名，确保这些变体都能被识别，
// 避免在 handler.go 追加 generatedNotice 时产生重复署名。
func TestHasGeneratedNotice(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"新名完整", "> 本文由 Hikami-Go 自动生成，基于直播转写和弹幕数据分析。", true},
		{"历史 Hazel 签名", "> 本文由 Hazel 自动生成，基于直播转写和弹幕数据分析。", true},
		{"本回顾由 AI", "> 本回顾由 AI 自动生成，如有错误请以原直播为准。", true},
		{"无前缀引用", "本文由 Hikami-Go 自动生成。", true},
		{"带强调修饰", "> **本文由 Hikami-Go 自动生成**", true},
		{"普通正文", "## 致绿冻们\n\n感谢大家的陪伴～", false},
		{"无关自动生成", "这个片段是自动生成的字幕", false},
		{"空行", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasGeneratedNotice(tt.line); got != tt.want {
				t.Fatalf("hasGeneratedNotice(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}
