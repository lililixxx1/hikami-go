package biliutil

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hikami-go/internal/db"
)

func newAccountTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func newTestStore(t *testing.T) *CookieAccountStore {
	t.Helper()
	database := newAccountTestDB(t)
	return NewCookieAccountStore(database)
}

func newTestStoreWithDir(t *testing.T) (*CookieAccountStore, string) {
	t.Helper()
	dir := t.TempDir()
	database := newAccountTestDB(t)
	return NewCookieAccountStore(database, dir), dir
}

func writeValidCookie(t *testing.T, dir string, filename string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	expires := time.Now().Add(30 * 24 * time.Hour).Unix()
	content := "# Netscape HTTP Cookie File\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t" + time.Unix(expires, 0).Format("1234567890") + "\tSESSDATA\ttestdata\n" +
		".bilibili.com\tTRUE\t/\tFALSE\t" + time.Unix(expires, 0).Format("1234567890") + "\tbili_jct\tcsrf\n" +
		".bilibili.com\tTRUE\t/\tFALSE\t" + time.Unix(expires, 0).Format("1234567890") + "\tDedeUserID\t99999\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
	return path
}

func TestCookieAccountStore_Create(t *testing.T) {
	store, dir := newTestStoreWithDir(t)
	cookiePath := writeValidCookie(t, dir, "acc1.txt")

	id, err := store.Create(context.Background(), &CookieAccount{
		UID:        1001,
		Nickname:   "测试账号",
		CookieFile: cookiePath,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id = %d, want > 0", id)
	}
}

func TestCookieAccountStore_Update(t *testing.T) {
	store, dir := newTestStoreWithDir(t)
	cookiePath := writeValidCookie(t, dir, "acc1.txt")

	id, _ := store.Create(context.Background(), &CookieAccount{
		UID:        1002,
		Nickname:   "旧名称",
		CookieFile: cookiePath,
	})

	cookiePath2 := writeValidCookie(t, dir, "acc1_updated.txt")
	err := store.Update(context.Background(), &CookieAccount{
		ID:         id,
		Nickname:   "新名称",
		CookieFile: cookiePath2,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.Nickname != "新名称" {
		t.Fatalf("nickname = %q, want 新名称", got.Nickname)
	}
}

func TestCookieAccountStore_Delete(t *testing.T) {
	store, dir := newTestStoreWithDir(t)
	cookiePath := writeValidCookie(t, dir, "acc_del.txt")

	id, _ := store.Create(context.Background(), &CookieAccount{
		UID:        1003,
		Nickname:   "待删除",
		CookieFile: cookiePath,
	})

	err := store.Delete(context.Background(), id)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = store.GetByID(context.Background(), id)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("after delete: err = %v, want ErrAccountNotFound", err)
	}
}

func TestCookieAccountStore_List(t *testing.T) {
	store, dir := newTestStoreWithDir(t)
	cp1 := writeValidCookie(t, dir, "list1.txt")
	cp2 := writeValidCookie(t, dir, "list2.txt")

	store.Create(context.Background(), &CookieAccount{UID: 2001, Nickname: "A", CookieFile: cp1})
	store.Create(context.Background(), &CookieAccount{UID: 2002, Nickname: "B", CookieFile: cp2})

	accounts, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("len(accounts) = %d, want 2", len(accounts))
	}
}

func TestCookieAccountStore_SetDefaultDownload(t *testing.T) {
	store, dir := newTestStoreWithDir(t)
	cp1 := writeValidCookie(t, dir, "dl1.txt")
	cp2 := writeValidCookie(t, dir, "dl2.txt")

	id1, _ := store.Create(context.Background(), &CookieAccount{UID: 3001, Nickname: "A", CookieFile: cp1, IsDefaultDownload: true})
	id2, _ := store.Create(context.Background(), &CookieAccount{UID: 3002, Nickname: "B", CookieFile: cp2})

	// id1 is default
	def, err := store.GetDefaultDownload(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if def.ID != id1 {
		t.Fatalf("default download id = %d, want %d", def.ID, id1)
	}

	// switch to id2
	if err := store.SetDefaultDownload(context.Background(), id2); err != nil {
		t.Fatalf("set default download: %v", err)
	}
	def, err = store.GetDefaultDownload(context.Background())
	if err != nil {
		t.Fatalf("get default after switch: %v", err)
	}
	if def.ID != id2 {
		t.Fatalf("default download id = %d, want %d", def.ID, id2)
	}
}

func TestCookieAccountStore_SetDefaultPublish(t *testing.T) {
	store, dir := newTestStoreWithDir(t)
	cp1 := writeValidCookie(t, dir, "pub1.txt")
	cp2 := writeValidCookie(t, dir, "pub2.txt")

	id1, _ := store.Create(context.Background(), &CookieAccount{UID: 4001, Nickname: "A", CookieFile: cp1, IsDefaultPublish: true})
	id2, _ := store.Create(context.Background(), &CookieAccount{UID: 4002, Nickname: "B", CookieFile: cp2})

	if err := store.SetDefaultPublish(context.Background(), id2); err != nil {
		t.Fatalf("set default publish: %v", err)
	}
	def, err := store.GetDefaultPublish(context.Background())
	if err != nil {
		t.Fatalf("get default publish: %v", err)
	}
	if def.ID != id2 {
		t.Fatalf("default publish id = %d, want %d", def.ID, id2)
	}
	// 确认旧的默认被清除
	got1, _ := store.GetByID(context.Background(), id1)
	if got1.IsDefaultPublish {
		t.Fatal("old default should no longer be default publish")
	}
}

func TestCookieAccountStore_DeleteDefaultAccount(t *testing.T) {
	store, dir := newTestStoreWithDir(t)
	cp := writeValidCookie(t, dir, "del_def.txt")

	id, _ := store.Create(context.Background(), &CookieAccount{
		UID:               5001,
		Nickname:          "唯一默认",
		CookieFile:        cp,
		IsDefaultDownload: true,
	})

	// 删除唯一默认账号
	store.Delete(context.Background(), id)

	_, err := store.GetDefaultDownload(context.Background())
	if !errors.Is(err, ErrNoDefaultAccount) {
		t.Fatalf("after deleting default: err = %v, want ErrNoDefaultAccount", err)
	}
}

func TestValidateCookiePath_Allowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies", "test.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_ = os.WriteFile(path, []byte("x"), 0o644)

	err := ValidateCookiePath(path, []string{dir})
	if err != nil {
		t.Fatalf("ValidateCookiePath allowed: %v", err)
	}
}

func TestValidateCookiePath_Traversal(t *testing.T) {
	err := ValidateCookiePath("/tmp/../../../etc/passwd", []string{"/tmp"})
	if !errors.Is(err, ErrInvalidCookiePath) {
		t.Fatalf("traversal: err = %v, want ErrInvalidCookiePath", err)
	}
}

func TestValidateCookiePath_AbsoluteRequired(t *testing.T) {
	// 相对路径不应包含 ..，但空字符串应被拒绝
	err := ValidateCookiePath("", nil)
	if !errors.Is(err, ErrInvalidCookiePath) {
		t.Fatalf("empty path: err = %v, want ErrInvalidCookiePath", err)
	}
}

func TestValidateCookiePath_NoAllowedDirs(t *testing.T) {
	// 无 allowedDirs 时任意路径都允许
	err := ValidateCookiePath("/any/path/cookie.txt", nil)
	if err != nil {
		t.Fatalf("no allowed dirs: %v", err)
	}
}

func TestResolveCookie_ChannelOverride(t *testing.T) {
	store, dir := newTestStoreWithDir(t)
	cp := writeValidCookie(t, dir, "resolve_override.txt")

	id, _ := store.Create(context.Background(), &CookieAccount{
		UID:        6001,
		Nickname:   "主播专用",
		CookieFile: cp,
	})

	cookie, err := store.ResolveCookie(
		context.Background(),
		sql.NullInt64{Int64: id, Valid: true}, // downloadAccountID override
		sql.NullInt64{},
		"download",
		"",
	)
	if err != nil {
		t.Fatalf("resolve cookie: %v", err)
	}
	if cookie.SESSDATA != "testdata" {
		t.Fatalf("SESSDATA = %q, want testdata", cookie.SESSDATA)
	}
}

func TestResolveCookie_Fallback(t *testing.T) {
	store := newTestStore(t)

	// 无 channel override、无默认账号、有 fallback
	_, err := store.ResolveCookie(
		context.Background(),
		sql.NullInt64{},
		sql.NullInt64{},
		"download",
		"/nonexistent/cookie.txt",
	)
	// fallback 文件不存在应返回文件读取错误
	if err == nil {
		t.Fatal("expected error for nonexistent fallback file")
	}
}

func TestResolveCookie_UnknownUsage(t *testing.T) {
	store := newTestStore(t)

	_, err := store.ResolveCookie(
		context.Background(),
		sql.NullInt64{},
		sql.NullInt64{},
		"invalid_usage",
		"",
	)
	if err == nil || err.Error() == "" {
		t.Fatal("expected error for unknown usage type")
	}
}
