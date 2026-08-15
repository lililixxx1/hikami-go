package worker

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"hikami-go/internal/db"
)

func TestTaskStoreLifecycle(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()

	task, err := store.Create(ctx, CreateInput{
		ChannelID: "huize",
		Type:      "discover",
		Payload:   `{"manual":true}`,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Status != StatusPending {
		t.Fatalf("created status = %s, want pending", task.Status)
	}

	task, err = store.MarkRunning(ctx, task.ID)
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if task.Status != StatusRunning {
		t.Fatalf("running status = %s", task.Status)
	}

	task, err = store.UpdateProgress(ctx, task.ID, 40, "working")
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if task.Progress != 40 || task.Message != "working" {
		t.Fatalf("unexpected progress task: %+v", task)
	}

	task, err = store.MarkSucceeded(ctx, task.ID, "done")
	if err != nil {
		t.Fatalf("succeeded: %v", err)
	}
	if task.Status != StatusSucceeded || task.Progress != 100 {
		t.Fatalf("unexpected completed task: %+v", task)
	}
}

func TestRetryFailedTask(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()

	task, err := store.Create(ctx, CreateInput{ChannelID: "huize", Type: "discover"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkFailed(ctx, task.ID, "failed", errors.New("boom")); err != nil {
		t.Fatalf("fail: %v", err)
	}

	retried, err := store.Retry(ctx, task.ID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.Status != StatusPending || retried.Attempt != 2 {
		t.Fatalf("unexpected retried task: %+v", retried)
	}
}

func TestCancelPendingTask(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()

	task, err := store.Create(ctx, CreateInput{ChannelID: "huize", Type: "discover"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cancelled, err := store.Cancel(ctx, task.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("cancelled status = %s", cancelled.Status)
	}
}

func TestActiveBySessionAndTypeFindsPendingOrRunningTask(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()
	if _, err := store.db.Exec(`
		INSERT INTO sessions(id, slug, channel_id, source_type, source_id, title, status)
		VALUES ('session_1', 'session_1', 'huize', 'live_record', 'live-1', 'Live', 'media_ready')
	`); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	task, err := store.Create(ctx, CreateInput{ChannelID: "huize", SessionID: "session_1", Type: "asr"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	active, ok, err := store.ActiveBySessionAndType(ctx, "session_1", "asr")
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if !ok || active.ID != task.ID {
		t.Fatalf("active=%+v ok=%t, want task %s", active, ok, task.ID)
	}

	if _, err := store.MarkRunning(ctx, task.ID); err != nil {
		t.Fatalf("running: %v", err)
	}
	active, ok, err = store.ActiveBySessionAndType(ctx, "session_1", "asr")
	if err != nil {
		t.Fatalf("active running: %v", err)
	}
	if !ok || active.ID != task.ID {
		t.Fatalf("active running=%+v ok=%t, want task %s", active, ok, task.ID)
	}

	if _, err := store.MarkSucceeded(ctx, task.ID, "done"); err != nil {
		t.Fatalf("succeeded: %v", err)
	}
	_, ok, err = store.ActiveBySessionAndType(ctx, "session_1", "asr")
	if err != nil {
		t.Fatalf("active succeeded: %v", err)
	}
	if ok {
		t.Fatalf("succeeded task should not be active")
	}
}

func TestRecoverRunningMarksFailed(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()

	task, err := store.Create(ctx, CreateInput{ChannelID: "huize", Type: "discover"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkRunning(ctx, task.ID); err != nil {
		t.Fatalf("running: %v", err)
	}
	if err := store.RecoverRunning(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	recovered, err := store.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if recovered.Status != StatusFailed {
		t.Fatalf("recovered status = %s, want failed", recovered.Status)
	}
}

// TestCreateBypassFailStateRoundTrip 验证 CreateInput.BypassFailState 正确持久化并能回读。
// 重新生成回顾等非推进型任务依赖此标志：失败时不降级 session 主状态（仅写 last_error）。
func TestCreateBypassFailStateRoundTrip(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()

	// 带 BypassFailState=true 的任务
	bypassTask, err := store.Create(ctx, CreateInput{
		ChannelID:       "huize",
		Type:            "recap",
		Payload:         "{}",
		BypassFailState: true,
	})
	if err != nil {
		t.Fatalf("create bypass task: %v", err)
	}
	if !bypassTask.BypassFailState {
		t.Fatalf("created task BypassFailState = false, want true")
	}

	// 回读验证（经 Get 走 scanTaskCore）
	got, err := store.Get(ctx, bypassTask.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.BypassFailState {
		t.Fatalf("reread task BypassFailState = false, want true")
	}

	// 对照：默认任务 BypassFailState=false
	normalTask, err := store.Create(ctx, CreateInput{
		ChannelID: "huize",
		Type:      "discover",
		Payload:   "{}",
	})
	if err != nil {
		t.Fatalf("create normal task: %v", err)
	}
	if normalTask.BypassFailState {
		t.Fatalf("default task BypassFailState = true, want false")
	}
	gotNormal, err := store.Get(ctx, normalTask.ID)
	if err != nil {
		t.Fatalf("get normal: %v", err)
	}
	if gotNormal.BypassFailState {
		t.Fatalf("reread default task BypassFailState = true, want false")
	}
}

func newTaskTestStore(t *testing.T) *Store {
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
	if _, err := database.Exec("INSERT INTO channels(id, name, uid) VALUES ('huize', 'Hikami', 1)"); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return NewStore(database)
}

// TestListReturnsChannelName 验证 Store.List 通过 LEFT JOIN channels 返回 channel_name。
func TestListReturnsChannelName(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()

	// 额外插入一个带中文名 + live_room_id 的频道，匹配 Task 0.1 的 session JOIN 用例风格。
	if _, err := store.db.Exec(`INSERT INTO channels (id, name, uid, live_room_id) VALUES ('huoxisi', '火西肆', 1401928, 924973)`); err != nil {
		t.Fatalf("seed channel huoxisi: %v", err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO sessions(id, slug, channel_id, source_type, source_id, title, status)
		VALUES ('session_hxs', 'session_hxs', 'huoxisi', 'live_record', 'live-924973', 'Live', 'media_ready')
	`); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	task, err := store.Create(ctx, CreateInput{
		ChannelID: "huoxisi",
		SessionID: "session_hxs",
		Type:      "asr",
		Payload:   "{}",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	tasks, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var found *Task
	for i := range tasks {
		if tasks[i].ID == task.ID {
			found = &tasks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("created task %s not in List result (len=%d)", task.ID, len(tasks))
	}
	if found.ChannelName != "火西肆" {
		t.Fatalf("ChannelName = %q, want %q", found.ChannelName, "火西肆")
	}
}

// TestStoreCreateTaskIfNoActiveIdempotent M11:同 session+type 的第二次调用不再创建,
// created=false 且返回既有任务;既有任务进入终态(succeeded)后可再次创建;
// 缺 session_id 拒绝(该原语以 session+type 为幂等键)。
// seedTaskTestSession 在 sessions 表插入最小行(tasks.session_id 有 FK → sessions 约束,
// 测试造任务前需先造 session)。
func seedTaskTestSession(t *testing.T, store *Store, id string) {
	t.Helper()
	if _, err := store.db.Exec(`INSERT INTO sessions(id, slug, channel_id, source_type, source_id, title, status)
		VALUES (?, ?, 'huize', 'download', 'BV1m11', 'M11 测试', 'recap_done')`, id, "slug_"+id); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

func TestStoreCreateTaskIfNoActiveIdempotent(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()
	seedTaskTestSession(t, store, "sess_m11")

	first, created, err := store.CreateTaskIfNoActive(ctx, CreateInput{
		ChannelID: "huize", SessionID: "sess_m11", Type: "publish", Payload: "{}",
	})
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	second, created, err := store.CreateTaskIfNoActive(ctx, CreateInput{
		ChannelID: "huize", SessionID: "sess_m11", Type: "publish", Payload: "{}",
	})
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if created {
		t.Fatalf("second create should not create a new task")
	}
	if second.ID != first.ID {
		t.Fatalf("second create returned existing task %q, want %q", second.ID, first.ID)
	}

	if _, err := store.MarkRunning(ctx, first.ID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if _, err := store.MarkSucceeded(ctx, first.ID, "done"); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	third, created, err := store.CreateTaskIfNoActive(ctx, CreateInput{
		ChannelID: "huize", SessionID: "sess_m11", Type: "publish", Payload: "{}",
	})
	if err != nil || !created {
		t.Fatalf("create after terminal: created=%v err=%v", created, err)
	}
	if third.ID == first.ID {
		t.Fatalf("create after terminal should be a new task")
	}

	if _, _, err := store.CreateTaskIfNoActive(ctx, CreateInput{ChannelID: "huize", Type: "publish"}); err == nil {
		t.Fatalf("expected error for missing session_id")
	}
}

// TestStoreCreateTaskIfNoActiveConcurrent M11:并发竞争下只创建一个任务
// (INSERT…SELECT…WHERE NOT EXISTS 单语句原子;单连接 SQLite 写串行)。
func TestStoreCreateTaskIfNoActiveConcurrent(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()
	seedTaskTestSession(t, store, "sess_m11_race")

	const n = 8
	var mu sync.Mutex
	createdCount := 0
	ids := make(map[string]struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, created, err := store.CreateTaskIfNoActive(ctx, CreateInput{
				ChannelID: "huize", SessionID: "sess_m11_race", Type: "publish", Payload: "{}",
			})
			if err != nil {
				t.Errorf("concurrent create: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if created {
				createdCount++
			}
			ids[task.ID] = struct{}{}
		}()
	}
	wg.Wait()
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
	if len(ids) != 1 {
		t.Fatalf("distinct task ids = %d, want 1: %v", len(ids), ids)
	}
}

// TestStoreUpdatePayload M11:payload 覆盖写 round-trip;空串归一为 "{}";
// 不存在任务返回 ErrTaskNotFound。
func TestStoreUpdatePayload(t *testing.T) {
	store := newTaskTestStore(t)
	ctx := context.Background()
	seedTaskTestSession(t, store, "sess_m11p")

	task, err := store.Create(ctx, CreateInput{ChannelID: "huize", SessionID: "sess_m11p", Type: "publish"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.UpdatePayload(ctx, task.ID, `{"draft_id":"12345"}`); err != nil {
		t.Fatalf("update payload: %v", err)
	}
	updated, err := store.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if updated.Payload != `{"draft_id":"12345"}` {
		t.Fatalf("payload = %q, want draft_id json", updated.Payload)
	}
	if err := store.UpdatePayload(ctx, task.ID, ""); err != nil {
		t.Fatalf("update empty payload: %v", err)
	}
	updated, err = store.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if updated.Payload != "{}" {
		t.Fatalf("payload = %q, want {}", updated.Payload)
	}
	if err := store.UpdatePayload(ctx, "task_missing", "{}"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("update missing task: err = %v, want ErrTaskNotFound", err)
	}
}

