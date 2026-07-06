package live_record

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/normalize"
	"hikami-go/internal/notify"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

var (
	ErrLiveDisabled         = errors.New("live record is disabled")
	ErrChannelNotRecordable = errors.New("channel is not recordable")
	ErrAlreadyRecording     = errors.New("channel is already recording")
	ErrNotRecording         = errors.New("channel is not recording")
	ErrNotLive              = errors.New("channel is not live")
)

// errStreamEndedWhileLive 表示 ffmpeg 收到上游 EOF 干净退出（recordAudio 返回 nil），
// 但直播间探测显示仍在播——典型场景是 CDN 切换 / 网络抖动导致流中途断开。
// 用于把"成功 EOF"从"正常结束"重新归类为"需要重连的中断"，让重连循环接管。
// 它只在 AutoReconnect 启用时由 decideAfterRecord 产出，作为重连触发器。
var errStreamEndedWhileLive = errors.New("stream ended cleanly while still live")

// afterRecordDecision 是「一次 recordAudio 结束后该如何继续」的三态决策。
// 用显式枚举替代 (bool, error)，避免调用方误把录制错误清成 nil。
type afterRecordDecision int

const (
	// afterRecordFinishSuccess：本次录制应正常收尾。调用方应把 err 置 nil，走
	// finalize + normalize 成功路径（主播下播，或干净 EOF + 探测错保守收尾）。
	afterRecordFinishSuccess afterRecordDecision = iota
	// afterRecordFinishError：保留错误退出。carryErr 必非 nil（取消、耗尽、原录制失败）。
	afterRecordFinishError
	// afterRecordReconnect：进入或继续重连。carryErr 是触发重连的错误（原 wantErr 或哨兵）。
	afterRecordReconnect
)

type Manager struct {
	cfg                *config.Config
	channels           *channel.Store
	sessions           *session.Store
	states             *state.Store
	workers            *worker.Pool
	client             BiliClient
	audio              AudioRecorder
	danmaku            DanmakuRecorder
	notifyMgr          *notify.Manager
	cookieAccountStore *biliutil.CookieAccountStore

	mu           sync.Mutex
	active       map[string]activeRecord
	fileSizes    map[string]int64 // channelID -> last known file size
	failCount    map[string]int   // channelID -> consecutive health check fail count
	healthCancel context.CancelFunc

	// 异常 #9:-352 频率风控的频道级冷却。last352Cooldown[id]=冷却到期时间,
	// cooldownStep[id]=连续 -352 次数(阶梯 5/10/20m)。冷却期内 checkOne 跳过该频道的 CheckLive。
	last352Cooldown map[string]time.Time
	cooldownStep    map[string]int
}

type liveRecordLogContextKey string

const (
	liveRecordChannelIDKey liveRecordLogContextKey = "channel_id"
	liveRecordSessionIDKey liveRecordLogContextKey = "session_id"
)

type activeRecord struct {
	SessionID         string
	TaskID            string
	Cancel            context.CancelFunc
	CurrentOutputPath string // 异常 #4:健康检测需检查当前实际输出文件(重连切分段后会变)
}

type taskPayload struct {
	RoomID int64 `json:"room_id"`
}

type processStartRecorder interface {
	RecordWithProcessStart(ctx context.Context, stream StreamInfo, outputPath string, onStart func(pid int) error) error
}

func NewManager(
	cfg *config.Config,
	channels *channel.Store,
	sessions *session.Store,
	states *state.Store,
	workers *worker.Pool,
	client BiliClient,
	audio AudioRecorder,
	danmaku DanmakuRecorder,
	cookieAccountStore ...*biliutil.CookieAccountStore,
) *Manager {
	var accounts *biliutil.CookieAccountStore
	if len(cookieAccountStore) > 0 {
		accounts = cookieAccountStore[0]
	}
	return &Manager{
		cfg:                cfg,
		channels:           channels,
		sessions:           sessions,
		states:             states,
		workers:            workers,
		client:             client,
		audio:              audio,
		danmaku:            danmaku,
		cookieAccountStore: accounts,
		active:             map[string]activeRecord{},
		fileSizes:          map[string]int64{},
		failCount:          map[string]int{},
		last352Cooldown:    map[string]time.Time{},
		cooldownStep:       map[string]int{},
	}
}

func (m *Manager) Register(pool *worker.Pool) {
	pool.Register(TaskType, m.HandleTask)
}

func (m *Manager) SetNotifyManager(notifyMgr *notify.Manager) {
	m.notifyMgr = notifyMgr
}

// cookieHeaderForChannel 加载主播的下载用 Cookie 并返回 Cookie header 字符串。
// 加载失败时记录警告并返回空字符串。
func (m *Manager) cookieHeaderForChannel(ctx context.Context, channelID string) string {
	ch, err := m.channels.Get(ctx, channelID)
	if err != nil {
		return ""
	}
	cookieFile := downloadCookieFileForChannel(ch, m.cfg.BootstrapChannels)
	if m.cookieAccountStore != nil {
		cookie, err := m.cookieAccountStore.ResolveCookie(ctx, nullInt64FromPtr(ch.DownloadAccountID), sql.NullInt64{}, "download", cookieFile)
		if err == nil {
			return cookie.CookieHeader()
		}
		if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
			slog.Warn("resolve download cookie account failed, falling back to legacy cookie file",
				"channel_id", channelID, "error", err)
		}
	}
	if cookieFile == "" {
		return ""
	}
	cookie, err := biliutil.LoadCookie(cookieFile)
	if err != nil {
		slog.Warn("load download cookie failed, using no cookie",
			"channel_id", channelID, "cookie_file", cookieFile, "error", err)
		return ""
	}
	return cookie.CookieHeader()
}

func nullInt64FromPtr(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func downloadCookieFileForChannel(ch channel.Channel, bootstrap []config.BootstrapChannel) string {
	if ch.DownloadCookieFile != "" {
		return ch.DownloadCookieFile
	}
	var fallback string
	for _, item := range bootstrap {
		if item.DownloadCookieFile == "" {
			continue
		}
		if fallback == "" {
			fallback = item.DownloadCookieFile
		}
		if item.ID == ch.ID || item.UID == ch.UID || item.LiveRoomID == ch.LiveRoomID {
			return item.DownloadCookieFile
		}
	}
	return fallback
}

func (m *Manager) CheckAll(ctx context.Context) ([]Status, error) {
	channels, err := m.channels.List(ctx)
	if err != nil {
		return nil, err
	}

	statuses := make([]Status, 0, len(channels))
	for _, item := range channels {
		status, err := m.Check(ctx, item.ID)
		if err != nil {
			statuses = append(statuses, Status{
				ChannelID: item.ID,
				RoomID:    item.LiveRoomID,
				Error:     err.Error(),
			})
			continue
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (m *Manager) CheckAndStartAll(ctx context.Context) ([]Status, error) {
	channels, err := m.channels.List(ctx)
	if err != nil {
		return nil, err
	}

	recordable := make([]channel.Channel, 0, len(channels))
	for _, item := range channels {
		if item.LiveRoomID <= 0 || !item.Enabled {
			continue
		}
		if item.SourceMode == "replay_only" {
			continue
		}
		recordable = append(recordable, item)
	}

	checkOne := func(item channel.Channel) Status {
		// 录制中频道早退(codex 边界提示):不发 CheckLive,不进任何兜底,降频首要来源。
		if active, ok := m.activeFor(item.ID); ok {
			return Status{
				ChannelID: item.ID,
				RoomID:    item.LiveRoomID,
				Recording: true,
				SessionID: active.SessionID,
				TaskID:    active.TaskID,
			}
		}
		// 异常 #9 第2层:-352 频道级冷却。冷却期内跳过 CheckLive,避免每 30s 全量重打被风控的端点。
		if until, cooled := m.cooldown352Until(item.ID); cooled {
			return Status{
				ChannelID: item.ID,
				RoomID:    item.LiveRoomID,
				Error:     fmt.Sprintf("risk control cooldown until %s", until.Format(time.RFC3339)),
			}
		}
		// 异常 #9 第3层:jitter(0~800ms)放在 activeFor/冷却 早退**之后**、CheckLive **之前**,
		// 只对真正要发请求的频道生效(codex 实际代码审核低项)。用 select 支持取消,避免 Stop 时空等。
		jitter := time.Duration(rand.Intn(800)) * time.Millisecond
		select {
		case <-time.After(jitter):
		case <-ctx.Done():
			return Status{ChannelID: item.ID, RoomID: item.LiveRoomID, Error: ctx.Err().Error()}
		}
		checkCtx := context.WithValue(ctx, liveRecordChannelIDKey, item.ID)
		info, err := m.client.CheckLive(checkCtx, item.LiveRoomID, m.cookieHeaderForChannel(ctx, item.ID))
		if err != nil {
			// 异常 #9 第2层:识别 -352 哨兵,触发阶梯冷却(5/10/20m)。
			// 非 -352 错误(网络抖动等)走原 #8 WARN 路径,不触发冷却(避免误冷却瞬时抖动)。
			if errors.Is(err, ErrRiskControl352) {
				until := m.applyCooldown352(item.ID)
				slog.Warn("live check: -352 risk control, channel enters cooldown",
					"channel_id", item.ID, "room_id", item.LiveRoomID, "cooldown_until", until)
				return Status{
					ChannelID: item.ID,
					RoomID:    item.LiveRoomID,
					Error:     fmt.Sprintf("risk control cooldown until %s", until.Format(time.RFC3339)),
				}
			}
			// 异常 #8:CheckLive 失败(网络抖动等)此前被 checkOne 静默吞掉,这里打 WARN 让失败可观测。
			slog.Warn("live check: CheckLive failed for channel",
				"channel_id", item.ID, "room_id", item.LiveRoomID, "error", err)
			return Status{
				ChannelID: item.ID,
				RoomID:    item.LiveRoomID,
				Error:     err.Error(),
			}
		}
		// CheckLive 成功:重置该频道的冷却计数(避免跨成功探测累积)。
		m.resetCooldown352(item.ID)
		if !info.Live {
			return Status{
				ChannelID: item.ID,
				RoomID:    item.LiveRoomID,
				Live:      false,
				Title:     info.Title,
				StartedAt: info.StartedAt,
			}
		}
		if !item.AutoRecord {
			return Status{
				ChannelID: item.ID,
				RoomID:    item.LiveRoomID,
				Live:      true,
				Title:     info.Title,
				StartedAt: info.StartedAt,
			}
		}
		// 异常 #9 第1层:checkOne 已 CheckLive 拿到 info,直接走 startWithInfo 透传 info,
		// 省掉原 m.Start 内部的二次 CheckLive。先复用 ensureStartAllowed 的全部防护(codex v2 #2)。
		if gerr := m.ensureStartAllowed(ctx, item); gerr != nil {
			if errors.Is(gerr, ErrAlreadyRecording) {
				// 同期已开播(竞态)或同槽已有 session:走既有兜底,查当前状态返回。
				status, cerr := m.Check(ctx, item.ID)
				if cerr == nil {
					return status
				}
			}
			slog.Warn("live check: ensureStartAllowed failed for channel",
				"channel_id", item.ID, "room_id", item.LiveRoomID, "live", info.Live, "error", gerr)
			return Status{
				ChannelID: item.ID,
				RoomID:    item.LiveRoomID,
				Live:      info.Live,
				Title:     info.Title,
				StartedAt: info.StartedAt,
				Error:     gerr.Error(),
			}
		}
		status, err := m.startWithInfo(ctx, item, info)
		if err != nil {
			// 异常 #8:startWithInfo 失败(CreateLive 冲突、Enqueue 失败等)此前被静默吞掉。
			slog.Warn("live check: startWithInfo failed for channel",
				"channel_id", item.ID, "room_id", item.LiveRoomID, "live", info.Live, "error", err)
			return Status{
				ChannelID: item.ID,
				RoomID:    item.LiveRoomID,
				Live:      info.Live,
				Title:     info.Title,
				StartedAt: info.StartedAt,
				Error:     err.Error(),
			}
		}
		return status
	}

	const maxConcurrent = 8
	statuses := make([]Status, len(recordable))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for i, item := range recordable {
		wg.Add(1)
		go func(i int, item channel.Channel) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			statuses[i] = checkOne(item)
		}(i, item)
	}
	wg.Wait()
	return statuses, nil
}

// cooldown352Until 返回该频道的 -352 冷却到期时间。第二返回值 true 表示仍在冷却期(应跳过 CheckLive)。
func (m *Manager) cooldown352Until(channelID string) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	until, ok := m.last352Cooldown[channelID]
	return until, ok && time.Now().Before(until)
}

// applyCooldown352 给频道设阶梯冷却(首次 5m、二次 10m、三次+ 20m,封顶 20m),返回到期时间。
func (m *Manager) applyCooldown352(channelID string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	step := m.cooldownStep[channelID]
	var d time.Duration
	switch step {
	case 0:
		d = 5 * time.Minute
	case 1:
		d = 10 * time.Minute
	default:
		d = 20 * time.Minute
	}
	until := time.Now().Add(d)
	m.last352Cooldown[channelID] = until
	m.cooldownStep[channelID] = step + 1
	return until
}

// resetCooldown352 清除频道的 -352 冷却和阶梯计数(CheckLive 成功后调用,避免跨成功探测累积)。
func (m *Manager) resetCooldown352(channelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.last352Cooldown, channelID)
	delete(m.cooldownStep, channelID)
}

func (m *Manager) Check(ctx context.Context, channelID string) (Status, error) {
	item, err := m.channels.Get(ctx, channelID)
	if err != nil {
		return Status{}, err
	}
	if item.LiveRoomID <= 0 || !item.Enabled {
		return Status{}, ErrChannelNotRecordable
	}
	// 异常 #9 第2层:Check(被 /api/live/status 30s 轮询)也尊重 -352 冷却,
	// 否则 Home 页轮询绕过冷却继续打被风控的端点(codex 实际代码审核中等项)。
	if until, cooled := m.cooldown352Until(channelID); cooled {
		return Status{
			ChannelID: item.ID,
			RoomID:    item.LiveRoomID,
			Error:     fmt.Sprintf("risk control cooldown until %s", until.Format(time.RFC3339)),
		}, nil
	}
	checkCtx := context.WithValue(ctx, liveRecordChannelIDKey, channelID)
	info, err := m.client.CheckLive(checkCtx, item.LiveRoomID, m.cookieHeaderForChannel(ctx, channelID))
	if err != nil {
		// -352 在 Check 路径也触发冷却(与 checkOne 一致),避免 /api/live/status 轮询单独累频。
		if errors.Is(err, ErrRiskControl352) {
			until := m.applyCooldown352(channelID)
			slog.Warn("check: -352 risk control, channel enters cooldown",
				"channel_id", channelID, "room_id", item.LiveRoomID, "cooldown_until", until)
			return Status{
				ChannelID: channelID,
				RoomID:    item.LiveRoomID,
				Error:     fmt.Sprintf("risk control cooldown until %s", until.Format(time.RFC3339)),
			}, nil
		}
		return Status{}, err
	}
	// 成功:重置冷却(与 checkOne 一致)。
	m.resetCooldown352(channelID)
	status := Status{
		ChannelID: item.ID,
		RoomID:    item.LiveRoomID,
		Live:      info.Live,
		Title:     info.Title,
		StartedAt: info.StartedAt,
	}
	if active, ok := m.activeFor(item.ID); ok {
		status.Recording = true
		status.SessionID = active.SessionID
		status.TaskID = active.TaskID
	}
	return status, nil
}

// ensureStartAllowed 承载 Start 的全部前置防护(codex 审核 v2 设计#2):
// 全局录播开关、频道可录制性、内存 active、DB active session。
// Start 和 checkOne → startWithInfo 路径都先调它,确保 checkOne 绕过 Start 直接建 session 时
// 不削弱任何防护(尤其 LiveRecord.Enabled=false 必须挡在建 session 之前)。
func (m *Manager) ensureStartAllowed(ctx context.Context, item channel.Channel) error {
	if !m.cfg.LiveRecord.Enabled {
		return ErrLiveDisabled
	}
	if item.LiveRoomID <= 0 || !item.Enabled {
		return ErrChannelNotRecordable
	}
	if _, ok := m.activeFor(item.ID); ok {
		return ErrAlreadyRecording
	}
	if _, ok, err := m.sessions.ActiveLiveForChannel(ctx, item.ID); err != nil {
		return err
	} else if ok {
		return ErrAlreadyRecording
	}
	return nil
}

// startWithInfo 用已得的 LiveInfo 建 session + 入队,不重复 CheckLive。
// checkOne 已 CheckLive 拿到 info,直接透传给它,省掉 Start 路径的二次 getInfoByRoom(异常 #9 第1层)。
func (m *Manager) startWithInfo(ctx context.Context, item channel.Channel, info LiveInfo) (Status, error) {
	createdSession, err := m.sessions.CreateLive(ctx, session.CreateLiveInput{
		ChannelID: item.ID,
		Title:     info.Title,
		RoomID:    item.LiveRoomID,
		StartedAt: info.StartedAt,
	})
	if err != nil {
		// 同一 (channel, 分钟槽) 已存在 live_record session（含后期态/失败态）。
		// 这是原始下播竞态的核心防护：靠同槽 UNIQUE 拒绝重复，而非 ActiveLiveForChannel
		// 的频道级白名单（后者曾误扩到 published 等终态，导致该频道永久禁录——见 codex 审核）。
		//
		// 映射成 ErrAlreadyRecording：在 cron (CheckAndStartAll) 场景下走既有兜底分支，
		// 把它当 no-op 返回当前状态，不报错——这是期望行为（同一场不重复录）。
		// 手动 Start 场景下报"already recording"语义略宽（实际是"这场已有 session"），
		// 但调用方（handler）统一映射成 409，用户体验一致；保持单一错误以避免改动 cron 分支。
		if errors.Is(err, session.ErrAlreadyLive) {
			return Status{}, ErrAlreadyRecording
		}
		return Status{}, err
	}

	payload, err := json.Marshal(taskPayload{RoomID: item.LiveRoomID})
	if err != nil {
		return Status{}, err
	}
	task, err := m.workers.Enqueue(ctx, worker.CreateInput{
		ChannelID: item.ID,
		SessionID: createdSession.ID,
		Type:      TaskType,
		Payload:   string(payload),
	})
	if err != nil {
		return Status{}, err
	}
	slog.Info("live record start requested",
		"channel_id", item.ID,
		"session_id", createdSession.ID,
		"room_id", item.LiveRoomID)

	return Status{
		ChannelID: item.ID,
		RoomID:    item.LiveRoomID,
		Live:      true,
		Title:     info.Title,
		StartedAt: info.StartedAt,
		Recording: true,
		SessionID: createdSession.ID,
		TaskID:    task.ID,
	}, nil
}

func (m *Manager) Start(ctx context.Context, channelID string) (Status, error) {
	item, err := m.channels.Get(ctx, channelID)
	if err != nil {
		return Status{}, err
	}
	if err := m.ensureStartAllowed(ctx, item); err != nil {
		return Status{}, err
	}

	checkCtx := context.WithValue(ctx, liveRecordChannelIDKey, channelID)
	info, err := m.client.CheckLive(checkCtx, item.LiveRoomID, m.cookieHeaderForChannel(ctx, channelID))
	if err != nil {
		return Status{}, err
	}
	if !info.Live {
		return Status{}, ErrNotLive
	}
	return m.startWithInfo(ctx, item, info)
}

func (m *Manager) Stop(channelID string) error {
	m.mu.Lock()
	active, ok := m.active[channelID]
	if ok {
		// 自清 active 记录（ISS-6）：正常录制由 HandleTask 的 defer clearActive 兜底，
		// 但 Adopt 接管的孤儿进程无 defer，Stop 必须显式清理。
		delete(m.active, channelID)
		delete(m.fileSizes, channelID)
		delete(m.failCount, channelID)
	}
	m.mu.Unlock()
	if !ok {
		return ErrNotRecording
	}
	active.Cancel()
	if m.notifyMgr != nil {
		m.notifyMgr.Send(context.Background(), notify.EventRecordStop, "直播录制已停止", fmt.Sprintf("频道 %s 的直播录制已停止", channelID))
	}
	return nil
}

// Adopt 接管服务重启后仍存活的 ffmpeg 录制进程（ISS-6）。
// 重建 activeRecord，使其 Cancel 句柄能向已知 PID 发送 SIGTERM，
// 让前端"停止录制"可正常接管残留进程，而非返回 ErrNotRecording。
// 返回 false 表示该 channel 已有活跃记录（未接管）。
func (m *Manager) Adopt(channelID, taskID, sessionID string, pid int) bool {
	if pid <= 0 {
		return false
	}
	cancel := func() {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
	// 异常 #4:重启接管时 CurrentOutputPath 未知(进程在跑但路径丢失),用 glob 兜底取最新音频文件。
	// cfg 为空时(测试桩)跳过 glob,CurrentOutputPath 留空,健康检测会 fallback。
	var outputPath string
	if m.cfg != nil {
		outputPath = globLatestAudio(m.cfg.OutputRoot, channelID, sessionID, m.cfg.LiveRecord.AudioContainer)
	}
	return m.setActive(channelID, activeRecord{SessionID: sessionID, TaskID: taskID, Cancel: cancel, CurrentOutputPath: outputPath})
}

func (m *Manager) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	payload := taskPayload{}
	if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
		return err
	}
	if payload.RoomID <= 0 {
		return fmt.Errorf("room_id is required")
	}

	runCtx, cancel := context.WithCancel(ctx)
	runCtx = context.WithValue(runCtx, liveRecordChannelIDKey, task.ChannelID)
	runCtx = context.WithValue(runCtx, liveRecordSessionIDKey, task.SessionID)
	if !m.setActive(task.ChannelID, activeRecord{SessionID: task.SessionID, TaskID: task.ID, Cancel: cancel}) {
		cancel()
		return ErrAlreadyRecording
	}
	defer func() {
		cancel()
		m.clearActive(task.ChannelID, task.ID)
	}()

	if _, err := m.states.Apply(ctx, task.SessionID, state.EventLiveRecordStarted, task.ID, ""); err != nil {
		return err
	}
	slog.Info("live record started",
		"channel_id", task.ChannelID,
		"session_id", task.SessionID,
		"room_id", payload.RoomID)
	if m.notifyMgr != nil {
		m.notifyMgr.Send(ctx, notify.EventRecordStart, "直播录制已开始", fmt.Sprintf("频道 %s 的直播录制已开始", task.ChannelID))
	}
	if err := reporter.Progress(ctx, 5, "checking live stream"); err != nil {
		return err
	}

	// 获取主播的 Cookie header
	cookieHeader := m.cookieHeaderForChannel(ctx, task.ChannelID)

	// 拉流前的开播再确认。从调度（CheckLive 判定开播）到真正拉流之间隔了
	// 建 session / 排队 / worker 调度的时间，主播可能在窗口期内已下播。
	// - 明确判定已下播（无 err）：直接放弃，避免对失效流硬拉，走干净失败路径；
	// - 探测本身出错（如 B 站风控 -352 / 网络抖动）：不阻断，沿用“拿到 URL 就试”
	//   的乐观策略，调度时已确认过开播，探测误判不应连累整条录制。
	// 用 runCtx（携带 active cancel）而非 ctx，确保 Stop / 取消发生在探测期间时能及时中断。
	// preflight 提到外层变量：成功在线时其 Cover 字段供后续下载封面复用，避免重复请求。
	var preflight LiveInfo
	preflight, preflightErr := m.client.CheckLive(runCtx, payload.RoomID, cookieHeader)
	if preflightErr != nil {
		if errors.Is(runCtx.Err(), context.Canceled) {
			return runCtx.Err()
		}
		slog.Warn("preflight live check failed, proceed optimistically",
			"channel_id", task.ChannelID, "room_id", payload.RoomID, "error", preflightErr)
		preflight = LiveInfo{} // 探测失败，无可复用的封面信息
	} else if !preflight.Live {
		slog.Info("preflight live check reports offline, skip recording",
			"channel_id", task.ChannelID, "room_id", payload.RoomID)
		return ErrNotLive
	}

	// 获取直播流：优先纯音频，根据配置决定回退策略
	stream, err := m.selectStream(ctx, payload.RoomID, cookieHeader)
	if err != nil {
		return err
	}

	sessionInfo, err := m.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(m.cfg.OutputRoot, task.ChannelID, sessionInfo.Slug)
	rawDir := filepath.Join(sessionDir, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return err
	}
	if err := m.writeLiveMetadata(rawDir, payload.RoomID, stream, task); err != nil {
		return err
	}
	// 下载直播间封面到 raw/cover.*（供 publisher 作为专栏封面）。仅当 preflight 成功且
	// 在线时复用其 Cover 字段；探测失败/preflight.Cover 为空时跳过。失败不阻断录制。
	if preflight.Cover != "" {
		biliutil.DownloadCover(runCtx, nil, preflight.Cover, cookieHeader, rawDir)
	}
	reportRecording := func(pid int) error {
		return reporter.Progress(ctx, 15, fmt.Sprintf("recording audio (pid:%d)", pid))
	}
	if _, ok := m.audio.(processStartRecorder); !ok {
		if err := reporter.Progress(ctx, 15, "recording live audio"); err != nil {
			return err
		}
	}

	// 从主播配置读取弹幕录制开关
	recordDanmaku := m.cfg.LiveRecord.RecordDanmaku
	if ch, err := m.channels.Get(ctx, task.ChannelID); err == nil {
		recordDanmaku = ch.RecordDanmaku
	}

	if recordDanmaku && m.danmaku != nil {
		dmCookieHeader := m.cookieHeaderForChannel(ctx, task.ChannelID)
		var dmUID int64
		// 优先从 cookie header 中提取 DedeUserID
		for _, part := range strings.Split(dmCookieHeader, ";") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) == 2 && kv[0] == "DedeUserID" {
				dmUID, _ = strconv.ParseInt(kv[1], 10, 64)
				break
			}
		}
		// fallback: 从 cookie 文件加载
		if dmUID == 0 {
			if ch, err := m.channels.Get(ctx, task.ChannelID); err == nil {
				cookieFile := downloadCookieFileForChannel(ch, m.cfg.BootstrapChannels)
				if bc, err := biliutil.LoadCookie(cookieFile); err == nil {
					dmUID, _ = strconv.ParseInt(bc.DedeUserID, 10, 64)
				}
			}
		}
		go func() {
			if err := recordDanmakuWithStartTime(runCtx, m.danmaku, payload.RoomID, filepath.Join(rawDir, "danmaku.jsonl"), dmCookieHeader, dmUID, sessionInfo.StartedAt); err != nil {
				slog.Error("danmaku record failed", "channel_id", task.ChannelID, "room_id", payload.RoomID, "error", err)
			}
		}()
	}

	audioPath := filepath.Join(rawDir, "audio."+m.cfg.LiveRecord.AudioContainer)
	// 异常 #4:首段录制前记录当前输出路径,供健康检测检查(重连切分段后会更新)。
	m.updateCurrentOutputPath(task.ChannelID, audioPath)
	recordStartedAt := time.Now()
	audioSegments := make([]string, 0, 1)
	recordedSegments := map[string]struct{}{}
	addAudioSegment := func(path string) {
		if audioFileExists(path) {
			if _, ok := recordedSegments[path]; ok {
				return
			}
			recordedSegments[path] = struct{}{}
			audioSegments = append(audioSegments, path)
		}
	}

	// Recording with optional auto-reconnect
	maxReconnect := 0
	reconnectDelay := 10 * time.Second
	if m.cfg.LiveRecord.AutoReconnect {
		maxReconnect = m.cfg.LiveRecord.MaxReconnect
		if maxReconnect <= 0 {
			maxReconnect = 3
		}
		if m.cfg.LiveRecord.ReconnectDelay > 0 {
			reconnectDelay = time.Duration(m.cfg.LiveRecord.ReconnectDelay) * time.Second
		}
	}

	err = m.recordAudio(runCtx, stream, audioPath, reportRecording)
	addAudioSegment(audioPath)

	// 重连循环：每次 recordAudio 后（含首段与每个重连分段）都通过 decideAfterRecord 判定
	// 「该如何继续」。这覆盖了「ffmpeg 收到上游 EOF 干净退出」这一最常见中断场景——
	// 原代码的 `for ... err != nil ...` 守卫会把干净 EOF（err==nil）直接放行，即便主播
	// 仍在播也误判为正常结束（见根因报告 docs/archive/investigations/录播时长不足-流断未重连.md）。
	//
	// 控制流说明（Go 语义）：select / switch 内的裸 break 只跳出自己，不跳出 for。
	// 因此取消等待的 Done 分支用 labeled break（break reconnect）退出整个循环；
	// decision 分支通过 fallthrough 到循环末尾的 break 退出。
	attemptsUsed := 0
	// 异常 #2:CDN 瞬时错误(404/connection reset)用独立预算 cdnRetryBudget,绕过 maxReconnect,
	// 避免短窗口(3×reconnectDelay)内全部失败。cdnAttempt 用于指数退避计算(base*2^n)。
	cdnRetryBudget := 5
	cdnAttempt := 0
	// 异常 #1:selectStream 失败时置位,下一轮跳过 decideAfterRecord(否则它会重新 CheckLive,
	// 在流断边缘态的瞬时抖动下误判 live:false 而提前放弃)。
	selectFailedPending := false
reconnect:
	for {
		// 取消优先：Stop / worker 取消发生时立即退出，不因 helper 内的 CheckLive 而延迟。
		if errors.Is(runCtx.Err(), context.Canceled) {
			if err == nil {
				err = runCtx.Err()
			}
			break reconnect
		}

		// === 异常 #1/#2 重试分支(在 decideAfterRecord 之前) ===
		// 这两类错误的重试不应调 CheckLive(decideAfterRecord 会),否则流断边缘态的瞬时抖动
		// (live:false)会提前放弃,尽管预算还剩。
		// 仅在 AutoReconnect 开启时生效(与 decideAfterRecord 的 AutoReconnect 语义一致):
		// 关闭自动重连时,任何错误(含 CDN/selectFail)都直接走 decideAfterRecord 的关闭语义(保留 err 退出)。
		// 异常 #2 优先:CDN 瞬时错误(404 等)用独立 cdnRetryBudget + 指数退避,绕过 maxReconnect。
		if m.cfg.LiveRecord.AutoReconnect && err != nil && isCDNTransientError(err) {
			if cdnRetryBudget <= 0 {
				// CDN 预算耗尽:放弃。
				break reconnect
			}
			cdnRetryBudget--
			cdnAttempt++
			backoff := cdnBackoff(cdnAttempt)
			slog.Warn("cdn transient error, retrying with backoff",
				"channel_id", task.ChannelID, "room_id", payload.RoomID,
				"error", err, "cdn_retry_budget", cdnRetryBudget, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-runCtx.Done():
				err = runCtx.Err()
				break reconnect
			}
			stream, sErr := m.selectStream(runCtx, payload.RoomID, m.cookieHeaderForChannel(ctx, task.ChannelID))
			if sErr != nil {
				// selectStream 也失败:转 selectFail 路径(用 maxReconnect 预算)。
				slog.Warn("reconnect stream selection failed after cdn error", "error", sErr)
				selectFailedPending = true
				err = sErr
				continue
			}
			segmentPath := reconnectAudioSegmentPath(audioPath, cdnAttempt+1)
			m.updateCurrentOutputPath(task.ChannelID, segmentPath)
			segErr := m.recordAudio(runCtx, stream, segmentPath, reportRecording)
			addAudioSegment(segmentPath)
			// 不在 segErr==nil 时直接成功退出:recordAudio 返回 nil 也可能是上游 clean EOF,
			// 需走 decideAfterRecord 判断是否仍在播(否则误判完成)。下轮统一进 decideAfterRecord。
			err = segErr
			continue
		}

		// 异常 #1:selectStream 失败用 maxReconnect 预算,不调 CheckLive。
		if selectFailedPending {
			selectFailedPending = false
			if attemptsUsed >= maxReconnect {
				break reconnect
			}
			_ = reporter.Progress(ctx, 15+attemptsUsed*5, fmt.Sprintf("reconnecting after stream select failure (attempt %d/%d)", attemptsUsed+1, maxReconnect))
			select {
			case <-time.After(reconnectDelay):
			case <-runCtx.Done():
				err = runCtx.Err()
				break reconnect
			}
			stream, sErr := m.selectStream(runCtx, payload.RoomID, m.cookieHeaderForChannel(ctx, task.ChannelID))
			if sErr != nil {
				slog.Warn("reconnect stream selection failed", "error", sErr)
				attemptsUsed++
				selectFailedPending = true
				err = sErr
				continue
			}
			segmentPath := reconnectAudioSegmentPath(audioPath, attemptsUsed+1)
			m.updateCurrentOutputPath(task.ChannelID, segmentPath)
			segErr := m.recordAudio(runCtx, stream, segmentPath, reportRecording)
			addAudioSegment(segmentPath)
			// 不在 segErr==nil 时直接成功退出:同上,需走 decideAfterRecord 判断是否仍在播。
			err = segErr
			attemptsUsed++
			continue
		}

		// === decideAfterRecord 路径(非 CDN / 非 selectFail 的 recordAudio 结果) ===
		// 唯一探测点：判定上一次 recordAudio（首段或重连分段）的结果该如何继续。
		decision, carryErr := m.decideAfterRecord(runCtx, task, payload, err)

		// decideAfterRecord 内若探测期间被取消，可能返回 FinishError + ctx 错误；
		// 再次检查取消，确保取消立即响应。
		if errors.Is(runCtx.Err(), context.Canceled) {
			err = carryErr
			if err == nil {
				err = runCtx.Err()
			}
			break reconnect
		}

		switch decision {
		case afterRecordFinishSuccess:
			err = nil
			break reconnect
		case afterRecordFinishError:
			err = carryErr
			break reconnect
		case afterRecordReconnect:
			// 想重连但额度耗尽：退出并保留错误。
			// clean-EOF + 仍 live 耗尽时 carryErr 是 errStreamEndedWhileLive（已失败路径是原 wantErr）。
			if attemptsUsed >= maxReconnect {
				err = carryErr
				break reconnect
			}
			err = carryErr
			_ = reporter.Progress(ctx, 15+attemptsUsed*5, fmt.Sprintf("reconnecting (attempt %d/%d)", attemptsUsed+1, maxReconnect))
		}

		// 可取消的等待：Stop / 取消发生在重连延迟期间时立即退出整个循环（labeled break），
		// 不依赖下一轮 helper，避免被 CheckLive 阻塞。
		select {
		case <-time.After(reconnectDelay):
		case <-runCtx.Done():
			err = runCtx.Err()
			break reconnect
		}

		// Re-select stream（用 runCtx 让取消能传播到拉流请求）。
		stream, sErr := m.selectStream(runCtx, payload.RoomID, m.cookieHeaderForChannel(ctx, task.ChannelID))
		if sErr != nil {
			slog.Warn("reconnect stream selection failed", "error", sErr)
			// 异常 #1:设置 selectFailedPending,下一轮走 selectFail 路径(不调 decideAfterRecord)。
			attemptsUsed++
			selectFailedPending = true
			err = sErr
			continue
		}

		segmentPath := reconnectAudioSegmentPath(audioPath, attemptsUsed+1)
		m.updateCurrentOutputPath(task.ChannelID, segmentPath)
		segErr := m.recordAudio(runCtx, stream, segmentPath, reportRecording)
		addAudioSegment(segmentPath)
		// 下轮按 err 类型分类:CDN 错误走 CDN 块(独立预算),非 CDN 走 decideAfterRecord。
		err = segErr
		attemptsUsed++
	}

	if err != nil {
		if errors.Is(runCtx.Err(), context.Canceled) {
			if finalizeErr := m.finalizeAudioSegments(ctx, audioPath, audioSegments); finalizeErr != nil {
				return finalizeErr
			}
			if updateErr := m.sessions.UpdateEndedAt(ctx, task.SessionID, time.Now()); updateErr != nil {
				slog.Warn("update live record ended_at failed", "session_id", task.SessionID, "error", updateErr)
			}
			if _, applyErr := m.states.Apply(ctx, task.SessionID, state.EventLiveRecordSucceeded, task.ID, ""); applyErr != nil {
				return applyErr
			}
			if _, enqueueErr := m.enqueueNormalize(ctx, task); enqueueErr != nil {
				return enqueueErr
			}
			logLiveRecordFinished(task.ChannelID, task.SessionID, recordStartedAt, audioPath)
			return reporter.Progress(ctx, 95, "live recording stopped")
		}
		return err
	}

	if err := m.finalizeAudioSegments(ctx, audioPath, audioSegments); err != nil {
		return err
	}

	if updateErr := m.sessions.UpdateEndedAt(ctx, task.SessionID, time.Now()); updateErr != nil {
		slog.Warn("update live record ended_at failed", "session_id", task.SessionID, "error", updateErr)
	}
	if _, err := m.states.Apply(ctx, task.SessionID, state.EventLiveRecordSucceeded, task.ID, ""); err != nil {
		return err
	}
	logLiveRecordFinished(task.ChannelID, task.SessionID, recordStartedAt, audioPath)

	// replay_first mode: if a download session already exists within the time window,
	// skip normalize since the replay download is preferred over the live recording.
	if ch, chErr := m.channels.Get(ctx, task.ChannelID); chErr == nil && ch.SourceMode == "replay_first" {
		sessInfo, sessErr := m.sessions.Get(ctx, task.SessionID)
		if sessErr == nil && sessInfo.StartedAt != "" {
			startedAt, parseErr := time.Parse(time.RFC3339, sessInfo.StartedAt)
			if parseErr == nil {
				if dlSess, dlErr := m.sessions.FindDownloadByTimeWindow(ctx, task.ChannelID, startedAt, 4*time.Hour); dlErr == nil && dlSess.ID != "" {
					return reporter.Progress(ctx, 90, "skipped normalize: replay_first mode, download session exists")
				}
			}
		}
	}

	if _, err := m.enqueueNormalize(ctx, task); err != nil {
		return err
	}
	return reporter.Progress(ctx, 95, "live recording finished")
}

// decideAfterRecord 判定一次 recordAudio 结束后该如何继续。wantErr 是本次 recordAudio
// 的返回值（nil = ffmpeg 干净退出；非 nil = 拉流 / 进程 / 拷贝失败）。
//
// 返回 (decision, carryErr)：
//   - FinishSuccess：调用方 err = nil 正常收尾。
//   - FinishError  ：调用方 err = carryErr 退出（carryErr 必非 nil）。
//   - Reconnect    ：调用方继续重连，err = carryErr（原 wantErr 或 errStreamEndedWhileLive）。
//
// 判定以 B 站直播间状态为准（设计原则：判定权从 ffmpeg 退出码转移到直播状态）。
// 探测出错（B 站风控 -352 / 网络抖动）的兜底方向因路径而异：
//   - wantErr != nil（已失败路径）：反正要重连，探测错仍保守重连（沿用现状）。
//   - wantErr == nil（干净退出路径）：本可收尾，探测错保守收尾，避免丢弃已录音频。
//
// 关键语义：wantErr != nil + 明确下播 时返回 FinishError(wantErr)，与现状（原循环 break
// 后由 manager.go 的 `if err != nil` 收尾分支返回错误）一致；只有 wantErr == nil + 下播
// 才是真正的正常收尾。这避免了"把失败录制推下游 normalize"的回归。
func (m *Manager) decideAfterRecord(ctx context.Context, task worker.Task, payload taskPayload, wantErr error) (afterRecordDecision, error) {
	// 关闭重连：保留原 err 语义，绝不吞错误。
	if !m.cfg.LiveRecord.AutoReconnect {
		if wantErr == nil {
			return afterRecordFinishSuccess, nil
		}
		return afterRecordFinishError, wantErr
	}

	liveInfo, liveErr := m.client.CheckLive(ctx, payload.RoomID, m.cookieHeaderForChannel(ctx, task.ChannelID))

	// 探测期间被取消（Stop / worker 取消恰好发生在探测期间）：直接返回 ctx 错误，
	// 让调用方走取消收尾。helper 契约要求 FinishError 的 carryErr 必非 nil。
	if ctx.Err() != nil {
		carry := ctx.Err()
		if carry == nil {
			carry = wantErr
		}
		return afterRecordFinishError, carry
	}

	// 明确已下播（探测成功且 !Live）：
	//   wantErr == nil → 真正的正常收尾；
	//   wantErr != nil → 保留原录制错误退出（不把失败当成功）。
	if liveErr == nil && !liveInfo.Live {
		if wantErr == nil {
			slog.Info("stream ended, skipping reconnect", "channel_id", task.ChannelID, "room_id", payload.RoomID)
			return afterRecordFinishSuccess, nil
		}
		slog.Info("stream ended after record failure, finishing with error",
			"channel_id", task.ChannelID, "room_id", payload.RoomID, "error", wantErr)
		return afterRecordFinishError, wantErr
	}

	switch {
	case liveErr == nil && liveInfo.Live:
		// 明确仍开播：重连。wantErr == nil（干净 EOF）时用哨兵触发。
		carry := wantErr
		if carry == nil {
			slog.Warn("stream ended cleanly while still live, attempting reconnect",
				"channel_id", task.ChannelID, "room_id", payload.RoomID)
			carry = errStreamEndedWhileLive
		} else {
			slog.Warn("stream interrupted, attempting reconnect",
				"channel_id", task.ChannelID, "room_id", payload.RoomID, "error", wantErr)
		}
		return afterRecordReconnect, carry
	default:
		// liveErr != nil（探测出错）：
		//   wantErr == nil → 保守收尾（不丢弃已录音频）；
		//   wantErr != nil → 保守重连（现状语义）。
		if wantErr == nil {
			slog.Info("clean exit with inconclusive live probe, treating as ended",
				"channel_id", task.ChannelID, "room_id", payload.RoomID, "probe_error", liveErr)
			return afterRecordFinishSuccess, nil
		}
		slog.Warn("reconnect liveness probe failed, attempt anyway",
			"channel_id", task.ChannelID, "room_id", payload.RoomID,
			"probe_error", liveErr, "stream_error", wantErr)
		return afterRecordReconnect, wantErr
	}
}

func (m *Manager) recordAudio(ctx context.Context, stream StreamInfo, audioPath string, onStart func(pid int) error) error {
	if recorder, ok := m.audio.(processStartRecorder); ok {
		return recorder.RecordWithProcessStart(ctx, stream, audioPath, onStart)
	}
	return m.audio.Record(ctx, stream, audioPath)
}

func reconnectAudioSegmentPath(audioPath string, index int) string {
	ext := filepath.Ext(audioPath)
	if ext == "" {
		return fmt.Sprintf("%s.part.%d", audioPath, index)
	}
	return fmt.Sprintf("%s.part.%d%s", strings.TrimSuffix(audioPath, ext), index, ext)
}

func audioFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// isCDNTransientError 判定错误是否为 CDN 瞬时错误(异常 #2):B 站 CDN 节点切换瞬间,
// selectStream 拿到的流地址在 Go http.Get 时返回 404,或连接被重置。这类错误值得用
// 独立预算 + 指数退避重试(每轮重新 selectStream 拿新地址),不应占用通用 maxReconnect 额度。
func isCDNTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "http status 404") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "EOF") && strings.Contains(msg, "open live stream")
}

// cdnBackoff 返回 CDN 重试的指数退避时长(base*2^n,上限 60s)。n 从 1 开始:2s,4s,8s,16s,32s,60s。
func cdnBackoff(attempt int) time.Duration {
	if attempt >= 6 { // 2^6=64 > 60,直接封顶
		return 60 * time.Second
	}
	return time.Duration(1<<attempt) * time.Second
}

// globLatestAudio 在 session 的 raw 目录下找 mtime 最新的音频文件(异常 #4 兜底)。
// 用于 Adopt 重启接管时恢复 CurrentOutputPath(进程在跑但路径丢失)。
func globLatestAudio(outputRoot, channelID, sessionID, container string) string {
	if outputRoot == "" || channelID == "" || sessionID == "" || container == "" {
		return ""
	}
	// sessionID 形如 bili_123_live_20260705_120000;slug 是 live_... 部分。
	slug := sessionID
	if idx := strings.LastIndex(sessionID, "_live_"); idx >= 0 {
		slug = sessionID[idx+1:] // live_20260705_120000
	}
	pattern := filepath.Join(outputRoot, channelID, slug, "raw", "audio*."+container)
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	latest := matches[0]
	latestMtime := time.Time{}
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil {
			if info.ModTime().After(latestMtime) {
				latestMtime = info.ModTime()
				latest = m
			}
		}
	}
	return latest
}

func (m *Manager) finalizeAudioSegments(ctx context.Context, audioPath string, segments []string) error {
	if len(segments) == 0 {
		return nil
	}
	if len(segments) == 1 {
		if segments[0] == audioPath {
			return nil
		}
		return os.Rename(segments[0], audioPath)
	}
	return m.concatAudioSegments(ctx, audioPath, segments)
}

func (m *Manager) concatAudioSegments(ctx context.Context, audioPath string, segments []string) error {
	listPath := audioPath + ".concat.txt"
	outputPath := concatAudioOutputPath(audioPath)
	if err := writeConcatList(listPath, segments); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(listPath)
		_ = os.Remove(outputPath)
	}()

	command := m.cfg.FFmpeg
	if command == "" {
		command = "ffmpeg"
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		outputPath,
	}
	if err := runFFmpegConcat(ctx, command, args...); err != nil {
		return err
	}
	if err := os.Rename(outputPath, audioPath); err != nil {
		return err
	}
	for _, segment := range segments {
		if segment == audioPath {
			continue
		}
		if err := os.Remove(segment); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func concatAudioOutputPath(audioPath string) string {
	ext := filepath.Ext(audioPath)
	if ext == "" {
		return audioPath + ".concat"
	}
	return strings.TrimSuffix(audioPath, ext) + ".concat" + ext
}

func writeConcatList(path string, segments []string) error {
	var builder strings.Builder
	for _, segment := range segments {
		builder.WriteString("file '")
		builder.WriteString(escapeConcatPath(absConcatPath(segment)))
		builder.WriteString("'\n")
	}
	return os.WriteFile(path, []byte(builder.String()), 0o600)
}

// absConcatPath returns an absolute, slash-normalized path for use inside an
// ffmpeg concat listfile.
//
// ffmpeg's concat demuxer resolves relative entries against the listfile's own
// directory (not the process CWD). When OutputRoot is itself relative (e.g.
// "./output"), writing the recorded segment paths verbatim makes ffmpeg look for
// "<listfileDir>/<relativeSegment>" and double up the path, failing with
// "Impossible to open '...raw/...raw/audio.m4a'". Absolute paths sidestep this
// entirely and are always safe regardless of CWD or OutputRoot form.
func absConcatPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		// Fall back to the original; best effort. Slashes are still normalized
		// so ffmpeg parsing on the same OS remains correct.
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(abs)
}

func escapeConcatPath(path string) string {
	path = strings.ReplaceAll(path, `\`, `\\`)
	return strings.ReplaceAll(path, `'`, `'\''`)
}

var runFFmpegConcat = func(ctx context.Context, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg concat failed: %w: %s", err, output.String())
	}
	return nil
}

func (r *FFmpegRecorder) RecordWithProcessStart(ctx context.Context, stream StreamInfo, outputPath string, onStart func(pid int) error) error {
	command := r.Command
	if command == "" {
		command = "ffmpeg"
	}
	args := buildFFmpegArgs(stream, outputPath)
	logFFmpegStarted(ctx, command, args, stream, outputPath)

	response, err := r.openStream(ctx, stream)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = r.stopGracePeriod()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open ffmpeg stdin: %w", err)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg record: %w", err)
	}
	if onStart != nil {
		if err := onStart(cmd.Process.Pid); err != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
			return err
		}
	}
	copyErrCh := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(stdin, response.Body)
		closeErr := stdin.Close()
		if copyErr != nil {
			copyErrCh <- copyErr
			return
		}
		copyErrCh <- closeErr
	}()

	waitErr := cmd.Wait()
	logFFmpegExited(ctx, outputPath, waitErr)
	_ = response.Body.Close()
	copyErr := <-copyErrCh
	if waitErr != nil {
		return fmt.Errorf("ffmpeg record failed: %w: %s", waitErr, output.String())
	}
	if copyErr != nil && ctx.Err() == nil {
		return fmt.Errorf("copy live stream to ffmpeg: %w", copyErr)
	}
	return nil
}

type startTimeDanmakuRecorder interface {
	RecordWithStartTime(ctx context.Context, roomID int64, outputPath string, cookieHeader string, uid int64, startedAt time.Time) error
}

func recordDanmakuWithStartTime(ctx context.Context, recorder DanmakuRecorder, roomID int64, outputPath string, cookieHeader string, uid int64, startedAtRaw string) error {
	startedAt, err := time.Parse(time.RFC3339, startedAtRaw)
	if err != nil {
		startedAt = time.Now()
	}
	if typed, ok := recorder.(startTimeDanmakuRecorder); ok {
		return typed.RecordWithStartTime(ctx, roomID, outputPath, cookieHeader, uid, startedAt)
	}
	return recorder.Record(ctx, roomID, outputPath, cookieHeader, uid)
}

// selectStream 按直播录制配置选择纯音频或混合流。
// 默认配置保持向后兼容：选择混合流，由 ffmpeg 抽取音频轨。
func (m *Manager) selectStream(ctx context.Context, roomID int64, cookieHeader string) (StreamInfo, error) {
	cfg := m.cfg.LiveRecord
	if cfg.AudioOnly || cfg.RequireAudioStream {
		stream, err := m.client.GetStream(ctx, roomID, true, cookieHeader)
		if err == nil {
			stream.AudioOnly = true
			return stream, nil
		}
		if cfg.RequireAudioStream || !cfg.FallbackExtractAudio {
			return StreamInfo{}, fmt.Errorf("audio stream not available: %w", err)
		}
	}

	stream, err := m.client.GetStream(ctx, roomID, false, cookieHeader)
	if err != nil {
		return StreamInfo{}, fmt.Errorf("mixed stream not available: %w", err)
	}
	stream.AudioOnly = false
	return stream, nil
}

func (m *Manager) enqueueNormalize(ctx context.Context, task worker.Task) (worker.Task, error) {
	return m.workers.Enqueue(ctx, worker.CreateInput{
		ChannelID: task.ChannelID,
		SessionID: task.SessionID,
		Type:      normalize.TaskType,
		Payload:   "{}",
	})
}

func (m *Manager) writeLiveMetadata(rawDir string, roomID int64, stream StreamInfo, task worker.Task) error {
	content := map[string]any{
		"room_id":    roomID,
		"stream_url": redactURL(stream.URL),
		"audio_only": stream.AudioOnly,
		"task_id":    task.ID,
		"created_at": time.Now().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(rawDir, "live.raw.json"), data, 0o600)
}

func logLiveRecordFinished(channelID string, sessionID string, startedAt time.Time, audioPath string) {
	fileSize := int64(0)
	if info, err := os.Stat(audioPath); err == nil {
		fileSize = info.Size()
	}
	slog.Info("live record finished",
		"channel_id", channelID,
		"session_id", sessionID,
		"duration", time.Since(startedAt).String(),
		"file_size", fileSize,
		"output_path", audioPath)
}

func redactURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}

func (m *Manager) activeFor(channelID string) (activeRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	active, ok := m.active[channelID]
	return active, ok
}

// StartHealthCheck starts a background goroutine that monitors active recordings.
func (m *Manager) StartHealthCheck(ctx context.Context, interval time.Duration) {
	// 停止旧的健康检查，防止重复启动
	m.StopHealthCheck()

	if interval <= 0 {
		interval = 60 * time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	m.healthCancel = cancel
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.checkRecordingHealth()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopHealthCheck stops the background health check goroutine.
func (m *Manager) StopHealthCheck() {
	if m.healthCancel != nil {
		m.healthCancel()
		m.healthCancel = nil
	}
}

func (m *Manager) checkRecordingHealth() {
	m.mu.Lock()
	channelIDs := make([]string, 0, len(m.active))
	for id := range m.active {
		channelIDs = append(channelIDs, id)
	}
	m.mu.Unlock()

	for _, channelID := range channelIDs {
		active, ok := m.activeFor(channelID)
		if !ok {
			continue
		}

		// Find output file by getting session info
		ctx := context.Background()
		sessionInfo, err := m.sessions.Get(ctx, active.SessionID)
		if err != nil {
			continue
		}
		sessionDir := filepath.Join(m.cfg.OutputRoot, channelID, sessionInfo.Slug)
		// 异常 #4:优先用 active.CurrentOutputPath(重连切分段后会更新为 part.N);
		// 为空时(Adopt 未填、或路径丢失)先 glob 最新音频文件,再 fallback 到硬编码 audio.{container}。
		fallbackPath := filepath.Join(sessionDir, "raw", "audio."+m.cfg.LiveRecord.AudioContainer)
		audioPath := active.CurrentOutputPath
		if audioPath == "" {
			if globbed := globLatestAudio(m.cfg.OutputRoot, channelID, active.SessionID, m.cfg.LiveRecord.AudioContainer); globbed != "" {
				audioPath = globbed
			} else {
				audioPath = fallbackPath
			}
		}

		info, err := os.Stat(audioPath)
		if err != nil {
			m.mu.Lock()
			m.failCount[channelID]++
			m.mu.Unlock()
			continue
		}

		m.mu.Lock()
		lastSize := m.fileSizes[channelID]
		if info.Size() > lastSize {
			m.failCount[channelID] = 0
			m.fileSizes[channelID] = info.Size()
		} else {
			m.failCount[channelID]++
			if m.failCount[channelID] >= 3 {
				slog.Warn("recording unhealthy: file not growing",
					"channel_id", channelID, "session_id", active.SessionID,
					"file_size", info.Size(), "fail_count", m.failCount[channelID])
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) setActive(channelID string, record activeRecord) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.active[channelID]; ok {
		return false
	}
	m.active[channelID] = record
	return true
}

// updateCurrentOutputPath 更新 active 记录的当前输出路径(异常 #4)。
// 切换文件时(首段/重连分段)在同一把锁下重置 fileSizes[channelID]=0 + failCount[channelID]=0,
// 避免健康检测拿旧大文件和新小分段错比,持续误报 unhealthy。
func (m *Manager) updateCurrentOutputPath(channelID, outputPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active, ok := m.active[channelID]; ok {
		if active.CurrentOutputPath != outputPath {
			active.CurrentOutputPath = outputPath
			m.active[channelID] = active
			// 切换文件:重置该 channel 的基线,强制重新学习新文件的 size。
			m.fileSizes[channelID] = 0
			m.failCount[channelID] = 0
		}
	}
}

func (m *Manager) clearActive(channelID string, taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active, ok := m.active[channelID]; ok && active.TaskID == taskID {
		delete(m.active, channelID)
		delete(m.fileSizes, channelID)
		delete(m.failCount, channelID)
	}
}
