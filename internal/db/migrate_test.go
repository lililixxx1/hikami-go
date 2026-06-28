package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateIsIdempotent(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := Migrate(database); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := Migrate(database); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != len(migrations) {
		t.Fatalf("migration count = %d, want %d", count, len(migrations))
	}
}

func TestMigrateCreatesCoreTables(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, table := range []string{"channels", "sessions", "tasks"} {
		var name string
		err := database.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s not created: %v", table, err)
		}
	}
}

func TestMigrateCreatesAllTables(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	expected := []string{
		"schema_migrations", "channels", "sessions", "tasks",
		"secrets", "glossary_entries", "glossary_meta",
		"recap_templates", "bili_cookie_accounts", "glossary_candidates",
	}
	for _, table := range expected {
		var name string
		err := database.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Errorf("表 %s 未创建: %v", table, err)
		}
	}
}

func TestMigrateCreatesIndexes(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	indexes := []string{
		"sessions_channel_source_uidx",
		"sessions_channel_slug_uidx",
		"tasks_status_idx",
		"tasks_channel_session_idx",
		"glossary_entries_channel_term_uidx",
		"recap_templates_channel_name_uidx",
		"glossary_candidates_channel_key_uidx",
	}
	for _, idx := range indexes {
		var name string
		err := database.QueryRow("SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?", idx).Scan(&name)
		if err != nil {
			t.Errorf("索引 %s 未创建: %v", idx, err)
		}
	}
}

func TestMigrateDefaultRecapTemplate(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM recap_templates WHERE channel_id = '' AND name = 'default' AND is_default = 1").Scan(&count)
	if err != nil {
		t.Fatalf("查询默认模板: %v", err)
	}
	if count != 1 {
		t.Errorf("默认回顾模板数量 = %d, 期望 1", count)
	}
}

func TestOpen_EnablesForeignKeys(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	var fkEnabled int
	err = database.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("查询 foreign_keys: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("foreign_keys = %d, 期望 1", fkEnabled)
	}
}

func TestMigrate_VersionSequence(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := database.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("查询迁移版本: %v", err)
	}
	defer rows.Close()

	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("扫描版本号: %v", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历版本: %v", err)
	}

	if len(versions) != len(migrations) {
		t.Fatalf("版本数量 = %d, 期望 %d", len(versions), len(migrations))
	}
	for i, v := range versions {
		if v != i+1 {
			t.Errorf("versions[%d] = %d, 期望 %d", i, v, i+1)
		}
	}
}

func TestMigrate_ChannelsColumns(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 验证关键追加列存在
	columns := []string{
		"download_cookie_file", "auto_record", "auto_asr", "record_danmaku",
		"source_mode", "discover_limit", "download_account_id",
		"publish_account_id", "recap_model", "max_continuations",
	}
	for _, col := range columns {
		var val interface{}
		// 用一条空结果查询验证列存在性
		err := database.QueryRow("SELECT " + col + " FROM channels LIMIT 0").Scan(&val)
		if err != nil && err != sql.ErrNoRows {
			t.Errorf("channels.%s 列不存在或不可查询: %v", col, err)
		}
	}
}

func TestOpen_SetsFilePermissions0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hikami.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	// 主文件必须收紧为 0600（ISS-7）
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("db file permission = %o, 期望 0600", perm)
	}

	// WAL/SHM 辅助文件若已生成，同样应收紧为 0600
	for _, suffix := range []string{"-wal", "-shm"} {
		auxInfo, statErr := os.Stat(path + suffix)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			t.Fatalf("stat %s: %v", suffix, statErr)
		}
		if perm := auxInfo.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s permission = %o, 期望 0600", suffix, perm)
		}
	}
}
