package channel

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/state"
)

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
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
// Store CRUD tests (1-26)
// ---------------------------------------------------------------------------

// 1. TestCreateSuccess
func TestCreateSuccess(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	ch, err := store.Create(ctx, UpsertInput{
		ID:               "ch1",
		Name:             "Test",
		UID:              1,
		Enabled:          true,
		RecapModel:       "v4-pro",
		MaxContinuations: 3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ch.ID != "ch1" {
		t.Fatalf("ID = %q, want %q", ch.ID, "ch1")
	}
	if ch.Name != "Test" {
		t.Fatalf("Name = %q, want %q", ch.Name, "Test")
	}
	if ch.UID != 1 {
		t.Fatalf("UID = %d, want %d", ch.UID, 1)
	}
	if !ch.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if ch.RecapModel != "v4-pro" {
		t.Fatalf("RecapModel = %q, want v4-pro", ch.RecapModel)
	}
	if ch.MaxContinuations != 3 {
		t.Fatalf("MaxContinuations = %d, want 3", ch.MaxContinuations)
	}
	if ch.CreatedAt == "" {
		t.Fatalf("CreatedAt is empty")
	}
	if ch.UpdatedAt == "" {
		t.Fatalf("UpdatedAt is empty")
	}
}

// 2. TestCreateDuplicate
func TestCreateDuplicate(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	input := UpsertInput{ID: "ch1", Name: "Test", UID: 1, Enabled: true}
	if _, err := store.Create(ctx, input); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := store.Create(ctx, input)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second create err = %v, want ErrDuplicate", err)
	}
}

// 3. TestCreateValidationNoID
func TestCreateValidationNoID(t *testing.T) {
	store := NewStore(setupDB(t))
	_, err := store.Create(context.Background(), UpsertInput{
		ID: "", Name: "Test", UID: 1, Enabled: true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// 4. TestCreateValidationNoName
func TestCreateValidationNoName(t *testing.T) {
	store := NewStore(setupDB(t))
	_, err := store.Create(context.Background(), UpsertInput{
		ID: "ch1", Name: "", UID: 1, Enabled: true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// 5. TestCreateValidationBadUID
func TestCreateValidationBadUID(t *testing.T) {
	store := NewStore(setupDB(t))
	_, err := store.Create(context.Background(), UpsertInput{
		ID: "ch1", Name: "Test", UID: 0, Enabled: true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}

	_, err = store.Create(context.Background(), UpsertInput{
		ID: "ch1", Name: "Test", UID: -1, Enabled: true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// 6. TestCreateValidationPathSeparator
func TestCreateValidationPathSeparator(t *testing.T) {
	store := NewStore(setupDB(t))

	_, err := store.Create(context.Background(), UpsertInput{
		ID: "ch/1", Name: "Test", UID: 1, Enabled: true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("forward slash err = %v, want ErrInvalid", err)
	}

	_, err = store.Create(context.Background(), UpsertInput{
		ID: "ch\\1", Name: "Test", UID: 1, Enabled: true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("backslash err = %v, want ErrInvalid", err)
	}
}

// 7. TestCreateValidationNegativeRoomID
func TestCreateValidationNegativeRoomID(t *testing.T) {
	store := NewStore(setupDB(t))
	_, err := store.Create(context.Background(), UpsertInput{
		ID: "ch1", Name: "Test", UID: 1, Enabled: true, LiveRoomID: -1,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// 8. TestGetSuccess
func TestGetSuccess(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	created, err := store.Create(ctx, UpsertInput{ID: "ch1", Name: "Test", UID: 1, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.Get(ctx, "ch1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID || got.Name != created.Name {
		t.Fatalf("got = %+v, want %+v", got, created)
	}
}

// 9. TestGetNotFound
func TestGetNotFound(t *testing.T) {
	store := NewStore(setupDB(t))
	_, err := store.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// 10. TestListEmpty
func TestListEmpty(t *testing.T) {
	store := NewStore(setupDB(t))
	channels, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if channels == nil {
		t.Fatalf("list returned nil, want non-nil empty slice")
	}
	if len(channels) != 0 {
		t.Fatalf("len = %d, want 0", len(channels))
	}
}

// 11. TestListOrdering
func TestListOrdering(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{ID: "b", Name: "B", UID: 1, Enabled: true})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	_, err = store.Create(ctx, UpsertInput{ID: "a", Name: "A", UID: 2, Enabled: true})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}

	channels, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("len = %d, want 2", len(channels))
	}
	if channels[0].ID != "a" || channels[1].ID != "b" {
		t.Fatalf("order = [%s, %s], want [a, b]", channels[0].ID, channels[1].ID)
	}
}

// 12. TestUpdateSuccess
func TestUpdateSuccess(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{ID: "ch1", Name: "Old", UID: 1, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := store.Update(ctx, "ch1", UpsertInput{Name: "New", UID: 2, Enabled: false, RecapModel: "v4-flash", MaxContinuations: 0})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "New" {
		t.Fatalf("Name = %q, want %q", updated.Name, "New")
	}
	if updated.UID != 2 {
		t.Fatalf("UID = %d, want %d", updated.UID, 2)
	}
	if updated.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if updated.RecapModel != "v4-flash" {
		t.Fatalf("RecapModel = %q, want v4-flash", updated.RecapModel)
	}
	if updated.MaxContinuations != 0 {
		t.Fatalf("MaxContinuations = %d, want 0", updated.MaxContinuations)
	}
}

// 13. TestUpdateNotFound
func TestUpdateNotFound(t *testing.T) {
	store := NewStore(setupDB(t))
	_, err := store.Update(context.Background(), "missing", UpsertInput{Name: "X", UID: 1})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// 14. TestUpdateValidationNoName
func TestUpdateValidationNoName(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{ID: "ch1", Name: "Test", UID: 1, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = store.Update(ctx, "ch1", UpsertInput{Name: "", UID: 1})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// 15. TestDeleteSuccess
func TestDeleteSuccess(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{ID: "ch1", Name: "Test", UID: 1, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Delete(ctx, "ch1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = store.Get(ctx, "ch1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete err = %v, want ErrNotFound", err)
	}
}

// 16. TestDeleteNotFound
func TestDeleteNotFound(t *testing.T) {
	store := NewStore(setupDB(t))
	err := store.Delete(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// 17. TestDeleteInUse
func TestDeleteInUse(t *testing.T) {
	database := setupDB(t)
	ctx := context.Background()

	// Create channel directly via SQL to avoid Store layer
	_, err := database.ExecContext(ctx, `
		INSERT INTO channels (id, name, uid, enabled) VALUES (?, ?, ?, ?)
	`, "ch1", "Test", 1, 1)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	// Create a session referencing the channel
	_, err = database.ExecContext(ctx, `
		INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "ch1_live_001", "live_001", "ch1", "live_record", "src1", "Title", string(state.StatusDiscovered))
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	store := NewStore(database)
	err = store.Delete(ctx, "ch1")
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

// 18. TestSaveIdentifiedNew
func TestSaveIdentifiedNew(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	ch, created, err := store.SaveIdentified(ctx, UpsertInput{
		ID:      "bili_123",
		Name:    "Test",
		UID:     123,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("save identified: %v", err)
	}
	if !created {
		t.Fatalf("created = false, want true")
	}
	if ch.ID != "bili_123" {
		t.Fatalf("ID = %q, want %q", ch.ID, "bili_123")
	}
}

// 19. TestSaveIdentifiedExisting
func TestSaveIdentifiedExisting(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{
		ID:      "bili_123",
		Name:    "Old",
		UID:     123,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ch, created, err := store.SaveIdentified(ctx, UpsertInput{
		ID:      "bili_123",
		Name:    "New",
		UID:     123,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("save identified: %v", err)
	}
	if created {
		t.Fatalf("created = true, want false")
	}
	if ch.Name != "New" {
		t.Fatalf("Name = %q, want %q", ch.Name, "New")
	}
}

// 20. TestSaveIdentifiedPreservesTitlePrefix
func TestSaveIdentifiedPreservesTitlePrefix(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{
		ID:          "bili_123",
		Name:        "Test",
		UID:         123,
		TitlePrefix: "custom",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ch, _, err := store.SaveIdentified(ctx, UpsertInput{
		ID:          "bili_123",
		Name:        "Updated",
		UID:         123,
		TitlePrefix: "【直播回放】",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("save identified: %v", err)
	}
	if ch.TitlePrefix != "custom" {
		t.Fatalf("TitlePrefix = %q, want %q", ch.TitlePrefix, "custom")
	}
}

func TestSaveIdentifiedPreservesRecapConfig(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{
		ID:               "bili_123",
		Name:             "Test",
		UID:              123,
		Enabled:          true,
		RecapModel:       "v4-pro",
		MaxContinuations: 2,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ch, _, err := store.SaveIdentified(ctx, UpsertInput{
		ID:               "bili_123",
		Name:             "Updated",
		UID:              123,
		Enabled:          true,
		RecapModel:       "v4-flash",
		MaxContinuations: 0,
	})
	if err != nil {
		t.Fatalf("save identified: %v", err)
	}
	if ch.RecapModel != "v4-pro" {
		t.Fatalf("RecapModel = %q, want v4-pro", ch.RecapModel)
	}
	if ch.MaxContinuations != 2 {
		t.Fatalf("MaxContinuations = %d, want 2", ch.MaxContinuations)
	}
}

// 21. TestSaveIdentifiedPreservesCookieFile
func TestSaveIdentifiedPreservesCookieFile(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{
		ID:         "bili_123",
		Name:       "Test",
		UID:        123,
		CookieFile: "cookie.txt",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ch, _, err := store.SaveIdentified(ctx, UpsertInput{
		ID:         "bili_123",
		Name:       "Updated",
		UID:        123,
		CookieFile: "",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("save identified: %v", err)
	}
	if ch.CookieFile != "cookie.txt" {
		t.Fatalf("CookieFile = %q, want %q", ch.CookieFile, "cookie.txt")
	}
}

// 22. TestSaveIdentifiedPreservesEnabled
func TestSaveIdentifiedPreservesEnabled(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{
		ID:      "bili_123",
		Name:    "Test",
		UID:     123,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ch, _, err := store.SaveIdentified(ctx, UpsertInput{
		ID:      "bili_123",
		Name:    "Updated",
		UID:     123,
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("save identified: %v", err)
	}
	if !ch.Enabled {
		t.Fatalf("Enabled = false, want true (preserved)")
	}
}

// 23. TestBootstrapEmpty
func TestBootstrapEmpty(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	err := store.Bootstrap(ctx, []config.BootstrapChannel{
		{ID: "ch1", Name: "A", UID: 1, Enabled: true},
		{ID: "ch2", Name: "B", UID: 2, Enabled: true},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	channels, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("len = %d, want 2", len(channels))
	}
}

// 24. TestBootstrapNonEmpty
func TestBootstrapNonEmpty(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	_, err := store.Create(ctx, UpsertInput{ID: "existing", Name: "Existing", UID: 1, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = store.Bootstrap(ctx, []config.BootstrapChannel{
		{ID: "new", Name: "New", UID: 2, Enabled: true},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	channels, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("len = %d, want 1", len(channels))
	}
	if channels[0].ID != "existing" {
		t.Fatalf("ID = %q, want %q", channels[0].ID, "existing")
	}
}

// 25. TestBootstrapEmptyList
func TestBootstrapEmptyList(t *testing.T) {
	store := NewStore(setupDB(t))
	err := store.Bootstrap(context.Background(), nil)
	if err != nil {
		t.Fatalf("bootstrap empty: %v", err)
	}

	channels, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("len = %d, want 0", len(channels))
	}
}

// 26. TestBootstrapValidation
func TestBootstrapValidation(t *testing.T) {
	store := NewStore(setupDB(t))
	err := store.Bootstrap(context.Background(), []config.BootstrapChannel{
		{ID: "ch1", Name: "", UID: 1, Enabled: true},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// ---------------------------------------------------------------------------
// identify.go tests (27-46)
// ---------------------------------------------------------------------------

// 27. TestNormalizeIdentifyInputExplicitRoomID
func TestNormalizeIdentifyInputExplicitRoomID(t *testing.T) {
	got, source, err := normalizeIdentifyInput(IdentifyInput{LiveRoomID: 123})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "explicit" {
		t.Fatalf("source = %q, want %q", source, "explicit")
	}
	if got.LiveRoomID != 123 {
		t.Fatalf("LiveRoomID = %d, want 123", got.LiveRoomID)
	}
}

// 28. TestNormalizeIdentifyInputExplicitUID
func TestNormalizeIdentifyInputExplicitUID(t *testing.T) {
	got, source, err := normalizeIdentifyInput(IdentifyInput{UID: 456})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "explicit" {
		t.Fatalf("source = %q, want %q", source, "explicit")
	}
	if got.UID != 456 {
		t.Fatalf("UID = %d, want 456", got.UID)
	}
}

// 29. TestNormalizeIdentifyInputExplicitBoth
func TestNormalizeIdentifyInputExplicitBoth(t *testing.T) {
	got, source, err := normalizeIdentifyInput(IdentifyInput{UID: 456, LiveRoomID: 123})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "explicit" {
		t.Fatalf("source = %q, want %q", source, "explicit")
	}
	if got.LiveRoomID != 123 {
		t.Fatalf("LiveRoomID = %d, want 123", got.LiveRoomID)
	}
	if got.UID != 456 {
		t.Fatalf("UID = %d, want 456", got.UID)
	}
}

// 30. TestNormalizeIdentifyInputLiveURL
func TestNormalizeIdentifyInputLiveURL(t *testing.T) {
	got, source, err := normalizeIdentifyInput(IdentifyInput{Input: "https://live.bilibili.com/123"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "live_url" {
		t.Fatalf("source = %q, want %q", source, "live_url")
	}
	if got.LiveRoomID != 123 {
		t.Fatalf("LiveRoomID = %d, want 123", got.LiveRoomID)
	}
}

// 31. TestNormalizeIdentifyInputLiveURLBlanc
func TestNormalizeIdentifyInputLiveURLBlanc(t *testing.T) {
	got, source, err := normalizeIdentifyInput(IdentifyInput{Input: "https://live.bilibili.com/blanc/456"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "live_url" {
		t.Fatalf("source = %q, want %q", source, "live_url")
	}
	if got.LiveRoomID != 456 {
		t.Fatalf("LiveRoomID = %d, want 456", got.LiveRoomID)
	}
}

// 32. TestNormalizeIdentifyInputSpaceURL
func TestNormalizeIdentifyInputSpaceURL(t *testing.T) {
	got, source, err := normalizeIdentifyInput(IdentifyInput{Input: "https://space.bilibili.com/789"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "space_url" {
		t.Fatalf("source = %q, want %q", source, "space_url")
	}
	if got.UID != 789 {
		t.Fatalf("UID = %d, want 789", got.UID)
	}
}

// 33. TestNormalizeIdentifyInputNumeric
func TestNormalizeIdentifyInputNumeric(t *testing.T) {
	got, source, err := normalizeIdentifyInput(IdentifyInput{Input: "123"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "uid" {
		t.Fatalf("source = %q, want %q", source, "uid")
	}
	if got.UID != 123 {
		t.Fatalf("UID = %d, want 123", got.UID)
	}
}

// 34. TestNormalizeIdentifyInputNumericZero
func TestNormalizeIdentifyInputNumericZero(t *testing.T) {
	_, _, err := normalizeIdentifyInput(IdentifyInput{Input: "0"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// 35. TestNormalizeIdentifyInputEmpty
func TestNormalizeIdentifyInputEmpty(t *testing.T) {
	_, _, err := normalizeIdentifyInput(IdentifyInput{Input: ""})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// 36. TestNormalizeIdentifyInputUnsupported
func TestNormalizeIdentifyInputUnsupported(t *testing.T) {
	_, _, err := normalizeIdentifyInput(IdentifyInput{Input: "https://example.com"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// 37. TestParseLiveURL
func TestParseLiveURL(t *testing.T) {
	tests := []struct {
		url    string
		roomID int64
		ok     bool
	}{
		{"https://live.bilibili.com/123", 123, true},
		{"https://live.bilibili.com/blanc/456", 456, true},
		{"live.bilibili.com/789", 789, true},
	}
	for _, tt := range tests {
		roomID, ok := parseLiveURL(tt.url)
		if ok != tt.ok {
			t.Errorf("parseLiveURL(%q) ok = %v, want %v", tt.url, ok, tt.ok)
		}
		if roomID != tt.roomID {
			t.Errorf("parseLiveURL(%q) roomID = %d, want %d", tt.url, roomID, tt.roomID)
		}
	}
}

// 38. TestParseLiveURLInvalid
func TestParseLiveURLInvalid(t *testing.T) {
	tests := []struct {
		url string
	}{
		{"https://live.bilibili.com/abc"},
		{""},
		{"https://example.com/123"},
	}
	for _, tt := range tests {
		roomID, ok := parseLiveURL(tt.url)
		if ok {
			t.Errorf("parseLiveURL(%q) ok = true, want false", tt.url)
		}
		if roomID != 0 {
			t.Errorf("parseLiveURL(%q) roomID = %d, want 0", tt.url, roomID)
		}
	}
}

// 39. TestParseSpaceURL
func TestParseSpaceURL(t *testing.T) {
	uid, ok := parseSpaceURL("https://space.bilibili.com/456")
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if uid != 456 {
		t.Fatalf("uid = %d, want 456", uid)
	}
}

// 40. TestParseSpaceURLInvalid
func TestParseSpaceURLInvalid(t *testing.T) {
	tests := []struct {
		url string
	}{
		{"https://space.bilibili.com/abc"},
		{""},
	}
	for _, tt := range tests {
		uid, ok := parseSpaceURL(tt.url)
		if ok {
			t.Errorf("parseSpaceURL(%q) ok = true, want false", tt.url)
		}
		if uid != 0 {
			t.Errorf("parseSpaceURL(%q) uid = %d, want 0", tt.url, uid)
		}
	}
}

// 41. TestIdentifyByRoom
func TestIdentifyByRoom(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/frontend/finger/spi" {
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"newbuvid3","b_4":"newbuvid4"}}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"room_info":{"uid":456,"room_id":123,"title":"直播标题"},
				"anchor_info":{"base_info":{"uid":456,"uname":"主播名"}}
			}
		}`))
	}))
	defer server.Close()

	identifier := NewIdentifierWithBaseURL(server.URL)
	withTestBuvidStore(identifier, server)
	result, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 123})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.ID != "bili_456" {
		t.Fatalf("ID = %q, want %q", result.Channel.ID, "bili_456")
	}
	if result.Channel.Name != "主播名" {
		t.Fatalf("Name = %q, want %q", result.Channel.Name, "主播名")
	}
	if result.Channel.LiveRoomID != 123 {
		t.Fatalf("LiveRoomID = %d, want 123", result.Channel.LiveRoomID)
	}
	if result.Source != "explicit" {
		t.Fatalf("Source = %q, want %q", result.Source, "explicit")
	}
}

// 42. TestIdentifyByRoomFallbackUID
func TestIdentifyByRoomFallbackUID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/frontend/finger/spi" {
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"newbuvid3","b_4":"newbuvid4"}}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"room_info":{"uid":0,"room_id":123,"title":"直播标题"},
				"anchor_info":{"base_info":{"uid":999,"uname":"主播名"}}
			}
		}`))
	}))
	defer server.Close()

	identifier := NewIdentifierWithBaseURL(server.URL)
	withTestBuvidStore(identifier, server)
	result, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 123})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.UID != 999 {
		t.Fatalf("UID = %d, want 999", result.Channel.UID)
	}
}

// 43. TestIdentifyByRoomMissingName
func TestIdentifyByRoomMissingName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/frontend/finger/spi" {
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"newbuvid3","b_4":"newbuvid4"}}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"room_info":{"uid":0,"room_id":123,"title":""},
				"anchor_info":{"base_info":{"uid":0,"uname":""}}
			}
		}`))
	}))
	defer server.Close()

	identifier := NewIdentifierWithBaseURL(server.URL)
	withTestBuvidStore(identifier, server)
	_, err := identifier.Identify(context.Background(), IdentifyInput{LiveRoomID: 123})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrInvalid) && err.Error() != "bilibili room info missing uid or name" {
		t.Fatalf("err = %v", err)
	}
}

// 44. TestIdentifyByUID
func TestIdentifyByUID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"newbuvid3","b_4":"newbuvid4"}}`))
		case "/room/v1/Room/getRoomInfoOld":
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"roomid":123}}`))
		case "/xlive/web-room/v1/index/getInfoByRoom":
			_, _ = w.Write([]byte(`{
				"code":0,
				"message":"0",
				"data":{
					"room_info":{"uid":456,"room_id":123,"title":"直播标题"},
					"anchor_info":{"base_info":{"uid":456,"uname":"主播名"}}
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	identifier := NewIdentifierWithBaseURL(server.URL)
	withTestBuvidStore(identifier, server)
	result, err := identifier.Identify(context.Background(), IdentifyInput{UID: 456})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.UID != 456 {
		t.Fatalf("UID = %d, want 456", result.Channel.UID)
	}
	if result.Channel.LiveRoomID != 123 {
		t.Fatalf("LiveRoomID = %d, want 123", result.Channel.LiveRoomID)
	}
	if result.Channel.Name != "主播名" {
		t.Fatalf("Name = %q, want %q", result.Channel.Name, "主播名")
	}
}

// 45. TestIdentifyByUIDNoRoom
func TestIdentifyByUIDNoRoom(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"b_3":"newbuvid3","b_4":"newbuvid4"}}`))
		case "/room/v1/Room/getRoomInfoOld":
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"roomid":0}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	identifier := NewIdentifierWithBaseURL(server.URL)
	withTestBuvidStore(identifier, server)
	result, err := identifier.Identify(context.Background(), IdentifyInput{UID: 999})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if result.Channel.UID != 999 {
		t.Fatalf("UID = %d, want 999", result.Channel.UID)
	}
	if result.Channel.LiveRoomID != 0 {
		t.Fatalf("LiveRoomID = %d, want 0", result.Channel.LiveRoomID)
	}
	if result.Channel.Name != "B站用户 999" {
		t.Fatalf("Name = %q, want %q", result.Channel.Name, "B站用户 999")
	}
}

// 46. TestIdentifyMissingBoth
func TestIdentifyMissingBoth(t *testing.T) {
	identifier := NewIdentifier()
	_, err := identifier.Identify(context.Background(), IdentifyInput{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// ---------------------------------------------------------------------------
// Helper function tests (47-48)
// ---------------------------------------------------------------------------

// 47. TestMergeIdentified
func TestMergeIdentified(t *testing.T) {
	tests := []struct {
		name     string
		existing Channel
		input    UpsertInput
		want     UpsertInput
	}{
		{
			name:     "preserves title prefix when existing is set",
			existing: Channel{TitlePrefix: "custom"},
			input:    UpsertInput{TitlePrefix: "【直播回放】", UID: 1, Name: "Test"},
			want:     UpsertInput{TitlePrefix: "custom", UID: 1, Name: "Test"},
		},
		{
			name:     "does not preserve title prefix when existing is empty",
			existing: Channel{TitlePrefix: ""},
			input:    UpsertInput{TitlePrefix: "【直播回放】", UID: 1, Name: "Test"},
			want:     UpsertInput{TitlePrefix: "【直播回放】", UID: 1, Name: "Test"},
		},
		{
			name:     "preserves cookie file when input is empty",
			existing: Channel{CookieFile: "cookie.txt"},
			input:    UpsertInput{CookieFile: "", UID: 1, Name: "Test"},
			want:     UpsertInput{CookieFile: "cookie.txt", UID: 1, Name: "Test"},
		},
		{
			name:     "does not preserve cookie file when input is set",
			existing: Channel{CookieFile: "old.txt"},
			input:    UpsertInput{CookieFile: "new.txt", UID: 1, Name: "Test"},
			want:     UpsertInput{CookieFile: "new.txt", UID: 1, Name: "Test"},
		},
		{
			name:     "always preserves existing enabled state",
			existing: Channel{Enabled: true},
			input:    UpsertInput{Enabled: false, UID: 1, Name: "Test"},
			want:     UpsertInput{Enabled: true, UID: 1, Name: "Test"},
		},
		{
			name:     "preserves disabled state",
			existing: Channel{Enabled: false},
			input:    UpsertInput{Enabled: true, UID: 1, Name: "Test"},
			want:     UpsertInput{Enabled: false, UID: 1, Name: "Test"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeIdentified(tt.existing, tt.input)
			if got.TitlePrefix != tt.want.TitlePrefix {
				t.Errorf("TitlePrefix = %q, want %q", got.TitlePrefix, tt.want.TitlePrefix)
			}
			if got.CookieFile != tt.want.CookieFile {
				t.Errorf("CookieFile = %q, want %q", got.CookieFile, tt.want.CookieFile)
			}
			if got.Enabled != tt.want.Enabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tt.want.Enabled)
			}
		})
	}
}

// 48. TestBoolToInt
func TestBoolToInt(t *testing.T) {
	if got := boolToInt(true); got != 1 {
		t.Fatalf("boolToInt(true) = %d, want 1", got)
	}
	if got := boolToInt(false); got != 0 {
		t.Fatalf("boolToInt(false) = %d, want 0", got)
	}
}

func TestUpdateCookieFile(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()
	if _, err := store.Create(ctx, UpsertInput{
		ID:      "huize",
		Name:    "Hikami",
		UID:     42,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	download, err := store.UpdateCookieFile(ctx, "huize", CookieUsageDownload, "/tmp/download.txt")
	if err != nil {
		t.Fatalf("update download cookie: %v", err)
	}
	if download.DownloadCookieFile != "/tmp/download.txt" || download.CookieFile != "" {
		t.Fatalf("unexpected download update: %+v", download)
	}

	publish, err := store.UpdateCookieFile(ctx, "huize", CookieUsagePublish, "/tmp/publish.txt")
	if err != nil {
		t.Fatalf("update publish cookie: %v", err)
	}
	if publish.CookieFile != "/tmp/publish.txt" || publish.DownloadCookieFile != "/tmp/download.txt" {
		t.Fatalf("unexpected publish update: %+v", publish)
	}
}

func TestSourceModeRoundTrip(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	ch, err := store.Create(ctx, UpsertInput{
		ID:         "ch_sm",
		Name:       "SourceMode Test",
		UID:        1,
		Enabled:    true,
		SourceMode: "live_only",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ch.SourceMode != "live_only" {
		t.Fatalf("SourceMode = %q, want %q", ch.SourceMode, "live_only")
	}

	updated, err := store.Update(ctx, "ch_sm", UpsertInput{
		Name:       "Updated",
		UID:        1,
		SourceMode: "replay_only",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.SourceMode != "replay_only" {
		t.Fatalf("SourceMode after update = %q, want %q", updated.SourceMode, "replay_only")
	}

	got, err := store.Get(ctx, "ch_sm")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SourceMode != "replay_only" {
		t.Fatalf("SourceMode after get = %q, want %q", got.SourceMode, "replay_only")
	}
}

func TestSourceModeDefaultBoth(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	ch, err := store.Create(ctx, UpsertInput{
		ID:      "ch_default",
		Name:    "Default",
		UID:     1,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Empty string is the Go default; the DB column defaults to "both"
	// After read-back from DB, it should be "both"
	if ch.SourceMode != "both" {
		t.Fatalf("SourceMode = %q, want %q", ch.SourceMode, "both")
	}
}

// TestAutoRecapRoundTrip 验证 auto_recap 的三态语义（*bool）：
//   - Create 不提供 → 默认 true（保持历史「ASR 后自动回顾」行为，对齐 v32 迁移 DEFAULT 1）
//   - Create 显式 false → 关闭
//   - Update 不提供（nil）→ 保留现有值
//   - Update 显式值 → 覆盖
func TestAutoRecapRoundTrip(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	// 1. Create 不提供 auto_recap → 默认 true
	ch, err := store.Create(ctx, UpsertInput{
		ID:   "ch_recap",
		Name: "RecapTest",
		UID:  1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !ch.AutoRecap {
		t.Fatalf("AutoRecap = false on create (omitted), want true (default)")
	}

	// 2. Update 显式关闭
	off := false
	updated, err := store.Update(ctx, ch.ID, UpsertInput{
		Name:      "RecapTest",
		UID:       1,
		AutoRecap: &off,
	})
	if err != nil {
		t.Fatalf("update off: %v", err)
	}
	if updated.AutoRecap {
		t.Fatalf("AutoRecap = true after explicit false, want false")
	}

	// 3. Update 不提供 auto_recap（nil）→ 应保留 false（上一步的值）
	kept, err := store.Update(ctx, ch.ID, UpsertInput{
		Name: "RecapTest",
		UID:  1,
	})
	if err != nil {
		t.Fatalf("update keep: %v", err)
	}
	if kept.AutoRecap {
		t.Fatalf("AutoRecap = true after nil update, want preserved false")
	}

	// 4. Update 显式开启
	on := true
	turnedOn, err := store.Update(ctx, ch.ID, UpsertInput{
		Name:      "RecapTest",
		UID:       1,
		AutoRecap: &on,
	})
	if err != nil {
		t.Fatalf("update on: %v", err)
	}
	if !turnedOn.AutoRecap {
		t.Fatalf("AutoRecap = false after explicit true, want true")
	}

	// 5. 再次 Update 不提供 → 应保留 true（证明 nil 保留的是当前值而非固定值）
	keptTrue, err := store.Update(ctx, ch.ID, UpsertInput{
		Name: "RecapTest",
		UID:  1,
	})
	if err != nil {
		t.Fatalf("update keep true: %v", err)
	}
	if !keptTrue.AutoRecap {
		t.Fatalf("AutoRecap = false after nil update, want preserved true")
	}
}

// boolPtr 是辅助函数，把 bool 字面量转为 *bool（auto_recap 三态用）。
func boolPtr(b bool) *bool { return &b }

// TestBootstrapAutoRecapDefault 验证 bootstrap_channels 配置省略 auto_recap（AutoRecap=nil）
// 时频道默认开启自动回顾（对齐 v32 迁移 DEFAULT 1 与历史「ASR 后自动回顾」行为），
// 显式 false 时则关闭。覆盖 codex 复审指出的「Bootstrap 默认 true 未真正生效」阻断项。
func TestBootstrapAutoRecapDefault(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()

	off := false
	err := store.Bootstrap(ctx, []config.BootstrapChannel{
		{ID: "ch_omit", Name: "Omit", UID: 1, Enabled: true},                // AutoRecap=nil → 默认 true
		{ID: "ch_off", Name: "Off", UID: 2, Enabled: true, AutoRecap: &off}, // 显式 false
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	omit, err := store.Get(ctx, "ch_omit")
	if err != nil {
		t.Fatalf("get ch_omit: %v", err)
	}
	if !omit.AutoRecap {
		t.Fatalf("ch_omit AutoRecap = false, want true (default when omitted)")
	}

	offCh, err := store.Get(ctx, "ch_off")
	if err != nil {
		t.Fatalf("get ch_off: %v", err)
	}
	if offCh.AutoRecap {
		t.Fatalf("ch_off AutoRecap = true, want false (explicit)")
	}
}

// 回归:见 2026-07-04 DB 时间字段统一本地时区修复。
// UpdateCookieFile 与 Update 的 updated_at 必须用本地时区 RFC3339 写入，
// 而不是 SQLite datetime('now') 的 UTC。
func TestUpdateWritesLocalTimezoneUpdatedAt(t *testing.T) {
	store := NewStore(setupDB(t))
	ctx := context.Background()
	if _, err := store.Create(ctx, UpsertInput{
		ID:      "tz1",
		Name:    "TZ",
		UID:     1,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 触发 UpdateCookieFile 的 download 分支。
	got, err := store.UpdateCookieFile(ctx, "tz1", CookieUsageDownload, "/tmp/d.txt")
	if err != nil {
		t.Fatalf("update cookie: %v", err)
	}
	mustBeLocalRFC3339(t, got.UpdatedAt)

	// 触发 Update (计划里的 UpdateChannel)。
	updated, err := store.Update(ctx, "tz1", UpsertInput{
		ID:      "tz1",
		Name:    "TZ2",
		UID:     1,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	mustBeLocalRFC3339(t, updated.UpdatedAt)
}
