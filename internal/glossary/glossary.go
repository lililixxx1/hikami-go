package glossary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// Sentinel errors.
var (
	ErrNotFound  = errors.New("glossary entry not found")
	ErrDuplicate = errors.New("glossary entry already exists")
)

// SQL queries.
const (
	sqlListGlobal    = `SELECT id, channel_id, term, canonical, category, enabled, created_at, updated_at FROM glossary_entries WHERE channel_id = '' ORDER BY term ASC`
	sqlListByChannel = `SELECT id, channel_id, term, canonical, category, enabled, created_at, updated_at FROM glossary_entries WHERE channel_id = ? ORDER BY term ASC`
	sqlUpsert        = `INSERT OR REPLACE INTO glossary_entries (channel_id, term, canonical, category, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, 1, datetime('now'), datetime('now'))`
	sqlDelete        = `DELETE FROM glossary_entries WHERE id = ?`
	sqlToggle        = `UPDATE glossary_entries SET enabled = ?, updated_at = datetime('now') WHERE id = ?`
	sqlGetNote       = `SELECT note FROM glossary_meta WHERE channel_id = ?`
	sqlSetNote       = `INSERT OR REPLACE INTO glossary_meta (channel_id, note, updated_at) VALUES (?, ?, datetime('now'))`
	sqlCountGlobal   = `SELECT COUNT(*) FROM glossary_entries WHERE channel_id = ''`
)

// Entry represents a single glossary term correction rule.
type Entry struct {
	ID        int64  `json:"id"`
	ChannelID string `json:"channel_id"`
	Term      string `json:"term"`
	Canonical string `json:"canonical"`
	Category  string `json:"category"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// MergedEntry is used by ListByChannel to annotate each entry with its source.
type MergedEntry struct {
	Entry
	Source string `json:"source"` // "global" or "channel"
}

// GlossaryExport is the JSON export structure for glossary entries.
type GlossaryExport struct {
	ChannelID  string         `json:"channel_id"`
	Entries    []GlossaryItem `json:"entries"`
	Note       string         `json:"note"`
	ExportedAt string         `json:"exported_at"`
}

// GlossaryItem represents a single entry in a JSON export/import.
type GlossaryItem struct {
	Term      string `json:"term"`
	Canonical string `json:"canonical"`
	Category  string `json:"category"`
	Enabled   bool   `json:"enabled"`
	Source    string `json:"source,omitempty"`
}

// Store provides access to the glossary_entries and glossary_meta tables.
type Store struct {
	db *sql.DB
}

// NewStore returns a new Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// scanEntry scans a row into an Entry.
func scanEntry(rows *sql.Rows) (Entry, error) {
	var e Entry
	var enabled int
	err := rows.Scan(&e.ID, &e.ChannelID, &e.Term, &e.Canonical, &e.Category, &enabled, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	e.Enabled = enabled == 1
	return e, nil
}

// queryEntries executes the given query (with optional args) and returns all rows as []Entry.
func (s *Store) queryEntries(ctx context.Context, query string, args ...any) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ListGlobal returns all global glossary entries (channel_id=”), including disabled ones, sorted by term.
func (s *Store) ListGlobal(ctx context.Context) ([]Entry, error) {
	return s.queryEntries(ctx, sqlListGlobal)
}

// ListByChannel returns the merged view of global + channel-level glossary entries.
//
// Merge rules:
//   - Global entries are included unless blocked by a channel-level entry with the same term and enabled=false.
//   - Channel-level entries with enabled=true override the global entry for the same term.
//   - Channel-level entries with enabled=false and matching a global term are not included themselves;
//     they only serve to block the global entry.
//   - Channel-level entries with enabled=false that do NOT match any global term are also excluded.
func (s *Store) ListByChannel(ctx context.Context, channelID string) ([]MergedEntry, error) {
	globals, err := s.queryEntries(ctx, sqlListGlobal)
	if err != nil {
		return nil, fmt.Errorf("list global entries: %w", err)
	}

	locals, err := s.queryEntries(ctx, sqlListByChannel, channelID)
	if err != nil {
		return nil, fmt.Errorf("list channel entries: %w", err)
	}

	// Build a map of channel-level entries keyed by term.
	localMap := make(map[string]Entry, len(locals))
	for _, e := range locals {
		localMap[e.Term] = e
	}
	globalTerms := make(map[string]struct{}, len(globals))
	for _, e := range globals {
		globalTerms[e.Term] = struct{}{}
	}

	var result []MergedEntry

	// First pass: add global entries unless blocked by a disabled local entry.
	for _, g := range globals {
		if loc, ok := localMap[g.Term]; ok {
			if !loc.Enabled {
				// Channel-level disabled entry blocks the global one.
				continue
			}
			// Channel-level enabled entry overrides global.
			result = append(result, MergedEntry{Entry: loc, Source: "channel"})
			continue
		}
		result = append(result, MergedEntry{Entry: g, Source: "global"})
	}

	// Second pass: add channel-level entries whose term does not exist in globals.
	for _, loc := range locals {
		if !loc.Enabled {
			continue
		}
		if _, found := globalTerms[loc.Term]; !found {
			result = append(result, MergedEntry{Entry: loc, Source: "channel"})
		}
	}

	return result, nil
}

// Upsert inserts or replaces a glossary entry based on the (channel_id, term) unique index.
func (s *Store) Upsert(ctx context.Context, channelID, term, canonical, category string) error {
	_, err := s.db.ExecContext(ctx, sqlUpsert, channelID, term, canonical, category)
	return err
}

// Delete removes a glossary entry by ID. Returns ErrNotFound if no row was deleted.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, sqlDelete, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Toggle switches the enabled flag of a glossary entry. Returns ErrNotFound if no row was updated.
func (s *Store) Toggle(ctx context.Context, id int64, enabled bool) error {
	res, err := s.db.ExecContext(ctx, sqlToggle, enabled, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetNote returns the free-text note for a channel from glossary_meta.
// Returns an empty string (not an error) if no note exists.
func (s *Store) GetNote(ctx context.Context, channelID string) (string, error) {
	var note string
	err := s.db.QueryRowContext(ctx, sqlGetNote, channelID).Scan(&note)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return note, err
}

// SetNote stores or replaces the free-text note for a channel.
func (s *Store) SetNote(ctx context.Context, channelID, note string) error {
	_, err := s.db.ExecContext(ctx, sqlSetNote, channelID, note)
	return err
}

// ExportForPrompt generates a formatted Markdown string for AI prompt injection.
// It merges global + channel-level entries (same logic as ListByChannel), keeps only enabled ones,
// formats them as a Markdown table, and appends the channel note if non-empty.
// Returns an empty string if there are no enabled entries and no note.
func (s *Store) ExportForPrompt(ctx context.Context, channelID string) (string, error) {
	merged, err := s.ListByChannel(ctx, channelID)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	first := true

	for _, m := range merged {
		if !m.Enabled {
			continue
		}
		if first {
			sb.WriteString("| 错误写法 | 正确写法 | 分类 |\n")
			sb.WriteString("|---|---|---|\n")
			first = false
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", m.Term, m.Canonical, m.Category))
	}

	note, err := s.GetNote(ctx, channelID)
	if err != nil {
		return "", err
	}
	if note != "" {
		if !first {
			sb.WriteString("\n")
		}
		sb.WriteString(note)
	}

	return strings.TrimSpace(sb.String()), nil
}

// ExportForASRVocabulary returns merged enabled glossary terms as Fun-ASR hotwords.
func (s *Store) ExportForASRVocabulary(ctx context.Context, channelID string) (map[string]int, error) {
	merged, err := s.ListByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}

	vocabulary := make(map[string]int)
	for _, item := range merged {
		if !item.Enabled {
			continue
		}
		word := strings.TrimSpace(item.Canonical)
		if word == "" {
			word = strings.TrimSpace(item.Term)
		}
		if word == "" {
			continue
		}
		vocabulary[word] = 4
	}
	if len(vocabulary) == 0 {
		return nil, nil
	}
	return vocabulary, nil
}

// CountGlobal returns the total number of global glossary entries.
func (s *Store) CountGlobal(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, sqlCountGlobal).Scan(&count)
	return count, err
}

// Markdown parsing patterns.
var (
	reHeading   = regexp.MustCompile(`^#{1,6}\s+(.+)$`)
	reSeparator = regexp.MustCompile(`^\|[\s\-:]+\|$`)
	reTableRow  = regexp.MustCompile(`^\|.*\|$`)
)

// ImportMarkdown parses a Markdown-formatted glossary and imports entries into the database.
// It supports heading-based categories and pipe-delimited tables.
// Returns the number of entries imported.
func (s *Store) ImportMarkdown(ctx context.Context, channelID, content string) (int, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, nil
	}

	lines := strings.Split(content, "\n")
	currentCategory := ""
	var count int

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Heading → update current category
		if m := reHeading.FindStringSubmatch(line); m != nil {
			currentCategory = strings.TrimSpace(m[1])
			continue
		}

		// Separator line → skip
		if reSeparator.MatchString(line) {
			continue
		}

		// Table row → parse
		if reTableRow.MatchString(line) {
			cols := strings.Split(line, "|")
			// cols[0] is empty (before first |), cols[1..] are actual columns
			// We need at least 4 columns: | term | canonical | category |
			if len(cols) < 4 {
				continue
			}

			firstCol := strings.TrimSpace(cols[1])
			// Skip header rows that contain "ASR" or "误识别"
			if strings.Contains(firstCol, "ASR") || strings.Contains(firstCol, "误识别") {
				continue
			}

			term := strings.TrimSpace(cols[1])
			canonical := strings.TrimSpace(cols[2])
			category := strings.TrimSpace(cols[3])

			// Use heading-based category if column is empty
			if category == "" {
				category = currentCategory
			}

			// Skip if term or canonical is empty
			if term == "" || canonical == "" {
				continue
			}

			// Split canonical by / and take the first value
			canonicalParts := strings.Split(canonical, "/")
			canonical = strings.TrimSpace(canonicalParts[0])

			// Skip if term == canonical after trimming
			if strings.TrimSpace(term) == canonical {
				continue
			}

			// Split term by / for multiple variants
			variants := strings.Split(term, "/")
			for _, v := range variants {
				v = strings.TrimSpace(v)
				if v == "" {
					continue
				}
				// Skip variant that equals canonical
				if v == canonical {
					continue
				}
				if _, err := tx.ExecContext(ctx, sqlUpsert, channelID, v, canonical, category); err != nil {
					return 0, fmt.Errorf("upsert %q: %w", v, err)
				}
				count++
			}
		}
	}

	return count, tx.Commit()
}

// ClearAll removes all glossary entries and meta.
func (s *Store) ClearAll(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM glossary_entries"); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM glossary_meta")
	return err
}

// ExportJSON exports glossary entries as a JSON document.
// If channelID is empty, global entries are exported; otherwise channel-specific entries are exported.
func (s *Store) ExportJSON(ctx context.Context, channelID string) ([]byte, error) {
	var entries []GlossaryItem

	if channelID == "" {
		raw, err := s.ListGlobal(ctx)
		if err != nil {
			return nil, fmt.Errorf("list global entries: %w", err)
		}
		for _, e := range raw {
			entries = append(entries, GlossaryItem{
				Term:      e.Term,
				Canonical: e.Canonical,
				Category:  e.Category,
				Enabled:   e.Enabled,
			})
		}
	} else {
		merged, err := s.ListByChannel(ctx, channelID)
		if err != nil {
			return nil, fmt.Errorf("list channel entries: %w", err)
		}
		for _, m := range merged {
			entries = append(entries, GlossaryItem{
				Term:      m.Term,
				Canonical: m.Canonical,
				Category:  m.Category,
				Enabled:   m.Enabled,
				Source:    m.Source,
			})
		}
	}

	note, err := s.GetNote(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("get note: %w", err)
	}

	export := GlossaryExport{
		ChannelID:  channelID,
		Entries:    entries,
		Note:       note,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}

	return json.MarshalIndent(export, "", "  ")
}

// ImportJSON imports glossary entries from a JSON document.
// Validates that each entry has non-empty term and canonical fields.
// Returns the number of entries imported.
func (s *Store) ImportJSON(ctx context.Context, channelID string, data []byte) (int, error) {
	var export GlossaryExport
	if err := json.Unmarshal(data, &export); err != nil {
		return 0, fmt.Errorf("invalid JSON: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	count := 0
	for _, item := range export.Entries {
		term := strings.TrimSpace(item.Term)
		canonical := strings.TrimSpace(item.Canonical)
		if term == "" || canonical == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, sqlUpsert, channelID, term, canonical, item.Category); err != nil {
			return 0, fmt.Errorf("upsert %q: %w", term, err)
		}
		count++
	}

	if strings.TrimSpace(export.Note) != "" {
		if _, err := tx.ExecContext(ctx, sqlSetNote, channelID, strings.TrimSpace(export.Note)); err != nil {
			return 0, fmt.Errorf("set note: %w", err)
		}
	}

	return count, tx.Commit()
}

// DeleteByIDs batch-deletes glossary entries matching the given IDs and channelID.
// Uses transactions with batchSize=900 to avoid SQLite parameter limits.
func (s *Store) DeleteByIDs(ctx context.Context, channelID string, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	const batchSize = 900
	total := 0
	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[i:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		query := fmt.Sprintf("DELETE FROM glossary_entries WHERE channel_id = ? AND id IN (%s)", placeholders)
		args := make([]any, 0, 1+len(batch))
		args = append(args, channelID)
		for _, id := range batch {
			args = append(args, id)
		}
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, fmt.Errorf("delete batch: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, tx.Commit()
}

// ToggleByIDs batch-toggles the enabled flag of glossary entries matching the given IDs and channelID.
// Uses transactions with batchSize=900 to avoid SQLite parameter limits.
func (s *Store) ToggleByIDs(ctx context.Context, channelID string, ids []int64, enabled bool) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	const batchSize = 900
	total := 0
	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[i:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		query := fmt.Sprintf("UPDATE glossary_entries SET enabled = ?, updated_at = datetime('now') WHERE channel_id = ? AND id IN (%s)", placeholders)
		args := make([]any, 0, 2+len(batch))
		args = append(args, enabled)
		args = append(args, channelID)
		for _, id := range batch {
			args = append(args, id)
		}
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, fmt.Errorf("toggle batch: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, tx.Commit()
}

// CopyFromChannel 将源主播的术语表条目复制到目标主播。
// 仅复制 source 为 "channel" 的条目（跳过全局条目）。
// 返回成功复制的条目数。
func (s *Store) CopyFromChannel(ctx context.Context, srcChannelID, dstChannelID string) (int, error) {
	merged, err := s.ListByChannel(ctx, srcChannelID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, m := range merged {
		if m.Source != "channel" {
			continue
		}
		if err := s.Upsert(ctx, dstChannelID, m.Term, m.Canonical, m.Category); err != nil {
			slog.Warn("copy glossary entry failed", "term", m.Term, "error", err)
			continue
		}
		count++
	}
	return count, nil
}
