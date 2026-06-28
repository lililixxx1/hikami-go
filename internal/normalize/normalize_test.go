package normalize

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSONL(t *testing.T, path string, items []DanmakuItem) {
	t.Helper()
	var lines []string
	for _, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		lines = append(lines, string(data))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeXMLDanmaku(t *testing.T, path string, entries []string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<i>\n")
	for _, e := range entries {
		sb.WriteString(e + "\n")
	}
	sb.WriteString("</i>")
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// JSONL danmaku parsing
// ---------------------------------------------------------------------------

func TestParseJSONLDanmaku(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	items := []DanmakuItem{
		{TimeMS: 12500, Type: "danmaku", Text: "hello", Source: "live"},
		{TimeMS: 30000, Type: "gift", Text: "gift_msg", Source: "live"},
	}
	writeJSONL(t, path, items)

	got, err := parseJSONLDanmaku(path, "live")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[0].TimeMS != 12500 || got[0].Type != "danmaku" || got[0].Text != "hello" || got[0].Source != "live" {
		t.Fatalf("item 0 mismatch: %+v", got[0])
	}
	if got[1].TimeMS != 30000 || got[1].Type != "gift" || got[1].Text != "gift_msg" {
		t.Fatalf("item 1 mismatch: %+v", got[1])
	}
}

func TestParseJSONLDanmakuEmptyLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	items := []DanmakuItem{
		{TimeMS: 1000, Type: "danmaku", Text: "only one", Source: "replay"},
	}
	writeJSONL(t, path, items)
	// Append extra blank lines
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.WriteString("\n\n   \n\n")
	f.Close()

	got, err := parseJSONLDanmaku(path, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	if got[0].Text != "only one" {
		t.Fatalf("text mismatch: %s", got[0].Text)
	}
}

func TestParseJSONLDanmakuDefaultsType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	// Write a line without "type" field
	line := `{"time_ms":5000,"text":"no type","source":"live"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := parseJSONLDanmaku(path, "live")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	if got[0].Type != "danmaku" {
		t.Fatalf("expected default type 'danmaku', got %q", got[0].Type)
	}
}

func TestParseJSONLDanmakuDefaultsSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	// Write a line without "source" field
	line := `{"time_ms":8000,"text":"no source","type":"danmaku"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := parseJSONLDanmaku(path, "import")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	if got[0].Source != "import" {
		t.Fatalf("expected source 'import', got %q", got[0].Source)
	}
}

func TestParseJSONLDanmakuInvalidJSONLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	content := `{"time_ms":1000,"text":"valid"}
{invalid json line}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := parseJSONLDanmaku(path, "live")
	if err == nil {
		t.Fatalf("expected error for invalid JSON line, got nil")
	}
}

// ---------------------------------------------------------------------------
// XML danmaku parsing
// ---------------------------------------------------------------------------

func TestParseXMLDanmaku(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	entries := []string{
		`<d p="12.5,1,25,FFFFFF,1234567890,0,abc123,987654">弹幕文本</d>`,
	}
	writeXMLDanmaku(t, path, entries)

	got, err := parseXMLDanmaku(path, "replay", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	if got[0].TimeMS != 12500 {
		t.Fatalf("expected time_ms=12500, got %d", got[0].TimeMS)
	}
	if got[0].Color != "#FFFFFF" {
		t.Fatalf("expected color '#FFFFFF', got %q", got[0].Color)
	}
	if got[0].Text != "弹幕文本" {
		t.Fatalf("expected text '弹幕文本', got %q", got[0].Text)
	}
	if got[0].Source != "replay" {
		t.Fatalf("expected source 'replay', got %q", got[0].Source)
	}
	if got[0].Type != "danmaku" {
		t.Fatalf("expected type 'danmaku', got %q", got[0].Type)
	}
}

func TestParseXMLDanmakuUnescapesEntities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	entries := []string{
		`<d p="12.5,1,25,FFFFFF,1234567890,0,abc123,987654">a&amp;b&lt;c&gt;d</d>`,
	}
	writeXMLDanmaku(t, path, entries)

	got, err := parseXMLDanmaku(path, "replay", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	if got[0].Text != "a&b<c>d" {
		t.Fatalf("text = %q, want %q", got[0].Text, "a&b<c>d")
	}
}

func TestParseXMLDanmakuWithTimeOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	entries := []string{
		`<d p="10.0,1,25,FFFFFF,1234567890,0,abc123,987654">偏移测试</d>`,
	}
	writeXMLDanmaku(t, path, entries)

	got, err := parseXMLDanmaku(path, "replay", 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	// 10.0 * 1000 + 5000 = 15000
	if got[0].TimeMS != 15000 {
		t.Fatalf("expected time_ms=15000, got %d", got[0].TimeMS)
	}
}

func TestParseXMLDanmakuSkipsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	entries := []string{
		`<d p="">empty p</d>`,
		`<d p="1">only one field</d>`,
		`<d p="5.0,1,25,FFFFFF,1234567890,0,abc123,987654">valid</d>`,
	}
	writeXMLDanmaku(t, path, entries)

	got, err := parseXMLDanmaku(path, "replay", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item (skipped 2 invalid), got %d", len(got))
	}
	if got[0].Text != "valid" {
		t.Fatalf("expected text 'valid', got %q", got[0].Text)
	}
}

func TestParseXMLDanmakuInvalidTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	entries := []string{
		`<d p="not_a_number,1,25,FFFFFF">bad time</d>`,
		`<d p="3.0,1,25,FFFFFF,1234567890,0,abc123,987654">good</d>`,
	}
	writeXMLDanmaku(t, path, entries)

	got, err := parseXMLDanmaku(path, "replay", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item (skipped invalid time), got %d", len(got))
	}
	if got[0].Text != "good" {
		t.Fatalf("expected text 'good', got %q", got[0].Text)
	}
	if got[0].TimeMS != 3000 {
		t.Fatalf("expected time_ms=3000, got %d", got[0].TimeMS)
	}
}

// ---------------------------------------------------------------------------
// Danmaku priority and merging
// ---------------------------------------------------------------------------

func TestNormalizeDanmakuJSONLPriority(t *testing.T) {
	dir := t.TempDir()
	// Create both danmaku.jsonl and danmaku.xml
	writeJSONL(t, filepath.Join(dir, "danmaku.jsonl"), []DanmakuItem{
		{TimeMS: 1000, Type: "danmaku", Text: "from jsonl", Source: "live"},
	})
	writeXMLDanmaku(t, filepath.Join(dir, "danmaku.xml"), []string{
		`<d p="5.0,1,25,FFFFFF,1234567890,0,abc123,987654">from xml</d>`,
	})

	got, err := normalizeDanmaku(dir, "live")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item from jsonl, got %d", len(got))
	}
	if got[0].Text != "from jsonl" {
		t.Fatalf("expected text from jsonl, got %q", got[0].Text)
	}
}

func TestNormalizeDanmakuXMLFallback(t *testing.T) {
	dir := t.TempDir()
	// Only danmaku.xml exists
	writeXMLDanmaku(t, filepath.Join(dir, "danmaku.xml"), []string{
		`<d p="8.0,1,25,FFFFFF,1234567890,0,abc123,987654">from xml only</d>`,
	})

	got, err := normalizeDanmaku(dir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	if got[0].Text != "from xml only" {
		t.Fatalf("expected text 'from xml only', got %q", got[0].Text)
	}
}

func TestMergeMultiPDanmaku(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write part_durations.json
	durations := []partDurationRecord{
		{Index: 1, DurSecs: 60.0},
		{Index: 2, DurSecs: 120.0},
	}
	durData, _ := json.Marshal(durations)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), durData, 0644); err != nil {
		t.Fatalf("write durations: %v", err)
	}

	// Write p001.xml (time at 10s)
	writeXMLDanmaku(t, filepath.Join(partsDir, "p001.xml"), []string{
		`<d p="10.0,1,25,FFFFFF,100,0,hash1,id1">part1</d>`,
	})
	// Write p002.xml (time at 5s, but should be offset by 60000ms)
	writeXMLDanmaku(t, filepath.Join(partsDir, "p002.xml"), []string{
		`<d p="5.0,1,25,FFFFFF,200,0,hash2,id2">part2</d>`,
	})

	got, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	// part1: 10.0 * 1000 + 0 = 10000
	if got[0].TimeMS != 10000 {
		t.Fatalf("p1 time_ms: expected 10000, got %d", got[0].TimeMS)
	}
	if got[0].Text != "part1" {
		t.Fatalf("p1 text: expected 'part1', got %q", got[0].Text)
	}
	// part2: 5.0 * 1000 + 60000 = 65000
	if got[1].TimeMS != 65000 {
		t.Fatalf("p2 time_ms: expected 65000, got %d", got[1].TimeMS)
	}
	if got[1].Text != "part2" {
		t.Fatalf("p2 text: expected 'part2', got %q", got[1].Text)
	}
}

func TestMergeMultiPDanmakuMissingPartFile(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// 3 parts in durations but only 2 XML files
	durations := []partDurationRecord{
		{Index: 1, DurSecs: 60.0},
		{Index: 2, DurSecs: 120.0},
		{Index: 3, DurSecs: 90.0},
	}
	durData, _ := json.Marshal(durations)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), durData, 0644); err != nil {
		t.Fatalf("write durations: %v", err)
	}

	writeXMLDanmaku(t, filepath.Join(partsDir, "p001.xml"), []string{
		`<d p="2.0,1,25,FFFFFF,100,0,h1,i1">p1</d>`,
	})
	writeXMLDanmaku(t, filepath.Join(partsDir, "p002.xml"), []string{
		`<d p="3.0,1,25,FFFFFF,200,0,h2,i2">p2</d>`,
	})
	// p003.xml intentionally missing

	got, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err != nil {
		t.Fatalf("expected no error for missing part file, got: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[1].TimeMS != 63000 {
		t.Fatalf("p2 time_ms: expected 63000, got %d", got[1].TimeMS)
	}
}

func TestMergeMultiPDanmakuMissingMiddlePartOffsetsNextXML(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	durations := []partDurationRecord{
		{Index: 1, DurSecs: 10.0},
		{Index: 2, DurSecs: 20.0},
		{Index: 3, DurSecs: 30.0},
	}
	durData, _ := json.Marshal(durations)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), durData, 0644); err != nil {
		t.Fatalf("write durations: %v", err)
	}
	writeXMLDanmaku(t, filepath.Join(partsDir, "p001.xml"), []string{
		`<d p="1.0,1,25,FFFFFF,100,0,h1,i1">p1</d>`,
	})
	writeXMLDanmaku(t, filepath.Join(partsDir, "p003.xml"), []string{
		`<d p="2.0,1,25,FFFFFF,300,0,h3,i3">p3</d>`,
	})

	got, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[1].TimeMS != 32000 {
		t.Fatalf("p3 time_ms: expected 32000, got %d", got[1].TimeMS)
	}
}

func TestMergeMultiPDanmakuMissingDurations(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No part_durations.json written
	writeXMLDanmaku(t, filepath.Join(partsDir, "p001.xml"), []string{
		`<d p="1.0,1,25,FFFFFF,100,0,h1,i1">orphan</d>`,
	})

	got, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items when durations missing, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// No danmaku files
// ---------------------------------------------------------------------------

func TestNormalizeDanmakuNoFiles(t *testing.T) {
	dir := t.TempDir()
	// Empty directory, no danmaku files at all

	got, err := normalizeDanmaku(dir, "live")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// File operations
// ---------------------------------------------------------------------------

func TestFindRawAudio(t *testing.T) {
	dir := t.TempDir()
	// Test with audio.m4a
	m4aPath := filepath.Join(dir, "audio.m4a")
	if err := os.WriteFile(m4aPath, []byte("fake"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := findRawAudio(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != m4aPath {
		t.Fatalf("expected %q, got %q", m4aPath, got)
	}
}

func TestFindRawAudioPriority(t *testing.T) {
	dir := t.TempDir()
	m4aPath := filepath.Join(dir, "audio.m4a")
	flacPath := filepath.Join(dir, "audio.flac")
	if err := os.WriteFile(m4aPath, []byte("fake"), 0644); err != nil {
		t.Fatalf("write m4a: %v", err)
	}
	if err := os.WriteFile(flacPath, []byte("fake"), 0644); err != nil {
		t.Fatalf("write flac: %v", err)
	}

	got, err := findRawAudio(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != m4aPath {
		t.Fatalf("expected m4a priority %q, got %q", m4aPath, got)
	}
}

func TestFindRawAudioNotFound(t *testing.T) {
	dir := t.TempDir()
	// Empty directory

	_, err := findRawAudio(dir)
	if err == nil {
		t.Fatalf("expected error for empty dir, got nil")
	}
	if !strings.Contains(err.Error(), "raw audio not found") {
		t.Fatalf("error message mismatch: %v", err)
	}
}

func TestWriteJSONAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.json")

	data := map[string]string{"key": "value"}
	if err := writeJSONAtomic(path, data); err != nil {
		t.Fatalf("writeJSONAtomic: %v", err)
	}

	// Verify file content
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["key"] != "value" {
		t.Fatalf("expected key=value, got %q", got["key"])
	}

	// Verify .tmp file does not exist
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected .tmp file to not exist, stat error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Metadata
// ---------------------------------------------------------------------------

func TestBuildMetadata(t *testing.T) {
	s := session.Session{
		ID:         "sess-001",
		ChannelID:  "ch-001",
		Slug:       "test-session",
		SourceType: "live",
		SourceID:   "src-001",
		Title:      "测试标题",
		StartedAt:  "2025-01-01T10:00:00Z",
		EndedAt:    "2025-01-01T11:00:00Z",
		SourceURL:  "https://live.bilibili.com/12345",
		Status:     "media_ready",
		CreatedAt:  "2025-01-01T10:00:00Z",
		UpdatedAt:  "2025-01-01T11:00:00Z",
	}

	m := buildMetadata(s, "/data/ch-001/test-session/raw/audio.m4a", "/data/ch-001/test-session/asr/audio.asr.mp3", 42)

	if m.SessionID != "sess-001" {
		t.Fatalf("SessionID: expected 'sess-001', got %q", m.SessionID)
	}
	if m.ChannelID != "ch-001" {
		t.Fatalf("ChannelID: expected 'ch-001', got %q", m.ChannelID)
	}
	if m.Slug != "test-session" {
		t.Fatalf("Slug: expected 'test-session', got %q", m.Slug)
	}
	if m.SourceType != "live" {
		t.Fatalf("SourceType: expected 'live', got %q", m.SourceType)
	}
	if m.SourceID != "src-001" {
		t.Fatalf("SourceID: expected 'src-001', got %q", m.SourceID)
	}
	if m.Title != "测试标题" {
		t.Fatalf("Title: expected '测试标题', got %q", m.Title)
	}
	if m.StartedAt != "2025-01-01T10:00:00Z" {
		t.Fatalf("StartedAt: expected '2025-01-01T10:00:00Z', got %q", m.StartedAt)
	}
	if m.EndedAt != "2025-01-01T11:00:00Z" {
		t.Fatalf("EndedAt: expected '2025-01-01T11:00:00Z', got %q", m.EndedAt)
	}
	if m.SourceURL != "https://live.bilibili.com/12345" {
		t.Fatalf("SourceURL: mismatch: %q", m.SourceURL)
	}
	if m.DanmakuCount != 42 {
		t.Fatalf("DanmakuCount: expected 42, got %d", m.DanmakuCount)
	}
	// Verify relative paths
	if m.RawAudioPath != "raw/audio.m4a" {
		t.Fatalf("RawAudioPath: expected 'raw/audio.m4a', got %q", m.RawAudioPath)
	}
	if m.ASRAudioPath != "asr/audio.asr.mp3" {
		t.Fatalf("ASRAudioPath: expected 'asr/audio.asr.mp3', got %q", m.ASRAudioPath)
	}
	if m.GeneratedAt == "" {
		t.Fatalf("GeneratedAt: expected non-empty")
	}
}

// ---------------------------------------------------------------------------
// Additional coverage tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// parseXMLDanmaku - additional edge cases
// ---------------------------------------------------------------------------

func TestParseXMLDanmakuUserIDAndRawTime(t *testing.T) {
	tests := []struct {
		name        string
		pAttr       string
		wantUserID  string
		wantRawTime string
		wantTimeMS  int64
		wantColor   string
		wantText    string
		skipItem    bool
	}{
		{
			name:        "8 fields with user_id and raw_time",
			pAttr:       "12.5,1,25,FFFFFF,1700000000,0,abc123,987654",
			wantUserID:  "987654",
			wantRawTime: "1700000000",
			wantTimeMS:  12500,
			wantColor:   "#FFFFFF",
			wantText:    "full fields",
		},
		{
			name:        "7 fields with raw_time but no user_id",
			pAttr:       "5.0,1,25,AABBCC,1700000000,0,abc123",
			wantUserID:  "",
			wantRawTime: "1700000000",
			wantTimeMS:  5000,
			wantColor:   "#AABBCC",
			wantText:    "seven fields",
		},
		{
			name:        "4 fields minimum valid",
			pAttr:       "3.0,1,25,16777215",
			wantUserID:  "",
			wantRawTime: "",
			wantTimeMS:  3000,
			wantColor:   "#16777215",
			wantText:    "minimal",
		},
		{
			name:     "3 fields too few - skipped",
			pAttr:    "1.0,1,25",
			skipItem: true,
		},
		{
			name:     "2 fields too few - skipped",
			pAttr:    "1.0,1",
			skipItem: true,
		},
		{
			name:     "empty fields - skipped",
			pAttr:    "",
			skipItem: true,
		},
		{
			name:     "non-numeric time - skipped",
			pAttr:    "abc,1,25,FFFFFF",
			skipItem: true,
		},
		{
			name:        "negative time still parsed",
			pAttr:       "-1.5,1,25,FFFFFF,0,0,hash,id",
			wantUserID:  "id",
			wantRawTime: "0",
			wantTimeMS:  -1500,
			wantColor:   "#FFFFFF",
			wantText:    "negative time",
		},
		{
			name:       "fractional millisecond precision",
			pAttr:      "1.234567,1,25,FFFFFF",
			wantTimeMS: 1234, // int64(1.234567*1000) = 1234
			wantColor:  "#FFFFFF",
			wantText:   "fractional",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "danmaku.xml")
			writeXMLDanmaku(t, path, []string{
				"<d p=\"" + tt.pAttr + "\">" + tt.wantText + "</d>",
			})

			got, err := parseXMLDanmaku(path, "replay", 0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.skipItem {
				if len(got) != 0 {
					t.Fatalf("expected item to be skipped, got %d items", len(got))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 item, got %d", len(got))
			}
			if got[0].UserID != tt.wantUserID {
				t.Errorf("UserID: expected %q, got %q", tt.wantUserID, got[0].UserID)
			}
			if got[0].RawTime != tt.wantRawTime {
				t.Errorf("RawTime: expected %q, got %q", tt.wantRawTime, got[0].RawTime)
			}
			if got[0].TimeMS != tt.wantTimeMS {
				t.Errorf("TimeMS: expected %d, got %d", tt.wantTimeMS, got[0].TimeMS)
			}
			if got[0].Color != tt.wantColor {
				t.Errorf("Color: expected %q, got %q", tt.wantColor, got[0].Color)
			}
		})
	}
}

func TestParseXMLDanmakuEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	writeXMLDanmaku(t, path, nil) // no <d> elements

	got, err := parseXMLDanmaku(path, "replay", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items for empty XML, got %d", len(got))
	}
}

func TestParseXMLDanmakuInvalidXML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	if err := os.WriteFile(path, []byte("not valid xml <<<"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := parseXMLDanmaku(path, "replay", 0)
	if err == nil {
		t.Fatalf("expected error for invalid XML, got nil")
	}
}

func TestParseXMLDanmakuFileNotFound(t *testing.T) {
	_, err := parseXMLDanmaku("/nonexistent/path/danmaku.xml", "replay", 0)
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

func TestParseXMLDanmakuNegativeTimeOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	writeXMLDanmaku(t, path, []string{
		`<d p="10.0,1,25,FFFFFF,0,0,h,id">text</d>`,
	})

	got, err := parseXMLDanmaku(path, "replay", -3000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	// 10.0 * 1000 + (-3000) = 7000
	if got[0].TimeMS != 7000 {
		t.Fatalf("expected time_ms=7000, got %d", got[0].TimeMS)
	}
}

// ---------------------------------------------------------------------------
// parseJSONLDanmaku - additional edge cases
// ---------------------------------------------------------------------------

func TestParseJSONLDanmakuEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := parseJSONLDanmaku(path, "live")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items for empty file, got %d", len(got))
	}
}

func TestParseJSONLDanmakuAllBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	if err := os.WriteFile(path, []byte("\n\n\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := parseJSONLDanmaku(path, "live")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items for all-blank file, got %d", len(got))
	}
}

func TestParseJSONLDanmakuFileNotFound(t *testing.T) {
	_, err := parseJSONLDanmaku("/nonexistent/path/danmaku.jsonl", "live")
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

func TestParseJSONLDanmakuPreservesSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	// Item already has a source field, should NOT be overridden
	line := `{"time_ms":5000,"text":"has source","type":"gift","source":"custom"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := parseJSONLDanmaku(path, "import")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	// Source is already set, should remain "custom" not "import"
	if got[0].Source != "custom" {
		t.Fatalf("expected source 'custom' (preserved), got %q", got[0].Source)
	}
	if got[0].Type != "gift" {
		t.Fatalf("expected type 'gift' (preserved), got %q", got[0].Type)
	}
}

func TestParseJSONLDanmakuAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.jsonl")
	line := `{"time_ms":9999,"type":"danmaku","user_id":"u123","user_name":"tester","text":"hello","color":"#FF0000","raw_time":"2025-01-01","source":"live"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := parseJSONLDanmaku(path, "other")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	d := got[0]
	if d.TimeMS != 9999 {
		t.Errorf("TimeMS: expected 9999, got %d", d.TimeMS)
	}
	if d.UserID != "u123" {
		t.Errorf("UserID: expected 'u123', got %q", d.UserID)
	}
	if d.UserName != "tester" {
		t.Errorf("UserName: expected 'tester', got %q", d.UserName)
	}
	if d.Text != "hello" {
		t.Errorf("Text: expected 'hello', got %q", d.Text)
	}
	if d.Color != "#FF0000" {
		t.Errorf("Color: expected '#FF0000', got %q", d.Color)
	}
	if d.RawTime != "2025-01-01" {
		t.Errorf("RawTime: expected '2025-01-01', got %q", d.RawTime)
	}
	if d.Source != "live" {
		t.Errorf("Source: expected 'live' (preserved), got %q", d.Source)
	}
}

// ---------------------------------------------------------------------------
// loadPartDurations
// ---------------------------------------------------------------------------

func TestLoadPartDurations(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantCount int
		wantErr   bool
	}{
		{
			name: "valid durations sorted by index",
			setup: func(t *testing.T, dir string) {
				data := []partDurationRecord{
					{Index: 3, DurSecs: 90.0},
					{Index: 1, DurSecs: 60.0},
					{Index: 2, DurSecs: 120.0},
				}
				raw, _ := json.Marshal(data)
				if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), raw, 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
			},
			wantCount: 3,
		},
		{
			name: "missing file returns nil",
			setup: func(t *testing.T, dir string) {
				// do not write anything
			},
			wantCount: 0,
		},
		{
			name: "empty array",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), []byte("[]"), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
			},
			wantCount: 0,
		},
		{
			name: "invalid JSON returns error",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), []byte("not json"), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
			},
			wantErr: true,
		},
		{
			name: "single part",
			setup: func(t *testing.T, dir string) {
				data := []partDurationRecord{{Index: 0, DurSecs: 45.5}}
				raw, _ := json.Marshal(data)
				if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), raw, 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)
			got, err := loadPartDurations(dir)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("expected %d durations, got %d", tt.wantCount, len(got))
			}
			// Verify sorted order
			for i := 1; i < len(got); i++ {
				if got[i].Index <= got[i-1].Index {
					t.Errorf("durations not sorted: got[%d].Index=%d <= got[%d].Index=%d",
						i, got[i].Index, i-1, got[i-1].Index)
				}
			}
		})
	}
}

func TestLoadPartDurationsSorted(t *testing.T) {
	dir := t.TempDir()
	data := []partDurationRecord{
		{Index: 3, DurSecs: 90.0},
		{Index: 1, DurSecs: 60.0},
		{Index: 2, DurSecs: 120.0},
	}
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), raw, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := loadPartDurations(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].Index != 1 || got[1].Index != 2 || got[2].Index != 3 {
		t.Fatalf("expected sorted indices [1,2,3], got %v", got)
	}
	if got[0].DurSecs != 60.0 || got[1].DurSecs != 120.0 || got[2].DurSecs != 90.0 {
		t.Fatalf("duration values mismatch after sort: %v", got)
	}
}

// ---------------------------------------------------------------------------
// mergeMultiPDanmaku - additional edge cases
// ---------------------------------------------------------------------------

func TestMergeMultiPDanmakuNoXMLFiles(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	durations := []partDurationRecord{{Index: 1, DurSecs: 60.0}}
	raw, _ := json.Marshal(durations)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), raw, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Write a non-XML file
	if err := os.WriteFile(filepath.Join(partsDir, "readme.txt"), []byte("not xml"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items when no XML files, got %d", len(got))
	}
}

func TestMergeMultiPDanmakuUnsortedParts(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	durations := []partDurationRecord{
		{Index: 1, DurSecs: 10.0},
		{Index: 2, DurSecs: 20.0},
	}
	raw, _ := json.Marshal(durations)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), raw, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Write parts in reverse order to verify sorting
	writeXMLDanmaku(t, filepath.Join(partsDir, "p002.xml"), []string{
		`<d p="5.0,1,25,FFFFFF,0,0,h2,i2">p2</d>`,
	})
	writeXMLDanmaku(t, filepath.Join(partsDir, "p001.xml"), []string{
		`<d p="3.0,1,25,FFFFFF,0,0,h1,i1">p1</d>`,
	})

	got, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	// p1 should come first (index 1), p2 second (index 2)
	if got[0].Text != "p1" {
		t.Fatalf("expected first item to be 'p1', got %q", got[0].Text)
	}
	if got[1].Text != "p2" {
		t.Fatalf("expected second item to be 'p2', got %q", got[1].Text)
	}
	// p1: 3.0 * 1000 + 0 = 3000
	if got[0].TimeMS != 3000 {
		t.Fatalf("p1 time: expected 3000, got %d", got[0].TimeMS)
	}
	// p2: 5.0 * 1000 + 10000 = 15000
	if got[1].TimeMS != 15000 {
		t.Fatalf("p2 time: expected 15000, got %d", got[1].TimeMS)
	}
}

func TestMergeMultiPDanmakuInvalidPartFilename(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	durations := []partDurationRecord{{Index: 1, DurSecs: 10.0}}
	raw, _ := json.Marshal(durations)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), raw, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Write a file that doesn't match pNNN.xml pattern
	writeXMLDanmaku(t, filepath.Join(partsDir, "part_one.xml"), []string{
		`<d p="1.0,1,25,FFFFFF,0,0,h,i">skip me</d>`,
	})

	got, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items for non-matching filename, got %d", len(got))
	}
}

func TestMergeMultiPDanmakuPartsDirNotExist(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	// Do not create partsDir

	got, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items for non-existent parts dir, got %d", len(got))
	}
}

func TestMergeMultiPDanmakuCorruptXML(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	durations := []partDurationRecord{{Index: 1, DurSecs: 10.0}}
	raw, _ := json.Marshal(durations)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), raw, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Write corrupt XML
	if err := os.WriteFile(filepath.Join(partsDir, "p001.xml"), []byte("<<<not xml>>>"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := mergeMultiPDanmaku(dir, partsDir, "replay")
	if err == nil {
		t.Fatalf("expected error for corrupt XML part, got nil")
	}
	if !strings.Contains(err.Error(), "parse danmaku part") {
		t.Fatalf("error should mention part parsing, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// normalizeDanmaku - additional edge cases
// ---------------------------------------------------------------------------

func TestNormalizeDanmakuEmptyJSONLFallsBack(t *testing.T) {
	dir := t.TempDir()
	// Write a valid but empty JSONL (no valid JSON lines)
	// normalizeDanmaku will get empty items, skip to XML (not found), skip to parts (not found)
	if err := os.WriteFile(filepath.Join(dir, "danmaku.jsonl"), []byte("\n\n"), 0644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	got, err := normalizeDanmaku(dir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 items, got %d", len(got))
	}
}

func TestNormalizeDanmakuJSONLErrorFallsBack(t *testing.T) {
	dir := t.TempDir()
	// Write invalid JSONL
	if err := os.WriteFile(filepath.Join(dir, "danmaku.jsonl"), []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := normalizeDanmaku(dir, "live")
	// parseJSONLDanmaku should fail and propagate
	if err == nil {
		t.Fatalf("expected error from invalid JSONL, got nil")
	}
}

func TestNormalizeDanmakuMultiPartsPriority(t *testing.T) {
	dir := t.TempDir()
	partsDir := filepath.Join(dir, "danmaku_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No danmaku.jsonl or danmaku.xml, only danmaku_parts
	durations := []partDurationRecord{{Index: 1, DurSecs: 10.0}}
	raw, _ := json.Marshal(durations)
	if err := os.WriteFile(filepath.Join(dir, "part_durations.json"), raw, 0644); err != nil {
		t.Fatalf("write durations: %v", err)
	}
	writeXMLDanmaku(t, filepath.Join(partsDir, "p001.xml"), []string{
		`<d p="1.0,1,25,FFFFFF,0,0,h,i">from parts</d>`,
	})

	got, err := normalizeDanmaku(dir, "replay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item from parts, got %d", len(got))
	}
	if got[0].Text != "from parts" {
		t.Fatalf("expected 'from parts', got %q", got[0].Text)
	}
}

func TestNormalizeDanmakuXMLError(t *testing.T) {
	dir := t.TempDir()
	// Only danmaku.xml with invalid content
	if err := os.WriteFile(filepath.Join(dir, "danmaku.xml"), []byte("<<<bad xml>>>"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := normalizeDanmaku(dir, "replay")
	// parseXMLDanmaku should fail, but normalizeDanmaku catches it and returns error
	if err == nil {
		t.Fatalf("expected error from invalid XML, got nil")
	}
}

// ---------------------------------------------------------------------------
// findRawAudio - additional edge cases
// ---------------------------------------------------------------------------

func TestFindRawAudioFallbackToAnyAudio(t *testing.T) {
	dir := t.TempDir()
	// No audio.m4a, but has audio.flac
	flacPath := filepath.Join(dir, "audio.flac")
	if err := os.WriteFile(flacPath, []byte("fake"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := findRawAudio(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != flacPath {
		t.Fatalf("expected %q, got %q", flacPath, got)
	}
}

func TestFindRawAudioDirNotExist(t *testing.T) {
	_, err := findRawAudio("/nonexistent/path/raw")
	if err == nil {
		t.Fatalf("expected error for non-existent dir, got nil")
	}
}

func TestFindRawAudioSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory starting with "audio."
	if err := os.Mkdir(filepath.Join(dir, "audio.subdir"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := findRawAudio(dir)
	if err == nil {
		t.Fatalf("expected error when only audio.* directories exist, got nil")
	}
}

func TestFindRawAudioMultipleAudioFiles(t *testing.T) {
	dir := t.TempDir()
	// Create audio.wav and audio.mp3 but no audio.m4a
	// ReadDir order is not guaranteed, but it should find one
	wavPath := filepath.Join(dir, "audio.wav")
	mp3Path := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(wavPath, []byte("fake"), 0644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	if err := os.WriteFile(mp3Path, []byte("fake"), 0644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	got, err := findRawAudio(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should find one of them
	if got != wavPath && got != mp3Path {
		t.Fatalf("expected one of audio.wav/audio.mp3, got %q", got)
	}
}

func TestFindRawAudioNonAudioFileIgnored(t *testing.T) {
	dir := t.TempDir()
	// Create a non-audio file
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("text"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Create audio.mp3
	mp3Path := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(mp3Path, []byte("fake"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := findRawAudio(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != mp3Path {
		t.Fatalf("expected %q, got %q", mp3Path, got)
	}
}

// ---------------------------------------------------------------------------
// writeJSONAtomic - additional edge cases
// ---------------------------------------------------------------------------

func TestWriteJSONAtomicWithSlice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.json")

	data := []DanmakuItem{
		{TimeMS: 1000, Text: "one"},
		{TimeMS: 2000, Text: "two"},
	}
	if err := writeJSONAtomic(path, data); err != nil {
		t.Fatalf("writeJSONAtomic: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got []DanmakuItem
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("content mismatch: %+v", got)
	}
	// Verify trailing newline
	if !strings.HasSuffix(string(content), "\n") {
		t.Fatalf("expected trailing newline in JSON output")
	}
}

func TestWriteJSONAtomicOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.json")

	// Write first
	if err := writeJSONAtomic(path, map[string]int{"v": 1}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Overwrite
	if err := writeJSONAtomic(path, map[string]int{"v": 2}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got map[string]int
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["v"] != 2 {
		t.Fatalf("expected v=2 after overwrite, got %d", got["v"])
	}
}

func TestWriteJSONAtomicMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")

	m := Metadata{
		SessionID:    "s1",
		ChannelID:    "c1",
		Slug:         "slug1",
		SourceType:   "live",
		SourceID:     "src1",
		Title:        "title",
		SourceURL:    "https://example.com",
		RawAudioPath: "raw/audio.m4a",
		ASRAudioPath: "asr/audio.asr.mp3",
		DanmakuCount: 10,
		GeneratedAt:  "2025-01-01T00:00:00Z",
	}
	if err := writeJSONAtomic(path, m); err != nil {
		t.Fatalf("writeJSONAtomic: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got Metadata
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID != "s1" || got.DanmakuCount != 10 {
		t.Fatalf("metadata mismatch: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// buildMetadata - additional edge cases
// ---------------------------------------------------------------------------

func TestBuildMetadataEmptySession(t *testing.T) {
	s := session.Session{}
	m := buildMetadata(s, "/data/raw/audio.m4a", "/data/asr/audio.asr.mp3", 0)

	if m.SessionID != "" {
		t.Errorf("expected empty SessionID, got %q", m.SessionID)
	}
	if m.DanmakuCount != 0 {
		t.Errorf("expected DanmakuCount=0, got %d", m.DanmakuCount)
	}
	if m.GeneratedAt == "" {
		t.Errorf("expected non-empty GeneratedAt")
	}
	// Verify relative path extraction
	if m.RawAudioPath != "raw/audio.m4a" {
		t.Errorf("RawAudioPath: expected 'raw/audio.m4a', got %q", m.RawAudioPath)
	}
	if m.ASRAudioPath != "asr/audio.asr.mp3" {
		t.Errorf("ASRAudioPath: expected 'asr/audio.asr.mp3', got %q", m.ASRAudioPath)
	}
}

func TestBuildMetadataWithSubdirs(t *testing.T) {
	s := session.Session{ID: "s1", ChannelID: "c1"}

	m := buildMetadata(s, "/a/b/c/ch-001/sess/raw/audio.m4a", "/a/b/c/ch-001/sess/asr/audio.asr.mp3", 5)

	if m.RawAudioPath != "raw/audio.m4a" {
		t.Errorf("RawAudioPath: expected 'raw/audio.m4a', got %q", m.RawAudioPath)
	}
	if m.ASRAudioPath != "asr/audio.asr.mp3" {
		t.Errorf("ASRAudioPath: expected 'asr/audio.asr.mp3', got %q", m.ASRAudioPath)
	}
}

// ---------------------------------------------------------------------------
// NewHandler
// ---------------------------------------------------------------------------

func TestNewHandler(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil)
	if h == nil {
		t.Fatalf("expected non-nil handler")
	}
	if h.cfg != nil {
		t.Errorf("expected nil config")
	}
	if h.sessions != nil {
		t.Errorf("expected nil sessions")
	}
	if h.states != nil {
		t.Errorf("expected nil states")
	}
	if h.converter != nil {
		t.Errorf("expected nil converter")
	}
}

// ---------------------------------------------------------------------------
// FFmpegConverter
// ---------------------------------------------------------------------------

func TestFFmpegConverterDefaultCommand(t *testing.T) {
	c := FFmpegConverter{}
	if c.Command != "" {
		t.Errorf("expected empty Command for zero value, got %q", c.Command)
	}
}

func TestFFmpegConverterWithCommand(t *testing.T) {
	c := FFmpegConverter{Command: "/usr/local/bin/ffmpeg"}
	if c.Command != "/usr/local/bin/ffmpeg" {
		t.Errorf("expected custom command, got %q", c.Command)
	}
}

// ---------------------------------------------------------------------------
// TaskType constant
// ---------------------------------------------------------------------------

func TestTaskType(t *testing.T) {
	if TaskType != "normalize" {
		t.Errorf("expected TaskType='normalize', got %q", TaskType)
	}
}

// ---------------------------------------------------------------------------
// DanmakuItem struct
// ---------------------------------------------------------------------------

func TestDanmakuItemJSONRoundTrip(t *testing.T) {
	original := DanmakuItem{
		TimeMS:   12345,
		Type:     "danmaku",
		UserID:   "user1",
		UserName: "TestUser",
		Text:     "Hello World",
		Color:    "#FF0000",
		RawTime:  "2025-01-01T12:00:00Z",
		Source:   "live",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got DanmakuItem
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != original {
		t.Fatalf("round-trip mismatch:\n  original: %+v\n  got:      %+v", original, got)
	}
}

func TestDanmakuItemOmitEmpty(t *testing.T) {
	item := DanmakuItem{TimeMS: 1000, Text: "hi"}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	// Fields with omitempty should not appear when zero
	if strings.Contains(s, `"user_id"`) {
		t.Errorf("expected user_id to be omitted, got: %s", s)
	}
	if strings.Contains(s, `"user_name"`) {
		t.Errorf("expected user_name to be omitted, got: %s", s)
	}
	if strings.Contains(s, `"color"`) {
		t.Errorf("expected color to be omitted, got: %s", s)
	}
	if strings.Contains(s, `"raw_time"`) {
		t.Errorf("expected raw_time to be omitted, got: %s", s)
	}
}

// ---------------------------------------------------------------------------
// Metadata struct
// ---------------------------------------------------------------------------

func TestMetadataJSONRoundTrip(t *testing.T) {
	original := Metadata{
		SessionID:    "s1",
		ChannelID:    "c1",
		Slug:         "slug1",
		SourceType:   "live",
		SourceID:     "src1",
		Title:        "My Title",
		StartedAt:    "2025-01-01T10:00:00Z",
		EndedAt:      "2025-01-01T11:00:00Z",
		SourceURL:    "https://example.com",
		RawAudioPath: "raw/audio.m4a",
		ASRAudioPath: "asr/audio.asr.mp3",
		DanmakuCount: 100,
		GeneratedAt:  "2025-01-01T12:00:00Z",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Metadata
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != original {
		t.Fatalf("round-trip mismatch:\n  original: %+v\n  got:      %+v", original, got)
	}
}

func TestMetadataOmitEmpty(t *testing.T) {
	m := Metadata{
		SessionID:    "s1",
		ChannelID:    "c1",
		SourceType:   "import",
		RawAudioPath: "raw/a.mp3",
		ASRAudioPath: "asr/a.mp3",
		GeneratedAt:  "now",
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	// StartedAt and EndedAt have omitempty
	if strings.Contains(s, `"started_at"`) {
		t.Errorf("expected started_at to be omitted, got: %s", s)
	}
	if strings.Contains(s, `"ended_at"`) {
		t.Errorf("expected ended_at to be omitted, got: %s", s)
	}
}

// ---------------------------------------------------------------------------
// HandleTask integration tests (in-memory SQLite + mock converter)
// ---------------------------------------------------------------------------

// mockConverter implements AudioConverter for testing.
type mockConverter struct {
	convertFn func(ctx context.Context, inputPath string, outputPath string) error
}

func (m *mockConverter) Convert(ctx context.Context, inputPath string, outputPath string) error {
	return m.convertFn(ctx, inputPath, outputPath)
}

// mockReporter implements worker.Reporter for testing.
type mockReporter struct {
	progressCalls []struct {
		percent int
		message string
	}
	err error
}

func (r *mockReporter) Progress(ctx context.Context, percent int, message string) error {
	r.progressCalls = append(r.progressCalls, struct {
		percent int
		message string
	}{percent, message})
	return r.err
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func setupChannelAndSession(t *testing.T) (*sql.DB, *session.Store, *state.Store, session.Session) {
	t.Helper()
	database := setupTestDB(t)

	chStore := channel.NewStore(database)
	sessStore := session.NewStore(database)
	stateStore := state.NewStore(database)

	// Create channel first (required by FK constraint)
	_, err := chStore.Create(context.Background(), channel.UpsertInput{
		ID:         "ch-test",
		Name:       "Test Channel",
		UID:        12345,
		LiveRoomID: 67890,
	})
	if err != nil {
		database.Close()
		t.Fatalf("create channel: %v", err)
	}

	sess, err := sessStore.CreateLive(context.Background(), session.CreateLiveInput{
		ChannelID: "ch-test",
		RoomID:    67890,
		Title:     "Test Stream",
	})
	if err != nil {
		database.Close()
		t.Fatalf("create session: %v", err)
	}

	// Move session to "recording" so normalize_succeeded transition is valid
	if _, err := stateStore.Apply(context.Background(), sess.ID, state.EventLiveRecordStarted, "task-0", ""); err != nil {
		database.Close()
		t.Fatalf("state transition: %v", err)
	}

	return database, sessStore, stateStore, sess
}

func TestHandleTask(t *testing.T) {
	database, sessStore, stateStore, sess := setupChannelAndSession(t)
	defer database.Close()

	outputRoot := t.TempDir()
	cfg := &config.Config{OutputRoot: outputRoot}

	// Set up raw directory with audio file
	rawDir := filepath.Join(outputRoot, "ch-test", sess.Slug, "raw")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		t.Fatalf("mkdir raw: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rawDir, "audio.m4a"), []byte("fake audio"), 0644); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	// Write danmaku.jsonl
	if err := os.WriteFile(filepath.Join(rawDir, "danmaku.jsonl"), []byte(
		`{"time_ms":1000,"text":"hello","source":"live"}`+"\n"), 0644); err != nil {
		t.Fatalf("write danmaku: %v", err)
	}

	convertCalled := false
	converter := &mockConverter{
		convertFn: func(ctx context.Context, inputPath string, outputPath string) error {
			convertCalled = true
			// Write a fake output file
			return os.WriteFile(outputPath, []byte("converted"), 0644)
		},
	}

	reporter := &mockReporter{}
	h := NewHandler(cfg, sessStore, stateStore, converter)

	task := worker.Task{
		ID:        "task-1",
		ChannelID: "ch-test",
		SessionID: sess.ID,
		Type:      TaskType,
	}

	if err := h.HandleTask(context.Background(), task, reporter); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	if !convertCalled {
		t.Error("expected converter.Convert to be called")
	}

	// Verify progress was called
	if len(reporter.progressCalls) == 0 {
		t.Fatal("expected at least one Progress call")
	}

	// Verify output files exist
	packageDir := filepath.Join(outputRoot, "ch-test", sess.Slug, "package")
	asrDir := filepath.Join(outputRoot, "ch-test", sess.Slug, "asr")
	if _, err := os.Stat(filepath.Join(asrDir, "audio.asr.mp3")); err != nil {
		t.Errorf("expected asr audio file to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(packageDir, "danmaku.json")); err != nil {
		t.Errorf("expected danmaku.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(packageDir, "metadata.json")); err != nil {
		t.Errorf("expected package metadata.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "ch-test", sess.Slug, "metadata.json")); err != nil {
		t.Errorf("expected session metadata.json to exist: %v", err)
	}
}

func TestHandleTaskSessionNotFound(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sessStore := session.NewStore(database)
	stateStore := state.NewStore(database)
	cfg := &config.Config{OutputRoot: t.TempDir()}
	converter := &mockConverter{
		convertFn: func(ctx context.Context, inputPath string, outputPath string) error {
			return nil
		},
	}

	h := NewHandler(cfg, sessStore, stateStore, converter)
	reporter := &mockReporter{}

	task := worker.Task{
		ID:        "task-1",
		ChannelID: "ch-test",
		SessionID: "nonexistent",
		Type:      TaskType,
	}

	err := h.HandleTask(context.Background(), task, reporter)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestHandleTaskConvertError(t *testing.T) {
	database, sessStore, stateStore, sess := setupChannelAndSession(t)
	defer database.Close()

	outputRoot := t.TempDir()
	cfg := &config.Config{OutputRoot: outputRoot}

	rawDir := filepath.Join(outputRoot, "ch-test", sess.Slug, "raw")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rawDir, "audio.m4a"), []byte("fake"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	converter := &mockConverter{
		convertFn: func(ctx context.Context, inputPath string, outputPath string) error {
			return fmt.Errorf("convert failed")
		},
	}

	h := NewHandler(cfg, sessStore, stateStore, converter)
	reporter := &mockReporter{}

	task := worker.Task{
		ID:        "task-1",
		ChannelID: "ch-test",
		SessionID: sess.ID,
		Type:      TaskType,
	}

	if err := h.HandleTask(context.Background(), task, reporter); err == nil {
		t.Fatal("expected error from convert failure")
	}
}

func TestHandleTaskRawAudioNotFound(t *testing.T) {
	database, sessStore, stateStore, sess := setupChannelAndSession(t)
	defer database.Close()

	outputRoot := t.TempDir()
	cfg := &config.Config{OutputRoot: outputRoot}

	// Create raw dir but no audio file
	rawDir := filepath.Join(outputRoot, "ch-test", sess.Slug, "raw")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	converter := &mockConverter{
		convertFn: func(ctx context.Context, inputPath string, outputPath string) error {
			return nil
		},
	}

	h := NewHandler(cfg, sessStore, stateStore, converter)
	reporter := &mockReporter{}

	task := worker.Task{
		ID:        "task-1",
		ChannelID: "ch-test",
		SessionID: sess.ID,
		Type:      TaskType,
	}

	if err := h.HandleTask(context.Background(), task, reporter); err == nil {
		t.Fatal("expected error for missing raw audio")
	}
}

func TestRegister(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sessStore := session.NewStore(database)
	stateStore := state.NewStore(database)
	converter := &mockConverter{
		convertFn: func(ctx context.Context, inputPath string, outputPath string) error {
			return nil
		},
	}

	h := NewHandler(nil, sessStore, stateStore, converter)

	pool := worker.NewPool(nil, nil, 1, nil)
	h.Register(pool)
	// No panic means Register succeeded
}

func TestConvertAtomic(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.mp3")
	outputPath := filepath.Join(dir, "output.mp3")

	if err := os.WriteFile(inputPath, []byte("input"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	converter := &mockConverter{
		convertFn: func(ctx context.Context, in string, out string) error {
			return os.WriteFile(out, []byte("output"), 0644)
		},
	}

	h := NewHandler(nil, nil, nil, converter)
	err := h.convertAtomic(context.Background(), inputPath, outputPath)
	if err != nil {
		t.Fatalf("convertAtomic: %v", err)
	}

	// Verify output exists
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "output" {
		t.Errorf("expected 'output', got %q", string(data))
	}

	// Verify .tmp file does not exist
	tmpPath := outputPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected .tmp to be cleaned up")
	}
}

func TestConvertAtomicError(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.mp3")
	outputPath := filepath.Join(dir, "output.mp3")

	if err := os.WriteFile(inputPath, []byte("input"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	converter := &mockConverter{
		convertFn: func(ctx context.Context, in string, out string) error {
			// Write partial output then fail
			os.WriteFile(out, []byte("partial"), 0644)
			return fmt.Errorf("conversion error")
		},
	}

	h := NewHandler(nil, nil, nil, converter)
	err := h.convertAtomic(context.Background(), inputPath, outputPath)
	if err == nil {
		t.Fatal("expected error from convertAtomic")
	}
	if !strings.Contains(err.Error(), "conversion error") {
		t.Errorf("expected 'conversion error', got: %v", err)
	}

	// Verify output does not exist (tmp cleaned up)
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Errorf("expected output to not exist after error")
	}
}
