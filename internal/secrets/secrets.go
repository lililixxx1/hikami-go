package secrets

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
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
		"INSERT OR REPLACE INTO secrets (key, value, updated_at) VALUES (?, ?, datetime('now'))",
		key, value)
	return err
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

func (s *Store) Clear(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM secrets")
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

func KnownKeys(dashScopeEnv, recapEnv, asrS3Env string) []string {
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
