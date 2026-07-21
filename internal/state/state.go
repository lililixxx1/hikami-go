package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// nowRFC3339 返回本地时区的 RFC3339 时间字符串，与 sessions 表其它时间字段
// （started_at/ended_at 用 time.Now().Format(time.RFC3339)）保持一致。
// 避免 SQLite datetime('now') 返回 UTC，导致同一 session 内不同时间字段时区混乱。
func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

var (
	ErrSessionNotFound   = errors.New("session not found")
	ErrInvalidTransition = errors.New("invalid session transition")
)

type Status string

const (
	StatusDiscovered   Status = "discovered"
	StatusDownloading  Status = "downloading"
	StatusRecording    Status = "recording"
	StatusImporting    Status = "importing"
	StatusMediaReady   Status = "media_ready"
	StatusASRSubmitted Status = "asr_submitted"
	StatusASRDone      Status = "asr_done"
	StatusRecapDone    Status = "recap_done"
	StatusUploaded     Status = "uploaded"
	StatusPublished    Status = "published"
	StatusFailed       Status = "failed"
)

type Event string

const (
	EventDownloadStarted     Event = "download_started"
	EventDownloadSucceeded   Event = "download_succeeded"
	EventLiveRecordStarted   Event = "live_record_started"
	EventLiveRecordSucceeded Event = "live_record_succeeded"
	EventImportStarted       Event = "import_started"
	EventImportSucceeded     Event = "import_succeeded"
	EventNormalizeSucceeded  Event = "normalize_succeeded"
	EventASRSubmitted        Event = "asr_submitted"
	EventASRSucceeded        Event = "asr_succeeded"
	EventRecapSucceeded      Event = "recap_succeeded"
	EventUploadSucceeded     Event = "upload_succeeded"
	EventPublishSucceeded    Event = "publish_succeeded"
	EventTaskFailed          Event = "task_failed"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Apply(ctx context.Context, sessionID string, event Event, taskID string, errorMessage string) (Status, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	next, err := applyInTx(ctx, tx, sessionID, event, taskID, errorMessage, "")
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return next, nil
}

// ApplyWithPublishTarget 提交 EventPublishSucceeded 并在**同一事务**内写入 publish_target，
// 避免状态先置为 published 而 publish_target 写入失败被吞（ISS-4）。
func (s *Store) ApplyWithPublishTarget(ctx context.Context, sessionID string, taskID string, publishTarget string) (Status, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	next, err := applyInTx(ctx, tx, sessionID, EventPublishSucceeded, taskID, "", publishTarget)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return next, nil
}

// applyInTx 在给定事务内读取当前状态、校验转换合法性并写入。
// publishTarget 仅对 EventPublishSucceeded 生效：非空时一并落库，空则保持原 publish_target 不变。
func applyInTx(ctx context.Context, tx *sql.Tx, sessionID string, event Event, taskID string, errorMessage string, publishTarget string) (Status, error) {
	var current Status
	err := tx.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = ?", sessionID).Scan(&current)
	if err == sql.ErrNoRows {
		return "", ErrSessionNotFound
	}
	if err != nil {
		return "", err
	}

	next, err := Next(current, event)
	if err != nil {
		return "", err
	}
	nowStr := nowRFC3339()

	if event == EventTaskFailed {
		// v7 修订(r20 BLOCKING):回退 v6 的 CAS 设计。
		// v6 CAS 假设"失败 callback 的 taskID 总是匹配 session.current_task_id",但实际
		// normalize/recap/publish 等任务在失败时 current_task_id 仍是上一阶段成功任务的 ID
		// (失败不更新 current_task_id,只有成功转换才写新 task ID)。CAS 会误判这些正常失败
		// callback 为 stale 并丢弃,导致 session 状态不更新、失败信息和 retry 入口丢失。
		//
		// v7 方案:恢复原无条件 UPDATE。reset 后的 callback 覆盖问题由两层防御处理:
		//   ① worker.go syncSessionState 的 attempt 校验(防 retry 后旧 attempt callback)
		//   ② ResetFailedSession 的 active task 守卫(防 reset 时有 pending/running task)
		// 实际触发"reset 后 callback 又把 session 写回 failed"的概率极低(需要 task 已 MarkFailed
		// 但 syncSessionState 尚未执行的几十毫秒窗口),且即使发生,用户可再次 reset。
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions
			SET status = ?, current_task_id = ?, last_error = ?, updated_at = ?
			WHERE id = ?
		`, next, nullable(taskID), nullable(errorMessage), nowStr, sessionID)
	} else if event == EventUploadSucceeded {
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions
			SET status = ?, current_task_id = ?, last_error = NULL, uploaded_at = ?, updated_at = ?
			WHERE id = ?
		`, next, nullable(taskID), nowStr, nowStr, sessionID)
	} else if event == EventPublishSucceeded {
		if publishTarget != "" {
			_, err = tx.ExecContext(ctx, `
				UPDATE sessions
				SET status = ?, current_task_id = ?, last_error = NULL, published_at = ?, publish_target = ?, updated_at = ?
				WHERE id = ?
			`, next, nullable(taskID), nowStr, publishTarget, nowStr, sessionID)
		} else {
			_, err = tx.ExecContext(ctx, `
				UPDATE sessions
				SET status = ?, current_task_id = ?, last_error = NULL, published_at = ?, updated_at = ?
				WHERE id = ?
			`, next, nullable(taskID), nowStr, nowStr, sessionID)
		}
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions
			SET status = ?, current_task_id = ?, last_error = NULL, updated_at = ?
			WHERE id = ?
		`, next, nullable(taskID), nowStr, sessionID)
	}
	if err != nil {
		return "", err
	}
	return next, nil
}

func Next(current Status, event Event) (Status, error) {
	if event == EventTaskFailed {
		return StatusFailed, nil
	}

	if next, ok := transitions[current][event]; ok {
		return next, nil
	}
	return "", fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, event)
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var transitions = map[Status]map[Event]Status{
	StatusDiscovered: {
		EventDownloadStarted:   StatusDownloading,
		EventLiveRecordStarted: StatusRecording,
		EventImportStarted:     StatusImporting,
	},
	StatusDownloading: {
		EventDownloadSucceeded:  StatusDownloading,
		EventNormalizeSucceeded: StatusMediaReady,
	},
	StatusRecording: {
		EventLiveRecordSucceeded: StatusRecording,
		EventNormalizeSucceeded:  StatusMediaReady,
	},
	StatusImporting: {
		EventImportSucceeded:    StatusImporting,
		EventNormalizeSucceeded: StatusMediaReady,
	},
	StatusFailed: {
		EventNormalizeSucceeded: StatusMediaReady,
		EventASRSubmitted:       StatusASRSubmitted,
		EventASRSucceeded:       StatusASRDone,
		EventRecapSucceeded:     StatusRecapDone,
		EventUploadSucceeded:    StatusUploaded,
		EventPublishSucceeded:   StatusPublished,
	},
	StatusMediaReady: {
		EventASRSubmitted: StatusASRSubmitted,
	},
	StatusASRSubmitted: {
		EventASRSucceeded: StatusASRDone,
	},
	StatusASRDone: {
		EventRecapSucceeded:  StatusRecapDone,
		EventUploadSucceeded: StatusUploaded,
	},
	StatusRecapDone: {
		EventUploadSucceeded:  StatusUploaded,
		EventPublishSucceeded: StatusPublished,
	},
	StatusUploaded: {
		EventRecapSucceeded:   StatusRecapDone,
		EventPublishSucceeded: StatusPublished,
	},
	// published 是终态：B站专栏只能由用户手动去 B站管理，本系统不删不改（无 publish_reverted 出口）。
	// 重新生成回顾走 recap/regenerate（覆盖本地 md，状态保持 published 不变）。
}
