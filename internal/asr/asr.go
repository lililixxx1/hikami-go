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
	ErrSessionNotReady  = errors.New("session is not ready for asr")
	ErrAudioMissing     = errors.New("asr audio is missing")
	ErrNoSpeechDetected = errors.New("inaSpeechSegmenter detected no speech; asr skipped")
)

type Handler struct {
	cfg           *config.Config
	sessions      *session.Store
	states        *state.Store
	transcriber   Transcriber
	glossary      *glossary.Store
	vad           *VADProcessor // nil = 禁用(老测试不注入,零回归;2026-07-27 VAD 引入)
	onSuccess     func(ctx context.Context, task worker.Task)
	payloadWriter taskPayloadWriter // nil 时 submit 后不持久化 taskID(仅 WARN,行为=修复前;ISSUE-006)
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

// submittingTranscriber 由支持「提交→持久化→等待」两阶段的转写器实现(当前仅
// DashScopeTranscriber)。拆分目的:submit 成功后立即把远端任务 ID 持久化进
// worker 任务 payload,崩溃恢复重入时走 await 轮询既有远端任务而非重新提交
// (ISSUE-006,见 plans/plan-issue006-dashscope-taskid-persist-2026-08-16.md)。
type submittingTranscriber interface {
	SubmitASRTask(ctx context.Context, audioPath string, sessionInfo session.Session, vocabulary map[string]int) (string, error)
	AwaitASRTask(ctx context.Context, taskID string, sessionInfo session.Session) (Result, error)
}

// taskPayloadWriter 是持久化任务 payload 的最小接口(照 publisher M11 范式,
// 避免 asr 反向持有整个 worker.Store 构造依赖),main.go 注入 workerPool.Store()。
type taskPayloadWriter interface {
	UpdatePayload(ctx context.Context, id string, payload string) error
}

type VocabularyTranscriber interface {
	TranscribeWithVocabulary(ctx context.Context, audioPath string, sessionInfo session.Session, vocabulary map[string]int) (Result, error)
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

// SetTaskPayloadWriter 注入任务 payload 写入能力(main.go 用 workerPool.Store(),
// ISSUE-006:submit 成功后立即持久化 dashscope_task_id,崩溃恢复重入走 await)。
func (h *Handler) SetTaskPayloadWriter(w taskPayloadWriter) {
	h.payloadWriter = w
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
	// G-1(2026-08-16,对齐 M11):活跃检查 + 创建原子化。旧「先 ActiveBySessionAndType
	// 查、再 Enqueue 插」两步在竞态下会创建重复 asr 任务——重复任务在 Apply(EventASRSubmitted)
	// 同步点失败,把正在转写(或已 asr_done)的 session 经 EventTaskFailed 降级,状态闪断。
	// created=false 即已有活跃任务,维持 409 语义。
	task, created, err := pool.EnqueueIfNoActive(ctx, worker.CreateInput{ChannelID: sessionInfo.ChannelID, SessionID: sessionInfo.ID, Type: TaskType, Payload: "{}"})
	if err != nil {
		return worker.Task{}, err
	}
	if !created {
		return worker.Task{}, fmt.Errorf("%w: active asr task already exists for session %s", worker.ErrTaskConflict, sessionInfo.ID)
	}
	return task, nil
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
	skipNoSpeech := false
	if h.vad != nil && h.cfg.VAD.Enabled {
		trimmed, smap, vadErr := h.applyVAD(ctx, audioPath, sessionDir)
		if errors.Is(vadErr, ErrNoSpeechDetected) {
			skipNoSpeech = true
			slog.InfoContext(ctx, "ina vad: no speech detected, skip paid asr",
				"session_id", task.SessionID)
		} else if vadErr != nil {
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

	var result Result
	if skipNoSpeech {
		result = Result{
			Transcript: "（inaSpeechSegmenter 未检测到可转写的说话片段，已跳过云端 ASR。）\n",
			SRT:        "",
			Segments:   []map[string]any{},
			Raw: map[string]any{
				"provider": "ina_skip",
				"reason":   ErrNoSpeechDetected.Error(),
			},
		}
	} else {
		var err error
		result, err = h.transcribe(ctx, task, asrAudioPath, sessionInfo)
		if err != nil {
			return err
		}
	}

	// 反向映射:把 result.Segments 从 trimmed 时间线平移回原始时间线(只调一次,写盘前)。
	// 用内存 activeSilenceMap 防重入(qoder v2 M-2),不读盘(避免 LoadSilenceMap 额外 IO)。
	if activeSilenceMap != nil {
		remapResultTimeline(&result, activeSilenceMap)
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
	if h.onSuccess != nil && !skipNoSpeech {
		h.onSuccess(ctx, task)
	}
	if skipNoSpeech {
		return reporter.Progress(ctx, 95, "no speech detected; paid asr skipped")
	}
	return reporter.Progress(ctx, 95, "asr package completed")
}

func remapResultTimeline(result *Result, sm *SilenceMap) {
	if result == nil || sm == nil {
		return
	}
	sm.RemapSegments(result.Segments)
	// DashScope 在裁剪后的时间线上生成 SRT。segments remap 后必须同步重建，
	// 否则 transcript.srt 与 segments.json/原直播时间线不一致。
	result.SRT = buildSRT(result.Segments)
}

// transcribe 是付费安全的转写决策树(ISSUE-006,2026-08-16):
//
//  1. payload 有 dashscope_task_id(崩溃恢复重入)→ await 轮询既有远端任务(零付费)。
//     远端已终态失败(ErrDashScopeTaskDead)→ 落到 2 重新提交(旧任务已死,重提交合法);
//     网络/超时等瞬态错误 → fail-closed 直接失败——远端任务可能仍在运行,静默重提交=双付费。
//     人工 retry 保留 payload 里的 taskID,重新进入本方法继续 await。
//  2. 新提交 → SubmitASRTask → 立即持久化 taskID → await。此步的 await 返回任何错误
//     (含 ErrDashScopeTaskDead)一律失败——dead-fallback 仅限第 1 步,否则「提交后立刻
//     FAILED」的远端任务会在同一 attempt 内 submit→dead→submit 无限重提交付费任务。
//  3. 转写器不支持两阶段(如 LocalTranscriber)→ 原有单次调用路径(零回归)。
//     payload 带 ID 而转写器无两阶段能力时打 WARN 再降级(消除静默重付费入口)。
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
	submitter, canSubmit := h.transcriber.(submittingTranscriber)
	if payload.DashScopeTaskID != "" {
		if canSubmit {
			result, err := submitter.AwaitASRTask(ctx, payload.DashScopeTaskID, sessionInfo)
			if err == nil {
				return result, nil
			}
			if !errors.Is(err, ErrDashScopeTaskDead) {
				return Result{}, err
			}
			slog.WarnContext(ctx, "asr: remote dashscope task dead, resubmitting",
				"session_id", task.SessionID, "dashscope_task_id", payload.DashScopeTaskID, "error", err)
		} else {
			slog.WarnContext(ctx, "asr: task payload has dashscope_task_id but transcriber does not support resume; falling back to full transcribe",
				"session_id", task.SessionID, "dashscope_task_id", payload.DashScopeTaskID)
		}
	}
	if canSubmit {
		taskID, err := submitter.SubmitASRTask(ctx, audioPath, sessionInfo, vocabulary)
		if err != nil {
			return Result{}, err
		}
		h.persistDashScopeTaskID(ctx, task, taskID)
		return submitter.AwaitASRTask(ctx, taskID, sessionInfo)
	}
	if transcriber, ok := h.transcriber.(VocabularyTranscriber); ok {
		return transcriber.TranscribeWithVocabulary(ctx, audioPath, sessionInfo, vocabulary)
	}
	return h.transcriber.Transcribe(ctx, audioPath, sessionInfo)
}

// persistDashScopeTaskID 把远端任务 ID 写进 worker 任务 payload(unmarshal→set→
// 覆盖写;当前 asrTaskPayload 仅 dashscope_task_id 一个字段,asr 任务 payload 无其他
// 写入方,round-trip 无丢失面)。best-effort:失败仅 WARN 不中断——中断会浪费已提交的
// 付费任务;代价是崩溃后失去恢复锚点,重入会重新提交(残余窗口,见 KNOWN_ISSUES.md ISSUE-006)。
func (h *Handler) persistDashScopeTaskID(ctx context.Context, task worker.Task, taskID string) {
	if h.payloadWriter == nil {
		slog.WarnContext(ctx, "asr: payload writer not injected, dashscope task id not persisted (crash before completion would resubmit)",
			"task_id", task.ID, "dashscope_task_id", taskID)
		return
	}
	payload := asrTaskPayload{}
	_ = json.Unmarshal([]byte(task.Payload), &payload)
	payload.DashScopeTaskID = taskID
	data, err := json.Marshal(payload)
	if err != nil {
		slog.WarnContext(ctx, "asr: marshal task payload failed", "task_id", task.ID, "error", err)
		return
	}
	if err := h.payloadWriter.UpdatePayload(ctx, task.ID, string(data)); err != nil {
		slog.WarnContext(ctx, "asr: persist dashscope task id failed (crash before completion would resubmit)",
			"task_id", task.ID, "dashscope_task_id", taskID, "error", err)
	}
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
	if h.cfg.VAD.EffectiveEngine() == "ina" {
		return h.applyInaVAD(ctx, audioPath, sessionDir)
	}
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

func (h *Handler) applyInaVAD(ctx context.Context, audioPath, sessionDir string) (string, *SilenceMap, error) {
	inaResultPath := filepath.Join(sessionDir, "asr", "ina_segments.json")
	segments, origMS, err := h.vad.DetectInaSpeech(ctx, audioPath, inaResultPath)
	if err != nil {
		return "", nil, err
	}
	smap := h.vad.BuildInaSpeechMap(segments, origMS)
	if smap == nil || len(smap.KeptSegments) == 0 {
		return "", nil, ErrNoSpeechDetected
	}
	trimmedPath := filepath.Join(sessionDir, "asr", "audio.asr.trimmed.mp3")
	_ = os.Remove(trimmedPath)
	if err := h.vad.Trim(ctx, audioPath, trimmedPath, smap); err != nil {
		_ = os.Remove(trimmedPath)
		return "", nil, fmt.Errorf("ina vad trim: %w", err)
	}
	info, err := os.Stat(trimmedPath)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(trimmedPath)
		return "", nil, fmt.Errorf("ina vad trim produced empty/missing output: %v", err)
	}
	return trimmedPath, smap, nil
}
