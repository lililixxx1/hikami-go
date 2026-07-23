package glossary

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

const (
	CandidateStatusPending  = "pending"
	CandidateStatusApproved = "approved"
	CandidateStatusRejected = "rejected"
)

var (
	ErrCandidateNotFound = errors.New("glossary candidate not found")
	ErrInvalidCandidate  = errors.New("invalid glossary candidate")
)

// Candidate represents a glossary discovery candidate awaiting review.
type Candidate struct {
	ID              int64   `json:"id"`
	ChannelID       string  `json:"channel_id"`
	Term            string  `json:"term"`
	Canonical       string  `json:"canonical"`
	Category        string  `json:"category"`
	Status          string  `json:"status"`
	Confidence      float64 `json:"confidence"`
	Score           float64 `json:"score"`
	OccurrenceCount int     `json:"occurrence_count"`
	SessionCount    int     `json:"session_count"`
	FirstSessionID  string  `json:"first_session_id"`
	LastSessionID   string  `json:"last_session_id"`
	Reason          string  `json:"reason"`
	NormalizedKey   string  `json:"normalized_key"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	ReviewedAt      string  `json:"reviewed_at,omitempty"`
	// AIReview 是 Phase 5 批量复核步骤写入的 AI 复核理由(含核实结论 + 置信度说明)。
	// 空表示未复核。status 不因此改变(仍 pending,保留人工把关)。
	AIReview string `json:"ai_review,omitempty"`
}

var normalizeSpaceRe = regexp.MustCompile(`\s+`)

func (s *Store) ListCandidates(ctx context.Context, channelID string, status string) ([]Candidate, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = CandidateStatusPending
	}
	var (
		rows *sql.Rows
		err  error
	)
	if status == "all" {
		rows, err = s.db.QueryContext(ctx, `SELECT id, channel_id, term, canonical, category, status, confidence, score, occurrence_count, session_count, first_session_id, last_session_id, reason, normalized_key, created_at, updated_at, COALESCE(reviewed_at, ''), ai_review
FROM glossary_candidates
WHERE channel_id = ?
ORDER BY score DESC, updated_at DESC`, channelID)
	} else {
		if !validCandidateStatus(status) {
			return nil, fmt.Errorf("%w: invalid status %q", ErrInvalidCandidate, status)
		}
		rows, err = s.db.QueryContext(ctx, `SELECT id, channel_id, term, canonical, category, status, confidence, score, occurrence_count, session_count, first_session_id, last_session_id, reason, normalized_key, created_at, updated_at, COALESCE(reviewed_at, ''), ai_review
FROM glossary_candidates
WHERE channel_id = ? AND status = ?
ORDER BY score DESC, updated_at DESC`, channelID, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]Candidate, 0)
	for rows.Next() {
		candidate, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (s *Store) GetCandidate(ctx context.Context, id int64) (Candidate, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, channel_id, term, canonical, category, status, confidence, score, occurrence_count, session_count, first_session_id, last_session_id, reason, normalized_key, created_at, updated_at, COALESCE(reviewed_at, ''), ai_review
FROM glossary_candidates
WHERE id = ?`, id)
	candidate, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, ErrCandidateNotFound
	}
	return candidate, err
}

func (s *Store) ApproveCandidate(ctx context.Context, id int64, term string, canonical string, category string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.approveCandidateTx(ctx, tx, id, "", term, canonical, category); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateCandidateReview 写入 AI 批量复核结果(Phase 5)。
// 只更新 ai_review + confidence + canonical(AI 可能纠正),不改 status(仍 pending,保留人工把关)。
// 高置信度(>0.9)的候选项前端会高亮,提示优先 approve。
func (s *Store) UpdateCandidateReview(ctx context.Context, id int64, canonical string, confidence float64, aiReview string) error {
	canonical = strings.TrimSpace(canonical)
	aiReview = strings.TrimSpace(aiReview)
	if canonical == "" {
		return fmt.Errorf("%w: canonical empty", ErrInvalidCandidate)
	}
	now := nowRFC3339()
	res, err := s.db.ExecContext(ctx, `UPDATE glossary_candidates
SET canonical = ?, confidence = ?, ai_review = ?, updated_at = ?
WHERE id = ? AND status = ?`, canonical, confidence, aiReview, now, id, CandidateStatusPending)
	if err != nil {
		return err
	}
	// 审核code-review Minor#4:检查影响行数,0 = 候选不存在或非 pending。
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCandidateNotFound
	}
	return nil
}

func (s *Store) RejectCandidate(ctx context.Context, id int64) error {
	now := nowRFC3339()
	res, err := s.db.ExecContext(ctx, `UPDATE glossary_candidates
SET status = 'rejected',
	reviewed_at = ?,
	updated_at = ?
WHERE id = ? AND status = 'pending'`, now, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	candidate, err := s.GetCandidate(ctx, id)
	if err != nil {
		return err
	}
	if candidate.Status == CandidateStatusRejected {
		return nil
	}
	if candidate.Status == CandidateStatusApproved {
		return fmt.Errorf("%w: approved candidate cannot be rejected", ErrInvalidCandidate)
	}
	return nil
}

func (s *Store) BatchApproveCandidates(ctx context.Context, channelID string, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("%w: ids are required", ErrInvalidCandidate)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for _, id := range ids {
		if id <= 0 {
			return 0, fmt.Errorf("%w: invalid candidate id", ErrInvalidCandidate)
		}
		if err := s.approveCandidateTx(ctx, tx, id, channelID, "", "", ""); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *Store) BatchRejectCandidates(ctx context.Context, channelID string, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("%w: ids are required", ErrInvalidCandidate)
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, fmt.Errorf("%w: invalid candidate id", ErrInvalidCandidate)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := nowRFC3339()
	query := `UPDATE glossary_candidates
SET status = 'rejected',
	reviewed_at = ?,
	updated_at = ?
WHERE channel_id = ?
  AND status = 'pending'
  AND id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, 0, len(ids)+3)
	args = append(args, now, now, channelID)
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (s *Store) UpsertCandidate(ctx context.Context, channelID string, item DiscoveryItem, sessionID string) error {
	item.Term = strings.TrimSpace(item.Term)
	item.Canonical = strings.TrimSpace(item.Canonical)
	item.Category = strings.TrimSpace(item.Category)
	item.Reason = strings.TrimSpace(item.Reason)
	item.Confidence = clamp01(item.Confidence)
	if item.OccurrenceCount <= 0 {
		item.OccurrenceCount = 1
	}
	key := normalizeKey(item.Term, item.Canonical)
	if item.Term == "" || item.Canonical == "" || key == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%w: term, canonical and session_id are required", ErrInvalidCandidate)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existing Candidate
	row := tx.QueryRowContext(ctx, `SELECT id, channel_id, term, canonical, category, status, confidence, score, occurrence_count, session_count, first_session_id, last_session_id, reason, normalized_key, created_at, updated_at, COALESCE(reviewed_at, ''), ai_review
FROM glossary_candidates
WHERE channel_id = ? AND normalized_key = ?`, channelID, key)
	err = row.Scan(&existing.ID, &existing.ChannelID, &existing.Term, &existing.Canonical, &existing.Category, &existing.Status, &existing.Confidence, &existing.Score, &existing.OccurrenceCount, &existing.SessionCount, &existing.FirstSessionID, &existing.LastSessionID, &existing.Reason, &existing.NormalizedKey, &existing.CreatedAt, &existing.UpdatedAt, &existing.ReviewedAt, &existing.AIReview)
	if errors.Is(err, sql.ErrNoRows) {
		score := calculateCandidateScore(item.Confidence, item.OccurrenceCount, 1)
		now := nowRFC3339()
		_, err = tx.ExecContext(ctx, `INSERT INTO glossary_candidates
(channel_id, term, canonical, category, status, confidence, score, occurrence_count, session_count, first_session_id, last_session_id, reason, normalized_key, created_at, updated_at)
VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
			channelID, item.Term, item.Canonical, item.Category, item.Confidence, score, item.OccurrenceCount, sessionID, sessionID, item.Reason, key, now, now)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}

	occurrences := existing.OccurrenceCount + item.OccurrenceCount
	sessionCount := existing.SessionCount
	if existing.LastSessionID != sessionID {
		sessionCount++
	}
	confidence := math.Max(existing.Confidence, item.Confidence)
	term := existing.Term
	canonical := existing.Canonical
	category := existing.Category
	reason := existing.Reason
	if existing.Status == CandidateStatusPending {
		term = item.Term
		if item.Canonical != "" {
			canonical = item.Canonical
		}
		if item.Category != "" {
			category = item.Category
		}
		if item.Reason != "" {
			reason = item.Reason
		}
	}
	score := calculateCandidateScore(confidence, occurrences, sessionCount)
	_, err = tx.ExecContext(ctx, `UPDATE glossary_candidates
SET term = ?,
	canonical = ?,
	category = ?,
	confidence = ?,
	score = ?,
	occurrence_count = ?,
	session_count = ?,
	last_session_id = ?,
	reason = ?,
	updated_at = ?
WHERE id = ?`,
		term, canonical, category, confidence, score, occurrences, sessionCount, sessionID, reason, nowRFC3339(), existing.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) approveCandidateTx(ctx context.Context, tx *sql.Tx, id int64, channelID string, term string, canonical string, category string) error {
	query := `SELECT id, channel_id, term, canonical, category, status, confidence, score, occurrence_count, session_count, first_session_id, last_session_id, reason, normalized_key, created_at, updated_at, COALESCE(reviewed_at, ''), ai_review
FROM glossary_candidates
WHERE id = ?`
	args := []any{id}
	if channelID != "" {
		query += ` AND channel_id = ?`
		args = append(args, channelID)
	}
	var candidate Candidate
	err := tx.QueryRowContext(ctx, query, args...).Scan(&candidate.ID, &candidate.ChannelID, &candidate.Term, &candidate.Canonical, &candidate.Category, &candidate.Status, &candidate.Confidence, &candidate.Score, &candidate.OccurrenceCount, &candidate.SessionCount, &candidate.FirstSessionID, &candidate.LastSessionID, &candidate.Reason, &candidate.NormalizedKey, &candidate.CreatedAt, &candidate.UpdatedAt, &candidate.ReviewedAt, &candidate.AIReview)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCandidateNotFound
	}
	if err != nil {
		return err
	}
	switch candidate.Status {
	case CandidateStatusApproved:
		return nil
	case CandidateStatusRejected:
		return fmt.Errorf("%w: rejected candidate cannot be approved", ErrInvalidCandidate)
	case CandidateStatusPending:
	default:
		return fmt.Errorf("%w: invalid status %q", ErrInvalidCandidate, candidate.Status)
	}

	term = firstNonEmpty(term, candidate.Term)
	canonical = firstNonEmpty(canonical, candidate.Canonical)
	category = firstNonEmpty(category, candidate.Category)
	if strings.TrimSpace(term) == "" || strings.TrimSpace(canonical) == "" {
		return fmt.Errorf("%w: term and canonical are required", ErrInvalidCandidate)
	}
	now := nowRFC3339()
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO glossary_entries
	(channel_id, term, canonical, category, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, ?, ?)`, candidate.ChannelID, term, canonical, category, now, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE glossary_candidates
SET term = ?,
	canonical = ?,
	category = ?,
	status = 'approved',
	reviewed_at = ?,
	updated_at = ?
WHERE id = ?`, term, canonical, category, now, now, id)
	return err
}

type candidateScanner interface {
	Scan(dest ...any) error
}

func scanCandidate(row candidateScanner) (Candidate, error) {
	var candidate Candidate
	err := row.Scan(&candidate.ID, &candidate.ChannelID, &candidate.Term, &candidate.Canonical, &candidate.Category, &candidate.Status, &candidate.Confidence, &candidate.Score, &candidate.OccurrenceCount, &candidate.SessionCount, &candidate.FirstSessionID, &candidate.LastSessionID, &candidate.Reason, &candidate.NormalizedKey, &candidate.CreatedAt, &candidate.UpdatedAt, &candidate.ReviewedAt, &candidate.AIReview)
	return candidate, err
}

func validCandidateStatus(status string) bool {
	return status == CandidateStatusPending || status == CandidateStatusApproved || status == CandidateStatusRejected
}

func firstNonEmpty(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func calculateCandidateScore(confidence float64, occurrenceCount int, sessionCount int) float64 {
	c := clamp01(confidence)
	if occurrenceCount < 0 {
		occurrenceCount = 0
	}
	if sessionCount < 0 {
		sessionCount = 0
	}

	sessionFactor := math.Min(1, float64(sessionCount)/3.0)
	occurrenceFactor := math.Min(1, math.Log1p(float64(occurrenceCount))/math.Log1p(8))

	score := 0.65*c + 0.20*sessionFactor + 0.15*occurrenceFactor
	return math.Round(clamp01(score)*10000) / 10000
}

func normalizeKey(term, canonical string) string {
	base := strings.TrimSpace(canonical)
	if base == "" {
		base = strings.TrimSpace(term)
	}
	base = strings.ToLower(base)
	base = strings.Trim(base, " \t\r\n\"'`，。！？、,.!?;；:：()（）[]【】")
	base = normalizeSpaceRe.ReplaceAllString(base, "")
	return base
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
