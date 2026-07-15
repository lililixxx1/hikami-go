package download

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

// createTempDir creates a temp directory for test isolation.
func createTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// writeFile writes content to path, failing the test on error.
func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

// ---------------------------------------------------------------------------
// findAudioInDir
// ---------------------------------------------------------------------------

func TestFindAudioInDir(t *testing.T) {
	dir := createTempDir(t)
	audioPath := filepath.Join(dir, "audio.m4a")
	writeFile(t, audioPath, "fake audio data")

	got, err := findAudioInDir(dir)
	if err != nil {
		t.Fatalf("findAudioInDir returned error: %v", err)
	}
	if got != audioPath {
		t.Fatalf("findAudioInDir = %q, want %q", got, audioPath)
	}
}

func TestFindAudioInDirSkipsMetadata(t *testing.T) {
	dir := createTempDir(t)
	writeFile(t, filepath.Join(dir, "audio.info.json"), "{}")
	writeFile(t, filepath.Join(dir, "audio.xml"), "<xml/>")

	_, err := findAudioInDir(dir)
	if err == nil {
		t.Fatal("findAudioInDir expected error when only metadata files present, got nil")
	}
}

func TestFindAudioInDirSkipsNonAudioPrefix(t *testing.T) {
	dir := createTempDir(t)
	writeFile(t, filepath.Join(dir, "video.info.json"), "{}")
	writeFile(t, filepath.Join(dir, "video.xml"), "<xml/>")

	_, err := findAudioInDir(dir)
	if err == nil {
		t.Fatal("findAudioInDir expected error for non-audio prefix files, got nil")
	}
}

func TestFindAudioInDirEmpty(t *testing.T) {
	dir := createTempDir(t)

	_, err := findAudioInDir(dir)
	if err == nil {
		t.Fatal("findAudioInDir expected error for empty directory, got nil")
	}
}

// TestFindAudioInDirIgnoresThumbnail r8 P1 回归：yt-dlp --write-thumbnail 会把封面写成
// audio.jpg/audio.webp，与 audio.m4a 共存时，必须返回音频而非缩略图（旧黑名单实现会先返回 audio.jpg）。
func TestFindAudioInDirIgnoresThumbnail(t *testing.T) {
	dir := createTempDir(t)
	// 故意让缩略图字典序靠前，验证白名单确实跳过它们。
	writeFile := func(name string, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("audio.jpg", "fake-jpg")
	writeFile("audio.webp", "fake-webp")
	audioPath := filepath.Join(dir, "audio.m4a")
	writeFile("audio.m4a", "audio")

	got, err := findAudioInDir(dir)
	if err != nil {
		t.Fatalf("findAudioInDir returned error: %v", err)
	}
	if got != audioPath {
		t.Fatalf("findAudioInDir = %q, want audio.m4a (thumbnails must be skipped), got %q", audioPath, got)
	}
}

// TestEscapeConcatListPathAbsolutizesRelativePaths 是针对相对 OutputRoot 导致
// 路径叠加 bug 的回归测试。
//
// ffmpeg 的 concat demuxer 以 listfile 所在目录为基准解析相对条目。当 OutputRoot
// 为相对路径时，分片路径也是相对的，会被 ffmpeg 叠加成 raw/raw/audio.m4a 导致打开失败。
// 修复要求写入 listfile 的条目必须是绝对路径。
func TestEscapeConcatListPathAbsolutizesRelativePaths(t *testing.T) {
	// 相对路径分片（模拟 OutputRoot="./output" 的产物）。
	rel := filepath.Join("output", "bili_1", "live_1", "raw", "audio.m4a")

	got := escapeConcatListPath(rel)
	if strings.Contains(got, "\\") {
		t.Fatalf("concat path should be slash-normalized, got %q", got)
	}
	// 绝对化：应当以当前工作目录为前缀。
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	wantPrefix := filepath.ToSlash(cwd)
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("concat path should be absolute (prefix %q), got %q", wantPrefix, got)
	}
	// 原始相对路径绝不能原样出现。
	if got == rel {
		t.Fatalf("concat path wrote relative path verbatim (%q), which makes ffmpeg double up the path", rel)
	}
}

// TestEscapeConcatListPathPreservesAbsolutePath 验证已经是绝对路径的输入仍被正确转义。
func TestEscapeConcatListPathPreservesAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "it's audio.m4a") // 含单引号，验证转义
	got := escapeConcatListPath(abs)
	// 单引号需被转义为 '\'' 。
	if !strings.Contains(got, `'\''`) {
		t.Fatalf("single quote should be escaped to '\\'', got %q", got)
	}
	// 仍包含原始文件名主体。
	if !strings.Contains(got, "it") || !strings.Contains(got, "audio.m4a") {
		t.Fatalf("escaped path lost filename, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// moveInfoJSON
// ---------------------------------------------------------------------------

func TestMoveInfoJSON(t *testing.T) {
	dir := createTempDir(t)
	metadataDir := filepath.Join(dir, "metadata_parts")
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		t.Fatalf("mkdir metadata_parts: %v", err)
	}

	infoFile := filepath.Join(dir, "video.info.json")
	writeFile(t, infoFile, `{"title":"test"}`)

	moveInfoJSON(dir, metadataDir, 1)

	expected := filepath.Join(metadataDir, "p001.info.json")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected file %s to exist after move: %v", expected, err)
	}
	if _, err := os.Stat(infoFile); err == nil {
		t.Fatalf("source file %s should no longer exist after move", infoFile)
	}
}

func TestMoveInfoJSONNotFound(t *testing.T) {
	dir := createTempDir(t)
	metadataDir := filepath.Join(dir, "metadata_parts")
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		t.Fatalf("mkdir metadata_parts: %v", err)
	}

	// No .info.json file in dir; should not panic.
	moveInfoJSON(dir, metadataDir, 1)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".info.json") {
			t.Fatalf("unexpected .info.json found in source dir: %s", e.Name())
		}
	}
}

func TestMoveInfoJSONTargetDirNotExist(t *testing.T) {
	dir := createTempDir(t)
	// metadata_parts does NOT exist.
	metadataDir := filepath.Join(dir, "metadata_parts")

	infoFile := filepath.Join(dir, "video.info.json")
	writeFile(t, infoFile, `{"title":"test"}`)

	// os.Rename will fail because dstDir doesn't exist, but error is silently ignored.
	moveInfoJSON(dir, metadataDir, 1)

	// Source file should still exist (move failed silently).
	if _, err := os.Stat(infoFile); err != nil {
		t.Fatalf("source file %s should still exist after failed move", infoFile)
	}
}

// ---------------------------------------------------------------------------
// fetchCidMapForMultiP（通过 view API 查 cid 映射）
// 注：fetchCidMapForMultiP 内部用默认 view client（默认 base url + 默认 http client），
// 无法在不改签名的情况下注入 mock base url，故单测只覆盖「无 BV / 无效 BV 时返回 nil」
// 的降级行为；带 BV 的实际 view API 查询由集成验证覆盖。
// ---------------------------------------------------------------------------

func TestFetchCidMapForMultiPNoBvid(t *testing.T) {
	cases := []string{
		"https://example.com/video/notabvid",
		"",
		"https://www.bilibili.com/",
	}
	for _, url := range cases {
		m := fetchCidMapForMultiP(context.Background(), url, "")
		if m != nil {
			t.Fatalf("url=%q: expected nil map, got %v", url, m)
		}
	}
}

// ---------------------------------------------------------------------------
// normalizeMetadataName
// ---------------------------------------------------------------------------

func TestNormalizeMetadataName(t *testing.T) {
	dir := createTempDir(t)
	infoFile := filepath.Join(dir, "xxx.info.json")
	writeFile(t, infoFile, `{"title":"test"}`)

	target := filepath.Join(dir, "metadata.ytdlp.json")

	// First call: should rename xxx.info.json -> metadata.ytdlp.json
	if err := normalizeMetadataName(dir); err != nil {
		t.Fatalf("normalizeMetadataName first call error: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected %s to exist after first call: %v", target, err)
	}
	if _, err := os.Stat(infoFile); err == nil {
		t.Fatalf("source file %s should not exist after rename", infoFile)
	}

	// Second call: target already exists, should return nil without error
	if err := normalizeMetadataName(dir); err != nil {
		t.Fatalf("normalizeMetadataName second call error: %v", err)
	}
}

func TestNormalizeMetadataNameDirNotExist(t *testing.T) {
	if err := normalizeMetadataName("/nonexistent/path/that/does/not/exist"); err == nil {
		t.Fatal("normalizeMetadataName expected error for nonexistent directory, got nil")
	}
}

// ---------------------------------------------------------------------------
// playlistEntry
// ---------------------------------------------------------------------------

func TestPlaylistEntryParsing(t *testing.T) {
	raw := `{"id":"abc123","title":"Test Video","webpage_url":"https://example.com/watch?v=abc123","_type":"video","_index":0}`
	var e playlistEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if e.ID != "abc123" {
		t.Fatalf("ID = %q, want %q", e.ID, "abc123")
	}
	if e.Title != "Test Video" {
		t.Fatalf("Title = %q, want %q", e.Title, "Test Video")
	}
	if e.WebpageURL != "https://example.com/watch?v=abc123" {
		t.Fatalf("WebpageURL = %q, want %q", e.WebpageURL, "https://example.com/watch?v=abc123")
	}
	if e.Index != 0 {
		t.Fatalf("Index = %d, want %d", e.Index, 0)
	}
}

func TestPlaylistEntryParsingEmptyID(t *testing.T) {
	raw := `{"id":"","title":"","webpage_url":"","_type":"video","_index":0}`
	var e playlistEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if e.ID != "" {
		t.Fatalf("ID = %q, want empty string", e.ID)
	}
	if e.Title != "" {
		t.Fatalf("Title = %q, want empty string", e.Title)
	}
	if e.WebpageURL != "" {
		t.Fatalf("WebpageURL = %q, want empty string", e.WebpageURL)
	}
	if e.Index != 0 {
		t.Fatalf("Index = %d, want 0", e.Index)
	}
}

// ---------------------------------------------------------------------------
// mock Downloader
// ---------------------------------------------------------------------------

type mockDownloader struct {
	downloadErr error
	downloadCb  func(sourceURL, rawDir string) error
}

func (m *mockDownloader) Download(_ context.Context, sourceURL string, rawDir string, cookieFile string) error {
	if m.downloadCb != nil {
		return m.downloadCb(sourceURL, rawDir)
	}
	return m.downloadErr
}

// --- test fixture ---

type downloadTestFixture struct {
	cfg          *config.Config
	sessions     *session.Store
	states       *state.Store
	pool         *worker.Pool
	database     *sql.DB
	taskStore    *worker.Store
	hub          *worker.Hub
	channelStore *channel.Store
}

func setupDownloadTest(t *testing.T) *downloadTestFixture {
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
	pool := worker.NewPool(taskStore, hub, 1, nil)
	return &downloadTestFixture{
		cfg:          cfg,
		sessions:     sessions,
		states:       states,
		pool:         pool,
		database:     database,
		taskStore:    taskStore,
		hub:          hub,
		channelStore: channel.NewStore(database),
	}
}

func (f *downloadTestFixture) insertChannel(t *testing.T, id string) {
	t.Helper()
	_, err := f.database.Exec(`INSERT OR IGNORE INTO channels (id, name, uid, live_room_id) VALUES (?, ?, ?, ?)`,
		id, "test-channel", 10001, 1234)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func (f *downloadTestFixture) insertSession(t *testing.T, id, slug, channelID, status, sourceURL string) {
	t.Helper()
	_, err := f.database.Exec(`INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status, source_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, slug, channelID, "download", "src-1", "Test Download", status, sourceURL)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// --- noop reporter ---

type noopReporter struct{}

func (noopReporter) Progress(_ context.Context, _ int, _ string) error { return nil }

// ---------------------------------------------------------------------------
// NewHandler / Register
// ---------------------------------------------------------------------------

func TestNewHandler(t *testing.T) {
	fix := setupDownloadTest(t)
	dl := &mockDownloader{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, dl, fix.channelStore)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	if h.downloader != dl {
		t.Fatal("downloader not set correctly")
	}
}

func TestRegister(t *testing.T) {
	fix := setupDownloadTest(t)
	dl := &mockDownloader{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, dl, fix.channelStore)
	h.Register(fix.pool)
}

// ---------------------------------------------------------------------------
// Handler.CreateFromURL
// ---------------------------------------------------------------------------

func TestCreateFromURL_Success(t *testing.T) {
	fix := setupDownloadTest(t)
	fix.insertChannel(t, "ch1")
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, &mockDownloader{}, fix.channelStore)

	task, err := h.CreateFromURL(context.Background(), "ch1", "https://www.bilibili.com/video/BV1xx411c7mD/?spm_id_from=333.999.0.0")
	if err != nil {
		t.Fatalf("CreateFromURL: %v", err)
	}
	if task.Type != TaskType {
		t.Fatalf("task type = %q, want %q", task.Type, TaskType)
	}

	// 场次应已建立，SourceID 为 BV 号，SourceURL 为归一化后的链接
	got, err := fix.sessions.Get(context.Background(), task.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if want := "BV1xx411c7mD"; got.SourceID != want {
		t.Fatalf("SourceID = %q, want %q", got.SourceID, want)
	}
	if got.SourceURL != "https://www.bilibili.com/video/BV1xx411c7mD/" {
		t.Fatalf("SourceURL = %q, want normalized", got.SourceURL)
	}
}

func TestCreateFromURL_DuplicateConflict(t *testing.T) {
	fix := setupDownloadTest(t)
	fix.insertChannel(t, "ch1")
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, &mockDownloader{}, fix.channelStore)

	if _, err := h.CreateFromURL(context.Background(), "ch1", "https://www.bilibili.com/video/BV1xx411c7mD"); err != nil {
		t.Fatalf("first CreateFromURL: %v", err)
	}
	// 同一 BV 号（不同跟踪参数）应识别为已存在，返回 ErrTaskConflict
	_, err := h.CreateFromURL(context.Background(), "ch1", "https://www.bilibili.com/video/BV1xx411c7mD/?spm_id_from=x")
	if !errors.Is(err, worker.ErrTaskConflict) {
		t.Fatalf("expected ErrTaskConflict, got %v", err)
	}
}

func TestCreateFromURL_InvalidInput(t *testing.T) {
	fix := setupDownloadTest(t)
	fix.insertChannel(t, "ch1")
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, &mockDownloader{}, fix.channelStore)

	if _, err := h.CreateFromURL(context.Background(), "", "https://example.com"); !errors.Is(err, session.ErrInvalid) {
		t.Fatalf("empty channel: expected ErrInvalid, got %v", err)
	}
	if _, err := h.CreateFromURL(context.Background(), "ch1", "  "); !errors.Is(err, session.ErrInvalid) {
		t.Fatalf("empty url: expected ErrInvalid, got %v", err)
	}
}

func TestCreateFromURL_ChannelMissing(t *testing.T) {
	fix := setupDownloadTest(t)
	// 不插入任何主播 → FK 约束失败
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, &mockDownloader{}, fix.channelStore)

	_, err := h.CreateFromURL(context.Background(), "ghost", "https://www.bilibili.com/video/BV1xx411c7mD")
	if err == nil {
		t.Fatal("expected error for missing channel FK")
	}
}

// ---------------------------------------------------------------------------
// Cookie 解析：注入 SetCookieAccountStore 后，HandleTask 应经 ResolveCookie 落盘临时 cookie。
// ---------------------------------------------------------------------------

func TestSetCookieAccountStore(t *testing.T) {
	fix := setupDownloadTest(t)
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, &mockDownloader{}, fix.channelStore)
	// 未注入 cookie 账号池时应为 nil，HandleTask 退化为只用 ch.DownloadCookieFile。
	if h.cookieAccountStore != nil {
		t.Fatal("cookieAccountStore should be nil by default")
	}
	store := biliutil.NewCookieAccountStore(fix.database, filepath.Join(fix.cfg.OutputRoot, ".cookies", "bilibili"))
	h.SetCookieAccountStore(store)
	if h.cookieAccountStore == nil {
		t.Fatal("cookieAccountStore should be set")
	}
}

func TestWriteTempCookieFile(t *testing.T) {
	dir := t.TempDir()
	cookie := &biliutil.BiliCookie{SESSDATA: "s1", BiliJct: "j1", DedeUserID: "u1"}
	path, err := writeTempCookieFile(dir, "sess1", cookie)
	if err != nil {
		t.Fatalf("writeTempCookieFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp cookie: %v", err)
	}
	if !strings.Contains(string(data), "SESSDATA\ts1") {
		t.Fatalf("temp cookie missing SESSDATA:\n%s", data)
	}
}

func TestWriteTempCookieFile_Nil(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeTempCookieFile(dir, "sess1", nil); err == nil {
		t.Fatal("expected error for nil cookie")
	}
}

// ---------------------------------------------------------------------------
// Handler.Enqueue
// ---------------------------------------------------------------------------

func TestEnqueueSuccess(t *testing.T) {
	fix := setupDownloadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_dl_bv123", "bv123", "ch1", string(state.StatusDiscovered), "https://example.com/video")

	dl := &mockDownloader{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, dl, fix.channelStore)

	task, err := h.Enqueue(context.Background(), "ch1_dl_bv123")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if task.Type != TaskType {
		t.Fatalf("task type = %q, want %q", task.Type, TaskType)
	}
	if task.SessionID != "ch1_dl_bv123" {
		t.Fatalf("task session ID = %q, want %q", task.SessionID, "ch1_dl_bv123")
	}
}

func TestEnqueueSessionNotFound(t *testing.T) {
	fix := setupDownloadTest(t)
	fix.insertChannel(t, "ch1")

	dl := &mockDownloader{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, dl, fix.channelStore)

	_, err := h.Enqueue(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

// ---------------------------------------------------------------------------
// Handler.HandleTask
// ---------------------------------------------------------------------------

func TestHandleTaskSuccess(t *testing.T) {
	fix := setupDownloadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_dl_bv123", "bv123", "ch1", string(state.StatusDiscovered), "https://example.com/video")

	dl := &mockDownloader{
		downloadCb: func(sourceURL, rawDir string) error {
			writeFile(t, filepath.Join(rawDir, "audio.m4a"), "fake audio")
			return nil
		},
	}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, dl, fix.channelStore)

	task := worker.Task{
		ID: "task_1", ChannelID: "ch1", SessionID: "ch1_dl_bv123",
		Type: TaskType, Payload: "{}",
	}
	reporter := noopReporter{}
	if err := h.HandleTask(context.Background(), task, reporter); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	rawDir := filepath.Join(fix.cfg.OutputRoot, "ch1", "bv123", "raw")
	if _, err := os.Stat(rawDir); err != nil {
		t.Fatalf("raw dir should exist: %v", err)
	}
}

func TestHandleTaskSessionNotFound(t *testing.T) {
	fix := setupDownloadTest(t)
	fix.insertChannel(t, "ch1")

	dl := &mockDownloader{}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, dl, fix.channelStore)

	task := worker.Task{ID: "task_1", ChannelID: "ch1", SessionID: "nonexistent", Type: TaskType}
	reporter := noopReporter{}
	err := h.HandleTask(context.Background(), task, reporter)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestHandleTaskDownloadFails(t *testing.T) {
	fix := setupDownloadTest(t)
	fix.insertChannel(t, "ch1")
	fix.insertSession(t, "ch1_dl_bv123", "bv123", "ch1", string(state.StatusDiscovered), "https://example.com/video")

	dl := &mockDownloader{
		downloadErr: fmt.Errorf("network error"),
	}
	h := NewHandler(fix.cfg, fix.sessions, fix.states, fix.pool, dl, fix.channelStore)

	task := worker.Task{
		ID: "task_1", ChannelID: "ch1", SessionID: "ch1_dl_bv123",
		Type: TaskType, Payload: "{}",
	}
	reporter := noopReporter{}
	err := h.HandleTask(context.Background(), task, reporter)
	if err == nil {
		t.Fatal("expected error for download failure")
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Fatalf("error should contain 'network error', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ffmpegLocationDir / ytDlpArgs（--ffmpeg-location 注入）
// ---------------------------------------------------------------------------

func TestFFmpegLocationDir(t *testing.T) {
	// 用 filepath.Join 构造跨平台路径，避免 Linux 上测 Windows 反斜杠路径语义不符的问题。
	absPath := filepath.Join(string(filepath.Separator), "opt", "hikami", "bin", "ffmpeg")
	relDir := filepath.Join("hikami", ".runtime", "ffmpeg", "bin")
	relPath := filepath.Join(relDir, "ffmpeg")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"bare name ffmpeg", "ffmpeg", ""},
		{"bare name ffprobe", "ffprobe", ""},
		{"absolute path", absPath, filepath.Join(string(filepath.Separator), "opt", "hikami", "bin")},
		{"relative path", relPath, relDir},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ffmpegLocationDir(c.in); got != c.want {
				t.Fatalf("ffmpegLocationDir(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestYTDLPArgsInjectsFFmpegLocation(t *testing.T) {
	d := YTDLPDownloader{FFmpeg: filepath.Join("some", "dir", "ffmpeg")}
	got := d.ytDlpArgs("", "-x", "--audio-format", "m4a")
	want := []string{"--ffmpeg-location", filepath.Join("some", "dir"), "-x", "--audio-format", "m4a"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestYTDLPArgsNoFFmpegLocationForBareName(t *testing.T) {
	d := YTDLPDownloader{FFmpeg: "ffmpeg"}
	got := d.ytDlpArgs("", "-x", "url")
	want := []string{"-x", "url"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v (bare name should not inject)", got, want)
	}
}

func TestYTDLPArgsCombinesCookiesAndFFmpegLocation(t *testing.T) {
	d := YTDLPDownloader{FFmpeg: filepath.Join("bin", "ffmpeg")}
	got := d.ytDlpArgs("cookies.txt", "-x", "url")
	want := []string{"--ffmpeg-location", "bin", "--cookies", "cookies.txt", "-x", "url"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestYTDLPArgsNoPrefixForEmptyAll(t *testing.T) {
	d := YTDLPDownloader{}
	got := d.ytDlpArgs("", "-x", "url")
	want := []string{"-x", "url"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v (empty FFmpeg + empty cookie → no prefix)", got, want)
	}
}

func TestYTDLPArgsCookiesOnlyWhenFFmpegBare(t *testing.T) {
	d := YTDLPDownloader{FFmpeg: "ffmpeg"}
	got := d.ytDlpArgs("cookies.txt", "-x", "url")
	want := []string{"--cookies", "cookies.txt", "-x", "url"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// singlePCid（单 P 弹幕抓取的 cid 解析）
// 注：singlePCid 内部用默认 view client（默认 base url + 默认 http client），
// 无法在不改签名的情况下注入 mock base url，故单测只覆盖「无 BV / 无效 BV 时返回 0」
// 的降级行为（不发网络请求）；带 BV 的实际 view API 查询由集成验证覆盖。
// 参照 TestFetchCidMapForMultiPNoBvid 的模式。
// ---------------------------------------------------------------------------

func TestSinglePCidNoBvid(t *testing.T) {
	cases := []string{
		"https://example.com/video/notabvid",
		"",
		"https://www.bilibili.com/",
	}
	for _, url := range cases {
		if cid := singlePCid(context.Background(), url, ""); cid != 0 {
			t.Fatalf("url=%q: expected cid 0, got %d", url, cid)
		}
	}
}
