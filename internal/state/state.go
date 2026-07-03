package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

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

	if event == EventTaskFailed {
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions
			SET status = ?, current_task_id = ?, last_error = ?, updated_at = datetime('now')
			WHERE id = ?
		`, next, nullable(taskID), nullable(errorMessage), sessionID)
	} else if event == EventUploadSucceeded {
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions
			SET status = ?, current_task_id = ?, last_error = NULL, uploaded_at = datetime('now'), updated_at = datetime('now')
			WHERE id = ?
		`, next, nullable(taskID), sessionID)
	} else if event == EventPublishSucceeded {
		if publishTarget != "" {
			_, err = tx.ExecContext(ctx, `
				UPDATE sessions
				SET status = ?, current_task_id = ?, last_error = NULL, published_at = datetime('now'), publish_target = ?, updated_at = datetime('now')
				WHERE id = ?
			`, next, nullable(taskID), publishTarget, sessionID)
		} else {
			_, err = tx.ExecContext(ctx, `
				UPDATE sessions
				SET status = ?, current_task_id = ?, last_error = NULL, published_at = datetime('now'), updated_at = datetime('now')
				WHERE id = ?
			`, next, nullable(taskID), sessionID)
		}
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions
			SET status = ?, current_task_id = ?, last_error = NULL, updated_at = datetime('now')
			WHERE id = ?
		`, next, nullable(taskID), sessionID)
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
