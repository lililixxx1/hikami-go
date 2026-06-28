package secrets

import (
	"context"
	"database/sql"
	"os"
	"testing"

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
