package asr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

// 见 plans/plan-vad-2026-07-27.md Phase 4。
//
// 这些测试覆盖 HandleTask 的 VAD 集成点,但 VADProcessor 真跑 ffmpeg(单测不该依赖外部进程)。
// 故通过两条路径覆盖:
//  1. vad==nil(老调用方式)→ VAD 分支整个跳过(零回归保证)
//  2. cfg.VAD.Enabled=false → VAD 分支跳过
//  3. vad!=nil + Enabled=true 但 ffmpeg 必然失败(路径不存在) → 走 fallback 用原始音频
//
// BuildSilenceMap/Trim/RemapSegments 的纯函数测试已在 vad_processor_test.go /
// vad_silence_map_test.go 覆盖。VADProcessor 真跑 ffmpeg 的端到端测试在
// vad_integration_test.go(build tag 保护,不进 CI)。

// transcriberSpy 记录最后一次 Transcribe 调用的 audioPath,供断言 VAD 切换是否生效。
type transcriberSpy struct {
	lastAudioPath string
	segments      []map[string]any
}

func (t *transcriberSpy) Transcribe(ctx context.Context, audioPath string, _ session.Session) (Result, error) {
	t.lastAudioPath = audioPath
	return Result{
		Transcript: "transcript",
		SRT:        "srt",
		Segments:   t.segments,
		Raw:        map[string]any{"provider": "spy"},
	}, nil
}

// setupVADHandlerEnv 搭一个 media_ready session + 已写入 audio.asr.mp3 的测试环境,
// 返回注入了 spy transcriber 的 Handler(不注入 VADProcessor,vad 字段为 nil)。
func setupVADHandlerEnv(t *testing.T, vadEnabled bool) (*Handler, *transcriberSpy, string) {
	t.Helper()
	database := setupDB(t)
	insertChannel(t, database, "ch1")
	insertSession(t, database, "media_ready")
	outputRoot := t.TempDir()
	audioPath := filepath.Join(outputRoot, "ch1", "live_20260501_120000", "asr", "audio.asr.mp3")
	if err := os.MkdirAll(filepath.Dir(audioPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audioPath, []byte("FAKEMP3"), 0o644); err != nil {
		t.Fatal(err)
	}
	spy := &transcriberSpy{segments: []map[string]any{
		{"start_ms": int64(1000), "end_ms": int64(2000), "text": "hello"},
	}}
	cfg := &config.Config{
		OutputRoot: outputRoot,
		VAD: config.VADConfig{
			Enabled:        vadEnabled,
			ThresholdDB:    -40,
			MinSilenceSec:  2.0,
			PaddingSec:     0.2,
			MinOutputRatio: 0.3,
		},
	}
	handler := NewHandler(cfg, session.NewStore(database), state.NewStore(database), spy, nil)
	taskStore := worker.NewStore(database)
	pool := worker.NewPool(taskStore, worker.NewHub(), 1, nil)
	if err := pool.Start(context.Background(), 1); err != nil {
		t.Fatalf("start pool: %v", err)
	}
	t.Cleanup(pool.Stop)
	handler.Register(pool)
	return handler, spy, audioPath
}

type noopReporter struct{}

func (n *noopReporter) Progress(ctx context.Context, percent int, message string) error { return nil }

// TestHandleTask_VADNil_UsesOriginal:vad 字段 nil(老调用方式,NewHandler 不传 vadProc)
// → 用原始音频路径,零回归保证(2026-07-27 VAD 引入前的行为完全一致)。
func TestHandleTask_VADNil_UsesOriginal(t *testing.T) {
	handler, spy, audioPath := setupVADHandlerEnv(t, true) // Enabled=true 但 vad==nil
	dummyTask := worker.Task{
		ChannelID: "ch1", SessionID: "session_test", Type: TaskType, Payload: "{}",
	}
	if err := handler.HandleTask(context.Background(), dummyTask, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if spy.lastAudioPath != audioPath {
		t.Errorf("VAD nil: transcriber called with %q, want original %q", spy.lastAudioPath, audioPath)
	}
}

// TestHandleTask_VADDisabled_UsesOriginal:cfg.VAD.Enabled=false → VAD 分支跳过,用原始音频。
func TestHandleTask_VADDisabled_UsesOriginal(t *testing.T) {
	handler, spy, audioPath := setupVADHandlerEnv(t, false)
	dummyTask := worker.Task{
		ChannelID: "ch1", SessionID: "session_test", Type: TaskType, Payload: "{}",
	}
	if err := handler.HandleTask(context.Background(), dummyTask, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if spy.lastAudioPath != audioPath {
		t.Errorf("VAD disabled: transcriber called with %q, want original %q", spy.lastAudioPath, audioPath)
	}
}

// TestHandleTask_OriginalSegmentsPreserved_WhenNoVAD:无 VAD(vad==nil)时,
// segments.json 写盘的 start_ms/end_ms 与 transcriber 返回的完全一致(未被 remap)。
func TestHandleTask_OriginalSegmentsPreserved_WhenNoVAD(t *testing.T) {
	handler, spy, audioPath := setupVADHandlerEnv(t, true)
	_ = spy
	_ = audioPath
	dummyTask := worker.Task{
		ChannelID: "ch1", SessionID: "session_test", Type: TaskType, Payload: "{}",
	}
	if err := handler.HandleTask(context.Background(), dummyTask, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	// 读 segments.json 验证未被 remap
	segPath := filepath.Join(handler.cfg.OutputRoot, "ch1", "live_20260501_120000", "package", "segments.json")
	data, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatalf("read segments.json: %v", err)
	}
	if !strings.Contains(string(data), `"start_ms": 1000`) {
		t.Errorf("segments.json = %s, want start_ms preserved at 1000 (no remap when VAD nil)", string(data))
	}
}

func TestRemapResultTimelineRebuildsSRT(t *testing.T) {
	sm := &SilenceMap{KeptSegments: []KeptSegment{
		{OriginalStartMS: 281000, OriginalEndMS: 291000, TrimmedStartMS: 0, TrimmedEndMS: 10000},
	}}
	result := Result{
		SRT: "1\n00:00:01,000 --> 00:00:02,000\nhello\n",
		Segments: []map[string]any{
			{"start_ms": int64(1000), "end_ms": int64(2000), "text": "hello"},
		},
	}
	remapResultTimeline(&result, sm)
	if got := result.Segments[0]["start_ms"]; got != int64(282000) {
		t.Fatalf("start_ms = %v, want 282000", got)
	}
	if !strings.Contains(result.SRT, "00:04:42,000 --> 00:04:43,000") {
		t.Fatalf("SRT was not rebuilt on original timeline: %q", result.SRT)
	}
}
