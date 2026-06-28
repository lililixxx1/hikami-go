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
	EventPublishReverted     Event = "publish_reverted"
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

// ApplyRevertPublish 提交 EventPublishReverted：已发布专栏删除后状态 published→uploaded，
// 在**同一事务**内清空 publish_target（保留 published_at 作历史）。
func (s *Store) ApplyRevertPublish(ctx context.Context, sessionID string, taskID string) (Status, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	next, err := applyInTx(ctx, tx, sessionID, EventPublishReverted, taskID, "", "")
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
	} else if event == EventPublishReverted {
		// 删除已发布专栏：状态回退 uploaded，清空 publish_target；保留 published_at 作历史。
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions
			SET status = ?, current_task_id = ?, last_error = NULL, publish_target = NULL, updated_at = datetime('now')
			WHERE id = ?
		`, next, nullable(taskID), sessionID)
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
	// published 的唯一出口：删除已发布专栏后回退到 uploaded（本地产物仍在，可重新发布）。
	StatusPublished: {
		EventPublishReverted: StatusUploaded,
	},
}
