package db

import (
	"database/sql"
	"fmt"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	`CREATE TABLE IF NOT EXISTS channels (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		uid INTEGER NOT NULL,
		live_room_id INTEGER NOT NULL DEFAULT 0,
		replay_source_url TEXT NOT NULL DEFAULT '',
		space_url TEXT NOT NULL DEFAULT '',
		title_prefix TEXT NOT NULL DEFAULT '',
		cookie_file TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		slug TEXT NOT NULL,
		channel_id TEXT NOT NULL,
		source_type TEXT NOT NULL,
		source_id TEXT NOT NULL,
		title TEXT NOT NULL,
		started_at TEXT,
		ended_at TEXT,
		source_url TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		current_task_id TEXT,
		last_error TEXT,
		local_available INTEGER NOT NULL DEFAULT 1,
		uploaded_at TEXT,
		published_at TEXT,
		publish_target TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY(channel_id) REFERENCES channels(id)
	);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS sessions_channel_source_uidx
		ON sessions(channel_id, source_type, source_id);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS sessions_channel_slug_uidx
		ON sessions(channel_id, slug);`,
	`CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		channel_id TEXT NOT NULL,
		session_id TEXT,
		type TEXT NOT NULL,
		status TEXT NOT NULL,
		payload TEXT NOT NULL DEFAULT '{}',
		progress INTEGER NOT NULL DEFAULT 0,
		message TEXT NOT NULL DEFAULT '',
		error TEXT,
		attempt INTEGER NOT NULL DEFAULT 1,
		started_at TEXT,
		finished_at TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY(channel_id) REFERENCES channels(id),
		FOREIGN KEY(session_id) REFERENCES sessions(id)
	);`,
	`CREATE INDEX IF NOT EXISTS tasks_status_idx ON tasks(status);`,
	`CREATE INDEX IF NOT EXISTS tasks_channel_session_idx ON tasks(channel_id, session_id);`,
	`ALTER TABLE channels ADD COLUMN download_cookie_file TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE channels ADD COLUMN auto_record INTEGER NOT NULL DEFAULT 1;`,
	`ALTER TABLE channels ADD COLUMN auto_asr INTEGER NOT NULL DEFAULT 0;`,
	`CREATE TABLE IF NOT EXISTS secrets (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	`ALTER TABLE channels ADD COLUMN record_danmaku INTEGER NOT NULL DEFAULT 1;`,
	`CREATE TABLE IF NOT EXISTS glossary_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL DEFAULT '',
		term TEXT NOT NULL,
		canonical TEXT NOT NULL,
		category TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS glossary_entries_channel_term_uidx
		ON glossary_entries(channel_id, term);`,
	`CREATE TABLE IF NOT EXISTS glossary_meta (
		channel_id TEXT NOT NULL DEFAULT '' PRIMARY KEY,
		note TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	`ALTER TABLE channels ADD COLUMN publish_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN publish_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN publish_category_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN publish_list_id INTEGER NOT NULL DEFAULT -1;
ALTER TABLE channels ADD COLUMN publish_private_pub INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN publish_original INTEGER NOT NULL DEFAULT -1;
ALTER TABLE channels ADD COLUMN auto_publish INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE channels ADD COLUMN publish_aigc INTEGER NOT NULL DEFAULT -1;
ALTER TABLE channels ADD COLUMN publish_timer_pub_time INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN publish_cover_url TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN publish_topics TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE channels ADD COLUMN source_mode TEXT NOT NULL DEFAULT 'both';`,
	`ALTER TABLE channels ADD COLUMN discover_limit INTEGER NOT NULL DEFAULT 0;`,
	// v21: recap templates
	`CREATE TABLE IF NOT EXISTS recap_templates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT 'default',
		system_prompt TEXT NOT NULL DEFAULT '',
		user_format TEXT NOT NULL DEFAULT '',
		fan_name TEXT NOT NULL DEFAULT '',
		extra_vars TEXT NOT NULL DEFAULT '{}',
		enabled INTEGER NOT NULL DEFAULT 1,
		is_default INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS recap_templates_channel_name_uidx
		ON recap_templates(channel_id, name);`,
	`INSERT INTO recap_templates (channel_id, name, system_prompt, user_format, fan_name, extra_vars, enabled, is_default)
	VALUES ('', 'default', '__builtin__', '__builtin__', '', '{}', 1, 1);`,
	// v22: cost tracking
	`ALTER TABLE tasks ADD COLUMN usage_metadata TEXT NOT NULL DEFAULT '{}';`,
	// v23: B站账号表
	`CREATE TABLE IF NOT EXISTS bili_cookie_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		uid INTEGER NOT NULL UNIQUE,
		nickname TEXT NOT NULL DEFAULT '',
		cookie_file TEXT NOT NULL,
		is_default_download INTEGER NOT NULL DEFAULT 0,
		is_default_publish INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	// v24: 主播关联账号
	`ALTER TABLE channels ADD COLUMN download_account_id INTEGER DEFAULT NULL;`,
	// v25: 主播关联发布账号
	`ALTER TABLE channels ADD COLUMN publish_account_id INTEGER DEFAULT NULL;`,
	// v28: glossary discovery candidates
	`CREATE TABLE IF NOT EXISTS glossary_candidates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL DEFAULT '',
		term TEXT NOT NULL,
		canonical TEXT NOT NULL DEFAULT '',
		category TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending', 'approved', 'rejected')),
		confidence REAL NOT NULL DEFAULT 0
			CHECK (confidence >= 0 AND confidence <= 1),
		score REAL NOT NULL DEFAULT 0
			CHECK (score >= 0 AND score <= 1),
		occurrence_count INTEGER NOT NULL DEFAULT 1
			CHECK (occurrence_count >= 0),
		session_count INTEGER NOT NULL DEFAULT 1
			CHECK (session_count >= 0),
		first_session_id TEXT NOT NULL DEFAULT '',
		last_session_id TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		normalized_key TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		reviewed_at TEXT,
		CHECK (trim(term) <> ''),
		CHECK (trim(normalized_key) <> '')
	);

	CREATE UNIQUE INDEX IF NOT EXISTS glossary_candidates_channel_key_uidx
		ON glossary_candidates(channel_id, normalized_key);

	CREATE INDEX IF NOT EXISTS glossary_candidates_channel_status_score_idx
		ON glossary_candidates(channel_id, status, score DESC, updated_at DESC);

	CREATE INDEX IF NOT EXISTS glossary_candidates_last_session_idx
		ON glossary_candidates(last_session_id);`,
	// v29-v30: per-channel recap config
	`ALTER TABLE channels ADD COLUMN recap_model TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE channels ADD COLUMN max_continuations INTEGER NOT NULL DEFAULT -1;`,
	// v31: session 归档时间戳（发布成功后自动归档到 WebDAV 用，不推进主状态）
	`ALTER TABLE sessions ADD COLUMN archived_at TEXT;`,
	// v32: per-channel 自动回顾开关（ASR 成功后是否自动生成回顾；默认 1=开，保持历史行为）
	`ALTER TABLE channels ADD COLUMN auto_recap INTEGER NOT NULL DEFAULT 1;`,
	// v33: 全局运行时配置持久化。config.yaml 降级为只读基线，UI 改动按配置段存此表，
	// 启动时 ApplyOverrides 用本表覆盖 viper 加载的基线值。data 是该段 DTO 的 JSON。
	// CHECK(section) 白名单限定 6 个全局段；CHECK(json_valid(data)) 保证 JSON 完整性。
	`CREATE TABLE IF NOT EXISTS runtime_settings (
		section TEXT NOT NULL CHECK (section IN ('publish','asr_s3','dashscope','recap_ai','webdav','archive')),
		data TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(data)),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (section)
	);`,
}

func Migrate(database *sql.DB) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for index, migration := range migrations {
		version := index + 1
		applied, err := migrationApplied(tx, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if _, err := tx.Exec(migration); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)", version); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
	}

	return tx.Commit()
}

func migrationApplied(tx *sql.Tx, version int) (bool, error) {
	if version == 1 {
		var name string
		err := tx.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'").Scan(&name)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	var exists int
	err := tx.QueryRow("SELECT 1 FROM schema_migrations WHERE version = ?", version).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}
