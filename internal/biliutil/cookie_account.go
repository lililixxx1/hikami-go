package biliutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrAccountNotFound     = errors.New("cookie account not found")
	ErrAccountUIDDuplicate = errors.New("cookie account uid already exists")
	ErrNoDefaultAccount    = errors.New("no default cookie account configured")
	ErrInvalidCookiePath   = errors.New("invalid cookie path")
	// ErrAccountInUse: 主播的 publish/download account_id 仍指向该账号时 Delete 拒绝,
	// 避免删除后主播静默回退全局默认账号、行为难以排查(M12,2026-08-15 审核)。
	ErrAccountInUse = errors.New("cookie account is still referenced by channel publish/download settings")
)

// CookieAccount represents a B站 account stored in the database.
type CookieAccount struct {
	ID                int64  `json:"id"`
	UID               int64  `json:"uid"`
	Nickname          string `json:"nickname"`
	CookieFile        string `json:"cookie_file"`
	IsDefaultDownload bool   `json:"is_default_download"`
	IsDefaultPublish  bool   `json:"is_default_publish"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// CookieAccountStore manages B站 cookie accounts in the database.
type CookieAccountStore struct {
	db          *sql.DB
	allowedDirs []string
}

// NewCookieAccountStore creates a new store.
func NewCookieAccountStore(db *sql.DB, allowedDirs ...string) *CookieAccountStore {
	return &CookieAccountStore{db: db, allowedDirs: allowedDirs}
}

// List returns all accounts.
func (s *CookieAccountStore) List(ctx context.Context) ([]CookieAccount, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at FROM bili_cookie_accounts ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []CookieAccount
	for rows.Next() {
		var a CookieAccount
		var dl, pub int
		if err := rows.Scan(&a.ID, &a.UID, &a.Nickname, &a.CookieFile, &dl, &pub, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.IsDefaultDownload = dl == 1
		a.IsDefaultPublish = pub == 1
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// GetByID returns an account by ID.
func (s *CookieAccountStore) GetByID(ctx context.Context, id int64) (*CookieAccount, error) {
	return s.getAccount(ctx, "SELECT id, uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at FROM bili_cookie_accounts WHERE id = ?", id)
}

// GetByUID returns an account by UID.
func (s *CookieAccountStore) GetByUID(ctx context.Context, uid int64) (*CookieAccount, error) {
	return s.getAccount(ctx, "SELECT id, uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at FROM bili_cookie_accounts WHERE uid = ?", uid)
}

func (s *CookieAccountStore) getAccount(ctx context.Context, query string, arg any) (*CookieAccount, error) {
	var a CookieAccount
	var dl, pub int
	err := s.db.QueryRowContext(ctx, query, arg).Scan(&a.ID, &a.UID, &a.Nickname, &a.CookieFile, &dl, &pub, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	a.IsDefaultDownload = dl == 1
	a.IsDefaultPublish = pub == 1
	return &a, nil
}

// Create inserts a new account.
func (s *CookieAccountStore) Create(ctx context.Context, a *CookieAccount) (int64, error) {
	if err := ValidateCookiePath(a.CookieFile, s.allowedDirs); err != nil {
		return 0, err
	}
	now := time.Now().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO bili_cookie_accounts (uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		a.UID, a.Nickname, a.CookieFile, boolToInt(a.IsDefaultDownload), boolToInt(a.IsDefaultPublish), now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrAccountUIDDuplicate, err)
	}
	return result.LastInsertId()
}

// Update modifies an existing account.
func (s *CookieAccountStore) Update(ctx context.Context, a *CookieAccount) error {
	if err := ValidateCookiePath(a.CookieFile, s.allowedDirs); err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE bili_cookie_accounts SET nickname = ?, cookie_file = ?, is_default_download = ?, is_default_publish = ?, updated_at = ? WHERE id = ?",
		a.Nickname, a.CookieFile, boolToInt(a.IsDefaultDownload), boolToInt(a.IsDefaultPublish), now, a.ID,
	)
	if err != nil {
		return err
	}
	return nil
}

func ValidateCookiePath(cookiePath string, allowedDirs []string) error {
	cleaned := filepath.Clean(strings.TrimSpace(cookiePath))
	if cleaned == "." || cleaned == "" {
		return fmt.Errorf("%w: cookie_file is required", ErrInvalidCookiePath)
	}
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("%w: cookie_file must not contain ..", ErrInvalidCookiePath)
	}

	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return fmt.Errorf("%w: resolve cookie_file: %v", ErrInvalidCookiePath, err)
	}
	if len(allowedDirs) == 0 {
		return nil
	}

	for _, dir := range allowedDirs {
		cleanedDir := filepath.Clean(strings.TrimSpace(dir))
		if cleanedDir == "." || cleanedDir == "" {
			continue
		}
		absDir, err := filepath.Abs(cleanedDir)
		if err != nil {
			return fmt.Errorf("%w: resolve allowed dir: %v", ErrInvalidCookiePath, err)
		}
		if pathHasPrefix(absPath, absDir) {
			return nil
		}
	}
	return fmt.Errorf("%w: cookie_file must be under allowed directories", ErrInvalidCookiePath)
}

func pathHasPrefix(path, prefix string) bool {
	path = filepath.Clean(path)
	prefix = filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(filepath.Separator))
}

// Delete removes an account. 仍被主播的 publish_account_id/download_account_id
// 引用时返回 ErrAccountInUse(handler 映射 409,提示先解除引用)。
func (s *CookieAccountStore) Delete(ctx context.Context, id int64) error {
	rows, err := s.db.QueryContext(ctx,
		"SELECT name FROM channels WHERE publish_account_id = ? OR download_account_id = ?", id, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(names) > 0 {
		return fmt.Errorf("%w (referenced by: %s)", ErrAccountInUse, strings.Join(names, ", "))
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM bili_cookie_accounts WHERE id = ?", id)
	return err
}

// ClearAll removes all cookie accounts.
func (s *CookieAccountStore) ClearAll(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM bili_cookie_accounts")
	return err
}

// CreateImported inserts an account without cookie path validation (for config import).
func (s *CookieAccountStore) CreateImported(ctx context.Context, a *CookieAccount) (int64, error) {
	now := time.Now().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO bili_cookie_accounts (uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at) VALUES (?, ?, '', ?, ?, ?, ?)",
		a.UID, a.Nickname, boolToInt(a.IsDefaultDownload), boolToInt(a.IsDefaultPublish), now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrAccountUIDDuplicate, err)
	}
	return result.LastInsertId()
}

// GetDefaultDownload returns the default download account.
func (s *CookieAccountStore) GetDefaultDownload(ctx context.Context) (*CookieAccount, error) {
	a, err := s.getAccount(ctx, "SELECT id, uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at FROM bili_cookie_accounts WHERE is_default_download = 1 LIMIT 1", nil)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrAccountNotFound) {
		return nil, ErrNoDefaultAccount
	}
	return a, err
}

// GetDefaultPublish returns the default publish account.
func (s *CookieAccountStore) GetDefaultPublish(ctx context.Context) (*CookieAccount, error) {
	a, err := s.getAccount(ctx, "SELECT id, uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at FROM bili_cookie_accounts WHERE is_default_publish = 1 LIMIT 1", nil)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrAccountNotFound) {
		return nil, ErrNoDefaultAccount
	}
	return a, err
}

// SetDefaultDownload sets the given account as the default for download.
func (s *CookieAccountStore) SetDefaultDownload(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE bili_cookie_accounts SET is_default_download = 0 WHERE is_default_download = 1"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE bili_cookie_accounts SET is_default_download = 1, updated_at = ? WHERE id = ?", time.Now().Format(time.RFC3339), id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetDefaultPublish sets the given account as the default for publish.
func (s *CookieAccountStore) SetDefaultPublish(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE bili_cookie_accounts SET is_default_publish = 0 WHERE is_default_publish = 1"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE bili_cookie_accounts SET is_default_publish = 1, updated_at = ? WHERE id = ?", time.Now().Format(time.RFC3339), id); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveCookie finds the effective cookie for a channel+usage pair.
// It checks: channel account override → global default account → fallback to legacy cookieFile path.
func (s *CookieAccountStore) ResolveCookie(ctx context.Context, downloadAccountID sql.NullInt64, publishAccountID sql.NullInt64, usage string, fallbackCookiePath string) (*BiliCookie, error) {
	var accountID sql.NullInt64
	switch usage {
	case "download":
		accountID = downloadAccountID
	case "publish":
		accountID = publishAccountID
	default:
		return nil, fmt.Errorf("unknown cookie usage: %s", usage)
	}

	// 1. Channel override
	if accountID.Valid {
		a, err := s.GetByID(ctx, accountID.Int64)
		if err == nil && a != nil {
			cookie, loadErr := LoadCookie(a.CookieFile)
			if loadErr == nil {
				return cookie, nil
			}
			// level-1 命中但 cookie 文件不可读:告警后降级走全局默认,不再静默
			// (M12,2026-08-15 审核)。
			slog.Warn("channel cookie account unavailable, falling back to default",
				"account_id", accountID.Int64, "cookie_file", a.CookieFile, "error", loadErr)
		} else {
			slog.Warn("channel cookie account unavailable, falling back to default",
				"account_id", accountID.Int64, "error", err)
		}
	}

	// 2. Global default
	var defaultAccount *CookieAccount
	var err error
	if usage == "download" {
		defaultAccount, err = s.GetDefaultDownload(ctx)
	} else {
		defaultAccount, err = s.GetDefaultPublish(ctx)
	}
	if err == nil && defaultAccount != nil {
		return LoadCookie(defaultAccount.CookieFile)
	}

	// 3. Legacy fallback
	if fallbackCookiePath != "" {
		return LoadCookie(fallbackCookiePath)
	}

	return nil, ErrNoDefaultAccount
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
