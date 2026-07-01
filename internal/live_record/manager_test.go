package live_record

import (
	"context"
	"database/sql"
	"errors"
	"io"
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

// mustCreateRunningTask 封装「创建 + MarkRunning」的样板，返回可直接交给 HandleTask 的 task。
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

// countingRecorder 记录 Record 调用次数与目标路径，用于断言录制是否被触发。
type countingRecorder struct {
	outputs []string
}

func (r *countingRecorder) Record(ctx context.Context, stream StreamInfo, outputPath string) error {
	r.outputs = append(r.outputs, outputPath)
	return os.WriteFile(outputPath, []byte("audio"), 0o644)
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
