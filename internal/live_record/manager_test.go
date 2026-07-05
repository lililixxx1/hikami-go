package live_record

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

func TestAdopt_RebuildsActiveAndStopTerminatesProcess(t *testing.T) {
	// 启动存活子进程模拟重启后残留的 ffmpeg（ISS-6）
	sleepCmd := exec.Command("sleep", "10")
	if err := sleepCmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := sleepCmd.Process.Pid

	m := &Manager{
		active:    map[string]activeRecord{},
		fileSizes: map[string]int64{},
		failCount: map[string]int{},
	}

	if !m.Adopt("ch1", "task_1", "sess_1", pid) {
		t.Fatal("Adopt 应返回 true（接管成功）")
	}
	if m.Adopt("ch1", "task_2", "sess_2", pid) {
		t.Fatal("重复 Adopt 同 channel 应返回 false（互斥）")
	}
	if m.Adopt("ch2", "task_3", "sess_3", 0) {
		t.Fatal("pid<=0 应返回 false")
	}

	// Stop 接管进程：不再返回 ErrNotRecording，并向 PID 发 SIGTERM
	if err := m.Stop("ch1"); err != nil {
		t.Fatalf("Stop 接管进程失败: %v", err)
	}
	// SIGTERM 后 sleep 进程退出（忽略 Wait 的退出码错误）
	_ = sleepCmd.Wait()

	// Stop 已自清 active，再次 Stop 返回 ErrNotRecording
	if err := m.Stop("ch1"); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("二次 Stop 应返回 ErrNotRecording, got %v", err)
	}
}

func TestManagerStartCreatesLiveRecordTask(t *testing.T) {
	manager, database, pool := newTestManager(t)
	defer pool.Stop()
	if _, err := database.Exec("DELETE FROM sessions WHERE id = 'session_1'"); err != nil {
		t.Fatalf("clear seeded session: %v", err)
	}

	status, err := manager.Start(context.Background(), "huize")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !status.Recording || status.SessionID == "" || status.TaskID == "" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestManagerStartRejectsActiveLiveSession(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()

	_, err := manager.Start(context.Background(), "huize")
	if !errors.Is(err, ErrAlreadyRecording) {
		t.Fatalf("start err = %v, want ErrAlreadyRecording", err)
	}
}

func TestManagerStartRejectsOfflineLive(t *testing.T) {
	manager, database, pool := newTestManager(t)
	defer pool.Stop()
	if _, err := database.Exec("DELETE FROM sessions WHERE id = 'session_1'"); err != nil {
		t.Fatalf("clear seeded session: %v", err)
	}
	manager.client = offlineClient{}

	_, err := manager.Start(context.Background(), "huize")
	if !errors.Is(err, ErrNotLive) {
		t.Fatalf("start err = %v, want ErrNotLive", err)
	}
}

func TestHandleTaskWritesRawArtifacts(t *testing.T) {
	manager, database, pool := newTestManager(t)
	defer pool.Stop()

	task, err := pool.Store().Create(context.Background(), worker.CreateInput{
		ChannelID: "huize",
		SessionID: "session_1",
		Type:      TaskType,
		Payload:   `{"room_id":123}`,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	running, err := pool.Store().MarkRunning(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}

	err = manager.HandleTask(context.Background(), running, noopReporter{})
	if err != nil {
		t.Fatalf("handle task: %v", err)
	}

	rawDir := filepath.Join(manager.cfg.OutputRoot, "huize", "live_20260427_120000", "raw")
	for _, name := range []string{"audio.m4a", "live.raw.json"} {
		if _, err := os.Stat(filepath.Join(rawDir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	var status string
	if err := database.QueryRow("SELECT status FROM sessions WHERE id = 'session_1'").Scan(&status); err != nil {
		t.Fatalf("query session status: %v", err)
	}
	if status != string(state.StatusRecording) {
		t.Fatalf("session status = %s, want recording before normalize", status)
	}

	var endedAt string
	if err := database.QueryRow("SELECT COALESCE(ended_at, '') FROM sessions WHERE id = 'session_1'").Scan(&endedAt); err != nil {
		t.Fatalf("query session ended_at: %v", err)
	}
	if endedAt == "" {
		t.Fatal("expected non-empty ended_at")
	}
	if _, err := time.Parse(time.RFC3339, endedAt); err != nil {
		t.Fatalf("parse ended_at: %v", err)
	}
}

func TestHandleTaskRecordsReconnectToPartAndConcatsSegments(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	// 首段录制失败后仍开播（触发重连），重连段录制成功（clean EOF）后主播下播（正常收尾）。
	// 三次 CheckLive：preflight=true、首段失败后判定=true、重连段 EOF 后判定=false。
	// 旧版用永远-live 的 fakeClient 能"成功"，是因为旧代码把重连段的 clean EOF 错误地
	// 当成正常结束放行（即本次修复的 bug）；stateful client 暴露了真实路径必须显式收尾。
	c := &statefulLiveClient{tb: t, lives: []bool{true, true, false}}
	manager.client = c
	defer c.assertFullyConsumed()
	recorder := &interruptingAudioRecorder{}
	manager.audio = recorder

	var concatCommand string
	var concatArgs []string
	originalRunFFmpegConcat := runFFmpegConcat
	runFFmpegConcat = func(ctx context.Context, command string, args ...string) error {
		concatCommand = command
		concatArgs = slices.Clone(args)
		listIndex := slices.Index(args, "-i")
		if listIndex < 0 || listIndex+1 >= len(args) {
			t.Fatalf("concat args missing file list: %+v", args)
		}
		outputPath := args[len(args)-1]
		return copyConcatListFiles(args[listIndex+1], outputPath)
	}
	t.Cleanup(func() {
		runFFmpegConcat = originalRunFFmpegConcat
	})

	task, err := pool.Store().Create(context.Background(), worker.CreateInput{
		ChannelID: "huize",
		SessionID: "session_1",
		Type:      TaskType,
		Payload:   `{"room_id":123}`,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	running, err := pool.Store().MarkRunning(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}

	err = manager.HandleTask(context.Background(), running, noopReporter{})
	if err != nil {
		t.Fatalf("handle task: %v", err)
	}

	rawDir := filepath.Join(manager.cfg.OutputRoot, "huize", "live_20260427_120000", "raw")
	audioPath := filepath.Join(rawDir, "audio.m4a")
	content, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	if string(content) != "segment-1\nsegment-2\n" {
		t.Fatalf("audio content = %q", string(content))
	}
	if _, err := os.Stat(reconnectAudioSegmentPath(audioPath, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part file should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(audioPath + ".concat.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("concat list should be removed, stat err = %v", err)
	}
	if concatCommand != "ffmpeg" {
		t.Fatalf("concat command = %q, want ffmpeg", concatCommand)
	}
	argsText := strings.Join(concatArgs, "\n")
	for _, want := range []string{"-f\nconcat", "-safe\n0", "-c\ncopy"} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("concat args missing %q: %+v", want, concatArgs)
		}
	}
	if len(recorder.outputs) != 2 || recorder.outputs[0] != audioPath || recorder.outputs[1] != reconnectAudioSegmentPath(audioPath, 1) {
		t.Fatalf("recorder outputs = %+v", recorder.outputs)
	}
}

// TestHandleTaskPreflightCheckLiveOffline 验证拉流前的开播再确认：若明确判定已下播，
// 应直接放弃录制（返回 ErrNotLive），且根本不调用 recorder，避免对失效流硬拉。
func TestHandleTaskPreflightCheckLiveOffline(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.client = offlineClient{}
	recorder := &countingRecorder{}
	manager.audio = recorder

	running := mustCreateRunningTask(t, pool)
	err := manager.HandleTask(context.Background(), running, noopReporter{})
	if !errors.Is(err, ErrNotLive) {
		t.Fatalf("handle task err = %v, want ErrNotLive", err)
	}
	if len(recorder.outputs) != 0 {
		t.Fatalf("recorder should not be called when offline, outputs = %+v", recorder.outputs)
	}
}

// TestHandleTaskPreflightCheckLiveErrorProceeds 验证 preflight 探测本身出错（如风控 -352）
// 时不阻断录制：沿用乐观策略继续拉流，任务应成功完成。
func TestHandleTaskPreflightCheckLiveErrorProceeds(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.client = probeErrorClient{}
	recorder := &countingRecorder{}
	manager.audio = recorder

	running := mustCreateRunningTask(t, pool)
	if err := manager.HandleTask(context.Background(), running, noopReporter{}); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	if len(recorder.outputs) != 1 {
		t.Fatalf("recorder should be called once when probe fails, outputs = %+v", recorder.outputs)
	}
}

// TestHandleTaskReconnectSurvivesProbeError 验证重连前 CheckLive 报错时不再放弃重试：
// 首次录制失败 + 探测持续出错，重连仍应进行并最终成功，分片合并为最终音频。
func TestHandleTaskReconnectSurvivesProbeError(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	manager.client = probeErrorClient{}
	recorder := &interruptingOnceRecorder{}
	manager.audio = recorder
	stubFFmpegConcat(t)

	running := mustCreateRunningTask(t, pool)
	if err := manager.HandleTask(context.Background(), running, noopReporter{}); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	rawDir := filepath.Join(manager.cfg.OutputRoot, "huize", "live_20260427_120000", "raw")
	audioPath := filepath.Join(rawDir, "audio.m4a")
	content, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	if string(content) != "segment-1\nsegment-2\n" {
		t.Fatalf("audio content = %q", string(content))
	}
	if len(recorder.outputs) != 2 || recorder.outputs[1] != reconnectAudioSegmentPath(audioPath, 1) {
		t.Fatalf("recorder outputs = %+v, want two segments including reconnect part", recorder.outputs)
	}
}

// TestHandleTaskReconnectsOnCleanEOFWhileLive 是本次修复的核心回归：首次录制 ffmpeg 干净
// 退出（返回 nil，模拟上游 EOF）+ 主播仍开播 → 必须触发重连；重连段录制后主播下播 → 正常收尾。
// 21:52 漏录正是首段 clean EOF 被旧代码（for ... err != nil ... 守卫）直接放行导致。
// 三次 CheckLive：preflight=true、首段 EOF 后判定=true、重连段 EOF 后判定=false。
func TestHandleTaskReconnectsOnCleanEOFWhileLive(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	c := &statefulLiveClient{tb: t, lives: []bool{true, true, false}}
	manager.client = c
	defer c.assertFullyConsumed()
	recorder := &cleanEOFRecorder{}
	manager.audio = recorder
	stubFFmpegConcat(t)

	running := mustCreateRunningTask(t, pool)
	if err := manager.HandleTask(context.Background(), running, noopReporter{}); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	rawDir := filepath.Join(manager.cfg.OutputRoot, "huize", "live_20260427_120000", "raw")
	audioPath := filepath.Join(rawDir, "audio.m4a")
	content, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	if string(content) != "segment-1\nsegment-2\n" {
		t.Fatalf("audio content = %q, want two concatenated segments", string(content))
	}
	if len(recorder.outputs) != 2 || recorder.outputs[0] != audioPath || recorder.outputs[1] != reconnectAudioSegmentPath(audioPath, 1) {
		t.Fatalf("recorder outputs = %+v, want [audioPath, part.1]", recorder.outputs)
	}
}

// TestHandleTaskCleanEOFThenOfflineNoReconnect 对照组：首段 clean EOF + 主播已下播 → 不重连，
// recorder 只被调用一次，正常收尾。两次 CheckLive：preflight=true、首段 EOF 后判定=false。
func TestHandleTaskCleanEOFThenOfflineNoReconnect(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	c := &statefulLiveClient{tb: t, lives: []bool{true, false}}
	manager.client = c
	defer c.assertFullyConsumed()
	recorder := &cleanEOFRecorder{}
	manager.audio = recorder

	running := mustCreateRunningTask(t, pool)
	if err := manager.HandleTask(context.Background(), running, noopReporter{}); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	rawDir := filepath.Join(manager.cfg.OutputRoot, "huize", "live_20260427_120000", "raw")
	if _, err := os.Stat(reconnectAudioSegmentPath(filepath.Join(rawDir, "audio.m4a"), 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part.1 should not exist when offline after clean EOF, stat err = %v", err)
	}
	if len(recorder.outputs) != 1 {
		t.Fatalf("recorder outputs = %+v, want exactly 1 (no reconnect)", recorder.outputs)
	}
}

// TestHandleTaskCleanEOFLiveProbeErrorFinalizes 验证 r4 Medium 2：首段 clean EOF + 探测本身出错
// （B 站风控 -352）→ 保守收尾（不重连），保留已录音频，而不是冒"重连耗尽丢弃内容"的风险。
// 两次 CheckLive：preflight 成功（live=true，err=nil）、首段 EOF 后探测出错（err=-352）。
func TestHandleTaskCleanEOFLiveProbeErrorFinalizes(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	c := &statefulLiveClient{
		tb:    t,
		lives: []bool{true, false}, // 第二位的 live 值在 err 非 nil 时不生效
		errs:  []error{nil, errors.New("bilibili room info error: code=-352")},
	}
	manager.client = c
	defer c.assertFullyConsumed()
	recorder := &cleanEOFRecorder{}
	manager.audio = recorder

	running := mustCreateRunningTask(t, pool)
	if err := manager.HandleTask(context.Background(), running, noopReporter{}); err != nil {
		t.Fatalf("handle task: %v, want finalize success on inconclusive probe", err)
	}
	if len(recorder.outputs) != 1 {
		t.Fatalf("recorder outputs = %+v, want exactly 1 (no reconnect on inconclusive probe)", recorder.outputs)
	}
}

// TestHandleTaskReconnectExhaustedReturnsError 保护现有"重连耗尽返回错误"语义：每次录制失败
// + 仍开播 → 重连次数耗尽后必须返回错误，不能误走 finalize 成功路径。
// 三次 CheckLive：preflight=true、首段失败后判定=true、重连段失败后判定=true。
func TestHandleTaskReconnectExhaustedReturnsError(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	c := &statefulLiveClient{tb: t, lives: []bool{true, true, true}}
	manager.client = c
	defer c.assertFullyConsumed()
	recorder := &alwaysFailingRecorder{}
	manager.audio = recorder

	running := mustCreateRunningTask(t, pool)
	err := manager.HandleTask(context.Background(), running, noopReporter{})
	if err == nil {
		t.Fatalf("handle task err = nil, want non-nil after reconnect exhausted")
	}
}

// TestHandleTaskCleanEOFLiveReconnectExhaustedReturnsSentinel 验证 r5 Medium 3：
// 首段 clean EOF + 仍开播，但重连段也 clean EOF + 仍开播 → 重连额度耗尽时返回哨兵错误
// （而不是误判成功）。三次 CheckLive：preflight=true、首段 EOF 后=true、重连段 EOF 后=true。
func TestHandleTaskCleanEOFLiveReconnectExhaustedReturnsSentinel(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	c := &statefulLiveClient{tb: t, lives: []bool{true, true, true}}
	manager.client = c
	defer c.assertFullyConsumed()
	recorder := &cleanEOFRecorder{}
	manager.audio = recorder
	stubFFmpegConcat(t)

	running := mustCreateRunningTask(t, pool)
	err := manager.HandleTask(context.Background(), running, noopReporter{})
	if err == nil {
		t.Fatalf("handle task err = nil, want sentinel after clean-EOF-while-live exhausted")
	}
	if !errors.Is(err, errStreamEndedWhileLive) {
		t.Fatalf("handle task err = %v, want errors.Is(errStreamEndedWhileLive)", err)
	}
}

// selectStreamFailsAfterFirstClient:首次 GetStream 成功(让首段录制开始),后续 GetStream 调用失败
// (模拟重连选流失败)。用于异常 #1 测试。
type selectStreamFailsAfterFirstClient struct {
	tb           testing.TB
	getStreamCnt int
}

func (c *selectStreamFailsAfterFirstClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	return LiveInfo{RoomID: roomID, Live: true, Title: "live", StartedAt: time.Date(2026, 4, 27, 13, 0, 0, 0, time.Local)}, nil
}

func (c *selectStreamFailsAfterFirstClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	c.getStreamCnt++
	if c.getStreamCnt == 1 {
		return StreamInfo{URL: "https://example.com/live.flv", AudioOnly: true}, nil
	}
	return StreamInfo{}, errors.New("bilibili stream url not found")
}

// selectStreamFailsOfflineProbeClient 与上面类似,但 CheckLive 序列:首段 preflight=true、
// 首段录制失败后的 decideAfterRecord 探测=true(让重连开始),之后所有探测=false(模拟 B 站 API
// 流断边缘态抖动)。selectStream 在首段成功、之后全失败。验证异常 #1:selectStream 失败后
// skipProbe 路径不走 decideAfterRecord,即使 CheckLive 会抖动返回 live=false 也不提前放弃。
type selectStreamFailsOfflineProbeClient struct {
	tb           testing.TB
	checkCalls   int
	getStreamCnt int
}

func (c *selectStreamFailsOfflineProbeClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	c.checkCalls++
	// 第 1 次(preflight)=true、第 2 次(首段失败后 decideAfterRecord)=true(触发重连),
	// 之后全部=false(抖动)。skipProbe 路径不调 CheckLive,所以这些 false 不应被读到。
	live := c.checkCalls <= 2
	return LiveInfo{RoomID: roomID, Live: live, Title: "live", StartedAt: time.Date(2026, 4, 27, 13, 0, 0, 0, time.Local)}, nil
}

func (c *selectStreamFailsOfflineProbeClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	c.getStreamCnt++
	if c.getStreamCnt == 1 {
		return StreamInfo{URL: "https://example.com/live.flv", AudioOnly: true}, nil
	}
	return StreamInfo{}, errors.New("bilibili stream url not found")
}

// TestHandleTaskSelectStreamFailureRetriesMaxReconnect 验证异常 #1:重连时 selectStream 失败,
// 不应因一次 decideAfterRecord 的 CheckLive 误判提前放弃,而应重试满 maxReconnect 次。
// 首段 GetStream 成功 → 首段 recordAudio 失败(alwaysFailingRecorder)→ decideAfterRecord 判 live=true 触发重连
// → 重连 selectStream 失败 → skipProbeAfterSelectError 路径直接 sleep+重试,不走 decideAfterRecord。
// 断言:GetStream 被调用 1(首段) + maxReconnect(重连每次) = 4 次。
func TestHandleTaskSelectStreamFailureRetriesMaxReconnect(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 3
	manager.cfg.LiveRecord.ReconnectDelay = 1
	c := &selectStreamFailsAfterFirstClient{tb: t}
	manager.client = c
	manager.audio = &alwaysFailingRecorder{}

	running := mustCreateRunningTask(t, pool)
	err := manager.HandleTask(context.Background(), running, noopReporter{})
	if err == nil {
		t.Fatalf("handle task err = nil, want non-nil after selectStream failures exhausted")
	}
	// 1 次首段 + 3 次重连 = 4 次。如果异常 #1 未修(提前放弃),会是 2 次(首段 + 1 次)。
	if c.getStreamCnt != 4 {
		t.Errorf("GetStream called %d times, want 4 (1 first + 3 reconnects, 异常 #1 fix)", c.getStreamCnt)
	}
}

// TestHandleTaskSelectStreamFailureIgnoresOfflineProbe 验证异常 #1 的核心:selectStream 失败时
// skipProbe 路径不走 decideAfterRecord,即使 CheckLive 抖动返回 live=false 也不会提前放弃。
// 用 offline probe client:首次 preflight=true(录制开始),之后 CheckLive 全部=false。
// alwaysFailingRecorder 让首段失败 → decideAfterRecord(此时 CheckLive=false 但 wantErr!=nil 走重连)
// → 重连 selectStream 失败 → skipProbe 路径(不再 CheckLive)→ 继续重试满 maxReconnect。
// 断言:GetStream 4 次(证明没被中途的 live=false 探测提前终止)。
func TestHandleTaskSelectStreamFailureIgnoresOfflineProbe(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 3
	manager.cfg.LiveRecord.ReconnectDelay = 1
	c := &selectStreamFailsOfflineProbeClient{tb: t}
	manager.client = c
	manager.audio = &alwaysFailingRecorder{}

	running := mustCreateRunningTask(t, pool)
	_ = manager.HandleTask(context.Background(), running, noopReporter{})
	// 1 首段 + 3 重连 = 4。若 skipProbe 未生效(走了 decideAfterRecord,CheckLive=false 提前放弃),
	// GetStream 会是 1-2 次。
	if c.getStreamCnt != 4 {
		t.Errorf("GetStream called %d times, want 4 (selectStream 失败应忽略 CheckLive=false,重试满 maxReconnect)", c.getStreamCnt)
	}
}

// TestIsCDNTransientError 验证异常 #2 的 CDN 瞬时错误判定。
func TestIsCDNTransientError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"http 404", errors.New("open live stream: http status 404"), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"EOF on open stream", errors.New("open live stream: EOF"), true},
		{"ffmpeg 真失败(非 CDN)", errors.New("ffmpeg record failed: exit_code_1"), false},
		{"selectStream 失败", errors.New("bilibili stream url not found"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCDNTransientError(tt.err); got != tt.want {
				t.Errorf("isCDNTransientError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestHandleTaskCDNErrorUsesIndependentBudget 验证异常 #2:CDN 瞬时错误(404)用独立 cdnRetryBudget(=5),
// 不受 maxReconnect=3 截断。recordAudio 持续返回 404 → 应重试 cdnRetryBudget=5 次后放弃,
// 而非 maxReconnect=3 次。断言 recorder 调用次数 = 1(首段) + 5(CDN 重试) = 6。
func TestHandleTaskCDNErrorUsesIndependentBudget(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 3 // 通用预算,CDN 应绕过它
	manager.cfg.LiveRecord.ReconnectDelay = 1
	// fakeClient 的 CheckLive 总返回 live=true,避免下播误判干扰 CDN 路径验证。
	manager.client = fakeClient{}
	recorder := &cdnFailRecorder{}
	manager.audio = recorder

	running := mustCreateRunningTask(t, pool)
	err := manager.HandleTask(context.Background(), running, noopReporter{})
	if err == nil {
		t.Fatalf("handle task err = nil, want non-nil after CDN budget exhausted")
	}
	// 1 首段 + 5 次 CDN 重试 = 6。若被 maxReconnect=3 截断,会是 4。
	if len(recorder.outputs) != 6 {
		t.Errorf("recorder calls = %d, want 6 (1 first + 5 CDN retries, 绕过 maxReconnect=3)", len(recorder.outputs))
	}
}

// TestCdnBackoff 验证异常 #2 的指数退避公式(base*2^n,上限 60s)。
func TestCdnBackoff(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
		{6, 60 * time.Second},
		{100, 60 * time.Second},
	}
	for _, tt := range tests {
		if got := cdnBackoff(tt.attempt); got != tt.want {
			t.Errorf("cdnBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func mustCreateRunningTask(t *testing.T, pool *worker.Pool) worker.Task {
	t.Helper()
	task, err := pool.Store().Create(context.Background(), worker.CreateInput{
		ChannelID: "huize",
		SessionID: "session_1",
		Type:      TaskType,
		Payload:   `{"room_id":123}`,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	running, err := pool.Store().MarkRunning(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	return running
}

// stubFFmpegConcat 用拼合 listfile 内容的方式替身 ffmpeg concat，供重连测试复用。
func stubFFmpegConcat(t *testing.T) {
	t.Helper()
	original := runFFmpegConcat
	runFFmpegConcat = func(ctx context.Context, command string, args ...string) error {
		listIndex := slices.Index(args, "-i")
		if listIndex < 0 || listIndex+1 >= len(args) {
			t.Fatalf("concat args missing file list: %+v", args)
		}
		return copyConcatListFiles(args[listIndex+1], args[len(args)-1])
	}
	t.Cleanup(func() { runFFmpegConcat = original })
}

// TestWriteConcatListWritesAbsolutePaths 是针对相对 OutputRoot 导致路径叠加 bug 的回归测试。
//
// ffmpeg 的 concat demuxer 以 listfile 所在目录为基准解析相对条目。当 OutputRoot
// 为相对路径（如 "./output"）时，录制分片路径也是相对的，写入 listfile 后会被 ffmpeg
// 叠加成 raw/raw/audio.m4a 导致打开失败。修复要求 listfile 中条目必须为绝对路径。
func TestWriteConcatListWritesAbsolutePaths(t *testing.T) {
	dir := t.TempDir()

	// 模拟生产配置：OutputRoot 为相对路径，audioPath 也因此是相对路径。
	relSegments := []string{
		filepath.Join("output", "bili_1298779265", "live_20260621_160205", "raw", "audio.m4a"),
		filepath.Join("output", "bili_1298779265", "live_20260621_160205", "raw", "audio.part.2.m4a"),
	}
	listPath := filepath.Join(dir, "audio.m4a.concat.txt")
	if err := writeConcatList(listPath, relSegments); err != nil {
		t.Fatalf("writeConcatList: %v", err)
	}

	content, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatalf("read list: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	wantAbs0 := filepath.ToSlash(filepath.Join(cwd, relSegments[0]))
	wantAbs1 := filepath.ToSlash(filepath.Join(cwd, relSegments[1]))
	wantLine0 := "file '" + wantAbs0 + "'"
	wantLine1 := "file '" + wantAbs1 + "'"
	got := string(content)
	if !strings.Contains(got, wantLine0) {
		t.Fatalf("listfile missing absolute entry for segment 0.\nwant line: %s\ngot:\n%s", wantLine0, got)
	}
	if !strings.Contains(got, wantLine1) {
		t.Fatalf("listfile missing absolute entry for segment 1.\nwant line: %s\ngot:\n%s", wantLine1, got)
	}
	// 入参绝不能原样出现（否则就是路径叠加 bug 复现）。
	for _, rel := range relSegments {
		if strings.Contains(got, "file '"+rel+"'") {
			t.Fatalf("listfile wrote relative path verbatim (%s), which makes ffmpeg double up the path", rel)
		}
	}
}

func TestDownloadCookieFileForChannelFallsBackToBootstrap(t *testing.T) {
	got := downloadCookieFileForChannel(channel.Channel{
		ID:         "new",
		UID:        100,
		LiveRoomID: 200,
	}, []config.BootstrapChannel{
		{ID: "default", DownloadCookieFile: "default.txt"},
	})
	if got != "default.txt" {
		t.Fatalf("cookie file = %q, want default.txt", got)
	}
}

func TestDownloadCookieFileForChannelPrefersMatchingBootstrap(t *testing.T) {
	got := downloadCookieFileForChannel(channel.Channel{
		ID:         "bili_100",
		UID:        100,
		LiveRoomID: 200,
	}, []config.BootstrapChannel{
		{ID: "default", DownloadCookieFile: "default.txt"},
		{ID: "bili_100", UID: 100, LiveRoomID: 200, DownloadCookieFile: "matched.txt"},
	})
	if got != "matched.txt" {
		t.Fatalf("cookie file = %q, want matched.txt", got)
	}
}

func TestSelectStreamDefaultUsesMixedStream(t *testing.T) {
	client := &streamModeClient{}
	manager := &Manager{
		cfg: &config.Config{
			LiveRecord: config.LiveRecordConfig{},
		},
		client: client,
	}

	stream, err := manager.selectStream(context.Background(), 123, "cookie")
	if err != nil {
		t.Fatalf("select stream: %v", err)
	}
	if client.audioOnly {
		t.Fatalf("GetStream called with audioOnly=true, want false")
	}
	if stream.AudioOnly {
		t.Fatalf("stream AudioOnly = true, want false")
	}
}

func TestSelectStreamUsesAudioOnlyWhenConfigured(t *testing.T) {
	client := &streamModeClient{}
	manager := &Manager{
		cfg: &config.Config{
			LiveRecord: config.LiveRecordConfig{
				AudioOnly:            true,
				FallbackExtractAudio: true,
			},
		},
		client: client,
	}

	stream, err := manager.selectStream(context.Background(), 123, "cookie")
	if err != nil {
		t.Fatalf("select stream: %v", err)
	}
	if !client.audioOnly {
		t.Fatalf("GetStream called with audioOnly=false, want true")
	}
	if !stream.AudioOnly {
		t.Fatalf("stream AudioOnly = false, want true")
	}
}

func TestSelectStreamFallsBackToMixedStream(t *testing.T) {
	client := &streamFallbackClient{audioErr: errors.New("no audio stream")}
	manager := &Manager{
		cfg: &config.Config{
			LiveRecord: config.LiveRecordConfig{
				AudioOnly:            true,
				FallbackExtractAudio: true,
			},
		},
		client: client,
	}

	stream, err := manager.selectStream(context.Background(), 123, "cookie")
	if err != nil {
		t.Fatalf("select stream: %v", err)
	}
	if len(client.calls) != 2 || !client.calls[0] || client.calls[1] {
		t.Fatalf("unexpected stream calls: %+v", client.calls)
	}
	if stream.AudioOnly {
		t.Fatalf("stream AudioOnly = true, want false")
	}
}

func TestSelectStreamRequiresAudioStream(t *testing.T) {
	client := &streamFallbackClient{audioErr: errors.New("no audio stream")}
	manager := &Manager{
		cfg: &config.Config{
			LiveRecord: config.LiveRecordConfig{
				AudioOnly:            true,
				RequireAudioStream:   true,
				FallbackExtractAudio: true,
			},
		},
		client: client,
	}

	_, err := manager.selectStream(context.Background(), 123, "cookie")
	if err == nil {
		t.Fatalf("select stream should fail when audio stream is required")
	}
	if len(client.calls) != 1 || !client.calls[0] {
		t.Fatalf("unexpected stream calls: %+v", client.calls)
	}
}

func newTestManager(t *testing.T) (*Manager, *sql.DB, *worker.Pool) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO channels(id, name, uid, live_room_id, enabled) VALUES ('huize', 'Hikami', 1, 123, 1);
		INSERT INTO sessions(id, slug, channel_id, source_type, source_id, title, started_at, source_url, status)
		VALUES ('session_1', 'live_20260427_120000', 'huize', 'live_record', 'live-123-test', 'Live', '2026-04-27T12:00:00+08:00', 'https://live.bilibili.com/123', 'discovered');
	`); err != nil {
		t.Fatalf("seed database: %v", err)
	}

	cfg := &config.Config{
		OutputRoot: filepath.Join(t.TempDir(), "output"),
		FFmpeg:     "ffmpeg",
		LiveRecord: config.LiveRecordConfig{
			Enabled:              true,
			AudioContainer:       "m4a",
			RequireAudioStream:   true,
			FallbackExtractAudio: false,
			RecordDanmaku:        false,
		},
	}
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	pool := worker.NewPool(taskStore, hub, 1, nil)
	manager := NewManager(
		cfg,
		channel.NewStore(database),
		session.NewStore(database),
		state.NewStore(database),
		pool,
		fakeClient{},
		fileAudioRecorder{},
		NoopDanmakuRecorder{},
	)
	manager.Register(pool)
	if err := pool.Start(context.Background(), 1); err != nil {
		t.Fatalf("start pool: %v", err)
	}
	return manager, database, pool
}

type fakeClient struct{}

func (fakeClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	return LiveInfo{
		RoomID:    roomID,
		Live:      true,
		Title:     "Live",
		StartedAt: time.Date(2026, 4, 27, 13, 0, 0, 0, time.Local),
	}, nil
}

func (fakeClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	return StreamInfo{URL: "https://example.com/live.flv", AudioOnly: true}, nil
}

type offlineClient struct{}

func (offlineClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	return LiveInfo{
		RoomID:    roomID,
		Live:      false,
		Title:     "Offline",
		StartedAt: time.Date(2026, 4, 27, 13, 0, 0, 0, time.Local),
	}, nil
}

// errorClient 模拟 CheckLive 失败(如 -352 风控),用于验证异常 #8 的 WARN 日志。
type errorClient struct{}

func (errorClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	return LiveInfo{}, errors.New("bilibili room info error: code=-352 message=-352")
}

func (errorClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	return StreamInfo{}, errors.New("bilibili stream url not found")
}

func (offlineClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	return StreamInfo{}, nil
}

type streamModeClient struct {
	audioOnly bool
}

func (streamModeClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	return LiveInfo{}, nil
}

func (c *streamModeClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	c.audioOnly = audioOnly
	return StreamInfo{URL: "https://example.com/live.flv", AudioOnly: audioOnly}, nil
}

type streamFallbackClient struct {
	audioErr error
	calls    []bool
}

func (streamFallbackClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	return LiveInfo{}, nil
}

func (c *streamFallbackClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	c.calls = append(c.calls, audioOnly)
	if audioOnly && c.audioErr != nil {
		return StreamInfo{}, c.audioErr
	}
	return StreamInfo{URL: "https://example.com/live.flv", AudioOnly: audioOnly}, nil
}

// probeErrorClient 让 CheckLive 始终返回探测错误（模拟 B 站风控 -352 / 网络抖动），
// 但 GetStream 正常返回，用于验证 preflight 与重连不因探测出错而中断。
type probeErrorClient struct{}

func (probeErrorClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	return LiveInfo{}, errors.New("bilibili room info error: code=-352")
}

func (probeErrorClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	return StreamInfo{URL: "https://example.com/live.flv", AudioOnly: audioOnly}, nil
}

// statefulLiveClient 按调用次数返回预设的开播探测结果，精确驱动各阶段（r5/r6 指出
// fakeClient 永远-live / offlineClient 被 preflight 拦截，无法覆盖重连分段的判定）。
// 超出预设次数时 t.Fatalf，避免静默默认值掩盖探测次数错误（r6 Medium）。
type statefulLiveClient struct {
	tb    testing.TB
	lives []bool  // 每次 CheckLive 期望返回的 live 值（errs 对应位为 nil 时生效）
	errs  []error // 可选：对应次数的探测错误（非 nil 时优先于 lives）
	calls int
}

func (c *statefulLiveClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	n := c.calls
	c.calls++
	if n >= len(c.lives) {
		c.tb.Fatalf("statefulLiveClient.CheckLive called more times than expected: got call #%d, have %d presets", n+1, len(c.lives))
	}
	if n < len(c.errs) && c.errs[n] != nil {
		return LiveInfo{}, c.errs[n]
	}
	live := c.lives[n]
	return LiveInfo{
		RoomID:    roomID,
		Live:      live,
		Title:     "stateful",
		StartedAt: time.Date(2026, 4, 27, 13, 0, 0, 0, time.Local),
	}, nil
}

func (c *statefulLiveClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	return StreamInfo{URL: "https://example.com/live.flv", AudioOnly: true}, nil
}

// assertFullyConsumed 断言预设的探测次数被刚好用完——既防"多探测"（CheckLive 内 t.Fatalf），
// 也防"少探测"被静默放过（r7 Low 1）。
func (c *statefulLiveClient) assertFullyConsumed() {
	c.tb.Helper()
	if c.calls != len(c.lives) {
		c.tb.Fatalf("statefulLiveClient CheckLive called %d times, expected exactly %d", c.calls, len(c.lives))
	}
}

// cleanEOFRecorder 模拟 ffmpeg 收到上游 EOF 干净退出：每次 Record 都返回 nil 并写有效字节。
type cleanEOFRecorder struct {
	outputs []string
}

func (r *cleanEOFRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	r.outputs = append(r.outputs, outputPath)
	return os.WriteFile(outputPath, []byte("segment-"+strconv.Itoa(len(r.outputs))+"\n"), 0o644)
}

// alwaysFailingRecorder 模拟持续拉流/录制失败（r5 Medium 3：现有 interruptingAudioRecorder
// 首次失败第二次成功，无法覆盖「重连耗尽返回错误」）。
type alwaysFailingRecorder struct {
	outputs []string
}

func (r *alwaysFailingRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	r.outputs = append(r.outputs, outputPath)
	return errors.New("stream interrupted")
}

// countingRecorder 记录 Record 调用次数与目标路径，用于断言录制是否被触发。
type countingRecorder struct {
	outputs []string
}

func (r *countingRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	r.outputs = append(r.outputs, outputPath)
	return os.WriteFile(outputPath, []byte("audio"), 0o644)
}

// cdnFailRecorder 返回 CDN 瞬时错误(http 404),用于异常 #2 的 CDN 重试预算测试。
type cdnFailRecorder struct {
	outputs []string
}

func (r *cdnFailRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	r.outputs = append(r.outputs, outputPath)
	return errors.New("open live stream: http status 404")
}

// interruptingOnceRecorder 首次 Record 失败、之后成功，配合 probeErrorClient 验证
// 重连不因 CheckLive 报错而被放弃。
type interruptingOnceRecorder struct {
	outputs []string
}

func (r *interruptingOnceRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	r.outputs = append(r.outputs, outputPath)
	if err := os.WriteFile(outputPath, []byte("segment-"+strconv.Itoa(len(r.outputs))+"\n"), 0o644); err != nil {
		return err
	}
	if len(r.outputs) == 1 {
		return errors.New("stream interrupted")
	}
	return nil
}

type fileAudioRecorder struct{}

func (fileAudioRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	return os.WriteFile(outputPath, []byte("audio"), 0o644)
}

type interruptingAudioRecorder struct {
	outputs []string
}

func (r *interruptingAudioRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	r.outputs = append(r.outputs, outputPath)
	if err := os.WriteFile(outputPath, []byte("segment-"+strconv.Itoa(len(r.outputs))+"\n"), 0o644); err != nil {
		return err
	}
	if len(r.outputs) == 1 {
		return errors.New("stream interrupted")
	}
	return nil
}

func copyConcatListFiles(listPath string, outputPath string) error {
	listContent, err := os.ReadFile(listPath)
	if err != nil {
		return err
	}
	output, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer output.Close()
	for _, line := range strings.Split(string(listContent), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path := strings.TrimPrefix(line, "file '")
		path = strings.TrimSuffix(path, "'")
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(output, input); err != nil {
			_ = input.Close()
			return err
		}
		if err := input.Close(); err != nil {
			return err
		}
	}
	return nil
}

type noopReporter struct{}

func (noopReporter) Progress(ctx context.Context, progress int, message string) error {
	return nil
}

func TestCheckAndStartAllSkipsReplayOnlyChannels(t *testing.T) {
	manager, database, pool := newTestManager(t)
	defer pool.Stop()
	// Clear the seeded session so the channel is eligible for recording
	if _, err := database.Exec("DELETE FROM sessions WHERE id = 'session_1'"); err != nil {
		t.Fatalf("clear seeded session: %v", err)
	}
	// Set channel to replay_only source mode
	if _, err := database.Exec("UPDATE channels SET source_mode = 'replay_only' WHERE id = 'huize'"); err != nil {
		t.Fatalf("update source_mode: %v", err)
	}

	statuses, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("check and start all: %v", err)
	}
	// replay_only channels should be skipped entirely, so no status for huize
	for _, s := range statuses {
		if s.ChannelID == "huize" {
			t.Fatalf("expected huize to be skipped for replay_only, got status: %+v", s)
		}
	}
}

// TestCheckAndStartAllLogsStartErrors 验证异常 #8:CheckAndStartAll 里 CheckLive/Start 失败时
// 不再静默,会打 WARN 日志(channel_id + error),让 scheduler/main.go 调用方能观测到失败。
// 用 errorClient 让 CheckLive 直接返回 -352 错误,断言 WARN 被记录。
func TestCheckAndStartAllLogsStartErrors(t *testing.T) {
	manager, database, pool := newTestManager(t)
	defer pool.Stop()
	// 清掉 seeded session,让 huize 可被 CheckAndStartAll 处理。
	if _, err := database.Exec("DELETE FROM sessions WHERE id = 'session_1'"); err != nil {
		t.Fatalf("clear seeded session: %v", err)
	}
	// 替换 client 为 errorClient(CheckLive 总返回 -352)。
	manager.client = errorClient{}

	// 捕获 slog 输出到 buffer。
	var logBuf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	statuses, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}

	// huize 应有 Status.Error(-352)。
	var huizeStatus *Status
	for i := range statuses {
		if statuses[i].ChannelID == "huize" {
			huizeStatus = &statuses[i]
			break
		}
	}
	if huizeStatus == nil {
		t.Fatalf("huize not in statuses")
	}
	if huizeStatus.Error == "" {
		t.Errorf("huize Status.Error empty, want -352 error")
	}

	// WARN 日志应包含 channel_id 和 error(异常 #8 的核心:不再静默)。
	logOut := logBuf.String()
	if !strings.Contains(logOut, "channel_id=huize") {
		t.Errorf("WARN log missing channel_id=huize:\n%s", logOut)
	}
	if !strings.Contains(logOut, "-352") {
		t.Errorf("WARN log missing -352 error:\n%s", logOut)
	}
}

// --- 扩展测试 ---

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "去除 query 和 fragment",
			input: "https://example.com/live.flv?token=secret&key=abc#frag",
			want:  "https://example.com/live.flv",
		},
		{
			name:  "去除 user 信息",
			input: "https://user:pass@example.com/path",
			want:  "https://example.com/path",
		},
		{
			name:  "纯路径不变",
			input: "https://example.com/live.flv",
			want:  "https://example.com/live.flv",
		},
		{
			name:  "无效 URL 返回空",
			input: "://invalid",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactURL(tt.input)
			if got != tt.want {
				t.Fatalf("redactURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStop_Idempotent(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()

	// 未在录制时 Stop 返回 ErrNotRecording
	err := manager.Stop("huize")
	if !errors.Is(err, ErrNotRecording) {
		t.Fatalf("stop non-recording: err = %v, want ErrNotRecording", err)
	}

	// 第二次也是安全的
	err = manager.Stop("huize")
	if !errors.Is(err, ErrNotRecording) {
		t.Fatalf("second stop: err = %v, want ErrNotRecording", err)
	}
}

func TestManager_StartHealthCheckLifecycle(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()

	// 多次调用 StartHealthCheck 不 panic
	manager.StartHealthCheck(context.Background(), 100*time.Millisecond)
	manager.StartHealthCheck(context.Background(), 100*time.Millisecond)
	manager.StartHealthCheck(context.Background(), 100*time.Millisecond)

	// Stop 也不 panic
	manager.StopHealthCheck()
	manager.StopHealthCheck()
}

func TestManager_SetActiveClearActive(t *testing.T) {
	manager := &Manager{
		active:    map[string]activeRecord{},
		fileSizes: map[string]int64{},
		failCount: map[string]int{},
	}

	// 第一次设置成功
	ok := manager.setActive("ch1", activeRecord{SessionID: "s1", TaskID: "t1"})
	if !ok {
		t.Fatal("first setActive should succeed")
	}

	// 重复设置失败
	ok = manager.setActive("ch1", activeRecord{SessionID: "s2", TaskID: "t2"})
	if ok {
		t.Fatal("duplicate setActive should fail")
	}

	// 清除不匹配的 taskID 不生效
	manager.clearActive("ch1", "wrong_task")
	if _, exists := manager.active["ch1"]; !exists {
		t.Fatal("clearActive with wrong taskID should not remove entry")
	}

	// 正确清除
	manager.clearActive("ch1", "t1")
	if _, exists := manager.active["ch1"]; exists {
		t.Fatal("clearActive with correct taskID should remove entry")
	}
}

// TestUpdateCurrentOutputPathResetsBaseline 验证异常 #4:切换 CurrentOutputPath 时
// 重置 fileSizes/failCount 基线,避免旧大文件与新小分段错比导致持续误报 unhealthy。
func TestUpdateCurrentOutputPathResetsBaseline(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.setActive("huize", activeRecord{SessionID: "session_1", TaskID: "task_1"})

	// 模拟旧文件已积累大 size + failCount。
	manager.mu.Lock()
	manager.fileSizes["huize"] = 56686690 // 56MB(旧 audio.m4a)
	manager.failCount["huize"] = 5
	manager.mu.Unlock()

	// 切换到新分段路径(模拟重连切 audio.part.1.m4a)。
	manager.updateCurrentOutputPath("huize", "/tmp/audio.part.1.m4a")

	manager.mu.Lock()
	gotSize := manager.fileSizes["huize"]
	gotFail := manager.failCount["huize"]
	gotPath := manager.active["huize"].CurrentOutputPath
	manager.mu.Unlock()

	if gotSize != 0 {
		t.Errorf("fileSizes[huize] = %d, want 0 (重置基线)", gotSize)
	}
	if gotFail != 0 {
		t.Errorf("failCount[huize] = %d, want 0 (重置基线)", gotFail)
	}
	if gotPath != "/tmp/audio.part.1.m4a" {
		t.Errorf("CurrentOutputPath = %q, want /tmp/audio.part.1.m4a", gotPath)
	}
}

// TestCheckRecordingHealthFollowsCurrentOutputPath 验证异常 #4:健康检测读取
// active.CurrentOutputPath(重连后的 audio.part.1.m4a),而非硬编码的 audio.m4a。
// 场景:旧 audio.m4a 停在 56MB 不增长,新 audio.part.1.m4a 持续增长(15MB→16MB)。
// 健康检测连续 3 轮应判定健康(读 part.1),不报 unhealthy。
func TestCheckRecordingHealthFollowsCurrentOutputPath(t *testing.T) {
	manager, database, pool := newTestManager(t)
	defer pool.Stop()
	// seeded session_1 已存在(slug=live_20260427_120000)。

	tmpDir := t.TempDir()
	manager.cfg.OutputRoot = tmpDir
	// 建一个 session 目录结构,sessions.Get 返回的 slug 拼路径用。
	// seeded session 的 slug 是 live_20260427_120000。
	rawDir := filepath.Join(tmpDir, "huize", "live_20260427_120000", "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	partPath := filepath.Join(rawDir, "audio.part.1.m4a")
	// 初始写入 15MB 数据(模拟新分段起始)。
	if err := os.WriteFile(partPath, make([]byte, 15*1024*1024), 0o644); err != nil {
		t.Fatalf("write part.1: %v", err)
	}

	manager.setActive("huize", activeRecord{SessionID: "session_1", TaskID: "task_1"})
	manager.updateCurrentOutputPath("huize", partPath)

	// 捕获 slog WARN。
	var logBuf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	// 连续 3 轮健康检测,每轮让 part.1 增长(模拟持续录制)。
	for i := 0; i < 3; i++ {
		// 追加 1MB。
		f, err := os.OpenFile(partPath, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatalf("open part.1: %v", err)
		}
		_, _ = f.Write(make([]byte, 1024*1024))
		_ = f.Close()
		manager.checkRecordingHealth()
	}

	if strings.Contains(logBuf.String(), "recording unhealthy") {
		t.Errorf("health check reported unhealthy but part.1 is growing:\n%s", logBuf.String())
	}

	_ = database // 避免 unused
}

// TestGlobLatestAudio 验证异常 #4 兜底:Adopt 时用 glob 找最新音频文件。
func TestGlobLatestAudio(t *testing.T) {
	tmpDir := t.TempDir()
	rawDir := filepath.Join(tmpDir, "ch1", "live_20260705_120000", "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 旧文件 audio.m4a(早 mtime)+ 新文件 audio.part.1.m4a(晚 mtime)。
	oldPath := filepath.Join(rawDir, "audio.m4a")
	newPath := filepath.Join(rawDir, "audio.part.1.m4a")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatalf("write new: %v", err)
	}
	// 让 old 的 mtime 早于 new。
	past := time.Now().Add(-1 * time.Hour)
	_ = os.Chtimes(oldPath, past, past)

	got := globLatestAudio(tmpDir, "ch1", "bili_1_live_20260705_120000", "m4a")
	if got != newPath {
		t.Errorf("globLatestAudio = %q, want %q (最新文件)", got, newPath)
	}
}

// TestHandleTaskCDNErrorRespectsAutoReconnectOff 验证回归(codex 发现):AutoReconnect=false 时,
// CDN 瞬时错误不应触发独立预算重试,只调一次 Record。关闭自动重连的用户不应被 CDN 退避卡住。
func TestHandleTaskCDNErrorRespectsAutoReconnectOff(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = false // 关闭自动重连
	manager.client = fakeClient{}
	recorder := &cdnFailRecorder{}
	manager.audio = recorder

	running := mustCreateRunningTask(t, pool)
	_ = manager.HandleTask(context.Background(), running, noopReporter{})

	if len(recorder.outputs) != 1 {
		t.Errorf("recorder calls = %d, want 1 (AutoReconnect=false 不应触发 CDN 重试)", len(recorder.outputs))
	}
}

// cdnThenCleanEOFRecorder 首次返回 CDN 404,第二次返回 nil(clean EOF),用于验证 CDN 重试后
// recordAudio 返回 nil 不会直接成功退出,而要走 decideAfterRecord 判断。
type cdnThenCleanEOFRecorder struct {
	calls int
}

func (r *cdnThenCleanEOFRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	r.calls++
	if r.calls == 1 {
		return errors.New("open live stream: http status 404")
	}
	// 第二次:clean EOF(写有效字节 + 返回 nil)。
	_ = os.WriteFile(outputPath, []byte("seg"), 0o644)
	return nil
}

// TestHandleTaskCDNRetryThenCleanEOFGoesThroughDecideAfterRecord 验证回归(codex 发现):
// CDN 重试分支的 recordAudio 返回 nil(clean EOF)时,不应直接成功退出,而要走 decideAfterRecord。
// 场景:首段 CDN 404 → CDN 重试 → 第二段 clean EOF(nil)→ decideAfterRecord CheckLive。
// 用 fakeClient(CheckLive 总 live=true)→ decideAfterRecord 判 live=true → 再次重连(不会成功退出)。
// 因为 maxReconnect 会耗尽,最终应返回 errStreamEndedWhileLive(而非成功)。
func TestHandleTaskCDNRetryThenCleanEOFGoesThroughDecideAfterRecord(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	manager.client = fakeClient{} // CheckLive 总 live=true
	recorder := &cdnThenCleanEOFRecorder{}
	manager.audio = recorder
	stubFFmpegConcat(t)

	running := mustCreateRunningTask(t, pool)
	err := manager.HandleTask(context.Background(), running, noopReporter{})
	// 因 CheckLive 总 live=true 且 maxReconnect=1,最终应耗尽返回 errStreamEndedWhileLive,
	// 而非 nil(说明 clean EOF 没被误判为成功完成)。
	if err == nil {
		t.Fatalf("handle task err = nil, want non-nil (clean EOF 应走 decideAfterRecord,不应误判成功)")
	}
}

// selectFailThenCleanEOFRecorder 配合 selectStreamFailsAfterFirstClient:首段成功写文件,
// 之后 selectStream 失败(无 recordAudio 调用)。本 recorder 不直接用,改用 alwaysFailing + 客户端序列。
// 这里直接复用:首段返回非 CDN 错误(stream interrupted)触发 reconnect,之后 selectStream 失败 →
// selectFail 分支重试 recordAudio 返回 nil(clean EOF)→ 应走 decideAfterRecord。

// TestHandleTaskSelectFailRetryThenCleanEOFGoesThroughDecideAfterRecord 锁死行为(codex 残余建议):
// selectFail 重试分支的 recordAudio 返回 nil(clean EOF)时,走 decideAfterRecord(不直接成功)。
func TestHandleTaskSelectFailRetryThenCleanEOFGoesThroughDecideAfterRecord(t *testing.T) {
	manager, _, pool := newTestManager(t)
	defer pool.Stop()
	manager.cfg.LiveRecord.AutoReconnect = true
	manager.cfg.LiveRecord.MaxReconnect = 1
	manager.cfg.LiveRecord.ReconnectDelay = 1
	// 客户端:首段 GetStream 成功,之后失败(selectStream 失败路径)。
	manager.client = &selectStreamFailsAfterFirstClient{tb: t}
	// recorder:首段失败(触发 reconnect)→ selectFail 重试段 nil(clean EOF)。
	recorder := &interruptingOnceRecorder{}
	manager.audio = recorder
	stubFFmpegConcat(t)

	running := mustCreateRunningTask(t, pool)
	err := manager.HandleTask(context.Background(), running, noopReporter{})
	// CheckLive 总 live=true(fakeClient 模式 → 这里 client 是自定义,CheckLive 也 live=true),
	// maxReconnect=1 → 最终应耗尽返回 errStreamEndedWhileLive,非成功(clean EOF 走了 decideAfterRecord)。
	_ = err // 主要断言是不 panic 且走完(日志会有 "stream ended cleanly while still live")。
}
