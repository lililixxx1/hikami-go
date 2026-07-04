package runtimeconfig

import (
	"context"
	"database/sql"
	"errors"
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

func TestLoadEmpty(t *testing.T) {
	store := NewStore(openTestDB(t))
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestSaveAndLoad(t *testing.T) {
	store := NewStore(openTestDB(t))
	ctx := context.Background()

	if err := store.Save(ctx, "publish", []byte(`{"cover_url":"/x.png","mode":"publish"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(got["publish"]) != `{"cover_url":"/x.png","mode":"publish"}` {
		t.Fatalf("unexpected data: %s", got["publish"])
	}
}

func TestSaveReplace(t *testing.T) {
	store := NewStore(openTestDB(t))
	ctx := context.Background()

	_ = store.Save(ctx, "webdav", []byte(`{"url":"http://a"}`))
	_ = store.Save(ctx, "webdav", []byte(`{"url":"http://b"}`))
	got, _ := store.Load(ctx)
	if !strings.Contains(string(got["webdav"]), "http://b") {
		t.Fatalf("Save should replace, got %s", got["webdav"])
	}
	if strings.Contains(string(got["webdav"]), "http://a") {
		t.Fatalf("old value should be replaced, got %s", got["webdav"])
	}
}

// DB 的 CHECK(json_valid(data)) 必须拒绝非法 JSON。
func TestSaveRejectsInvalidJSON(t *testing.T) {
	store := NewStore(openTestDB(t))
	if err := store.Save(context.Background(), "publish", []byte(`not-json`)); err == nil {
		t.Fatal("expected CHECK(json_valid) to reject invalid JSON")
	}
}

// DB 的 CHECK(section IN (...)) 必须拒绝未知 section。
func TestSaveRejectsUnknownSection(t *testing.T) {
	store := NewStore(openTestDB(t))
	if err := store.Save(context.Background(), "bogus", []byte(`{}`)); err == nil {
		t.Fatal("expected CHECK(section) to reject unknown section")
	}
}

// SaveTx 与 WithTx：fn 成功则提交、可见；fn 失败则整段回滚。
func TestWithTxCommits(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	err := WithTx(ctx, database, func(tx *sql.Tx) error {
		return store.SaveTx(ctx, tx, "archive", []byte(`{"cleanup_policy":"temp"}`))
	})
	if err != nil {
		t.Fatalf("WithTx commit: %v", err)
	}
	got, _ := store.Load(ctx)
	if string(got["archive"]) != `{"cleanup_policy":"temp"}` {
		t.Fatalf("expected committed row, got %s", got["archive"])
	}
}

func TestWithTxRollsBackOnFnError(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	want := errors.New("boom")
	err := WithTx(ctx, database, func(tx *sql.Tx) error {
		if err := store.SaveTx(ctx, tx, "archive", []byte(`{"cleanup_policy":"temp"}`)); err != nil {
			return err
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected fn error propagated, got %v", err)
	}
	got, _ := store.Load(ctx)
	if _, ok := got["archive"]; ok {
		t.Fatal("expected rollback to discard the write")
	}
}

// SaveTx 与外层 secrets 写入可共享同一事务（模拟：把两段 SaveTx 放进一个 WithTx，
// 两次写入要么都成功要么都回滚）。
func TestWithTxAtomicTwoSections(t *testing.T) {
	database := openTestDB(t)
	store := NewStore(database)
	ctx := context.Background()

	err := WithTx(ctx, database, func(tx *sql.Tx) error {
		if err := store.SaveTx(ctx, tx, "publish", []byte(`{"mode":"draft"}`)); err != nil {
			return err
		}
		// 第二次写入用非法 JSON，触发 DB 约束 → 整事务回滚，publish 也不应留下。
		return store.SaveTx(ctx, tx, "webdav", []byte(`bad`))
	})
	if err == nil {
		t.Fatal("expected second write to fail json_valid check")
	}
	got, _ := store.Load(ctx)
	if _, ok := got["publish"]; ok {
		t.Fatal("atomic tx should have rolled back publish too")
	}
}

// mustBeLocalRFC3339 断言 got 是本地时区 RFC3339 字符串(非 UTC "Z" 后缀)。
// 回归:见 2026-07-04 DB 时间字段统一本地时区修复。
// 单纯比对瞬时时间无法区分 UTC 与本地(UTC 解析后 In(time.Local) 仍接近 now),
// 必须显式校验字符串以本地 offset 结尾。
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

// updated_at 必须用本地时区 RFC3339 写入，而不是 SQLite datetime('now') 的 UTC。
// 防止时间戳与 sessions/tasks 表（统一本地时区）相差一个时区，前端展示混乱。
// 回归：见 2026-07-04 runtimeconfig 时区修复。
func TestSaveWritesLocalTimezoneUpdatedAt(t *testing.T) {
	store := NewStore(openTestDB(t))
	ctx := context.Background()

	if err := store.Save(ctx, "archive", []byte(`{"cleanup_policy":"all"}`)); err != nil {
		t.Fatal(err)
	}
	var updatedAt string
	if err := store.db.QueryRowContext(ctx,
		"SELECT updated_at FROM runtime_settings WHERE section = ?", "archive",
	).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	mustBeLocalRFC3339(t, updatedAt)
}
