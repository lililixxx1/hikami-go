package recap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Sentinel errors.
var (
	ErrTemplateNotFound = errors.New("recap template not found")
	ErrTemplateBuiltIn  = errors.New("cannot delete built-in template")
)

// nowRFC3339 返回本地时区的 RFC3339 时间字符串，与 sessions/tasks 表的时间字段
// （time.Now().Format(time.RFC3339)）保持一致。避免 SQLite datetime('now') 返回 UTC，
// 导致前端展示与其它表时间字段相差一个时区。
func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

// SQL queries.
const (
	sqlTemplateGet        = `SELECT id, channel_id, name, system_prompt, user_format, fan_name, extra_vars, enabled, is_default, created_at, updated_at FROM recap_templates WHERE channel_id = ? AND name = ?`
	sqlTemplateGetByID    = `SELECT id, channel_id, name, system_prompt, user_format, fan_name, extra_vars, enabled, is_default, created_at, updated_at FROM recap_templates WHERE id = ?`
	sqlTemplateListGlobal = `SELECT id, channel_id, name, system_prompt, user_format, fan_name, extra_vars, enabled, is_default, created_at, updated_at FROM recap_templates WHERE channel_id = '' ORDER BY name ASC`
	sqlTemplateUpsert     = `INSERT OR REPLACE INTO recap_templates (channel_id, name, system_prompt, user_format, fan_name, extra_vars, enabled, is_default, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	sqlTemplateDelete     = `DELETE FROM recap_templates WHERE id = ?`
)

// Template represents a single recap template row.
type Template struct {
	ID           int64  `json:"id"`
	ChannelID    string `json:"channel_id"`
	Name         string `json:"name"`
	SystemPrompt string `json:"system_prompt"`
	UserFormat   string `json:"user_format"`
	FanName      string `json:"fan_name"`
	ExtraVars    string `json:"extra_vars"`
	Enabled      bool   `json:"enabled"`
	IsDefault    bool   `json:"is_default"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type TemplateExport struct {
	ChannelID  string     `json:"channel_id"`
	Templates  []Template `json:"templates"`
	ExportedAt string     `json:"exported_at"`
}

// ResolvedTemplate is the result of merging global + channel-level templates.
type ResolvedTemplate struct {
	SystemPrompt string            `json:"system_prompt"`
	UserFormat   string            `json:"user_format"`
	FanName      string            `json:"fan_name"`
	ExtraVars    map[string]string `json:"extra_vars"`
}

// TemplateStore provides access to the recap_templates table.
type TemplateStore struct {
	db *sql.DB
}

// NewTemplateStore returns a new TemplateStore backed by db.
func NewTemplateStore(db *sql.DB) *TemplateStore {
	return &TemplateStore{db: db}
}

// scanTemplate scans a row into a Template.
func scanTemplate(row *sql.Row) (*Template, error) {
	var t Template
	var enabled, isDefault int
	var systemPrompt, userFormat, fanName, extraVars sql.NullString
	err := row.Scan(&t.ID, &t.ChannelID, &t.Name, &systemPrompt, &userFormat, &fanName, &extraVars, &enabled, &isDefault, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.Enabled = enabled == 1
	t.IsDefault = isDefault == 1
	if systemPrompt.Valid {
		t.SystemPrompt = systemPrompt.String
	}
	if userFormat.Valid {
		t.UserFormat = userFormat.String
	}
	if fanName.Valid {
		t.FanName = fanName.String
	}
	if extraVars.Valid {
		t.ExtraVars = extraVars.String
	}
	return &t, nil
}

// scanTemplateRows scans rows from a query into []Template.
func scanTemplateRows(rows *sql.Rows) ([]Template, error) {
	var templates []Template
	for rows.Next() {
		var t Template
		var enabled, isDefault int
		var systemPrompt, userFormat, fanName, extraVars sql.NullString
		err := rows.Scan(&t.ID, &t.ChannelID, &t.Name, &systemPrompt, &userFormat, &fanName, &extraVars, &enabled, &isDefault, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return nil, err
		}
		t.Enabled = enabled == 1
		t.IsDefault = isDefault == 1
		if systemPrompt.Valid {
			t.SystemPrompt = systemPrompt.String
		}
		if userFormat.Valid {
			t.UserFormat = userFormat.String
		}
		if fanName.Valid {
			t.FanName = fanName.String
		}
		if extraVars.Valid {
			t.ExtraVars = extraVars.String
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

// GetGlobal returns a global template (channel_id=”) by name.
// Returns ErrTemplateNotFound if no such template exists.
func (s *TemplateStore) GetGlobal(ctx context.Context, name string) (*Template, error) {
	row := s.db.QueryRowContext(ctx, sqlTemplateGet, "", name)
	t, err := scanTemplate(row)
	if err == sql.ErrNoRows {
		return nil, ErrTemplateNotFound
	}
	return t, err
}

// GetByChannel returns a channel-level template by channelID and name.
// Returns ErrTemplateNotFound if no such template exists.
func (s *TemplateStore) GetByChannel(ctx context.Context, channelID, name string) (*Template, error) {
	row := s.db.QueryRowContext(ctx, sqlTemplateGet, channelID, name)
	t, err := scanTemplate(row)
	if err == sql.ErrNoRows {
		return nil, ErrTemplateNotFound
	}
	return t, err
}

// Resolve merges global and channel-level templates into a single ResolvedTemplate.
//
// Logic:
//  1. Fetch global template. If not found, use built-in defaults.
//  2. Fetch channel-level template.
//  3. If channel template exists and is enabled:
//     - system_prompt: use channel value if non-empty and not '__builtin__', else use global
//     - user_format: same logic
//     - fan_name: use channel value if non-empty, else use global
//     - extra_vars: merge (global as base, channel overrides)
//  4. If channel template does not exist or is disabled: use global template directly.
//  5. Replace any remaining '__builtin__' markers with actual built-in constants.
func (s *TemplateStore) Resolve(ctx context.Context, channelID, name string) (*ResolvedTemplate, error) {
	// Step 1: Get global template
	globalTmpl, err := s.GetGlobal(ctx, name)
	if err != nil && !errors.Is(err, ErrTemplateNotFound) {
		return nil, fmt.Errorf("get global template: %w", err)
	}

	// Build base from global or built-in defaults
	var baseSystemPrompt, baseUserFormat, baseFanName string
	var baseExtraVars map[string]string

	if globalTmpl != nil {
		baseSystemPrompt = globalTmpl.SystemPrompt
		baseUserFormat = globalTmpl.UserFormat
		baseFanName = globalTmpl.FanName
		baseExtraVars = parseExtraVars(globalTmpl.ExtraVars)
	} else {
		baseSystemPrompt = defaultSystemPrompt
		baseUserFormat = defaultUserFormat
		baseFanName = ""
		baseExtraVars = map[string]string{}
	}

	// Step 2: Get channel template
	channelTmpl, err := s.GetByChannel(ctx, channelID, name)
	if err != nil && !errors.Is(err, ErrTemplateNotFound) {
		return nil, fmt.Errorf("get channel template: %w", err)
	}

	// Step 3 & 4: Merge
	resolved := &ResolvedTemplate{
		FanName:   baseFanName,
		ExtraVars: baseExtraVars,
	}

	if channelTmpl != nil && channelTmpl.Enabled {
		// system_prompt: channel overrides if non-empty and not builtin marker
		if channelTmpl.SystemPrompt != "" && channelTmpl.SystemPrompt != "__builtin__" {
			resolved.SystemPrompt = channelTmpl.SystemPrompt
		} else {
			resolved.SystemPrompt = baseSystemPrompt
		}
		// user_format: same logic
		if channelTmpl.UserFormat != "" && channelTmpl.UserFormat != "__builtin__" {
			resolved.UserFormat = channelTmpl.UserFormat
		} else {
			resolved.UserFormat = baseUserFormat
		}
		// fan_name: channel overrides if non-empty
		if channelTmpl.FanName != "" {
			resolved.FanName = channelTmpl.FanName
		}
		// extra_vars: merge (channel overrides global)
		channelExtraVars := parseExtraVars(channelTmpl.ExtraVars)
		for k, v := range channelExtraVars {
			resolved.ExtraVars[k] = v
		}
	} else {
		// No channel template or disabled: use global directly
		resolved.SystemPrompt = baseSystemPrompt
		resolved.UserFormat = baseUserFormat
	}

	// Step 5: Replace any remaining __builtin__ markers with actual constants
	if resolved.SystemPrompt == "__builtin__" {
		resolved.SystemPrompt = defaultSystemPrompt
	}
	if resolved.UserFormat == "__builtin__" {
		resolved.UserFormat = defaultUserFormat
	}

	return resolved, nil
}

// Upsert inserts or replaces a template based on the (channel_id, name) unique index.
func (s *TemplateStore) Upsert(ctx context.Context, t *Template) error {
	enabled := 0
	if t.Enabled {
		enabled = 1
	}
	isDefault := 0
	if t.IsDefault {
		isDefault = 1
	}
	now := nowRFC3339()
	_, err := s.db.ExecContext(ctx, sqlTemplateUpsert,
		t.ChannelID, t.Name, t.SystemPrompt, t.UserFormat, t.FanName,
		t.ExtraVars, enabled, isDefault, now, now)
	return err
}

// Delete removes a template by ID. Returns ErrTemplateBuiltIn if the template
// has is_default=1 (built-in templates cannot be deleted).
// Returns ErrTemplateNotFound if no row was deleted.
func (s *TemplateStore) Delete(ctx context.Context, id int64) error {
	// First check if it's a built-in template
	var isDefault int
	err := s.db.QueryRowContext(ctx, `SELECT is_default FROM recap_templates WHERE id = ?`, id).Scan(&isDefault)
	if err == sql.ErrNoRows {
		return ErrTemplateNotFound
	}
	if err != nil {
		return fmt.Errorf("check template: %w", err)
	}
	if isDefault == 1 {
		return ErrTemplateBuiltIn
	}

	res, err := s.db.ExecContext(ctx, sqlTemplateDelete, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// ListGlobal returns all global templates (channel_id=”), sorted by name.
func (s *TemplateStore) ListGlobal(ctx context.Context) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, sqlTemplateListGlobal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTemplateRows(rows)
}

// ClearCustom removes all non-built-in templates.
func (s *TemplateStore) ClearCustom(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM recap_templates WHERE is_default = 0")
	return err
}

func (s *TemplateStore) ExportJSON(ctx context.Context, channelID string) ([]byte, error) {
	var templates []Template
	var err error
	if channelID == "" {
		templates, err = s.ListGlobal(ctx)
	} else {
		templates, err = s.ListByChannel(ctx, channelID)
	}
	if err != nil {
		return nil, err
	}
	for i := range templates {
		templates[i].ID = 0
		templates[i].ChannelID = channelID
		templates[i].CreatedAt = ""
		templates[i].UpdatedAt = ""
	}
	export := TemplateExport{
		ChannelID:  channelID,
		Templates:  templates,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return json.MarshalIndent(export, "", "  ")
}

func (s *TemplateStore) ImportJSON(ctx context.Context, channelID string, data []byte) (int, error) {
	var export TemplateExport
	if err := json.Unmarshal(data, &export); err != nil {
		var single Template
		if singleErr := json.Unmarshal(data, &single); singleErr != nil {
			return 0, fmt.Errorf("invalid JSON: %w", err)
		}
		export.Templates = []Template{single}
	}

	count := 0
	for _, template := range export.Templates {
		name := strings.TrimSpace(template.Name)
		if name == "" {
			name = "default"
		}
		template.ID = 0
		template.ChannelID = channelID
		template.Name = name
		template.IsDefault = false
		if !template.Enabled {
			template.Enabled = true
		}
		if err := s.Upsert(ctx, &template); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// parseExtraVars parses a JSON object string into a map[string]string.
// Returns an empty map if the string is empty or invalid JSON.

func parseExtraVars(raw string) map[string]string {
	if raw == "" || raw == "{}" {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]string{}
	}
	return m
}

// ListByChannel returns all templates for a given channel, sorted by name.
func (s *TemplateStore) ListByChannel(ctx context.Context, channelID string) ([]Template, error) {
	const q = `SELECT id, channel_id, name, system_prompt, user_format, fan_name, extra_vars, enabled, is_default, created_at, updated_at FROM recap_templates WHERE channel_id = ? ORDER BY name ASC`
	rows, err := s.db.QueryContext(ctx, q, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTemplateRows(rows)
}

// CopyFromChannel copies all channel-level templates from srcChannelID to dstChannelID.
// Copied templates have IsDefault set to false. Returns the number of templates copied.
func (s *TemplateStore) CopyFromChannel(ctx context.Context, srcChannelID, dstChannelID string) (int, error) {
	templates, err := s.ListByChannel(ctx, srcChannelID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, t := range templates {
		t.ChannelID = dstChannelID
		t.ID = 0
		t.IsDefault = false
		if err := s.Upsert(ctx, &t); err != nil {
			slog.Warn("copy template failed", "name", t.Name, "error", err)
			continue
		}
		count++
	}
	return count, nil
}
