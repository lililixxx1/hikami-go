package worker

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

var (
	ErrTaskNotFound = errors.New("task not found")
	ErrInvalidTask  = errors.New("invalid task")
	ErrTaskConflict = errors.New("task conflict")
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Task struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
	SessionID  string `json:"session_id,omitempty"`
	Type       string `json:"type"`
	Status     Status `json:"status"`
	Payload    string `json:"payload"`
	Progress   int    `json:"progress"`
	Message    string `json:"message"`
	Error      string `json:"error,omitempty"`
	Attempt    int    `json:"attempt"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	// BypassFailState 为 true 时，任务失败不降级 session 主状态（仅写 last_error）。
	// 用于"重新生成回顾"等非推进型任务：published/recap_done 状态下重跑 recap，
	// 失败不应把已发布/已生成的 session 打成 failed。与类型级 WithBypassFailState 取 OR。
	BypassFailState bool `json:"bypass_fail_state,omitempty"`
}

type CreateInput struct {
	ChannelID string
	SessionID string
	Type      string
	Payload   string
	// BypassFailState 置 true 时，入队任务失败不降级 session 主状态（见 Task.BypassFailState）。
	BypassFailState bool
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Task, error) {
	if err := validateCreate(input); err != nil {
		return Task{}, err
	}
	taskID, err := newTaskID()
	if err != nil {
		return Task{}, err
	}
	payload := input.Payload
	if strings.TrimSpace(payload) == "" {
		payload = "{}"
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO tasks (
			id,
			channel_id,
			session_id,
			type,
			status,
			payload,
			bypass_fail_state
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, taskID, input.ChannelID, nullable(input.SessionID), input.Type, StatusPending, payload, boolToInt(input.BypassFailState))
	if err != nil {
		return Task{}, err
	}
	return s.Get(ctx, taskID)
}

func (s *Store) List(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, listSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if tasks == nil {
		return []Task{}, nil
	}
	return tasks, nil
}

func (s *Store) Get(ctx context.Context, id string) (Task, error) {
	task, err := scanTask(s.db.QueryRowContext(ctx, getSQL, id))
	if err == sql.ErrNoRows {
		return Task{}, ErrTaskNotFound
	}
	return task, err
}

func (s *Store) ActiveBySessionAndType(ctx context.Context, sessionID string, taskType string) (Task, bool, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(taskType) == "" {
		return Task{}, false, fmt.Errorf("%w: session_id and type are required", ErrInvalidTask)
	}
	task, err := scanTask(s.db.QueryRowContext(ctx, activeBySessionAndTypeSQL, sessionID, taskType, StatusPending, StatusRunning))
	if err == sql.ErrNoRows {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	return task, true, nil
}

// ListRunning 返回所有状态为 running 的任务。
func (s *Store) ListRunning(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, listRunningSQL, StatusRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if tasks == nil {
		return []Task{}, nil
	}
	return tasks, nil
}

func (s *Store) MarkRunning(ctx context.Context, id string) (Task, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, started_at = COALESCE(started_at, datetime('now')), updated_at = datetime('now')
		WHERE id = ? AND status = ?
	`, StatusRunning, id, StatusPending)
	if err != nil {
		return Task{}, err
	}
	if err := requireAffected(result); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

// ResetToPending 将任务重置为 pending 状态，同时重置 started_at、progress 和 attempt 计数。
// 用于服务重启时恢复可重新执行的任务。
func (s *Store) ResetToPending(ctx context.Context, id string) (Task, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, progress = 0, message = '', error = NULL, attempt = attempt + 1,
			started_at = NULL, finished_at = NULL, updated_at = datetime('now')
		WHERE id = ? AND status = ?
	`, StatusPending, id, StatusRunning)
	if err != nil {
		return Task{}, err
	}
	if err := requireAffected(result); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) UpdateProgress(ctx context.Context, id string, progress int, message string) (Task, error) {
	if progress < 0 || progress > 100 {
		return Task{}, fmt.Errorf("%w: progress must be between 0 and 100", ErrInvalidTask)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET progress = ?, message = ?, updated_at = datetime('now')
		WHERE id = ? AND status = ?
	`, progress, message, id, StatusRunning)
	if err != nil {
		return Task{}, err
	}
	if err := requireAffected(result); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) MarkSucceeded(ctx context.Context, id string, message string) (Task, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, progress = 100, message = ?, error = NULL, finished_at = datetime('now'), updated_at = datetime('now')
		WHERE id = ? AND status = ?
	`, StatusSucceeded, message, id, StatusRunning)
	if err != nil {
		return Task{}, err
	}
	if err := requireAffected(result); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) MarkFailed(ctx context.Context, id string, message string, taskError error) (Task, error) {
	errorMessage := ""
	if taskError != nil {
		errorMessage = taskError.Error()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, message = ?, error = ?, finished_at = datetime('now'), updated_at = datetime('now')
		WHERE id = ? AND status IN (?, ?)
	`, StatusFailed, message, nullable(errorMessage), id, StatusPending, StatusRunning)
	if err != nil {
		return Task{}, err
	}
	if err := requireAffected(result); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Retry(ctx context.Context, id string) (Task, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, progress = 0, message = '', error = NULL, attempt = attempt + 1,
			started_at = NULL, finished_at = NULL, updated_at = datetime('now')
		WHERE id = ? AND status = ?
	`, StatusPending, id, StatusFailed)
	if err != nil {
		return Task{}, err
	}
	if err := requireAffected(result); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Cancel(ctx context.Context, id string) (Task, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, message = 'cancelled', finished_at = datetime('now'), updated_at = datetime('now')
		WHERE id = ? AND status IN (?, ?)
	`, StatusCancelled, id, StatusPending, StatusRunning)
	if err != nil {
		return Task{}, err
	}
	if err := requireAffected(result); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

func (s *Store) DeleteByStatus(ctx context.Context, status Status) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE status = ?`, status)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) DeleteBySession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE session_id = ?`, sessionID)
	return err
}

func (s *Store) DeleteByFailedSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE session_id IN (SELECT id FROM sessions WHERE status = 'failed')`)
	return err
}

// RecoverRunning 统一将所有 running 状态任务标记为 failed。
// 保留此方法作为通用降级方案，Pool.RecoverRunning 提供更精细的分类型恢复。
func (s *Store) RecoverRunning(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, message = 'interrupted by service restart', error = 'task was running during startup',
			finished_at = datetime('now'), updated_at = datetime('now')
		WHERE status = ?
	`, StatusFailed, StatusRunning)
	return err
}

// isProcessAlive 检查指定 PID 的进程是否仍在运行。
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// 发送信号 0 不会杀死进程，仅检查是否存在
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// parsePIDFromMessage 尝试从任务 message 中提取 ffmpeg PID。
// 如果无法提取或格式不匹配，返回 0。
func parsePIDFromMessage(message string) int {
	message = strings.TrimSpace(message)
	pid, err := strconv.Atoi(message)
	if err == nil && pid > 0 {
		return pid
	}
	const marker = "pid:"
	index := strings.Index(message, marker)
	if index < 0 {
		return 0
	}
	value := strings.TrimLeft(message[index+len(marker):], " \t")
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	pid, err = strconv.Atoi(value[:end])
	if err == nil && pid > 0 {
		return pid
	}
	return 0
}

func validateCreate(input CreateInput) error {
	if strings.TrimSpace(input.ChannelID) == "" {
		return fmt.Errorf("%w: channel_id is required", ErrInvalidTask)
	}
	if strings.TrimSpace(input.Type) == "" {
		return fmt.Errorf("%w: type is required", ErrInvalidTask)
	}
	return nil
}

func newTaskID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "task_" + hex.EncodeToString(bytes[:]), nil
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrTaskConflict
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(row scanner) (Task, error) {
	var task Task
	var sessionID sql.NullString
	var errorMessage sql.NullString
	var startedAt sql.NullString
	var finishedAt sql.NullString
	err := row.Scan(
		&task.ID,
		&task.ChannelID,
		&sessionID,
		&task.Type,
		&task.Status,
		&task.Payload,
		&task.Progress,
		&task.Message,
		&errorMessage,
		&task.Attempt,
		&startedAt,
		&finishedAt,
		&task.CreatedAt,
		&task.UpdatedAt,
		&task.BypassFailState,
	)
	task.SessionID = sessionID.String
	task.Error = errorMessage.String
	task.StartedAt = startedAt.String
	task.FinishedAt = finishedAt.String
	return task, err
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// boolToInt 把 bool 转成 SQLite 的整数布尔表示（1/0）。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const selectTaskColumns = `
	id,
	channel_id,
	session_id,
	type,
	status,
	payload,
	progress,
	message,
	error,
	attempt,
	started_at,
	finished_at,
	created_at,
	updated_at,
	bypass_fail_state
`

const listSQL = `SELECT ` + selectTaskColumns + ` FROM tasks ORDER BY created_at DESC, id DESC`
const getSQL = `SELECT ` + selectTaskColumns + ` FROM tasks WHERE id = ?`
const listRunningSQL = `SELECT ` + selectTaskColumns + ` FROM tasks WHERE status = ? ORDER BY created_at ASC`
const activeBySessionAndTypeSQL = `SELECT ` + selectTaskColumns + `
FROM tasks
WHERE session_id = ? AND type = ? AND status IN (?, ?)
ORDER BY created_at DESC, id DESC
LIMIT 1`

// RecentFailedTasks returns the most recent failed tasks up to limit.
func (s *Store) RecentFailedTasks(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+selectTaskColumns+` FROM tasks
		WHERE status = ? ORDER BY updated_at DESC LIMIT ?
	`, string(StatusFailed), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if tasks == nil {
		return []Task{}, nil
	}
	return tasks, nil
}

// TaskSummary returns count of tasks grouped by status.
func (s *Store) TaskSummary(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM tasks GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if rows.Scan(&status, &count) == nil {
			result[status] = count
		}
	}
	return result, nil
}
