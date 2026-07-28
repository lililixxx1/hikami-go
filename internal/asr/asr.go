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
	vad         *VADProcessor // nil = 禁用(老测试不注入,零回归;2026-07-27 VAD 引入)
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

// NewHandler 创建 ASR Handler。
//
// vadProc 是可选参数(variadic):不传时禁用 VAD(老调用零回归,签名向后兼容)。
// main.go 装配时传 NewVADProcessor(cfg);cfg.VAD.Enabled=false 时 VADProcessor 存在但 HandleTask
// 内部用 cfg.VAD.Enabled 判断是否真跑 VAD。2026-07-27 引入,见 plans/plan-vad-2026-07-27.md。
func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store,
	transcriber Transcriber, glossaryStore *glossary.Store, vadProc ...*VADProcessor,
) *Handler {
	if transcriber == nil {
		transcriber = LocalTranscriber{}
	}
	h := &Handler{cfg: cfg, sessions: sessions, states: states, transcriber: transcriber, glossary: glossaryStore}
	if len(vadProc) > 0 {
		h.vad = vadProc[0]
	}
	return h
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

	// VAD:启用时尝试裁剪静音,失败回退原始音频。详见 plans/plan-vad-2026-07-27.md Phase 4。
	// 单一真相源:audio.asr.mp3 保持原始时间线不变,产出 audio.asr.trimmed.mp3 + silence_map.json,
	// ASR 返回后用 silence_map 把 result.Segments 从 trimmed 时间线平移回原始时间线,
	// 让所有下游消费者(recap/glossary/danmaku/frontend)零改动。
	asrAudioPath := audioPath
	var activeSilenceMap *SilenceMap // 非空 = 待 remap(用内存对象避免再读盘,防重入)
	silenceMapPath := ""
	if h.vad != nil && h.cfg.VAD.Enabled {
		trimmed, smap, vadErr := h.applyVAD(ctx, audioPath, sessionDir)
		if vadErr != nil {
			slog.WarnContext(ctx, "vad: fallback to original audio (processing failed)",
				"session_id", task.SessionID, "error", vadErr)
		} else if smap != nil {
			silenceMapPath = filepath.Join(sessionDir, "asr", "silence_map.json")
			if err := smap.SaveJSON(silenceMapPath); err != nil {
				slog.WarnContext(ctx, "vad: save silence_map.json failed, fallback",
					"session_id", task.SessionID, "error", err)
				// 不清理 trimmedPath(可能成功生成),但放弃 remap(无 map)
			} else {
				asrAudioPath = trimmed
				activeSilenceMap = smap
				slog.InfoContext(ctx, "vad: trimmed",
					"session_id", task.SessionID,
					"orig_ms", smap.OriginalDurationMS,
					"trimmed_ms", smap.TrimmedDurationMS,
					"ratio", smap.OutputRatio())
			}
		}
	}

	result, err := h.transcribe(ctx, task, asrAudioPath, sessionInfo)
	if err != nil {
		return err
	}

	// 反向映射:把 result.Segments 从 trimmed 时间线平移回原始时间线(只调一次,写盘前)。
	// 用内存 activeSilenceMap 防重入(qoder v2 M-2),不读盘(避免 LoadSilenceMap 额外 IO)。
	if activeSilenceMap != nil {
		activeSilenceMap.RemapSegments(result.Segments)
		slog.InfoContext(ctx, "vad: remapped segments to original timeline",
			"session_id", task.SessionID, "segment_count", len(result.Segments))
		activeSilenceMap = nil
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

// applyVAD 跑 detect → build map → trim 三步,返回 trimmed 路径 + SilenceMap。
//
// 失败时返回 error,调用方据此回退原始音频(零回归)。任何 ffmpeg error 都视为可回退。
// Trim 用 atrim+concat 按 sm 的 kept 段切(qoder C-1 关键修订),输出与 smap.TrimmedDurationMS 严格对应。
//
// Fallback 决策树(见 plans/plan-vad-2026-07-27.md §4):
//   - Detect 失败                      → error,用原始
//   - BuildSilenceMap 返回 nil          → (nil, nil),用原始(全静音或 origMS<=0)
//   - OutputRatio < MinOutputRatio      → error,用原始(防 ffmpeg bug 裁过头)
//   - Trim 失败                         → error,用原始,删残留
//   - Trimmed 文件 size==0              → error,用原始(同 normalize convertAtomic post-condition)
func (h *Handler) applyVAD(ctx context.Context, audioPath, sessionDir string) (string, *SilenceMap, error) {
	intervals, origMS, err := h.vad.Detect(ctx, audioPath)
	if err != nil {
		return "", nil, fmt.Errorf("vad detect: %w", err)
	}
	smap := h.vad.BuildSilenceMap(intervals, origMS)
	if smap == nil {
		// 全静音或 origMS<=0:不裁剪,silence_map 为 nil,用原始音频
		return "", nil, nil
	}
	if smap.OutputRatio() < h.cfg.VAD.MinOutputRatio {
		return "", nil, fmt.Errorf("vad output ratio %.3f below threshold %.3f (suspicious ffmpeg behavior)",
			smap.OutputRatio(), h.cfg.VAD.MinOutputRatio)
	}
	trimmedPath := filepath.Join(sessionDir, "asr", "audio.asr.trimmed.mp3")
	_ = os.Remove(trimmedPath) // 清理上次失败残留
	if err := h.vad.Trim(ctx, audioPath, trimmedPath, smap); err != nil {
		_ = os.Remove(trimmedPath)
		return "", nil, fmt.Errorf("vad trim: %w", err)
	}
	// 验证裁剪后文件大小非零(与 normalize convertAtomic 一致的 post-condition)
	info, err := os.Stat(trimmedPath)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(trimmedPath)
		return "", nil, fmt.Errorf("vad trim produced empty/missing output: %v", err)
	}
	return trimmedPath, smap, nil
}
