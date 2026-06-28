package recap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"hikami-go/internal/db"
)

// --- helpers ---

func openTemplateTestDB(t *testing.T) *sql.DB {
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

// ---------------------------------------------------------------------------
// TemplateStore: GetGlobal
// ---------------------------------------------------------------------------

func TestGetGlobal_Success(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// The built-in default template is inserted by migration v21
	tmpl, err := store.GetGlobal(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.ChannelID != "" {
		t.Fatalf("expected channel_id='', got %q", tmpl.ChannelID)
	}
	if tmpl.Name != "default" {
		t.Fatalf("expected name='default', got %q", tmpl.Name)
	}
	if tmpl.SystemPrompt != "__builtin__" {
		t.Fatalf("expected system_prompt='__builtin__', got %q", tmpl.SystemPrompt)
	}
	if !tmpl.Enabled {
		t.Fatal("expected enabled=true")
	}
	if !tmpl.IsDefault {
		t.Fatal("expected is_default=true")
	}
}

func TestGetGlobal_NotFound(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	_, err := store.GetGlobal(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent template")
	}
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TemplateStore: GetByChannel
// ---------------------------------------------------------------------------

func TestGetByChannel_Success(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	err := store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "custom",
		SystemPrompt: "custom prompt",
		UserFormat:   "custom format",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tmpl, err := store.GetByChannel(ctx, "ch1", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.ChannelID != "ch1" {
		t.Fatalf("expected channel_id='ch1', got %q", tmpl.ChannelID)
	}
	if tmpl.SystemPrompt != "custom prompt" {
		t.Fatalf("expected system_prompt='custom prompt', got %q", tmpl.SystemPrompt)
	}
	if !tmpl.Enabled {
		t.Fatal("expected enabled=true")
	}
}

func TestGetByChannel_NotFound(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	_, err := store.GetByChannel(ctx, "ch1", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent channel template")
	}
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TemplateStore: Resolve
// ---------------------------------------------------------------------------

func TestResolve_NoTemplates_UsesBuiltInDefaults(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Use a name that has no global or channel template
	resolved, err := store.Resolve(ctx, "ch1", "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SystemPrompt != defaultSystemPrompt {
		t.Fatal("expected built-in system prompt when no templates exist")
	}
	if resolved.UserFormat != defaultUserFormat {
		t.Fatal("expected built-in user format when no templates exist")
	}
	if resolved.FanName != "" {
		t.Fatalf("expected empty fan_name, got %q", resolved.FanName)
	}
}

func TestResolve_GlobalOverridesBuiltIn(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// The migration inserts a default global template with __builtin__ markers.
	// Override it with custom values.
	err := store.Upsert(ctx, &Template{
		ChannelID:    "",
		Name:         "default",
		SystemPrompt: "global custom prompt",
		UserFormat:   "global custom format",
		FanName:      "global-fan",
		ExtraVars:    `{"custom_key":"global_val"}`,
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := store.Resolve(ctx, "ch1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SystemPrompt != "global custom prompt" {
		t.Fatalf("expected global custom prompt, got %q", resolved.SystemPrompt)
	}
	if resolved.UserFormat != "global custom format" {
		t.Fatalf("expected global custom format, got %q", resolved.UserFormat)
	}
	if resolved.FanName != "global-fan" {
		t.Fatalf("expected 'global-fan', got %q", resolved.FanName)
	}
	if resolved.ExtraVars["custom_key"] != "global_val" {
		t.Fatalf("expected extra_vars[custom_key]='global_val', got %q", resolved.ExtraVars["custom_key"])
	}
}

func TestResolve_ChannelOverridesGlobal_SystemPrompt(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Set up global template
	err := store.Upsert(ctx, &Template{
		ChannelID:    "",
		Name:         "default",
		SystemPrompt: "global prompt",
		UserFormat:   "global format",
		FanName:      "global-fan",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set up channel template that overrides system_prompt
	err = store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "default",
		SystemPrompt: "channel specific prompt",
		UserFormat:   "channel format",
		FanName:      "channel-fan",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := store.Resolve(ctx, "ch1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SystemPrompt != "channel specific prompt" {
		t.Fatalf("expected channel system prompt, got %q", resolved.SystemPrompt)
	}
	if resolved.UserFormat != "channel format" {
		t.Fatalf("expected channel user format, got %q", resolved.UserFormat)
	}
	if resolved.FanName != "channel-fan" {
		t.Fatalf("expected 'channel-fan', got %q", resolved.FanName)
	}
}

func TestResolve_ChannelPartialOverride_UserFormatEmpty(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Set up global template
	err := store.Upsert(ctx, &Template{
		ChannelID:    "",
		Name:         "default",
		SystemPrompt: "global prompt",
		UserFormat:   "global format",
		FanName:      "global-fan",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Channel template overrides only system_prompt; user_format is empty -> should fall back to global
	err = store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "default",
		SystemPrompt: "channel specific prompt",
		UserFormat:   "", // empty -> follow global
		FanName:      "", // empty -> follow global
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := store.Resolve(ctx, "ch1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SystemPrompt != "channel specific prompt" {
		t.Fatalf("expected channel system prompt, got %q", resolved.SystemPrompt)
	}
	if resolved.UserFormat != "global format" {
		t.Fatalf("expected global user format (channel is empty), got %q", resolved.UserFormat)
	}
	if resolved.FanName != "global-fan" {
		t.Fatalf("expected global fan_name (channel is empty), got %q", resolved.FanName)
	}
}

func TestResolve_ChannelDisabled_UsesGlobal(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Set up global template
	err := store.Upsert(ctx, &Template{
		ChannelID:    "",
		Name:         "default",
		SystemPrompt: "global prompt",
		UserFormat:   "global format",
		FanName:      "global-fan",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Channel template is disabled
	err = store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "default",
		SystemPrompt: "channel prompt",
		UserFormat:   "channel format",
		FanName:      "channel-fan",
		Enabled:      false,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := store.Resolve(ctx, "ch1", "default")
	if err != nil {
		t.Fatal(err)
	}
	// Should use global values since channel is disabled
	if resolved.SystemPrompt != "global prompt" {
		t.Fatalf("expected global system prompt (channel disabled), got %q", resolved.SystemPrompt)
	}
	if resolved.UserFormat != "global format" {
		t.Fatalf("expected global user format (channel disabled), got %q", resolved.UserFormat)
	}
	if resolved.FanName != "global-fan" {
		t.Fatalf("expected global fan_name (channel disabled), got %q", resolved.FanName)
	}
}

// ---------------------------------------------------------------------------
// TemplateStore: Upsert
// ---------------------------------------------------------------------------

func TestUpsert_Create(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	err := store.Upsert(ctx, &Template{
		ChannelID:    "",
		Name:         "custom",
		SystemPrompt: "new prompt",
		UserFormat:   "new format",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tmpl, err := store.GetGlobal(ctx, "custom")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.SystemPrompt != "new prompt" {
		t.Fatalf("expected 'new prompt', got %q", tmpl.SystemPrompt)
	}
	if tmpl.UserFormat != "new format" {
		t.Fatalf("expected 'new format', got %q", tmpl.UserFormat)
	}
}

func TestUpsert_Update(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Create
	err := store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "test",
		SystemPrompt: "original",
		UserFormat:   "original format",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Update (same channel_id + name)
	err = store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "test",
		SystemPrompt: "updated",
		UserFormat:   "updated format",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tmpl, err := store.GetByChannel(ctx, "ch1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.SystemPrompt != "updated" {
		t.Fatalf("expected 'updated', got %q", tmpl.SystemPrompt)
	}
	if tmpl.UserFormat != "updated format" {
		t.Fatalf("expected 'updated format', got %q", tmpl.UserFormat)
	}
}

// ---------------------------------------------------------------------------
// TemplateStore: Delete
// ---------------------------------------------------------------------------

func TestDelete_Success(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Create a non-default template
	err := store.Upsert(ctx, &Template{
		ChannelID:    "",
		Name:         "deletable",
		SystemPrompt: "to delete",
		UserFormat:   "format",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tmpl, _ := store.GetGlobal(ctx, "deletable")
	err = store.Delete(ctx, tmpl.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.GetGlobal(ctx, "deletable")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound after delete, got %v", err)
	}
}

func TestDelete_BuiltInRejected(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// The migration inserts a default template with is_default=1
	tmpl, err := store.GetGlobal(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}

	err = store.Delete(ctx, tmpl.ID)
	if err == nil {
		t.Fatal("expected error when deleting built-in template")
	}
	if !errors.Is(err, ErrTemplateBuiltIn) {
		t.Fatalf("expected ErrTemplateBuiltIn, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TemplateStore: ListGlobal
// ---------------------------------------------------------------------------

func TestListGlobal(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Built-in "default" template exists from migration
	templates, err := store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) < 1 {
		t.Fatalf("expected at least 1 global template (built-in default), got %d", len(templates))
	}

	// Add more global templates
	_ = store.Upsert(ctx, &Template{ChannelID: "", Name: "alpha", SystemPrompt: "a", Enabled: true})
	_ = store.Upsert(ctx, &Template{ChannelID: "", Name: "beta", SystemPrompt: "b", Enabled: true})

	templates, err = store.ListGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) < 3 {
		t.Fatalf("expected at least 3 global templates, got %d", len(templates))
	}

	// Verify sorted by name
	for i := 1; i < len(templates); i++ {
		if templates[i].Name < templates[i-1].Name {
			t.Fatalf("templates not sorted: %q < %q", templates[i].Name, templates[i-1].Name)
		}
	}
}

// ---------------------------------------------------------------------------
// RenderTemplate
// ---------------------------------------------------------------------------

func TestRenderTemplate_Variables(t *testing.T) {
	tmpl := "Title: {{title}}, Date: {{date}}, Channel: {{channel_name}}"
	vars := &TemplateVars{
		Title:       "Test Live",
		Date:        "2026.05.13",
		ChannelName: "TestChannel",
	}
	result := RenderTemplate(tmpl, vars, nil)
	expected := "Title: Test Live, Date: 2026.05.13, Channel: TestChannel"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestRenderTemplate_EmptyTemplate(t *testing.T) {
	result := RenderTemplate("", &TemplateVars{Title: "test"}, nil)
	if result != "" {
		t.Fatalf("expected empty string, got %q", result)
	}
}

func TestRenderTemplate_NilVars(t *testing.T) {
	tmpl := "Title: {{title}}"
	result := RenderTemplate(tmpl, nil, nil)
	// nil vars -> empty TemplateVars -> title becomes empty string
	expected := "Title: "
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestRenderTemplate_UnknownVarsPreserved(t *testing.T) {
	tmpl := "Known: {{title}}, Unknown: {{nonexistent_var}}"
	vars := &TemplateVars{Title: "Test"}
	result := RenderTemplate(tmpl, vars, nil)
	if !contains(result, "Unknown: {{nonexistent_var}}") {
		t.Fatalf("unknown variable should be preserved, got %q", result)
	}
	if !contains(result, "Known: Test") {
		t.Fatalf("known variable should be replaced, got %q", result)
	}
}

func TestRenderTemplate_AllStandardVars(t *testing.T) {
	tmpl := "{{channel_name}}|{{channel_id}}|{{date}}|{{date_time}}|{{title}}|{{duration}}|{{duration_min}}|{{fan_name}}|{{danmaku_count}}|{{unique_users}}|{{avg_per_min}}|{{slug}}"
	vars := &TemplateVars{
		ChannelName:  "Ch",
		ChannelID:    "ch1",
		Date:         "2026.05.13",
		DateTime:     "2026-05-13T20:00:00+08:00",
		Title:        "Live",
		Duration:     "2h30m",
		DurationMin:  150,
		FanName:      "Fan",
		DanmakuCount: 100,
		UniqueUsers:  50,
		AvgPerMin:    "1.5",
		Slug:         "live_001",
	}
	result := RenderTemplate(tmpl, vars, nil)
	if !contains(result, "Ch|ch1|2026.05.13") {
		t.Fatalf("first part mismatch, got %q", result)
	}
	if !contains(result, "100|50|1.5|live_001") {
		t.Fatalf("last part mismatch, got %q", result)
	}
}

func TestRenderTemplate_ExtraVars(t *testing.T) {
	tmpl := "Name: {{fan_name}}, Custom: {{custom_key}}, Section: {{extra_section}}"
	vars := &TemplateVars{FanName: "TestFan"}
	extra := map[string]string{
		"custom_key":    "custom_value",
		"extra_section": "bonus section",
	}
	result := RenderTemplate(tmpl, vars, extra)
	if !contains(result, "Name: TestFan") {
		t.Fatalf("fan_name not replaced, got %q", result)
	}
	if !contains(result, "Custom: custom_value") {
		t.Fatalf("extra var not replaced, got %q", result)
	}
	if !contains(result, "Section: bonus section") {
		t.Fatalf("extra var not replaced, got %q", result)
	}
}

func TestRenderTemplate_ExtraVarsNil(t *testing.T) {
	tmpl := "Title: {{title}}, Extra: {{key}}"
	vars := &TemplateVars{Title: "Test"}
	result := RenderTemplate(tmpl, vars, nil)
	// nil extra vars -> {{key}} preserved
	if !contains(result, "Extra: {{key}}") {
		t.Fatalf("unknown extra var should be preserved, got %q", result)
	}
}

func TestRenderTemplate_NumericVars(t *testing.T) {
	tmpl := "Count: {{danmaku_count}}, Users: {{unique_users}}, Min: {{duration_min}}"
	vars := &TemplateVars{
		DanmakuCount: 42,
		UniqueUsers:  7,
		DurationMin:  90,
	}
	result := RenderTemplate(tmpl, vars, nil)
	if !contains(result, "Count: 42") {
		t.Fatalf("danmaku_count not replaced, got %q", result)
	}
	if !contains(result, "Users: 7") {
		t.Fatalf("unique_users not replaced, got %q", result)
	}
	if !contains(result, "Min: 90") {
		t.Fatalf("duration_min not replaced, got %q", result)
	}
}

func TestResolve_ExtraVarsMerge(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Global template with extra vars
	err := store.Upsert(ctx, &Template{
		ChannelID:    "",
		Name:         "default",
		SystemPrompt: "global prompt",
		UserFormat:   "global format",
		ExtraVars:    `{"a":"global_a","b":"global_b"}`,
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Channel template overrides one extra var and adds a new one
	err = store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "default",
		SystemPrompt: "channel prompt",
		UserFormat:   "channel format",
		ExtraVars:    `{"b":"channel_b","c":"channel_c"}`,
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := store.Resolve(ctx, "ch1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ExtraVars["a"] != "global_a" {
		t.Fatalf("expected a='global_a' (from global), got %q", resolved.ExtraVars["a"])
	}
	if resolved.ExtraVars["b"] != "channel_b" {
		t.Fatalf("expected b='channel_b' (overridden by channel), got %q", resolved.ExtraVars["b"])
	}
	if resolved.ExtraVars["c"] != "channel_c" {
		t.Fatalf("expected c='channel_c' (from channel), got %q", resolved.ExtraVars["c"])
	}
}

func TestResolve_ChannelBuiltinMarker_FallsBackToGlobal(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	// Global with actual values
	err := store.Upsert(ctx, &Template{
		ChannelID:    "",
		Name:         "default",
		SystemPrompt: "real global prompt",
		UserFormat:   "real global format",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Channel with __builtin__ markers -> should fall back to global
	err = store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "default",
		SystemPrompt: "__builtin__",
		UserFormat:   "__builtin__",
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := store.Resolve(ctx, "ch1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SystemPrompt != "real global prompt" {
		t.Fatalf("expected global prompt (channel has __builtin__), got %q", resolved.SystemPrompt)
	}
	if resolved.UserFormat != "real global format" {
		t.Fatalf("expected global format (channel has __builtin__), got %q", resolved.UserFormat)
	}
}

func TestTemplateStoreExportImportJSON(t *testing.T) {
	database := openTemplateTestDB(t)
	store := NewTemplateStore(database)
	ctx := context.Background()

	if err := store.Upsert(ctx, &Template{
		ChannelID:    "ch1",
		Name:         "default",
		SystemPrompt: "prompt",
		UserFormat:   "format",
		FanName:      "fans",
		ExtraVars:    `{"k":"v"}`,
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	data, err := store.ExportJSON(ctx, "ch1")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported TemplateExport
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if exported.ChannelID != "ch1" || len(exported.Templates) != 1 {
		t.Fatalf("unexpected export: %+v", exported)
	}
	if exported.Templates[0].ID != 0 || exported.Templates[0].ChannelID != "ch1" {
		t.Fatalf("export should omit id and keep scoped channel: %+v", exported.Templates[0])
	}

	importDB := openTemplateTestDB(t)
	importStore := NewTemplateStore(importDB)
	count, err := importStore.ImportJSON(ctx, "ch2", data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if count != 1 {
		t.Fatalf("imported count = %d, want 1", count)
	}
	got, err := importStore.GetByChannel(ctx, "ch2", "default")
	if err != nil {
		t.Fatalf("get imported: %v", err)
	}
	if got.SystemPrompt != "prompt" || got.UserFormat != "format" || got.FanName != "fans" {
		t.Fatalf("unexpected imported template: %+v", got)
	}
	if got.IsDefault {
		t.Fatal("imported template should not be built-in default")
	}
}

// helper
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
