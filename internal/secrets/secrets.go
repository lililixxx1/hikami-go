package secrets

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// nowRFC3339 返回本地时区的 RFC3339 时间字符串，与 sessions/tasks 表的时间字段
// （time.Now().Format(time.RFC3339)）保持一致。避免 SQLite datetime('now') 返回 UTC，
// 导致前端展示与其它表时间字段相差一个时区。
func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

type Secret struct {
	Key       string `json:"key"`
	Value     string `json:"-"`
	Set       bool   `json:"set"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Store) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM secrets WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO secrets (key, value, updated_at) VALUES (?, ?, ?)",
		key, value, nowRFC3339())
	return err
}

// SetTx 是 Set 的事务版，与 runtimeconfig.SaveTx 共享同一 *sql.Tx，
// 保证「密钥写入 + 全局配置段写入」原子提交（r11 [High]）。
func (s *Store) SetTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		"INSERT OR REPLACE INTO secrets (key, value, updated_at) VALUES (?, ?, ?)",
		key, value, nowRFC3339())
	return err
}

// GetTx 是 Get 的事务版。env rename（oldEnv→newEnv）的「读旧值」必须在同一事务内完成，
// 避免并发更新迁移到陈旧值（r12 [Medium]）。
func (s *Store) GetTx(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, "SELECT value FROM secrets WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) List(ctx context.Context) ([]Secret, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value, updated_at FROM secrets ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Secret
	for rows.Next() {
		var s Secret
		if err := rows.Scan(&s.Key, &s.Value, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Set = s.Value != ""
		items = append(items, s)
	}
	return items, rows.Err()
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM secrets WHERE key = ?", key)
	return err
}

// DeleteTx 是 Delete 的事务版（同 SetTx 的原子性用途）。
func (s *Store) DeleteTx(ctx context.Context, tx *sql.Tx, key string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM secrets WHERE key = ?", key)
	return err
}

func (s *Store) Clear(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM secrets")
	return err
}

// ClearTx 是 Clear 的事务版（同 SetTx/DeleteTx 的原子性用途）。
// 配置备份 import 的 overwrite 策略用它把「清旧密钥 + 写新密钥 + 写配置段」绑进同一事务，
// 避免 Clear 成功但后续写入失败导致密钥全丢。
func (s *Store) ClearTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM secrets")
	return err
}

func (s *Store) LoadIntoEnv(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM secrets WHERE value != ''")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		os.Setenv(key, value)
	}
	return rows.Err()
}

func MaskValue(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

// KnownKeys 收集所有已知密钥 env 名，用于 updateSecret 的白名单校验。
// webdavEnv 在 WebDAV 密码迁移到 secrets 后纳入（r15 Effective* 闭环）。
func KnownKeys(dashScopeEnv, recapEnv, asrS3Env, webdavEnv string) []string {
	var keys []string
	if dashScopeEnv != "" {
		keys = append(keys, dashScopeEnv)
	}
	if recapEnv != "" {
		keys = append(keys, recapEnv)
	}
	if asrS3Env != "" {
		keys = append(keys, asrS3Env)
	}
	if webdavEnv != "" {
		keys = append(keys, webdavEnv)
	}
	return keys
}

type SecretView struct {
	Key         string `json:"key"`
	MaskedValue string `json:"masked_value"`
	Set         bool   `json:"set"`
	Source      string `json:"source"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

func BuildView(key, dbValue string) SecretView {
	envValue := os.Getenv(key)
	source := "none"
	masked := ""
	set := false

	if dbValue != "" {
		source = "database"
		masked = MaskValue(dbValue)
		set = true
	} else if envValue != "" {
		source = "environment"
		masked = MaskValue(envValue)
		set = true
	}

	return SecretView{
		Key:         key,
		MaskedValue: masked,
		Set:         set,
		Source:      source,
	}
}

func ValidateKey(key string, knownKeys []string) error {
	for _, k := range knownKeys {
		if key == k {
			return nil
		}
	}
	return fmt.Errorf("unknown secret key: %s (allowed: %s)", key, strings.Join(knownKeys, ", "))
}
