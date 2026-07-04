package secrets

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/db"
)

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

func TestSetAndGet(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	val, _ := store.Get(ctx, "TEST_KEY")
	if val != "" {
		t.Fatalf("expected empty, got %q", val)
	}

	if err := store.Set(ctx, "TEST_KEY", "secret123"); err != nil {
		t.Fatal(err)
	}

	val, err := store.Get(ctx, "TEST_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if val != "secret123" {
		t.Fatalf("expected secret123, got %q", val)
	}
}

func TestSetOverwrite(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	store.Set(ctx, "K", "old")
	store.Set(ctx, "K", "new")

	val, _ := store.Get(ctx, "K")
	if val != "new" {
		t.Fatalf("expected new, got %q", val)
	}
}

func TestDelete(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	store.Set(ctx, "K", "v")
	store.Delete(ctx, "K")

	val, _ := store.Get(ctx, "K")
	if val != "" {
		t.Fatalf("expected empty after delete, got %q", val)
	}
}

func TestList(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	store.Set(ctx, "B_KEY", "b")
	store.Set(ctx, "A_KEY", "a")

	items, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Key != "A_KEY" || items[1].Key != "B_KEY" {
		t.Fatalf("expected sorted order, got %v", items)
	}
	if !items[0].Set {
		t.Fatal("expected Set=true")
	}
}

func TestLoadIntoEnv(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	os.Unsetenv("HIKAMI_TEST_SECRET")
	store.Set(ctx, "HIKAMI_TEST_SECRET", "fromdb")
	store.LoadIntoEnv(ctx)

	if v := os.Getenv("HIKAMI_TEST_SECRET"); v != "fromdb" {
		t.Fatalf("expected fromdb, got %q", v)
	}
	os.Unsetenv("HIKAMI_TEST_SECRET")
}

func TestLoadIntoEnvSkipsEmpty(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	os.Unsetenv("HIKAMI_TEST_EMPTY")
	store.Set(ctx, "HIKAMI_TEST_EMPTY", "")
	store.LoadIntoEnv(ctx)

	if v := os.Getenv("HIKAMI_TEST_EMPTY"); v != "" {
		t.Fatalf("expected empty, got %q", v)
	}
}

func TestMaskValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"short", "****"},
		{"sk-1234567890abcdef", "****cdef"},
	}
	for _, tt := range tests {
		got := MaskValue(tt.input)
		if got != tt.expected {
			t.Errorf("MaskValue(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestValidateKey(t *testing.T) {
	known := []string{"DASHSCOPE_API_KEY", "AI_API_KEY"}
	if err := ValidateKey("DASHSCOPE_API_KEY", known); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKey("UNKNOWN", known); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

// 回归:见 2026-07-04 DB 时间字段统一本地时区修复。
// Set / SetTx 写入的 updated_at 必须是本地时区 RFC3339。
func TestSetWritesLocalTimezoneUpdatedAt(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	if err := store.Set(ctx, "k1", "v1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	var updatedAt string
	if err := db.QueryRowContext(ctx, "SELECT updated_at FROM secrets WHERE key=?", "k1").Scan(&updatedAt); err != nil {
		t.Fatalf("query: %v", err)
	}
	mustBeLocalRFC3339(t, updatedAt)

	// SetTx 路径。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := store.SetTx(ctx, tx, "k2", "v2"); err != nil {
		t.Fatalf("settx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT updated_at FROM secrets WHERE key=?", "k2").Scan(&updatedAt); err != nil {
		t.Fatalf("query k2: %v", err)
	}
	mustBeLocalRFC3339(t, updatedAt)
}
