package live_record

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// captureLogs 把默认 slog 切到内存 buffer(t.Cleanup 恢复),用于断言 F2 诚实化日志的发射面。
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return &buf
}

// 槽位复活(2026-08-20 同场重录修复)测试。
// 范式:真实 sqlite + 真实 Store/Pool 但 pool 不 Start(同 newAnomaly9Manager,
// manager_anomaly9_test.go:47-89)——tasks 入队但不被消费,HandleTask 不会跑,
// 对 task 状态/attempt 的断言无竞态。端到端 case 10 例外,用 newTestManager。

var rerecordSlotStartedAt = time.Date(2026, 4, 27, 13, 0, 0, 0, time.Local)

// newRerecordManager 构造未启动 worker pool 的 Manager(冷却 600s / 上限 3,均为显式配置:
// config 字面量不经 viper setDefault,0 会被 EffectiveRerecordCooldown 解释为禁用)。
func newRerecordManager(t *testing.T, client BiliClient) (*Manager, *worker.Pool, *sql.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO channels(id, name, uid, live_room_id, enabled, auto_record)
		VALUES ('huize', 'Hikami', 1, 123, 1, 1);
	`); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	cfg := &config.Config{
		OutputRoot: t.TempDir(),
		FFmpeg:     "ffmpeg",
		LiveRecord: config.LiveRecordConfig{
			Enabled:                 true,
			AudioContainer:          "m4a",
			RerecordCooldownSeconds: 600,
			RerecordMaxAttempts:     3,
		},
	}
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	// pool 不 Start:tasks 入队但不会被消费,HandleTask 不会跑。
	pool := worker.NewPool(taskStore, hub, 1, nil)
	manager := NewManager(
		cfg,
		channel.NewStore(database),
		session.NewStore(database),
		state.NewStore(database),
		pool,
		client,
		fileAudioRecorder{},
		NoopDanmakuRecorder{},
	)
	return manager, pool, database
}

func mustExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// seedSlotSession 直接落库一个撞槽场景:槽 session(与 countingClient/fakeClient 的固定
// StartedAt 同槽)+ 指定状态的 live_record 任务。返回 (slotID, taskID)。
func seedSlotSession(t *testing.T, database *sql.DB, slotStatus, updatedAt, taskStatus string, taskAttempt int) (string, string) {
	t.Helper()
	slugTime := session.LiveSlotSlugTime(rerecordSlotStartedAt)
	slotID := "huize_live_" + slugTime
	mustExec(t, database, `
		INSERT INTO sessions(id, slug, channel_id, source_type, source_id, title, started_at, source_url, status, updated_at)
		VALUES (?, ?, 'huize', 'live_record', ?, 'Live', '2026-04-27T13:00:00+08:00', 'https://live.bilibili.com/123', ?, ?)`,
		slotID, "live_"+slugTime, "live-123-"+slugTime, slotStatus, updatedAt)
	taskID := "task_rerecord_seed"
	mustExec(t, database, `
		INSERT INTO tasks(id, channel_id, session_id, type, status, payload, progress, message, error, attempt)
		VALUES (?, 'huize', ?, 'live_record', ?, '{"room_id":123}', 15, 'record failed', 'open live stream: http status 404', ?)`,
		taskID, slotID, taskStatus, taskAttempt)
	return slotID, taskID
}

// slotRawDir 返回槽 session 的 raw 目录(不存在时创建)。
func slotRawDir(t *testing.T, m *Manager) string {
	t.Helper()
	dir := filepath.Join(m.cfg.OutputRoot, "huize", "live_"+session.LiveSlotSlugTime(rerecordSlotStartedAt), "raw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir raw: %v", err)
	}
	return dir
}

// Case 1:撞槽 + 槽会话 failed(冷却已过)+ 零音频 → 复活重录(两阶段:首轮真实建槽,
// 钉死 CreateLive 与 LiveSlotSessionID 的 id 一致性;raw/ 预置非音频文件,钉死门控只匹配 audio.*)。
func TestRerecordFailedSlot_RevivesAfterCooldown(t *testing.T) {
	client := &countingClient{live: true}
	manager, pool, database := newRerecordManager(t, client)
	logs := captureLogs(t)
	ctx := context.Background()

	// 首轮:真实建槽 session + 任务(任务 pending,attempt=1)。
	if _, err := manager.CheckAndStartAll(ctx); err != nil {
		t.Fatalf("first CheckAndStartAll: %v", err)
	}
	slotID := session.LiveSlotSessionID("huize", rerecordSlotStartedAt)
	task, err := pool.Store().LatestBySessionAndType(ctx, slotID, TaskType)
	if err != nil {
		t.Fatalf("lookup slot task: %v", err)
	}
	if task.Status != worker.StatusPending || task.Attempt != 1 {
		t.Fatalf("after first tick: task=%s attempt=%d, want pending/1", task.Status, task.Attempt)
	}

	// 模拟录制失败:session failed(20 分钟前)+ task failed。
	failedAt := time.Now().Add(-20 * time.Minute).Format(time.RFC3339)
	mustExec(t, database, `UPDATE sessions SET status='failed', updated_at=? WHERE id=?`, failedAt, slotID)
	mustExec(t, database, `UPDATE tasks SET status='failed', error='open live stream: http status 404' WHERE id=?`, task.ID)

	// raw/ 只放非音频文件(8-19 真实现场即 77KB 弹幕 + 封面),不得阻断门控。
	rawDir := slotRawDir(t, manager)
	if err := os.WriteFile(filepath.Join(rawDir, "danmaku.jsonl"), []byte(`{"text":"77KB"}`), 0o644); err != nil {
		t.Fatalf("seed danmaku: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rawDir, "cover.jpg"), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatalf("seed cover: %v", err)
	}

	// 二轮:撞槽 → 冷却复活。
	statuses, err := manager.CheckAndStartAll(ctx)
	if err != nil {
		t.Fatalf("second CheckAndStartAll: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Recording || statuses[0].SessionID != slotID {
		t.Fatalf("expected revived recording status for slot session, got %+v", statuses)
	}
	retried, err := pool.Store().Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if retried.Status != worker.StatusPending {
		t.Fatalf("task status after revival = %s, want pending(re-enqueued)", retried.Status)
	}
	if retried.Attempt != 2 {
		t.Fatalf("task attempt after revival = %d, want 2", retried.Attempt)
	}
	if !strings.Contains(logs.String(), "rerecord scheduled for failed live slot session") {
		t.Fatalf("F2 log missing 'rerecord scheduled', got:\n%s", logs.String())
	}
}

// Case 2:冷却未到 → 不复活,任务不变。
func TestRerecordFailedSlot_WaitsCooldown(t *testing.T) {
	client := &countingClient{live: true}
	manager, pool, database := newRerecordManager(t, client)
	slotID, taskID := seedSlotSession(t, database, "failed", time.Now().Format(time.RFC3339), "failed", 1)

	statuses, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	for _, st := range statuses {
		if st.Recording {
			t.Fatalf("cooldown not elapsed: unexpected recording status %+v", st)
		}
	}
	task, err := pool.Store().Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != worker.StatusFailed || task.Attempt != 1 {
		t.Fatalf("task should stay failed/1 during cooldown, got %s/%d", task.Status, task.Attempt)
	}
	_ = slotID
}

// Case 3:attempt 达上限 → 不复活。
func TestRerecordFailedSlot_AttemptsExhausted(t *testing.T) {
	client := &countingClient{live: true}
	manager, pool, database := newRerecordManager(t, client)
	_, taskID := seedSlotSession(t, database, "failed", time.Now().Add(-20*time.Minute).Format(time.RFC3339), "failed", 3)

	if _, err := manager.CheckAndStartAll(context.Background()); err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	task, err := pool.Store().Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != worker.StatusFailed || task.Attempt != 3 {
		t.Fatalf("attempts exhausted: task should stay failed/3, got %s/%d", task.Status, task.Attempt)
	}
}

// Case 4:槽会话非 failed(published)→ 不复活。
func TestRerecordFailedSlot_SkipsNonFailedSlot(t *testing.T) {
	client := &countingClient{live: true}
	manager, pool, database := newRerecordManager(t, client)
	_, taskID := seedSlotSession(t, database, "published", time.Now().Add(-20*time.Minute).Format(time.RFC3339), "failed", 1)

	if _, err := manager.CheckAndStartAll(context.Background()); err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	task, err := pool.Store().Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != worker.StatusFailed || task.Attempt != 1 {
		t.Fatalf("non-failed slot: task should stay failed/1, got %s/%d", task.Status, task.Attempt)
	}
}

// Case 5(H-1 残留音频保全):raw/ 存在非空音频 → 禁自动复活防 -y 截断销毁。
func TestRerecordFailedSlot_BlockedByResidualAudio(t *testing.T) {
	client := &countingClient{live: true}
	manager, pool, database := newRerecordManager(t, client)
	logs := captureLogs(t)
	_, taskID := seedSlotSession(t, database, "failed", time.Now().Add(-20*time.Minute).Format(time.RFC3339), "failed", 1)

	rawDir := slotRawDir(t, manager)
	if err := os.WriteFile(filepath.Join(rawDir, "audio.m4a"), []byte("precious-recording"), 0o644); err != nil {
		t.Fatalf("seed residual audio: %v", err)
	}

	if _, err := manager.CheckAndStartAll(context.Background()); err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	task, err := pool.Store().Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != worker.StatusFailed || task.Attempt != 1 {
		t.Fatalf("residual audio must block revival, got %s/%d", task.Status, task.Attempt)
	}
	if _, err := os.Stat(filepath.Join(rawDir, "audio.m4a")); err != nil {
		t.Fatalf("residual audio must be preserved: %v", err)
	}
	if !strings.Contains(logs.String(), "residual recorded audio") {
		t.Fatalf("F2 log missing residual-audio WARN, got:\n%s", logs.String())
	}
}

// Case 6:零字节残壳被清理,复活照常发生。
func TestRerecordFailedSlot_CleansZeroByteHusks(t *testing.T) {
	client := &countingClient{live: true}
	manager, pool, database := newRerecordManager(t, client)
	_, taskID := seedSlotSession(t, database, "failed", time.Now().Add(-20*time.Minute).Format(time.RFC3339), "failed", 1)

	rawDir := slotRawDir(t, manager)
	husk := filepath.Join(rawDir, "audio.part.1.m4a")
	if err := os.WriteFile(husk, nil, 0o644); err != nil {
		t.Fatalf("seed husk: %v", err)
	}

	if _, err := manager.CheckAndStartAll(context.Background()); err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	if _, err := os.Stat(husk); !os.IsNotExist(err) {
		t.Fatalf("zero-byte husk should be removed, stat err=%v", err)
	}
	task, err := pool.Store().Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != worker.StatusPending || task.Attempt != 2 {
		t.Fatalf("husk cleanup path should revive task, got %s/%d", task.Status, task.Attempt)
	}
}

// Case 7(M-2 死槽):任务被人工删除 → 不复活、不 panic。
func TestRerecordFailedSlot_TaskMissing(t *testing.T) {
	client := &countingClient{live: true}
	manager, _, database := newRerecordManager(t, client)
	seedSlotSession(t, database, "failed", time.Now().Add(-20*time.Minute).Format(time.RFC3339), "failed", 1)
	mustExec(t, database, `DELETE FROM tasks WHERE id='task_rerecord_seed'`)

	if _, err := manager.CheckAndStartAll(context.Background()); err != nil {
		t.Fatalf("CheckAndStartAll with missing task: %v", err)
	}
}

// Case 8(M-2 死槽):任务 cancelled → 不复活。
func TestRerecordFailedSlot_TaskCancelled(t *testing.T) {
	client := &countingClient{live: true}
	manager, pool, database := newRerecordManager(t, client)
	_, taskID := seedSlotSession(t, database, "failed", time.Now().Add(-20*time.Minute).Format(time.RFC3339), "cancelled", 1)

	if _, err := manager.CheckAndStartAll(context.Background()); err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	task, err := pool.Store().Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != worker.StatusCancelled {
		t.Fatalf("cancelled task must not be revived, got %s", task.Status)
	}
}

// Case 9:updated_at 非法 → 按冷却未到保守处理,不复活。
func TestRerecordFailedSlot_InvalidUpdatedAt(t *testing.T) {
	client := &countingClient{live: true}
	manager, pool, database := newRerecordManager(t, client)
	_, taskID := seedSlotSession(t, database, "failed", "not-a-timestamp", "failed", 1)

	if _, err := manager.CheckAndStartAll(context.Background()); err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	task, err := pool.Store().Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != worker.StatusFailed || task.Attempt != 1 {
		t.Fatalf("invalid updated_at should be treated as cooldown-not-elapsed, got %s/%d", task.Status, task.Attempt)
	}
}

// 复活门槛纯函数表驱动(R1-6):全部 7 种判定结果钉死,不依赖 manager 装配。
func TestEvaluateRerecordGate(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	failedAt := now.Add(-20 * time.Minute).Format(time.RFC3339)
	base := rerecordGateInput{
		cooldown:      600 * time.Second,
		maxAttempts:   3,
		now:           now,
		slotStatus:    string(state.StatusFailed),
		slotUpdatedAt: failedAt,
		taskFound:     true,
		taskStatus:    worker.StatusFailed,
		taskAttempt:   1,
	}
	cases := []struct {
		name string
		mut  func(*rerecordGateInput)
		want rerecordDecision
	}{
		{"proceed", func(*rerecordGateInput) {}, rerecordProceed},
		{"disabled(cooldown<=0)", func(in *rerecordGateInput) { in.cooldown = 0 }, rerecordDisabled},
		{"slot not failed", func(in *rerecordGateInput) { in.slotStatus = "recording" }, rerecordSlotNotFailed},
		{"residual audio", func(in *rerecordGateInput) { in.hasResidualAudio = true }, rerecordResidualAudio},
		{"residual audio outranks cooldown-wait", func(in *rerecordGateInput) {
			in.hasResidualAudio = true
			in.slotUpdatedAt = now.Format(time.RFC3339)
		}, rerecordResidualAudio},
		{"task missing", func(in *rerecordGateInput) { in.taskFound = false }, rerecordTaskUnavailable},
		{"task cancelled", func(in *rerecordGateInput) { in.taskStatus = worker.StatusCancelled }, rerecordTaskUnavailable},
		{"task running(already revived)", func(in *rerecordGateInput) { in.taskStatus = worker.StatusRunning }, rerecordTaskUnavailable},
		{"attempts exhausted", func(in *rerecordGateInput) { in.taskAttempt = 3 }, rerecordAttemptsExhausted},
		{"cooldown not elapsed", func(in *rerecordGateInput) { in.slotUpdatedAt = now.Format(time.RFC3339) }, rerecordCooldown},
		{"invalid updated_at treated as cooldown", func(in *rerecordGateInput) { in.slotUpdatedAt = "garbage" }, rerecordCooldown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mut(&in)
			if got := evaluateRerecordGate(in); got != tc.want {
				t.Fatalf("evaluateRerecordGate(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// Case 10(端到端,newTestManager,pool 已启动):复活后 worker 真跑 HandleTask,
// session 经 failed→recording(M6)重入并正常收尾。
func TestRerecordFailedSlot_EndToEndRevivedRun(t *testing.T) {
	manager, database, pool := newTestManager(t)
	// newTestManager 的种子 channel 无 auto_record、session_1 处于 discovered(会命中
	// ActiveLiveForChannel 白名单挡在 ensureStartAllowed)、cfg 未带 rerecord 字段
	// (字面量构造不经 viper,0 = 禁用复活),先校正。
	mustExec(t, database, `UPDATE channels SET auto_record=1 WHERE id='huize'`)
	mustExec(t, database, `UPDATE sessions SET status='failed' WHERE id='session_1'`)
	manager.cfg.LiveRecord.RerecordCooldownSeconds = 600
	manager.cfg.LiveRecord.RerecordMaxAttempts = 3

	slotID, taskID := seedSlotSession(t, database, "failed", time.Now().Add(-20*time.Minute).Format(time.RFC3339), "failed", 1)

	statuses, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Recording || statuses[0].SessionID != slotID {
		t.Fatalf("expected revived recording status, got %+v", statuses)
	}

	// worker 异步消费复活任务:轮询直到 session 经 EventLiveRecordStarted 进入 recording
	// 且任务落终态(fakeClient 恒 live、fileAudioRecorder 秒级成功 → AutoReconnect=false
	// 走 FinishSuccess 收尾)。
	deadline := time.Now().Add(10 * time.Second)
	for {
		sess, getErr := manager.sessions.Get(context.Background(), slotID)
		if getErr != nil {
			t.Fatalf("get session: %v", getErr)
		}
		task, taskErr := pool.Store().Get(context.Background(), taskID)
		if taskErr != nil {
			t.Fatalf("get task: %v", taskErr)
		}
		if sess.Status == string(state.StatusRecording) && task.Status == worker.StatusSucceeded {
			if task.Attempt != 2 {
				t.Fatalf("end-to-end: task attempt = %d, want 2", task.Attempt)
			}
			return
		}
		if task.Status == worker.StatusFailed {
			t.Fatalf("end-to-end: revived run failed: %s", task.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("end-to-end: timed out, session=%s task=%s", sess.Status, task.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// F3:openStream 默认 transport 只限定建连/响应头阶段(不设 Client.Timeout,长流不被掐断)。
func TestOpenStreamTransportDefaults(t *testing.T) {
	if openStreamTransport.ResponseHeaderTimeout != 15*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 15s", openStreamTransport.ResponseHeaderTimeout)
	}
	if openStreamTransport.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v, want 10s", openStreamTransport.TLSHandshakeTimeout)
	}
	if openStreamTransport.DialContext == nil {
		t.Fatal("DialContext must be set (10s dial timeout)")
	}
	if openStreamTransport.Proxy == nil {
		t.Fatal("Proxy must be inherited from DefaultTransport.Clone()")
	}
}

// F3:CDN 挂起(建连后不回响应头)时 openStream 必须被 ResponseHeaderTimeout 切断,
// 而不是无限阻塞占住 active 槽。handler 挂在 channel 上,测试断言完成后释放,
// 让 httptest.Server.Close() 正常回收。
func TestOpenStreamHangsAreCutByResponseHeaderTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	recorder := &FFmpegRecorder{HTTPClient: &http.Client{
		Transport: &http.Transport{ResponseHeaderTimeout: 50 * time.Millisecond},
	}}
	start := time.Now()
	_, err := recorder.openStream(context.Background(), StreamInfo{URL: srv.URL + "/live.flv"})
	if err == nil {
		t.Fatal("openStream should fail on hanging server")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("openStream hung %v, ResponseHeaderTimeout not effective", elapsed)
	}
}
