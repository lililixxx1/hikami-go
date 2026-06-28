package runtime

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"hikami-go/internal/channel"
	"hikami-go/internal/db"
)

func newTestDB(t *testing.T) *sql.DB {
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

func writeNetscapeCookie(t *testing.T, path string, expiresAt int64) {
	t.Helper()
	expiresStr := strconv.FormatInt(expiresAt, 10)
	content := "# Netscape HTTP Cookie File\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t" + expiresStr + "\tSESSDATA\ttestdata\n" +
		".bilibili.com\tTRUE\t/\tFALSE\t" + expiresStr + "\tbili_jct\ttestcsrf\n" +
		".bilibili.com\tTRUE\t/\tFALSE\t" + expiresStr + "\tDedeUserID\t12345\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
}

func TestCheckCookieExpiry_NoCookieFile(t *testing.T) {
	database := newTestDB(t)
	chStore := channel.NewStore(database)

	_, err := chStore.Create(context.Background(), channel.UpsertInput{
		ID:      "ch1",
		Name:    "测试主播",
		UID:     100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	warnings := CheckCookieExpiry(context.Background(), chStore)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for missing cookie file, got %d", len(warnings))
	}
}

func TestCheckCookieExpiry_Expired(t *testing.T) {
	database := newTestDB(t)
	chStore := channel.NewStore(database)

	_, err := chStore.Create(context.Background(), channel.UpsertInput{
		ID:      "ch2",
		Name:    "过期主播",
		UID:     200,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// 写入已过期 cookie（过期时间为 1 天前）
	cookiePath := filepath.Join(t.TempDir(), "cookie_expired.txt")
	writeNetscapeCookie(t, cookiePath, time.Now().Add(-24*time.Hour).Unix())

	// 设置 cookie_file
	_, err = database.Exec("UPDATE channels SET cookie_file = ? WHERE id = 'ch2'", cookiePath)
	if err != nil {
		t.Fatalf("update cookie_file: %v", err)
	}

	warnings := CheckCookieExpiry(context.Background(), chStore)
	t.Logf("warnings count: %d", len(warnings))
	for _, w := range warnings {
		t.Logf("warning: channel=%s type=%s expired=%v daysLeft=%d", w.ChannelID, w.CookieType, w.IsExpired, w.DaysLeft)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning for expired cookie")
	}
	found := false
	for _, w := range warnings {
		if w.ChannelID == "ch2" && w.IsExpired {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected expired warning for ch2, got: %+v", warnings)
	}
}

func TestCheckCookieExpiry_ExpiringSoon(t *testing.T) {
	database := newTestDB(t)
	chStore := channel.NewStore(database)

	_, err := chStore.Create(context.Background(), channel.UpsertInput{
		ID:      "ch3",
		Name:    "即将过期",
		UID:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// 过期时间为 3 天后
	cookiePath := filepath.Join(t.TempDir(), "cookie_soon.txt")
	writeNetscapeCookie(t, cookiePath, time.Now().Add(3*24*time.Hour).Unix())

	_, err = database.Exec("UPDATE channels SET download_cookie_file = ? WHERE id = 'ch3'", cookiePath)
	if err != nil {
		t.Fatalf("update download_cookie_file: %v", err)
	}

	warnings := CheckCookieExpiry(context.Background(), chStore)
	if len(warnings) == 0 {
		t.Fatal("expected warning for cookie expiring soon")
	}
	found := false
	for _, w := range warnings {
		if w.ChannelID == "ch3" && w.CookieType == "download" && w.DaysLeft <= 7 && !w.IsExpired {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected soon-expiring warning for ch3, got: %+v", warnings)
	}
}

func TestCheckCookieExpiry_Valid(t *testing.T) {
	database := newTestDB(t)
	chStore := channel.NewStore(database)

	_, err := chStore.Create(context.Background(), channel.UpsertInput{
		ID:      "ch4",
		Name:    "正常主播",
		UID:     400,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// 过期时间为 30 天后
	cookiePath := filepath.Join(t.TempDir(), "cookie_valid.txt")
	writeNetscapeCookie(t, cookiePath, time.Now().Add(30*24*time.Hour).Unix())

	_, err = database.Exec("UPDATE channels SET cookie_file = ? WHERE id = 'ch4'", cookiePath)
	if err != nil {
		t.Fatalf("update cookie_file: %v", err)
	}

	warnings := CheckCookieExpiry(context.Background(), chStore)
	for _, w := range warnings {
		if w.ChannelID == "ch4" {
			t.Fatalf("should not have warning for valid cookie, got: %+v", w)
		}
	}
}

func TestCheckCookieExpiry_MultipleChannels(t *testing.T) {
	database := newTestDB(t)
	chStore := channel.NewStore(database)

	ch1, _ := chStore.Create(context.Background(), channel.UpsertInput{ID: "mc1", Name: "主播1", UID: 501, Enabled: true})
	ch2, _ := chStore.Create(context.Background(), channel.UpsertInput{ID: "mc2", Name: "主播2", UID: 502, Enabled: true})
	if ch1.ID == "" || ch2.ID == "" {
		t.Fatal("create channels failed")
	}

	// ch1 过期
	cp1 := filepath.Join(t.TempDir(), "c1.txt")
	writeNetscapeCookie(t, cp1, time.Now().Add(-24*time.Hour).Unix())
	_, _ = database.Exec("UPDATE channels SET cookie_file = ? WHERE id = 'mc1'", cp1)

	// ch2 有效
	cp2 := filepath.Join(t.TempDir(), "c2.txt")
	writeNetscapeCookie(t, cp2, time.Now().Add(30*24*time.Hour).Unix())
	_, _ = database.Exec("UPDATE channels SET cookie_file = ? WHERE id = 'mc2'", cp2)

	warnings := CheckCookieExpiry(context.Background(), chStore)
	expiredCount := 0
	for _, w := range warnings {
		if w.ChannelID == "mc1" && w.IsExpired {
			expiredCount++
		}
	}
	if expiredCount == 0 {
		t.Fatal("expected expired warning for mc1")
	}
}

func TestCheckCookieExpiry_DisabledChannel(t *testing.T) {
	database := newTestDB(t)
	chStore := channel.NewStore(database)

	_, err := chStore.Create(context.Background(), channel.UpsertInput{
		ID:      "disabled_ch",
		Name:    "禁用主播",
		UID:     600,
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// 禁用主播
	_, err = database.Exec("UPDATE channels SET enabled = 0 WHERE id = 'disabled_ch'")
	if err != nil {
		t.Fatalf("disable channel: %v", err)
	}

	// 写入过期 cookie
	cookiePath := filepath.Join(t.TempDir(), "cookie_disabled.txt")
	writeNetscapeCookie(t, cookiePath, time.Now().Add(-24*time.Hour).Unix())
	_, err = database.Exec("UPDATE channels SET cookie_file = ? WHERE id = 'disabled_ch'", cookiePath)
	if err != nil {
		t.Fatalf("update cookie_file: %v", err)
	}

	warnings := CheckCookieExpiry(context.Background(), chStore)
	for _, w := range warnings {
		if w.ChannelID == "disabled_ch" {
			t.Fatalf("disabled channel should be skipped, got warning: %+v", w)
		}
	}
}

func TestCheckDiskUsage_LowUsage(t *testing.T) {
	dir := t.TempDir()
	// 写入小文件确保目录存在
	if err := os.WriteFile(filepath.Join(dir, "test"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	results := CheckDiskUsage([]string{dir})
	if len(results) == 0 {
		t.Fatal("expected at least one disk info result")
	}
	info := results[0]
	if info.Path == "" {
		t.Fatal("path should not be empty")
	}
	if info.TotalGB <= 0 {
		t.Fatalf("total_gb = %f, want > 0", info.TotalGB)
	}
	if info.FreeGB < 0 {
		t.Fatalf("free_gb = %f, want >= 0", info.FreeGB)
	}
	if info.UsedPercent < 0 || info.UsedPercent > 100 {
		t.Fatalf("used_percent = %f, want 0-100", info.UsedPercent)
	}
	t.Logf("磁盘使用: total=%.1fGB, used=%.1fGB, free=%.1fGB, percent=%.1f%%",
		info.TotalGB, info.UsedGB, info.FreeGB, info.UsedPercent)
}

func TestCheckDiskUsage_DeduplicatesPaths(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "test"), []byte("x"), 0o644)

	results := CheckDiskUsage([]string{dir, dir, dir})
	if len(results) != 1 {
		t.Fatalf("expected 1 result for duplicate paths, got %d", len(results))
	}
}
