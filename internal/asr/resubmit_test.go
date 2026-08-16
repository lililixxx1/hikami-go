package asr

// ISSUE-006(2026-08-16)付费安全契约测试:DashScope submit 成功后立即把远端任务 ID
// 持久化进 worker 任务 payload,崩溃恢复重入走 await 轮询而非重新提交。
// 见 plans/plan-issue006-dashscope-taskid-persist-2026-08-16.md §4 T1-T9。
//
// 核心契约(按付费安全排序):
//   T2 恢复重入 → Submit 零调用(不重复付费)
//   T4 恢复重入遇瞬态错误 → fail-closed 失败,Submit 零调用(不静默重提交)
//   T9 新提交后 await 报远端已死 → 失败且不发生第二次 submit(防无限重提交循环)
//   T3 远端已死(重入路径)→ 重新提交合法,payload 被新 ID 覆盖
//   T1/T5/T6/T7 持久化与降级路径

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

// submitAwaitFake 实现 Transcriber + submittingTranscriber,记录调用计数与 ID。
type submitAwaitFake struct {
	submitCalls    int
	awaitCalls     int
	transcribeCall int
	submittedIDs   []string
	awaitedIDs     []string
	submitErr      error
	awaitFn        func(call int, taskID string) (Result, error)
}

func (f *submitAwaitFake) Transcribe(ctx context.Context, audioPath string, _ session.Session) (Result, error) {
	f.transcribeCall++
	return Result{Transcript: "plain transcribe", Segments: []map[string]any{}}, nil
}

func (f *submitAwaitFake) SubmitASRTask(ctx context.Context, audioPath string, _ session.Session, _ map[string]int) (string, error) {
	f.submitCalls++
	if f.submitErr != nil {
		return "", f.submitErr
	}
	f.submittedIDs = append(f.submittedIDs, "ds-task-new")
	return "ds-task-new", nil
}

func (f *submitAwaitFake) AwaitASRTask(ctx context.Context, taskID string, _ session.Session) (Result, error) {
	f.awaitCalls++
	f.awaitedIDs = append(f.awaitedIDs, taskID)
	if f.awaitFn != nil {
		return f.awaitFn(f.awaitCalls, taskID)
	}
	return Result{Transcript: "awaited transcript", Segments: []map[string]any{}}, nil
}

// payloadRecorder 实现 taskPayloadWriter,记录每次 UpdatePayload。
type payloadRecorder struct {
	updates map[string]string
	err     error
	calls   int
}

func (r *payloadRecorder) UpdatePayload(ctx context.Context, id string, payload string) error {
	r.calls++
	if r.updates == nil {
		r.updates = map[string]string{}
	}
	r.updates[id] = payload
	return r.err
}

// setupResubmitEnv 搭一个 media_ready session + audio.asr.mp3,返回注入
// submitAwaitFake 的 Handler、payloadRecorder 与带指定 payload 的 task。
func setupResubmitEnv(t *testing.T, taskPayload string) (*Handler, *submitAwaitFake, *payloadRecorder, worker.Task) {
	t.Helper()
	database := setupDB(t)
	insertChannel(t, database, "ch1")
	insertSession(t, database, string(state.StatusMediaReady))
	outputRoot := t.TempDir()
	audioDir := filepath.Join(outputRoot, "ch1", "live_20260501_120000", "asr")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "audio.asr.mp3"), []byte("FAKEMP3"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &submitAwaitFake{}
	handler := NewHandler(&config.Config{OutputRoot: outputRoot}, session.NewStore(database), state.NewStore(database), fake, nil)
	recorder := &payloadRecorder{}
	handler.SetTaskPayloadWriter(recorder)
	task := worker.Task{
		ID: "task-1", ChannelID: "ch1", SessionID: "session_test", Type: TaskType, Payload: taskPayload,
	}
	return handler, fake, recorder, task
}

func decodeDashScopePayload(t *testing.T, raw string) asrTaskPayload {
	t.Helper()
	var payload asrTaskPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode payload %q: %v", raw, err)
	}
	return payload
}

// T1:payload 无 ID → Submit 被调 → 持久化含 dashscope_task_id 的新 payload(round-trip
// 不丢字段)→ await 用同一 ID。
func TestHandleTask_SubmitsAndPersistsTaskID(t *testing.T) {
	handler, fake, recorder, task := setupResubmitEnv(t, "{}")

	if err := handler.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if fake.submitCalls != 1 {
		t.Fatalf("submitCalls = %d, want 1", fake.submitCalls)
	}
	if fake.awaitCalls != 1 || fake.awaitedIDs[0] != "ds-task-new" {
		t.Fatalf("awaitedIDs = %v, want [ds-task-new]", fake.awaitedIDs)
	}
	if recorder.calls != 1 {
		t.Fatalf("payload writer calls = %d, want 1", recorder.calls)
	}
	payload := decodeDashScopePayload(t, recorder.updates[task.ID])
	if payload.DashScopeTaskID != "ds-task-new" {
		t.Fatalf("persisted dashscope_task_id = %q, want ds-task-new", payload.DashScopeTaskID)
	}
}

// T2(核心契约):payload 有 ID 重入 → 仅 await,Submit 零调用(不重复付费),不触发持久化。
func TestHandleTask_ResumeAwaitsWithoutSubmit(t *testing.T) {
	handler, fake, recorder, task := setupResubmitEnv(t, `{"dashscope_task_id":"ds-old"}`)

	if err := handler.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if fake.submitCalls != 0 {
		t.Fatalf("submitCalls = %d, want 0 (resume must not resubmit paid task)", fake.submitCalls)
	}
	if fake.awaitCalls != 1 || fake.awaitedIDs[0] != "ds-old" {
		t.Fatalf("awaitedIDs = %v, want [ds-old]", fake.awaitedIDs)
	}
	if recorder.calls != 0 {
		t.Fatalf("payload writer calls = %d, want 0 (resume path must not persist)", recorder.calls)
	}
}

// T3:重入路径 await 报远端已死(ErrDashScopeTaskDead)→ 重新提交合法,payload 被新 ID 覆盖。
func TestHandleTask_ResumeDeadResubmitsAndOverwritesPayload(t *testing.T) {
	handler, fake, recorder, task := setupResubmitEnv(t, `{"dashscope_task_id":"ds-old"}`)
	fake.awaitFn = func(call int, taskID string) (Result, error) {
		if call == 1 {
			return Result{}, ErrDashScopeTaskDead
		}
		return Result{Transcript: "second attempt", Segments: []map[string]any{}}, nil
	}

	if err := handler.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if fake.submitCalls != 1 {
		t.Fatalf("submitCalls = %d, want 1 (dead remote allows one resubmit)", fake.submitCalls)
	}
	if fake.awaitCalls != 2 || fake.awaitedIDs[0] != "ds-old" || fake.awaitedIDs[1] != "ds-task-new" {
		t.Fatalf("awaitedIDs = %v, want [ds-old ds-task-new]", fake.awaitedIDs)
	}
	payload := decodeDashScopePayload(t, recorder.updates[task.ID])
	if payload.DashScopeTaskID != "ds-task-new" {
		t.Fatalf("persisted dashscope_task_id = %q, want ds-task-new (overwritten)", payload.DashScopeTaskID)
	}
}

// T4(核心契约):重入路径 await 瞬态错误(网络/超时)→ fail-closed 失败,Submit 零调用。
func TestHandleTask_ResumeTransientErrorFailsClosed(t *testing.T) {
	handler, fake, recorder, task := setupResubmitEnv(t, `{"dashscope_task_id":"ds-old"}`)
	fake.awaitFn = func(call int, taskID string) (Result, error) {
		return Result{}, errors.New("dial tcp: connection refused")
	}

	err := handler.HandleTask(context.Background(), task, &noopReporter{})
	if err == nil {
		t.Fatal("HandleTask must fail on transient await error (fail-closed)")
	}
	if fake.submitCalls != 0 {
		t.Fatalf("submitCalls = %d, want 0 (transient error must not silently resubmit)", fake.submitCalls)
	}
	if recorder.calls != 0 {
		t.Fatalf("payload writer calls = %d, want 0 (no submit → no persist)", recorder.calls)
	}
}

// T9(护栏):新提交后 await 报远端已死 → 任务失败,不发生第二次 submit
// (dead-fallback 仅限重入路径,否则同 attempt 内 submit→dead→submit 无限循环重提交)。
func TestHandleTask_FreshAwaitDeadFailsWithoutLoop(t *testing.T) {
	handler, fake, recorder, task := setupResubmitEnv(t, "{}")
	fake.awaitFn = func(call int, taskID string) (Result, error) {
		return Result{}, ErrDashScopeTaskDead
	}

	err := handler.HandleTask(context.Background(), task, &noopReporter{})
	if err == nil {
		t.Fatal("HandleTask must fail when fresh submit's await reports dead remote")
	}
	if fake.submitCalls != 1 {
		t.Fatalf("submitCalls = %d, want 1 (no loop resubmit on fresh-path dead)", fake.submitCalls)
	}
	if recorder.calls != 1 {
		t.Fatalf("payload writer calls = %d, want 1 (only the first submit persists)", recorder.calls)
	}
}

// T5:UpdatePayload 失败 → 仅 WARN,转写仍完成(不浪费已提交的付费任务)。
func TestHandleTask_PersistFailureStillCompletes(t *testing.T) {
	handler, fake, recorder, task := setupResubmitEnv(t, "{}")
	recorder.err = errors.New("db down")

	if err := handler.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v (persist failure must not abort paid transcribe)", err)
	}
	if fake.awaitCalls != 1 {
		t.Fatalf("awaitCalls = %d, want 1", fake.awaitCalls)
	}
	if recorder.calls != 1 {
		t.Fatalf("payload writer calls = %d, want 1 (attempted)", recorder.calls)
	}
}

// T6:未注入 payloadWriter(老装配路径)→ 跳过持久化,转写完成(零回归)。
func TestHandleTask_NoPayloadWriterStillCompletes(t *testing.T) {
	handler, fake, _, task := setupResubmitEnv(t, "{}")
	handler.SetTaskPayloadWriter(nil)

	if err := handler.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if fake.submitCalls != 1 || fake.awaitCalls != 1 {
		t.Fatalf("submit=%d await=%d, want 1/1", fake.submitCalls, fake.awaitCalls)
	}
}

// T7(A-1):payload 有 ID 但转写器不支持两阶段(人工改 DB / 非 DashScope 转写器残留)
// → 打 WARN 后降级普通 Transcribe,不 panic。
func TestHandleTask_PayloadIDWithNonResumingTranscriberFallsBack(t *testing.T) {
	database := setupDB(t)
	insertChannel(t, database, "ch1")
	insertSession(t, database, string(state.StatusMediaReady))
	outputRoot := t.TempDir()
	audioDir := filepath.Join(outputRoot, "ch1", "live_20260501_120000", "asr")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "audio.asr.mp3"), []byte("FAKEMP3"), 0o644); err != nil {
		t.Fatal(err)
	}
	spy := &transcriberSpy{}
	handler := NewHandler(&config.Config{OutputRoot: outputRoot}, session.NewStore(database), state.NewStore(database), spy, nil)
	task := worker.Task{
		ID: "task-1", ChannelID: "ch1", SessionID: "session_test", Type: TaskType,
		Payload: `{"dashscope_task_id":"ds-stale"}`,
	}

	if err := handler.HandleTask(context.Background(), task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v (must degrade to plain transcribe with WARN)", err)
	}
	if spy.lastAudioPath == "" {
		t.Fatal("plain Transcribe was not called (fallback broken)")
	}
}
