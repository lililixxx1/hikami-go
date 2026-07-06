package live_record

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

// countingClient 统计 CheckLive 调用次数,可配置返回的 LiveInfo 或 error。
// 用于异常 #9 测试:验证 checkOne 去重(单 tick 内 CheckLive 仅 1 次)、冷却跳过(checkLiveCalls==0)等。
type countingClient struct {
	mu           sync.Mutex
	checkLiveCnt int32
	live         bool
	err          error // 非 nil 时 CheckLive 返回此错误
}

func (c *countingClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	atomic.AddInt32(&c.checkLiveCnt, 1)
	if c.err != nil {
		return LiveInfo{}, c.err
	}
	return LiveInfo{
		RoomID:    roomID,
		Live:      c.live,
		Title:     "test",
		StartedAt: time.Date(2026, 4, 27, 13, 0, 0, 0, time.Local),
	}, nil
}

func (c *countingClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	return StreamInfo{URL: "https://example.com/live.flv", AudioOnly: true}, nil
}

// newAnomaly9Manager 构造带真实 channels/sessions store 但**未启动 worker pool** 的 Manager。
// 不启动 pool 是为了隔离 checkOne 入队前路径(codex v1 #7):避免 HandleTask 的 preflight CheckLive
// 干扰 checkOne 路径的 CheckLive 计数。channel huize 设为 auto_record=true。
func newAnomaly9Manager(t *testing.T, client BiliClient) (*Manager, *worker.Pool) {
	t.Helper()
	database, err := db.Open(t.TempDir() + "/hikami.db")
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
			Enabled:        true,
			AudioContainer: "m4a",
		},
	}
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	// pool 不 Start:tasks 入队但不会被消费,HandleTask 不会跑,preflight CheckLive 不触发
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
	return manager, pool
}

// TestCheckAndStartAllDedupCheckLive (异常 #9 第1层):
// live+auto_record 频道在单 tick 内 CheckLive 应仅被调用 1 次(checkOne 去重,不再经 Start 二次 CheckLive)。
func TestCheckAndStartAllDedupCheckLive(t *testing.T) {
	client := &countingClient{live: true}
	manager, _ := newAnomaly9Manager(t, client)

	statuses, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	if got := atomic.LoadInt32(&client.checkLiveCnt); got != 1 {
		t.Fatalf("CheckLive calls = %d, want 1 (checkOne dedup; was 2 before fix)", got)
	}
	// huize 应被处理(live=true, auto_record=true → 尝试 startWithInfo)
	var found bool
	for _, s := range statuses {
		if s.ChannelID == "huize" {
			found = true
		}
	}
	if !found {
		t.Fatalf("huize missing from statuses: %+v", statuses)
	}
}

// TestCheckAndStartAllMarksCooldownOn352 (异常 #9 第2层):
// CheckLive 返回 ErrRiskControl352 → 频道进入冷却(lastRiskCooldown[id] 设为未来时间)。
func TestCheckAndStartAllMarksCooldownOn352(t *testing.T) {
	client := &countingClient{err: ErrRiskControl352}
	manager, _ := newAnomaly9Manager(t, client)

	before := time.Now()
	_, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	until, cooled := manager.cooldownRiskUntil("huize")
	if !cooled {
		t.Fatal("expected huize to be in cooldown after -352")
	}
	// 首次冷却 5 分钟
	if until.Before(before.Add(4 * time.Minute)) {
		t.Fatalf("cooldown until = %v, want at least 4m from %v", until, before)
	}
}

// TestCheckAndStartAllSkipsCooldown352 (异常 #9 第2层):
// 预置冷却 → 该频道 CheckLive 不被调用(checkLiveCalls==0),Status 非空(带频道身份)。
func TestCheckAndStartAllSkipsCooldown352(t *testing.T) {
	client := &countingClient{live: true}
	manager, _ := newAnomaly9Manager(t, client)
	// 预置冷却到 5 分钟后
	manager.applyCooldownRiskControl("huize")

	statuses, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	if got := atomic.LoadInt32(&client.checkLiveCnt); got != 0 {
		t.Fatalf("CheckLive calls = %d, want 0 (channel in cooldown, skipped)", got)
	}
	// Status 必须非空(codex v1 #4):带频道身份 + Error
	var huizeStatus *Status
	for i := range statuses {
		if statuses[i].ChannelID == "huize" {
			huizeStatus = &statuses[i]
			break
		}
	}
	if huizeStatus == nil {
		t.Fatal("huize status missing (cooldown Status must not be empty)")
	}
	if huizeStatus.RoomID != 123 {
		t.Errorf("cooldown Status RoomID = %d, want 123", huizeStatus.RoomID)
	}
	if huizeStatus.Error == "" {
		t.Error("cooldown Status Error empty, want cooldown message")
	}
}

// TestCheckAndStartAllResetsCooldownOnSuccess (异常 #9 第2层):
// 冷却中的频道在冷却到期后 CheckLive 成功 → 冷却被清除(cooldownStep 归零,不跨成功累积)。
func TestCheckAndStartAllResetsCooldownOnSuccess(t *testing.T) {
	client := &countingClient{live: false} // offline 但成功响应(无 error)
	manager, _ := newAnomaly9Manager(t, client)
	// 先打一个冷却(step→1, until=now+5m)
	manager.applyCooldownRiskControl("huize")
	// 手动让冷却到期
	manager.mu.Lock()
	manager.lastRiskCooldown["huize"] = time.Now().Add(-1 * time.Minute) // 已过期
	manager.mu.Unlock()

	_, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	// 成功 CheckLive(offline 但无 error)应清掉冷却和 step
	manager.mu.Lock()
	_, hasCooldown := manager.lastRiskCooldown["huize"]
	step := manager.cooldownStep["huize"]
	manager.mu.Unlock()
	if hasCooldown {
		t.Error("cooldown should be cleared after successful CheckLive")
	}
	if step != 0 {
		t.Errorf("cooldownStep = %d, want 0 (reset on success)", step)
	}
}

// TestCheckAndStartAllSkipsWhenLiveRecordDisabled (异常 #9 codex v2 #2):
// LiveRecord.Enabled=false → ensureStartAllowed 挡住,不建 session、不入队。
func TestCheckAndStartAllSkipsWhenLiveRecordDisabled(t *testing.T) {
	client := &countingClient{live: true}
	manager, _ := newAnomaly9Manager(t, client)
	manager.cfg.LiveRecord.Enabled = false

	statuses, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	// huize 应有 Status.Error(ensureStartAllowed 返回 ErrLiveDisabled)
	var huizeStatus *Status
	for i := range statuses {
		if statuses[i].ChannelID == "huize" {
			huizeStatus = &statuses[i]
			break
		}
	}
	if huizeStatus == nil {
		t.Fatal("huize status missing")
	}
	if huizeStatus.Error == "" {
		t.Error("expected Status.Error when LiveRecord disabled")
	}
}

// TestEnsureStartAllowed (异常 #9 第1层 codex v2 #2):
// 覆盖 4 个防护分支:Enabled / LiveRoomID+Enabled / activeFor / ActiveLiveForChannel。
func TestEnsureStartAllowed(t *testing.T) {
	client := &countingClient{live: true}
	manager, _ := newAnomaly9Manager(t, client)
	ch := channel.Channel{ID: "huize", LiveRoomID: 123, Enabled: true}

	// 1. Enabled=false → ErrLiveDisabled
	manager.cfg.LiveRecord.Enabled = false
	if err := manager.ensureStartAllowed(context.Background(), ch); !errors.Is(err, ErrLiveDisabled) {
		t.Errorf("disabled: err = %v, want ErrLiveDisabled", err)
	}
	manager.cfg.LiveRecord.Enabled = true

	// 2. LiveRoomID<=0 → ErrChannelNotRecordable
	ch2 := ch
	ch2.LiveRoomID = 0
	if err := manager.ensureStartAllowed(context.Background(), ch2); !errors.Is(err, ErrChannelNotRecordable) {
		t.Errorf("roomID=0: err = %v, want ErrChannelNotRecordable", err)
	}

	// 3. Enabled=false on channel → ErrChannelNotRecordable
	ch3 := ch
	ch3.Enabled = false
	if err := manager.ensureStartAllowed(context.Background(), ch3); !errors.Is(err, ErrChannelNotRecordable) {
		t.Errorf("channel disabled: err = %v, want ErrChannelNotRecordable", err)
	}

	// 4. 正常情况(无 active、无 DB active session)→ nil
	if err := manager.ensureStartAllowed(context.Background(), ch); err != nil {
		t.Errorf("normal: err = %v, want nil", err)
	}
}

// TestCheckRespectsCooldown352 (异常 #9 codex 实际审核中等项):
// Check(/api/live/status 30s 轮询走它)也尊重 -352 冷却——冷却期内不发 CheckLive,返回冷却 Status。
// 否则 Home 页轮询绕过冷却,继续打被风控的端点。
func TestCheckRespectsCooldown352(t *testing.T) {
	client := &countingClient{live: true}
	manager, _ := newAnomaly9Manager(t, client)
	// 预置冷却
	manager.applyCooldownRiskControl("huize")

	status, err := manager.Check(context.Background(), "huize")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// 冷却期内不应打 CheckLive
	if got := atomic.LoadInt32(&client.checkLiveCnt); got != 0 {
		t.Fatalf("CheckLive calls = %d, want 0 (Check respects cooldown)", got)
	}
	if status.Error == "" {
		t.Error("expected cooldown Error in Status")
	}
}

// TestCheckMarksCooldownOn352:Check 路径收到 -352 也触发冷却(避免轮询单独累频)。
func TestCheckMarksCooldownOn352(t *testing.T) {
	client := &countingClient{err: ErrRiskControl352}
	manager, _ := newAnomaly9Manager(t, client)

	_, err := manager.Check(context.Background(), "huize")
	if err != nil {
		t.Fatalf("Check should swallow -352 into Status, got err: %v", err)
	}
	if _, cooled := manager.cooldownRiskUntil("huize"); !cooled {
		t.Error("expected huize in cooldown after Check got -352")
	}
}

// TestCheckAndStartAllMarksCooldownOnHTTP412 (异常 P2):
// countingClient 返回 ErrHTTPRiskControl 包装错 → checkOne 识别触发与 -352 相同的阶梯冷却。
func TestCheckAndStartAllMarksCooldownOnHTTP412(t *testing.T) {
	client := &countingClient{err: fmt.Errorf("%w: status=412", ErrHTTPRiskControl)}
	manager, _ := newAnomaly9Manager(t, client)

	before := time.Now()
	_, err := manager.CheckAndStartAll(context.Background())
	if err != nil {
		t.Fatalf("CheckAndStartAll: %v", err)
	}
	until, cooled := manager.cooldownRiskUntil("huize")
	if !cooled {
		t.Fatal("expected huize to be in cooldown after HTTP 412")
	}
	// 首次冷却 5 分钟(与 -352 同阶梯)
	if until.Before(before.Add(4 * time.Minute)) {
		t.Fatalf("cooldown until = %v, want at least 4m from %v", until, before)
	}
}

// TestCheckMarksCooldownOnHTTP412 (异常 P2, codex v2 测试缺口):
// Check(/api/live/status 轮询路径)收到 ErrHTTPRiskControl 也触发冷却(与 CheckAndStartAll 对称)。
func TestCheckMarksCooldownOnHTTP412(t *testing.T) {
	client := &countingClient{err: fmt.Errorf("%w: status=412", ErrHTTPRiskControl)}
	manager, _ := newAnomaly9Manager(t, client)

	status, err := manager.Check(context.Background(), "huize")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(status.Error, "risk control cooldown") {
		t.Errorf("status.Error = %q, want contains 'risk control cooldown'", status.Error)
	}
	if _, cooled := manager.cooldownRiskUntil("huize"); !cooled {
		t.Error("expected huize in cooldown after Check got HTTP 412")
	}
}
