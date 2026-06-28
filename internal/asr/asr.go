package asr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/fsutil"
	"hikami-go/internal/glossary"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

const TaskType = "asr"

var (
	ErrSessionNotReady = errors.New("session is not ready for asr")
	ErrAudioMissing    = errors.New("asr audio is missing")
)

type Handler struct {
	cfg         *config.Config
	sessions    *session.Store
	states      *state.Store
	transcriber Transcriber
	glossary    *glossary.Store
	onSuccess   func(ctx context.Context, task worker.Task)
}

type Result struct {
	Transcript string
	SRT        string
	Segments   []map[string]any
	Raw        map[string]any
}

type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string, sessionInfo session.Session) (Result, error)
}

type VocabularyTranscriber interface {
	TranscribeWithVocabulary(ctx context.Context, audioPath string, sessionInfo session.Session, vocabulary map[string]int) (Result, error)
}

type resumableTranscriber interface {
	TranscribeWithTaskID(ctx context.Context, audioPath string, sessionInfo session.Session, taskID string) (Result, error)
}

type resumableVocabularyTranscriber interface {
	TranscribeWithTaskIDAndVocabulary(ctx context.Context, audioPath string, sessionInfo session.Session, taskID string, vocabulary map[string]int) (Result, error)
}

type LocalTranscriber struct{}

func (LocalTranscriber) Transcribe(ctx context.Context, audioPath string, sessionInfo session.Session) (Result, error) {
	return Result{
		Transcript: fmt.Sprintf("# %s\n\n（ASR 占位转写，等待接入 DashScope 结果。）\n", sessionInfo.Title),
		SRT:        "",
		Segments:   []map[string]any{},
		Raw: map[string]any{
			"provider":      "local_placeholder",
			"audio_path":    filepath.ToSlash(audioPath),
			"generated_at":  time.Now().Format(time.RFC3339),
			"session_id":    sessionInfo.ID,
			"session_title": sessionInfo.Title,
		},
	}, nil
}

func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store, transcriber Transcriber, glossaryStore *glossary.Store) *Handler {
	if transcriber == nil {
		transcriber = LocalTranscriber{}
	}
	return &Handler{cfg: cfg, sessions: sessions, states: states, transcriber: transcriber, glossary: glossaryStore}
}

func (h *Handler) SetOnSuccess(fn func(ctx context.Context, task worker.Task)) {
	h.onSuccess = fn
}

func (h *Handler) Register(pool *worker.Pool) {
	pool.Register(TaskType, h.HandleTask)
}

func (h *Handler) CreateTask(ctx context.Context, pool *worker.Pool, sessionID string) (worker.Task, error) {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return worker.Task{}, err
	}
	if sessionInfo.Status != string(state.StatusMediaReady) {
		return worker.Task{}, fmt.Errorf("%w: status must be %s, got %s", ErrSessionNotReady, state.StatusMediaReady, sessionInfo.Status)
	}
	if _, err := os.Stat(h.audioPath(sessionInfo)); err != nil {
		if os.IsNotExist(err) {
			return worker.Task{}, fmt.Errorf("%w: %s", ErrAudioMissing, h.audioPath(sessionInfo))
		}
		return worker.Task{}, err
	}
	if _, ok, err := pool.Store().ActiveBySessionAndType(ctx, sessionInfo.ID, TaskType); err != nil {
		return worker.Task{}, err
	} else if ok {
		return worker.Task{}, fmt.Errorf("%w: active asr task already exists for session %s", worker.ErrTaskConflict, sessionInfo.ID)
	}
	return pool.Enqueue(ctx, worker.CreateInput{ChannelID: sessionInfo.ChannelID, SessionID: sessionInfo.ID, Type: TaskType, Payload: "{}"})
}

func (h *Handler) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	slog.Info("asr task started", "channel_id", task.ChannelID, "session_id", task.SessionID)
	sessionInfo, err := h.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	sessionDir := h.sessionDir(sessionInfo)
	audioPath := h.audioPath(sessionInfo)
	if _, err := os.Stat(audioPath); err != nil {
		return err
	}
	if _, err := h.states.Apply(ctx, task.SessionID, state.EventASRSubmitted, task.ID, ""); err != nil {
		return err
	}
	if err := reporter.Progress(ctx, 40, "generating transcript package"); err != nil {
		return err
	}
	result, err := h.transcribe(ctx, task, audioPath, sessionInfo)
	if err != nil {
		return err
	}
	packageDir := filepath.Join(sessionDir, "package")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(packageDir, "transcript.txt"), []byte(result.Transcript), 0o644); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(packageDir, "transcript.srt"), []byte(result.SRT), 0o644); err != nil {
		return err
	}
	slog.Info("asr task completed",
		"channel_id", task.ChannelID,
		"session_id", task.SessionID,
		"output_path", filepath.ToSlash(packageDir))
	if err := fsutil.WriteJSONAtomic(filepath.Join(packageDir, "segments.json"), result.Segments, 0o644); err != nil {
		return err
	}
	if err := correctDanmakuTiming(packageDir, result.Segments); err != nil {
		return err
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(sessionDir, "asr", "result.raw.json"), result.Raw, 0o644); err != nil {
		return err
	}
	if _, err := h.states.Apply(ctx, task.SessionID, state.EventASRSucceeded, task.ID, ""); err != nil {
		return err
	}
	if h.onSuccess != nil {
		h.onSuccess(ctx, task)
	}
	return reporter.Progress(ctx, 95, "asr package completed")
}

func (h *Handler) transcribe(ctx context.Context, task worker.Task, audioPath string, sessionInfo session.Session) (Result, error) {
	payload := asrTaskPayload{}
	_ = json.Unmarshal([]byte(task.Payload), &payload)
	var vocabulary map[string]int
	if h.glossary != nil {
		var err error
		vocabulary, err = h.glossary.ExportForASRVocabulary(ctx, sessionInfo.ChannelID)
		if err != nil {
			slog.WarnContext(ctx, "export asr vocabulary failed", "channel_id", sessionInfo.ChannelID, "error", err)
			vocabulary = nil
		}
	}
	if payload.DashScopeTaskID != "" {
		if transcriber, ok := h.transcriber.(resumableVocabularyTranscriber); ok {
			return transcriber.TranscribeWithTaskIDAndVocabulary(ctx, audioPath, sessionInfo, payload.DashScopeTaskID, vocabulary)
		}
		if transcriber, ok := h.transcriber.(resumableTranscriber); ok {
			return transcriber.TranscribeWithTaskID(ctx, audioPath, sessionInfo, payload.DashScopeTaskID)
		}
	}
	if transcriber, ok := h.transcriber.(VocabularyTranscriber); ok {
		return transcriber.TranscribeWithVocabulary(ctx, audioPath, sessionInfo, vocabulary)
	}
	return h.transcriber.Transcribe(ctx, audioPath, sessionInfo)
}

type asrTaskPayload struct {
	DashScopeTaskID string `json:"dashscope_task_id"`
}

func (h *Handler) sessionDir(sessionInfo session.Session) string {
	return filepath.Join(h.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug)
}

func (h *Handler) audioPath(sessionInfo session.Session) string {
	return filepath.Join(h.sessionDir(sessionInfo), "asr", "audio.asr.mp3")
}
