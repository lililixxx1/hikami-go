package asr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func insertChannel(t *testing.T, database *sql.DB, channelID string) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO channels (id, name, uid, live_room_id, enabled) VALUES (?, ?, ?, ?, ?)`,
		channelID, "TestChannel", 1, 100, 1)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
}

func insertSession(t *testing.T, database *sql.DB, status string) string {
	t.Helper()
	sessionID := "session_test"
	slug := "live_20260501_120000"
	channelID := "ch1"
	_, err := database.Exec(`INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, source_url, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, slug, channelID, "live_record", "live-100-20260501_120000", "Test Session", "https://example.com", status)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return sessionID
}

// setupTestEnv creates a shared database, pool, and output root.
// Returns handler, pool, database, and outputRoot.
func setupTestEnv(t *testing.T) (*Handler, *worker.Pool, *sql.DB, string) {
	t.Helper()
	database := setupDB(t)
	insertChannel(t, database, "ch1")
	outputRoot := t.TempDir()

	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	pool := worker.NewPool(taskStore, hub, 1, nil)
	if err := pool.Start(context.Background(), 1); err != nil {
		t.Fatalf("start pool: %v", err)
	}
	t.Cleanup(pool.Stop)

	cfg := &config.Config{OutputRoot: outputRoot}
	handler := NewHandler(cfg, session.NewStore(database), state.NewStore(database), LocalTranscriber{}, nil)
	handler.Register(pool)

	return handler, pool, database, outputRoot
}

// ---------------------------------------------------------------------------
// 1-5. CreateTask handler tests
// ---------------------------------------------------------------------------

func TestCreateTaskSuccess(t *testing.T) {
	handler, pool, database, outputRoot := setupTestEnv(t)

	sessionID := insertSession(t, database, string(state.StatusMediaReady))

	// create the expected audio directory and file
	audioDir := filepath.Join(outputRoot, "ch1", "live_20260501_120000", "asr")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "audio.asr.mp3"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write audio file: %v", err)
	}

	task, err := handler.CreateTask(context.Background(), pool, sessionID)
	if err != nil {
		t.Fatalf("CreateTask error: %v", err)
	}
	if task.Type != TaskType {
		t.Fatalf("task.Type = %q, want %q", task.Type, TaskType)
	}
	if task.SessionID != sessionID {
		t.Fatalf("task.SessionID = %q, want %q", task.SessionID, sessionID)
	}
}

func TestCreateTaskWrongStatus(t *testing.T) {
	handler, pool, database, _ := setupTestEnv(t)

	sessionID := insertSession(t, database, string(state.StatusDiscovered))

	_, err := handler.CreateTask(context.Background(), pool, sessionID)
	if err == nil {
		t.Fatalf("expected error for wrong status")
	}
	if !errors.Is(err, ErrSessionNotReady) {
		t.Fatalf("error = %v, want ErrSessionNotReady", err)
	}
}

func TestCreateTaskAudioMissing(t *testing.T) {
	handler, pool, database, _ := setupTestEnv(t)

	sessionID := insertSession(t, database, string(state.StatusMediaReady))

	_, err := handler.CreateTask(context.Background(), pool, sessionID)
	if err == nil {
		t.Fatalf("expected error for missing audio")
	}
	if !errors.Is(err, ErrAudioMissing) {
		t.Fatalf("error = %v, want ErrAudioMissing", err)
	}
}

func TestCreateTaskActiveConflict(t *testing.T) {
	handler, pool, database, outputRoot := setupTestEnv(t)

	sessionID := insertSession(t, database, string(state.StatusMediaReady))

	// create the expected audio directory and file
	audioDir := filepath.Join(outputRoot, "ch1", "live_20260501_120000", "asr")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "audio.asr.mp3"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write audio file: %v", err)
	}

	// First CreateTask succeeds
	_, err := handler.CreateTask(context.Background(), pool, sessionID)
	if err != nil {
		t.Fatalf("first CreateTask error: %v", err)
	}

	// Second CreateTask should conflict (first task is pending in queue)
	_, err = handler.CreateTask(context.Background(), pool, sessionID)
	if err == nil {
		t.Fatalf("expected error for active conflict")
	}
	if !errors.Is(err, worker.ErrTaskConflict) {
		t.Fatalf("error = %v, want worker.ErrTaskConflict", err)
	}
}

func TestCreateTaskSessionNotFound(t *testing.T) {
	handler, pool, _, _ := setupTestEnv(t)

	_, err := handler.CreateTask(context.Background(), pool, "nonexistent_session")
	if err == nil {
		t.Fatalf("expected error for nonexistent session")
	}
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("error = %v, want session.ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// 6. LocalTranscriber
// ---------------------------------------------------------------------------

func TestLocalTranscriber(t *testing.T) {
	transcriber := LocalTranscriber{}
	result, err := transcriber.Transcribe(context.Background(), "/tmp/audio.mp3", session.Session{
		ID:    "sess1",
		Title: "Test Title",
	})
	if err != nil {
		t.Fatalf("Transcribe error: %v", err)
	}
	if result.Transcript == "" {
		t.Fatalf("expected non-empty transcript")
	}
	if result.Transcript != "# Test Title\n\n（ASR 占位转写，等待接入 DashScope 结果。）\n" {
		t.Fatalf("unexpected transcript: %q", result.Transcript)
	}
	if len(result.Segments) != 0 {
		t.Fatalf("expected empty segments, got %d", len(result.Segments))
	}
	if result.SRT != "" {
		t.Fatalf("expected empty SRT, got %q", result.SRT)
	}
}

// ---------------------------------------------------------------------------
// 7-12. DashScope model/body helpers
// ---------------------------------------------------------------------------

func TestNormalizeDashScopeASRModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "fun-asr"},
		{"qwen-asr", "qwen3-asr-flash-filetrans"},
		{"fun-asr", "fun-asr"},
		{"Fun-ASR", "fun-asr"},
		{"other", "other"},
		{"QWEN-ASR", "qwen3-asr-flash-filetrans"},
	}
	for _, tt := range tests {
		got := NormalizeDashScopeASRModel(tt.input)
		if got != tt.want {
			t.Fatalf("NormalizeDashScopeASRModel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsQwenFileTransModel(t *testing.T) {
	if !IsQwenFileTransModel("qwen3-asr-flash-filetrans") {
		t.Fatalf("expected true for qwen3-asr-flash-filetrans")
	}
	if IsQwenFileTransModel("other") {
		t.Fatalf("expected false for other")
	}
	if IsQwenFileTransModel("") {
		t.Fatalf("expected false for empty string")
	}
}

func TestDashScopeRequestMode(t *testing.T) {
	if DashScopeRequestMode("qwen3-asr-flash-filetrans") != "file_url" {
		t.Fatalf("expected file_url for qwen model")
	}
	if DashScopeRequestMode("") != "file_urls" {
		t.Fatalf("expected file_urls for empty model (normalizes to fun-asr)")
	}
	if DashScopeRequestMode("fun-asr") != "file_urls" {
		t.Fatalf("expected file_urls for fun-asr model")
	}
	if DashScopeRequestMode("other") != "file_urls" {
		t.Fatalf("expected file_urls for non-qwen model")
	}
}

func TestBuildDashScopeSubmitBody(t *testing.T) {
	body := buildDashScopeSubmitBody(&config.DashScopeConfig{Model: "qwen3-asr-flash-filetrans"}, "http://example.com/audio.mp3", nil)
	input, ok := body["input"].(map[string]any)
	if !ok {
		t.Fatalf("input is not a map")
	}
	if input["file_url"] != "http://example.com/audio.mp3" {
		t.Fatalf("input.file_url = %v, want URL", input["file_url"])
	}
	if _, exists := input["file_urls"]; exists {
		t.Fatalf("input.file_urls should not exist for qwen model")
	}
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not a map")
	}
	if params["enable_itn"] != false {
		t.Fatalf("parameters.enable_itn = %v, want false", params["enable_itn"])
	}
}

func TestBuildDashScopeSubmitBodyWithLanguage(t *testing.T) {
	body := buildDashScopeSubmitBody(&config.DashScopeConfig{Model: "qwen3-asr-flash-filetrans", Language: "zh"}, "http://example.com/audio.mp3", nil)
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not a map")
	}
	if params["language"] != "zh" {
		t.Fatalf("parameters.language = %v, want zh", params["language"])
	}
}

func TestBuildDashScopeSubmitBodyFileURLs(t *testing.T) {
	body := buildDashScopeSubmitBody(&config.DashScopeConfig{Model: "paraformer-v2"}, "http://example.com/audio.mp3", nil)
	input, ok := body["input"].(map[string]any)
	if !ok {
		t.Fatalf("input is not a map")
	}
	urls, ok := input["file_urls"].([]string)
	if !ok || len(urls) != 1 || urls[0] != "http://example.com/audio.mp3" {
		t.Fatalf("input.file_urls = %v, want [http://example.com/audio.mp3]", input["file_urls"])
	}
	if _, exists := input["file_url"]; exists {
		t.Fatalf("input.file_url should not exist for non-qwen model")
	}
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not a map")
	}
	if _, exists := params["language"]; exists {
		t.Fatalf("parameters.language should not exist for non-qwen model")
	}
}

func TestBuildDashScopeSubmitBodyFunASR(t *testing.T) {
	cfg := &config.DashScopeConfig{
		Model:              "fun-asr",
		Language:           "zh",
		DiarizationEnabled: true,
		SpeakerCount:       2,
		VocabularyID:       "vocab-123",
	}
	body := buildDashScopeSubmitBody(cfg, "http://example.com/audio.mp3", nil)

	if body["model"] != "fun-asr" {
		t.Fatalf("model = %v, want fun-asr", body["model"])
	}

	input, ok := body["input"].(map[string]any)
	if !ok {
		t.Fatalf("input is not a map")
	}
	urls, ok := input["file_urls"].([]string)
	if !ok || len(urls) != 1 || urls[0] != "http://example.com/audio.mp3" {
		t.Fatalf("input.file_urls = %v, want [http://example.com/audio.mp3]", input["file_urls"])
	}

	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not a map")
	}
	if params["diarization_enabled"] != true {
		t.Fatalf("parameters.diarization_enabled = %v, want true", params["diarization_enabled"])
	}
	if params["speaker_count"] != 2 {
		t.Fatalf("parameters.speaker_count = %v, want 2", params["speaker_count"])
	}
	langHints, ok := params["language_hints"].([]string)
	if !ok || len(langHints) != 1 || langHints[0] != "zh" {
		t.Fatalf("parameters.language_hints = %v, want [zh]", params["language_hints"])
	}

	if body["vocabulary_id"] != "vocab-123" {
		t.Fatalf("vocabulary_id = %v, want vocab-123", body["vocabulary_id"])
	}
}

func TestBuildDashScopeSubmitBodyDiarizationDisabled(t *testing.T) {
	cfg := &config.DashScopeConfig{
		Model:              "fun-asr",
		DiarizationEnabled: false,
	}
	body := buildDashScopeSubmitBody(cfg, "http://example.com/audio.mp3", nil)
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not a map")
	}
	if _, exists := params["diarization_enabled"]; exists {
		t.Fatalf("parameters.diarization_enabled should not exist when disabled")
	}
	if _, exists := params["speaker_count"]; exists {
		t.Fatalf("parameters.speaker_count should not exist when diarization disabled")
	}
}

func TestBuildDashScopeSubmitBodyNoVocabularyID(t *testing.T) {
	cfg := &config.DashScopeConfig{
		Model:        "fun-asr",
		VocabularyID: "",
	}
	body := buildDashScopeSubmitBody(cfg, "http://example.com/audio.mp3", nil)
	if _, exists := body["vocabulary_id"]; exists {
		t.Fatalf("vocabulary_id should not exist when empty")
	}
}

func TestBuildDashScopeSubmitBody_Qwen_DiarizationNotInjected(t *testing.T) {
	cfg := &config.DashScopeConfig{
		Model:              "qwen3-asr-flash-filetrans",
		DiarizationEnabled: true,
		SpeakerCount:       3,
		VocabularyID:       "vocab-x",
	}
	body := buildDashScopeSubmitBody(cfg, "http://example.com/audio.mp3", nil)
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not a map")
	}
	if _, exists := params["diarization_enabled"]; exists {
		t.Fatal("diarization_enabled should NOT be injected for qwen model")
	}
	if _, exists := params["speaker_count"]; exists {
		t.Fatal("speaker_count should NOT be injected for qwen model")
	}
	if _, exists := body["vocabulary_id"]; exists {
		t.Fatal("vocabulary_id should NOT be injected for qwen model")
	}
	if _, exists := params["vocabulary"]; exists {
		t.Fatal("parameters.vocabulary should NOT be injected for qwen model")
	}
}

func TestBuildDashScopeSubmitBodyFunASRWithVocabulary(t *testing.T) {
	cfg := &config.DashScopeConfig{Model: "fun-asr"}
	body := buildDashScopeSubmitBody(cfg, "http://example.com/audio.mp3", map[string]int{"灰泽": 4})
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not a map")
	}
	vocabulary, ok := params["vocabulary"].(map[string]int)
	if !ok {
		t.Fatalf("parameters.vocabulary = %T, want map[string]int", params["vocabulary"])
	}
	if vocabulary["灰泽"] != 4 {
		t.Fatalf("vocabulary[灰泽] = %d, want 4", vocabulary["灰泽"])
	}
}

func TestBuildDashScopeSubmitBody_SpeakerCountZero(t *testing.T) {
	cfg := &config.DashScopeConfig{
		Model:              "fun-asr",
		DiarizationEnabled: true,
		SpeakerCount:       0,
	}
	body := buildDashScopeSubmitBody(cfg, "http://example.com/audio.mp3", nil)
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not a map")
	}
	if params["diarization_enabled"] != true {
		t.Fatalf("parameters.diarization_enabled = %v, want true", params["diarization_enabled"])
	}
	if _, exists := params["speaker_count"]; exists {
		t.Fatal("speaker_count should NOT be injected when SpeakerCount is 0")
	}
}

// ---------------------------------------------------------------------------
// 13-17. ExtractTranscript tests
// ---------------------------------------------------------------------------

func TestExtractTranscript(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{"transcription field", map[string]any{"transcription": "hello"}, "hello"},
		{"text field", map[string]any{"text": "world"}, "world"},
		{"nested output.text", map[string]any{"output": map[string]any{"text": "out"}}, "out"},
	}
	for _, tt := range tests {
		got := extractTranscript(tt.raw)
		if got != tt.want {
			t.Fatalf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestExtractTranscriptEmpty(t *testing.T) {
	got := extractTranscript(map[string]any{})
	if got != "" {
		t.Fatalf("got %q, want empty string", got)
	}
}

func TestExtractTranscriptList(t *testing.T) {
	raw := map[string]any{
		"transcripts": []any{
			map[string]any{"text": "first line"},
			map[string]any{"text": "second line"},
		},
	}
	got := extractTranscript(raw)
	want := "first line\nsecond line"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractTranscriptFromSentences(t *testing.T) {
	raw := map[string]any{
		"transcripts": []any{
			map[string]any{
				"sentences": []any{
					map[string]any{"text": "sentence one"},
					map[string]any{"text": "sentence two"},
				},
			},
		},
	}
	got := extractTranscript(raw)
	want := "sentence one\nsentence two"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractTranscriptListMixed(t *testing.T) {
	raw := map[string]any{
		"transcripts": []any{
			map[string]any{"text": "direct text"},
			map[string]any{
				"sentences": []any{
					map[string]any{"text": "via sentence"},
				},
			},
		},
	}
	got := extractTranscript(raw)
	want := "direct text\nvia sentence"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// 18-21. ExtractSegments tests
// ---------------------------------------------------------------------------

func TestExtractSegments(t *testing.T) {
	raw := map[string]any{
		"transcripts": []any{
			map[string]any{
				"channel_id": 0,
				"sentences": []any{
					map[string]any{
						"begin_time":  1000,
						"end_time":    5000,
						"text":        "hello",
						"sentence_id": 1,
						"speaker_id":  2,
					},
				},
			},
		},
	}
	segments := extractSegments(raw)
	if len(segments) != 1 {
		t.Fatalf("len(segments) = %d, want 1", len(segments))
	}
	s := segments[0]
	if s["start_ms"] != int64(1000) {
		t.Fatalf("start_ms = %v, want 1000", s["start_ms"])
	}
	if s["end_ms"] != int64(5000) {
		t.Fatalf("end_ms = %v, want 5000", s["end_ms"])
	}
	if s["text"] != "hello" {
		t.Fatalf("text = %v, want hello", s["text"])
	}
	if s["channel_id"] != int64(0) {
		t.Fatalf("channel_id = %v, want 0", s["channel_id"])
	}
	if s["sentence_id"] != int64(1) {
		t.Fatalf("sentence_id = %v, want 1", s["sentence_id"])
	}
	if s["speaker_id"] != int64(2) {
		t.Fatalf("speaker_id = %v, want 2", s["speaker_id"])
	}
}

func TestExtractSegmentsEmpty(t *testing.T) {
	segments := extractSegments(map[string]any{})
	if len(segments) != 0 {
		t.Fatalf("len(segments) = %d, want 0", len(segments))
	}
}

func TestExtractSegmentsInvalidTimeRange(t *testing.T) {
	raw := map[string]any{
		"transcripts": []any{
			map[string]any{
				"sentences": []any{
					map[string]any{
						"begin_time": 5000,
						"end_time":   1000,
						"text":       "invalid",
					},
				},
			},
		},
	}
	segments := extractSegments(raw)
	if len(segments) != 0 {
		t.Fatalf("len(segments) = %d, want 0 (invalid time range should be skipped)", len(segments))
	}
}

func TestExtractSegmentsMissingSentences(t *testing.T) {
	raw := map[string]any{
		"transcripts": []any{
			map[string]any{"text": "no sentences field"},
		},
	}
	segments := extractSegments(raw)
	if len(segments) != 0 {
		t.Fatalf("len(segments) = %d, want 0 (missing sentences should be skipped)", len(segments))
	}
}

// ---------------------------------------------------------------------------
// 22-24. BuildSRT tests
// ---------------------------------------------------------------------------

func TestBuildSRT(t *testing.T) {
	segments := []map[string]any{
		{"start_ms": int64(1000), "end_ms": int64(5000), "text": "text1"},
		{"start_ms": int64(6000), "end_ms": int64(10000), "text": "text2"},
	}
	got := buildSRT(segments)
	want := "1\n00:00:01,000 --> 00:00:05,000\ntext1\n\n2\n00:00:06,000 --> 00:00:10,000\ntext2\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildSRTEmpty(t *testing.T) {
	got := buildSRT(nil)
	if got != "" {
		t.Fatalf("got %q, want empty string", got)
	}
}

func TestBuildSRTMissingTime(t *testing.T) {
	segments := []map[string]any{
		{"end_ms": int64(5000), "text": "no start"},
	}
	got := buildSRT(segments)
	if got != "" {
		t.Fatalf("got %q, want empty string (segment missing start_ms should be skipped)", got)
	}
}

// ---------------------------------------------------------------------------
// 25. FormatSRTTime
// ---------------------------------------------------------------------------

func TestFormatSRTTime(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{3661500, "01:01:01,500"},
		{0, "00:00:00,000"},
		{-1, "00:00:00,000"},
		{1, "00:00:00,001"},
		{3599999, "00:59:59,999"},
	}
	for _, tt := range tests {
		got := formatSRTTime(tt.input)
		if got != tt.want {
			t.Fatalf("formatSRTTime(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 26. LookupString
// ---------------------------------------------------------------------------

func TestLookupString(t *testing.T) {
	raw := map[string]any{"a": map[string]any{"b": "val"}}
	got := lookupString(raw, "a", "b")
	if got != "val" {
		t.Fatalf("lookupString = %q, want val", got)
	}
	got = lookupString(raw, "x", "y")
	if got != "" {
		t.Fatalf("lookupString nonexistent = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// 27. NumberToInt
// ---------------------------------------------------------------------------

func TestNumberToInt(t *testing.T) {
	tests := []struct {
		input    any
		want     int64
		wantBool bool
	}{
		{int(1), 1, true},
		{int64(2), 2, true},
		{float64(3.0), 3, true},
		{json.Number("4"), 4, true},
		{nil, 0, false},
		{"x", 0, false},
	}
	for _, tt := range tests {
		got, ok := numberToInt(tt.input)
		if got != tt.want || ok != tt.wantBool {
			t.Fatalf("numberToInt(%v) = (%d, %v), want (%d, %v)", tt.input, got, ok, tt.want, tt.wantBool)
		}
	}
}

// ---------------------------------------------------------------------------
// 28. FindResultURL
// ---------------------------------------------------------------------------

func TestFindResultURL(t *testing.T) {
	raw := map[string]any{
		"output": map[string]any{
			"results": []any{
				map[string]any{"transcription_url": "http://x"},
			},
		},
	}
	got := findResultURL(raw)
	if got != "http://x" {
		t.Fatalf("findResultURL = %q, want http://x", got)
	}
}

// ---------------------------------------------------------------------------
// 29. LookupLooseStringArrayIndex
// ---------------------------------------------------------------------------

func TestLookupLooseStringArrayIndex(t *testing.T) {
	raw := map[string]any{
		"items": []any{"first"},
	}
	got := lookupLooseString(raw, "items", "0")
	if got != "first" {
		t.Fatalf("lookupLooseString = %q, want first", got)
	}
	// empty array
	raw2 := map[string]any{"items": []any{}}
	got = lookupLooseString(raw2, "items", "0")
	if got != "" {
		t.Fatalf("lookupLooseString empty array = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// 30. JoinSentenceText
// ---------------------------------------------------------------------------

func TestJoinSentenceText(t *testing.T) {
	sentences := []any{
		map[string]any{"text": "hello"},
		map[string]any{"text": "world"},
	}
	got := joinSentenceText(sentences)
	if got != "hello\nworld" {
		t.Fatalf("joinSentenceText = %q, want hello\\nworld", got)
	}
}

// ---------------------------------------------------------------------------
// 31. NormalizeSRTText
// ---------------------------------------------------------------------------

func TestNormalizeSRTText(t *testing.T) {
	got := normalizeSRTText("a\r\nb\rc")
	if got != "a\nb\nc" {
		t.Fatalf("normalizeSRTText = %q, want a\\nb\\nc", got)
	}
	got = normalizeSRTText("  hello  \n  world  ")
	if got != "hello\nworld" {
		t.Fatalf("normalizeSRTText trimmed = %q, want hello\\nworld", got)
	}
}
