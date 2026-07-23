package glossary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	return database
}

// mustBeLocalRFC3339 断言 got 是本地时区 RFC3339 字符串(非 UTC "Z" 后缀)。
// 回归:见 2026-07-04 DB 时间字段统一本地时区修复。
func mustBeLocalRFC3339(t *testing.T, got string) {
	t.Helper()
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Fatalf("%q not RFC3339: %v", got, err)
	}
	if strings.HasSuffix(got, "Z") {
		t.Fatalf("%q ends with Z (UTC), expected local timezone offset", got)
	}
	localOffset := time.Now().In(time.Local).Format("-07:00")
	if !strings.HasSuffix(got, localOffset) {
		t.Fatalf("%q must end with local offset %q", got, localOffset)
	}
}

// ---------------------------------------------------------------------------
// Store CRUD
// ---------------------------------------------------------------------------

func TestUpsertAndListGlobal(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	if err := store.Upsert(ctx, "", "AI", "人工智能", "技术"); err != nil {
		t.Fatal(err)
	}

	entries, err := store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Term != "AI" {
		t.Fatalf("expected term AI, got %q", entries[0].Term)
	}
	if entries[0].Canonical != "人工智能" {
		t.Fatalf("expected canonical 人工智能, got %q", entries[0].Canonical)
	}
	if entries[0].Category != "技术" {
		t.Fatalf("expected category 技术, got %q", entries[0].Category)
	}
	if !entries[0].Enabled {
		t.Fatal("expected enabled=true")
	}
}

func TestUpsertIdempotent(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	if err := store.Upsert(ctx, "ch1", "foo", "bar", "cat1"); err != nil {
		t.Fatal(err)
	}
	// Same (channel_id, term) should update canonical and category.
	if err := store.Upsert(ctx, "ch1", "foo", "baz", "cat2"); err != nil {
		t.Fatal(err)
	}

	entries, err := store.queryEntries(ctx, sqlListByChannel, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Canonical != "baz" {
		t.Fatalf("expected canonical baz, got %q", entries[0].Canonical)
	}
	if entries[0].Category != "cat2" {
		t.Fatalf("expected category cat2, got %q", entries[0].Category)
	}
}

func TestDelete(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	if err := store.Upsert(ctx, "", "AI", "人工智能", ""); err != nil {
		t.Fatal(err)
	}

	entries, _ := store.ListGlobal(ctx)
	id := entries[0].ID

	if err := store.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}

	entries, err := store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after delete, got %d", len(entries))
	}
}

func TestDeleteNotFound(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	err := store.Delete(ctx, 99999)
	if err == nil {
		t.Fatal("expected error for deleting non-existent entry")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestToggle(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	if err := store.Upsert(ctx, "", "AI", "人工智能", ""); err != nil {
		t.Fatal(err)
	}

	entries, _ := store.ListGlobal(ctx)
	id := entries[0].ID

	if err := store.Toggle(ctx, id, false); err != nil {
		t.Fatal(err)
	}

	entries, err := store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Enabled {
		t.Fatal("expected enabled=false after toggle")
	}
}

// ---------------------------------------------------------------------------
// Merge logic
// ---------------------------------------------------------------------------

func TestListByChannelMerge(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// Global entry
	store.Upsert(ctx, "", "AI", "人工智能", "技术")
	// Channel entry with a different term
	store.Upsert(ctx, "ch1", "LLM", "大语言模型", "技术")

	merged, err := store.ListByChannel(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(merged))
	}

	// Find each entry by term.
	byTerm := make(map[string]MergedEntry)
	for _, m := range merged {
		byTerm[m.Term] = m
	}

	g, ok := byTerm["AI"]
	if !ok {
		t.Fatal("missing global entry AI")
	}
	if g.Source != "global" {
		t.Fatalf("expected source global, got %q", g.Source)
	}

	c, ok := byTerm["LLM"]
	if !ok {
		t.Fatal("missing channel entry LLM")
	}
	if c.Source != "channel" {
		t.Fatalf("expected source channel, got %q", c.Source)
	}
}

func TestListByChannelOverride(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// Global entry
	store.Upsert(ctx, "", "foo", "global-foo", "cat")
	// Channel entry overrides with different canonical
	store.Upsert(ctx, "ch1", "foo", "bar", "cat")

	merged, err := store.ListByChannel(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(merged))
	}
	if merged[0].Canonical != "bar" {
		t.Fatalf("expected canonical bar, got %q", merged[0].Canonical)
	}
	if merged[0].Source != "channel" {
		t.Fatalf("expected source channel, got %q", merged[0].Source)
	}
}

func TestListByChannelBlock(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// Global entry enabled=true
	store.Upsert(ctx, "", "foo", "global-foo", "cat")
	// Channel entry disabled blocks the global one
	store.Upsert(ctx, "ch1", "foo", "channel-foo", "cat")
	entries, _ := store.queryEntries(ctx, sqlListByChannel, "ch1")
	store.Toggle(ctx, entries[0].ID, false)

	merged, err := store.ListByChannel(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 0 {
		t.Fatalf("expected 0 entries (global blocked, channel disabled), got %d", len(merged))
	}
}

// ---------------------------------------------------------------------------
// ExportForPrompt
// ---------------------------------------------------------------------------

func TestExportForPromptEmpty(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	out, err := store.ExportForPrompt(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("expected empty string, got %q", out)
	}
}

func TestExportForPromptWithEntries(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	store.Upsert(ctx, "", "AI", "人工智能", "技术")
	store.Upsert(ctx, "ch1", "LLM", "大语言模型", "AI")

	out, err := store.ExportForPrompt(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "AI") {
		t.Fatal("output should contain term AI")
	}
	if !strings.Contains(out, "人工智能") {
		t.Fatal("output should contain canonical 人工智能")
	}
	if !strings.Contains(out, "LLM") {
		t.Fatal("output should contain term LLM")
	}
	if !strings.Contains(out, "大语言模型") {
		t.Fatal("output should contain canonical 大语言模型")
	}
	if !strings.Contains(out, "|") {
		t.Fatal("output should contain Markdown table pipe characters")
	}
}

func TestExportForPromptSkipsDisabled(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	store.Upsert(ctx, "", "AI", "人工智能", "技术")
	store.Upsert(ctx, "ch1", "BLOCK", "blocked", "test")
	entries, _ := store.queryEntries(ctx, sqlListByChannel, "ch1")
	store.Toggle(ctx, entries[0].ID, false)

	out, err := store.ExportForPrompt(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "BLOCK") {
		t.Fatal("disabled entry should not appear in export")
	}
	// The global AI should still appear.
	if !strings.Contains(out, "AI") {
		t.Fatal("enabled global entry should appear in export")
	}
}

func TestExportForPromptWithNote(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	store.Upsert(ctx, "", "AI", "人工智能", "技术")
	store.SetNote(ctx, "ch1", "注意：这是一个测试备注。")

	out, err := store.ExportForPrompt(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "注意：这是一个测试备注。") {
		t.Fatalf("output should contain the note, got: %q", out)
	}
}

// ---------------------------------------------------------------------------
// Note
// ---------------------------------------------------------------------------

func TestGetNoteEmpty(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	note, err := store.GetNote(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Fatalf("expected empty note, got %q", note)
	}
}

func TestSetAndGetNote(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	if err := store.SetNote(ctx, "ch1", "这是备注内容"); err != nil {
		t.Fatal(err)
	}

	note, err := store.GetNote(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if note != "这是备注内容" {
		t.Fatalf("expected 这是备注内容, got %q", note)
	}
}

// ---------------------------------------------------------------------------
// CountGlobal
// ---------------------------------------------------------------------------

func TestCountGlobal(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// Add 3 global entries and 1 channel entry.
	for _, term := range []string{"AI", "LLM", "AGI"} {
		if err := store.Upsert(ctx, "", term, "canonical-"+term, ""); err != nil {
			t.Fatal(err)
		}
	}
	store.Upsert(ctx, "ch1", "foo", "bar", "")

	count, err := store.CountGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 global entries, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// ImportMarkdown
// ---------------------------------------------------------------------------

func TestImportMarkdownBasic(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	content := `| 误识别 | 正确 | 备注 |
|---|---|---|
| AI | 人工智能 | 技术 |
| LLM | 大语言模型 | AI |`

	count, err := store.ImportMarkdown(ctx, "", content)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 entries, got %d", count)
	}

	entries, err := store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 global entries, got %d", len(entries))
	}
}

func TestImportMarkdownMultiVariant(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	content := `| 误识别 | 正确 |
|---|---|
| AGI/ASI | 通用人工智能 |`

	count, err := store.ImportMarkdown(ctx, "", content)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 entries (AGI + ASI), got %d", count)
	}

	entries, err := store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byTerm := make(map[string]string)
	for _, e := range entries {
		byTerm[e.Term] = e.Canonical
	}
	if byTerm["AGI"] != "通用人工智能" {
		t.Fatalf("expected AGI -> 通用人工智能, got %q", byTerm["AGI"])
	}
	if byTerm["ASI"] != "通用人工智能" {
		t.Fatalf("expected ASI -> 通用人工智能, got %q", byTerm["ASI"])
	}
}

func TestImportMarkdownCategory(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	content := `# 技术术语

| 误识别 | 正确 |
|---|---|
| AI | 人工智能 |`

	count, err := store.ImportMarkdown(ctx, "", content)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 entry, got %d", count)
	}

	entries, err := store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Category != "技术术语" {
		t.Fatalf("expected category '技术术语', got %q", entries[0].Category)
	}
}

func TestImportMarkdownSkipHeaders(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	content := `| ASR误识别 | 正确写法 | 分类 |
|---|---|---|
| AI | 人工智能 | 技术 |`

	count, err := store.ImportMarkdown(ctx, "", content)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 entry (header skipped), got %d", count)
	}
}

func TestImportMarkdownSkipIdentical(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	content := `| 误识别 | 正确 |
|---|---|
| AI | AI |`

	count, err := store.ImportMarkdown(ctx, "", content)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 entries (identical skipped), got %d", count)
	}
}

func TestImportMarkdownEmpty(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	count, err := store.ImportMarkdown(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 entries, got %d", count)
	}

	count, err = store.ImportMarkdown(ctx, "", "   \n\n  ")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 entries for whitespace, got %d", count)
	}
}

func TestImportMarkdownOnlyHeadings(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	content := `# 技术术语

## AI

### 子分类`

	count, err := store.ImportMarkdown(ctx, "", content)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 entries for headings only, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// ExportJSON / ImportJSON
// ---------------------------------------------------------------------------

func TestExportImportJSON(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// Setup: add entries and a note
	store.Upsert(ctx, "", "AI", "人工智能", "技术")
	store.Upsert(ctx, "", "LLM", "大语言模型", "AI")
	store.SetNote(ctx, "", "这是一个备注")

	// Export
	data, err := store.ExportJSON(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	// Verify JSON structure
	var export GlossaryExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatal(err)
	}
	if export.ChannelID != "" {
		t.Fatalf("expected empty channel_id, got %q", export.ChannelID)
	}
	if len(export.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(export.Entries))
	}
	if export.Note != "这是一个备注" {
		t.Fatalf("expected note '这是一个备注', got %q", export.Note)
	}
	if export.ExportedAt == "" {
		t.Fatal("expected non-empty exported_at")
	}

	// Import into a different store (simulating round-trip)
	database2 := openTestDB(t)
	store2 := NewStore(database2)

	count, err := store2.ImportJSON(ctx, "", data)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 imported entries, got %d", count)
	}

	// Verify imported entries
	entries, err := store2.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 global entries after import, got %d", len(entries))
	}

	// Verify note was imported
	note, err := store2.GetNote(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if note != "这是一个备注" {
		t.Fatalf("expected note '这是一个备注', got %q", note)
	}
}

func TestImportJSONInvalid(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	_, err := store.ImportJSON(ctx, "", []byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestImportJSONMissingFields(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	jsonData := `{
		"channel_id": "",
		"entries": [
			{"term": "AI", "canonical": "人工智能", "category": "技术"},
			{"term": "", "canonical": "空term"},
			{"term": "LLM", "canonical": "", "category": "AI"},
			{"canonical": "无term字段"}
		],
		"note": "测试备注"
	}`

	count, err := store.ImportJSON(ctx, "", []byte(jsonData))
	if err != nil {
		t.Fatal(err)
	}
	// Only the first entry has both term and canonical
	if count != 1 {
		t.Fatalf("expected 1 valid entry, got %d", count)
	}

	// Verify note was set
	note, err := store.GetNote(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if note != "测试备注" {
		t.Fatalf("expected note '测试备注', got %q", note)
	}
}

// TestImportJSONArrayInput 校验裸数组 body(前端 importGlobalJSON 的典型形态)能被正确导入。
// 回归 bug 报告 #1:前端 JSON.parse([{...}]) 直接 POST,后端原只接受 GlossaryExport 对象 → 导入永久失败。
func TestImportJSONArrayInput(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// 前端实际发送的裸数组形态
	arrayBody := []byte(`[
		{"term":"AI","canonical":"人工智能","category":"技术"},
		{"term":"LLM","canonical":"大语言模型","category":"AI"},
		{"term":"","canonical":"空term应被跳过"}
	]`)

	count, err := store.ImportJSON(ctx, "", arrayBody)
	if err != nil {
		t.Fatalf("array body should import without error, got: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 valid entries imported, got %d", count)
	}

	entries, err := store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 stored entries, got %d", len(entries))
	}
}

// TestImportJSONSingleObjectNoEntries 记录现有契约:单个 GlossaryItem 对象
// {"term":...} 会作为未知字段被 GlossaryExport 静默忽略(无 ErrInvalidJSON,但 count=0)。
// 这是 json.Unmarshal 对未知字段的默认行为;不在本修复范围扩展(前端不发此形态)。
func TestImportJSONSingleObjectNoEntries(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	single := []byte(`{"term":"AI","canonical":"人工智能","category":"技术"}`)
	count, err := store.ImportJSON(ctx, "", single)
	if err != nil {
		t.Fatalf("single object should not error (ignored as unknown fields), got: %v", err)
	}
	if count != 0 {
		t.Fatalf("single object imports 0 entries (ignored by GlossaryExport), got %d", count)
	}
}

// TestImportJSONInvalidReturnsSentinel 校验非法 JSON 返回 ErrInvalidJSON 哨兵(handler 据此映射 400)。
func TestImportJSONInvalidReturnsSentinel(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	_, err := store.ImportJSON(ctx, "", []byte("not json at all"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("expected ErrInvalidJSON sentinel, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// DeleteByIDs
// ---------------------------------------------------------------------------

func TestDeleteByIDs(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	_ = s.Upsert(ctx, "", "term1", "canon1", "")
	_ = s.Upsert(ctx, "", "term2", "canon2", "")
	_ = s.Upsert(ctx, "", "term3", "canon3", "")

	entries, _ := s.ListGlobal(ctx)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	ids := []int64{entries[0].ID, entries[1].ID}
	deleted, err := s.DeleteByIDs(ctx, "", ids)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", deleted)
	}

	remaining, _ := s.ListGlobal(ctx)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
}

func TestDeleteByIDsEmpty(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	deleted, err := s.DeleteByIDs(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted, got %d", deleted)
	}
}

func TestDeleteByIDsChannelScope(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	_ = s.Upsert(ctx, "", "global_term", "canon1", "")
	_ = s.Upsert(ctx, "ch1", "channel_term", "canon2", "")

	chEntries, _ := s.queryEntries(ctx, sqlListByChannel, "ch1")
	if len(chEntries) != 1 {
		t.Fatalf("expected 1 channel entry, got %d", len(chEntries))
	}

	// 尝试用 channelID="" 删除频道词条 → 不应删除
	deleted, err := s.DeleteByIDs(ctx, "", []int64{chEntries[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted (wrong scope), got %d", deleted)
	}

	// 验证频道词条仍然存在
	chEntries2, _ := s.queryEntries(ctx, sqlListByChannel, "ch1")
	if len(chEntries2) != 1 {
		t.Fatalf("expected 1 channel entry after wrong-scope delete, got %d", len(chEntries2))
	}
}

// ---------------------------------------------------------------------------
// ToggleByIDs
// ---------------------------------------------------------------------------

func TestToggleByIDs(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	_ = s.Upsert(ctx, "", "term1", "canon1", "")
	_ = s.Upsert(ctx, "", "term2", "canon2", "")

	entries, _ := s.ListGlobal(ctx)
	ids := []int64{entries[0].ID, entries[1].ID}

	// Toggle off
	updated, err := s.ToggleByIDs(ctx, "", ids, false)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 2 {
		t.Fatalf("expected 2 updated, got %d", updated)
	}

	entries2, _ := s.ListGlobal(ctx)
	for _, e := range entries2 {
		if e.Enabled {
			t.Fatalf("expected entry %q to be disabled", e.Term)
		}
	}

	// Toggle on
	updated2, err := s.ToggleByIDs(ctx, "", ids, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated2 != 2 {
		t.Fatalf("expected 2 updated, got %d", updated2)
	}
}

func TestToggleByIDsEmpty(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	updated, err := s.ToggleByIDs(ctx, "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 0 {
		t.Fatalf("expected 0 updated, got %d", updated)
	}
}

func TestToggleByIDsChannelScope(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	_ = s.Upsert(ctx, "", "global_term", "canon1", "")
	_ = s.Upsert(ctx, "ch1", "channel_term", "canon2", "")

	chEntries, _ := s.queryEntries(ctx, sqlListByChannel, "ch1")
	ids := []int64{chEntries[0].ID}

	// 用 channelID="" 切换频道词条 → 不应更新
	updated, err := s.ToggleByIDs(ctx, "", ids, false)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 0 {
		t.Fatalf("expected 0 updated (wrong scope), got %d", updated)
	}

	// 验证频道词条仍启用
	chEntries2, _ := s.queryEntries(ctx, sqlListByChannel, "ch1")
	if !chEntries2[0].Enabled {
		t.Fatal("expected channel entry to remain enabled after wrong-scope toggle")
	}
}

// ---------------------------------------------------------------------------
// ExportForASRVocabulary
// ---------------------------------------------------------------------------

func TestExportForASRVocabularyEmpty(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	vocab, err := store.ExportForASRVocabulary(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if vocab != nil {
		t.Fatalf("expected nil vocabulary when no entries exist, got %v", vocab)
	}
}

func TestExportForASRVocabularyWithEntries(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	store.Upsert(ctx, "", "AI", "人工智能", "技术")
	store.Upsert(ctx, "", "LLM", "大语言模型", "AI")

	vocab, err := store.ExportForASRVocabulary(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(vocab) != 2 {
		t.Fatalf("expected 2 hotwords, got %d (%v)", len(vocab), vocab)
	}
	// 权重固定为 4（Fun-ASR vocabulary 期望 map[string]int）
	if vocab["人工智能"] != 4 {
		t.Fatalf("expected weight 4 for 人工智能, got %d", vocab["人工智能"])
	}
	if vocab["大语言模型"] != 4 {
		t.Fatalf("expected weight 4 for 大语言模型, got %d", vocab["大语言模型"])
	}
}

func TestExportForASRVocabularySkipsDisabled(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	store.Upsert(ctx, "", "AI", "人工智能", "技术")
	store.Upsert(ctx, "", "BLOCK", "blocked", "test")
	// 禁用 BLOCK 条目
	entries, _ := store.ListGlobal(ctx)
	for _, e := range entries {
		if e.Term == "BLOCK" {
			if err := store.Toggle(ctx, e.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}

	vocab, err := store.ExportForASRVocabulary(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vocab["blocked"]; ok {
		t.Fatal("disabled entry should not appear in vocabulary")
	}
	if _, ok := vocab["人工智能"]; !ok {
		t.Fatal("enabled global entry should appear in vocabulary")
	}
}

func TestExportForASRVocabularyFallsBackToTerm(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// canonical 为空时，应回退使用 term 作为热词
	store.Upsert(ctx, "", "主播小名", "", "昵称")

	vocab, err := store.ExportForASRVocabulary(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(vocab) != 1 {
		t.Fatalf("expected 1 hotword, got %d (%v)", len(vocab), vocab)
	}
	if _, ok := vocab["主播小名"]; !ok {
		t.Fatalf("expected term '主播小名' as hotword when canonical is empty, got %v", vocab)
	}
}

func TestExportForASRVocabularyChannelOverride(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// 全局条目
	store.Upsert(ctx, "", "foo", "global-foo", "cat")
	// 主播条目覆盖全局（不同 canonical）
	store.Upsert(ctx, "ch1", "foo", "channel-foo", "cat")

	vocab, err := store.ExportForASRVocabulary(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(vocab) != 1 {
		t.Fatalf("expected 1 hotword, got %d (%v)", len(vocab), vocab)
	}
	if _, ok := vocab["channel-foo"]; !ok {
		t.Fatalf("expected channel canonical 'channel-foo' (override), got %v", vocab)
	}
	if _, ok := vocab["global-foo"]; ok {
		t.Fatal("global canonical should be overridden by channel entry")
	}
}

func TestExportForASRVocabularyChannelBlock(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// 全局条目
	store.Upsert(ctx, "", "foo", "global-foo", "cat")
	// 主播条目禁用以屏蔽全局
	store.Upsert(ctx, "ch1", "foo", "channel-foo", "cat")
	entries, _ := store.queryEntries(ctx, sqlListByChannel, "ch1")
	if err := store.Toggle(ctx, entries[0].ID, false); err != nil {
		t.Fatal(err)
	}

	vocab, err := store.ExportForASRVocabulary(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(vocab) != 0 {
		t.Fatalf("expected 0 hotwords (global blocked, channel disabled), got %d (%v)", len(vocab), vocab)
	}
}

// ExportForASRVocabulary 对 DashScope 热词表上限（MaxASRHotwords=500）只告警不截断：
// 超出阈值时打 warn 日志，但返回完整的 map，让云端决定拒收还是接收。
func TestExportForASRVocabularyExceedsLimitWarnsNoTruncate(t *testing.T) {
	if MaxASRHotwords != 500 {
		t.Fatalf("MaxASRHotwords should match DashScope Paraformer-v1 limit (500), got %d", MaxASRHotwords)
	}

	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	// 插入 MaxASRHotwords+50 条全局术语，全部启用、canonical 各不相同。
	// 用 "词000" ~ "词549" 保证 canonical 互不冲突且可预测。
	total := MaxASRHotwords + 50
	for i := 0; i < total; i++ {
		term := fmt.Sprintf("t%03d", i)
		canonical := fmt.Sprintf("词%03d", i)
		if err := store.Upsert(ctx, "", term, canonical, "test"); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
	}

	vocab, err := store.ExportForASRVocabulary(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	// 不截断：返回的条目数必须等于插入总数，超过阈值的部分不丢弃。
	if len(vocab) != total {
		t.Fatalf("expected %d hotwords (no truncation), got %d", total, len(vocab))
	}
	// 抽查首尾条目都在，确认未发生前/后截断。
	if _, ok := vocab["词000"]; !ok {
		t.Error("first entry '词000' missing — possible truncation")
	}
	if _, ok := vocab[fmt.Sprintf("词%03d", total-1)]; !ok {
		t.Error("last entry missing — possible truncation")
	}
}

// 回归:见 2026-07-04 DB 时间字段统一本地时区修复。
// Upsert / Toggle / SetNote / ToggleByIDs 写入的 created_at/updated_at 必须是
// 本地时区 RFC3339,而不是 SQLite datetime('now') 的 UTC。
func TestStoreWritesLocalTimezoneTimestamps(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	if err := store.Upsert(ctx, "ch1", "term1", "canonical1", "cat"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var id int64
	var createdAt, updatedAt string
	if err := db.QueryRowContext(ctx,
		"SELECT id, created_at, updated_at FROM glossary_entries WHERE channel_id=? AND term=?",
		"ch1", "term1",
	).Scan(&id, &createdAt, &updatedAt); err != nil {
		t.Fatalf("query: %v", err)
	}
	mustBeLocalRFC3339(t, createdAt)
	mustBeLocalRFC3339(t, updatedAt)

	// Toggle 触发 updated_at 重写。
	if err := store.Toggle(ctx, id, false); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT updated_at FROM glossary_entries WHERE id=?", id).Scan(&updatedAt); err != nil {
		t.Fatalf("query after toggle: %v", err)
	}
	mustBeLocalRFC3339(t, updatedAt)

	// SetNote 写 glossary_meta.updated_at。
	if err := store.SetNote(ctx, "ch1", "a note"); err != nil {
		t.Fatalf("setnote: %v", err)
	}
	var metaUpdatedAt string
	if err := db.QueryRowContext(ctx, "SELECT updated_at FROM glossary_meta WHERE channel_id=?", "ch1").Scan(&metaUpdatedAt); err != nil {
		t.Fatalf("query meta: %v", err)
	}
	mustBeLocalRFC3339(t, metaUpdatedAt)

	// ToggleByIDs (batch update) 触发 updated_at 重写。
	if _, err := store.ToggleByIDs(ctx, "ch1", []int64{id}, true); err != nil {
		t.Fatalf("togglebyids: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT updated_at FROM glossary_entries WHERE id=?", id).Scan(&updatedAt); err != nil {
		t.Fatalf("query after batch: %v", err)
	}
	mustBeLocalRFC3339(t, updatedAt)
}
