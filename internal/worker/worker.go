package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/notify"
)

type Handler func(ctx context.Context, task Task, reporter Reporter) error

// registeredHandler 携带注册时的 handler 及其任务行为策略（设计 4.3）。
// bypassFailState=true 表示该任务是「状态旁路任务」（如 upload/archive），
// 失败时不应经 EventTaskFailed 降级 session 主状态，仅写 last_error。
type registeredHandler struct {
	handler         Handler
	bypassFailState bool
}

// RegisterOption 是 Register 的可选策略。
type RegisterOption func(*registeredHandler)

// WithBypassFailState 标记任务类型为状态旁路任务（失败不降级主状态）。
// 设计 4.3：替代 cmd/hikami 对 upload/archive 的硬编码类型特判。
func WithBypassFailState() RegisterOption {
	return func(r *registeredHandler) { r.bypassFailState = true }
}

type Reporter interface {
	Progress(ctx context.Context, progress int, message string) error
}

// FailSessionStateFunc 在任务恢复失败时同步更新 session 状态。
// bypassState=true 时调用方应视为「状态旁路任务」：仅写 last_error，不降级主状态（设计 4.3）。
type FailSessionStateFunc func(ctx context.Context, task Task, event string, taskID string, errorMessage string, bypassState bool) error

type Pool struct {
	store            *Store
	hub              *Hub
	handlers         map[string]registeredHandler
	failSessionState FailSessionStateFunc
	adoptLiveRecord  func(ctx context.Context, task Task, pid int)
	notifyMgr        *notify.Manager
	cfg              *config.Config
	queue            chan string
	done             chan struct{}
	wg               sync.WaitGroup
	runningMu        sync.Mutex
	running          map[string]context.CancelFunc
}

func NewPool(store *Store, hub *Hub, workerCount int, cfg *config.Config) *Pool {
	if workerCount <= 0 {
		workerCount = 1
	}
	return &Pool{
		store:    store,
		hub:      hub,
		handlers: map[string]registeredHandler{},
		cfg:      cfg,
		queue:    make(chan string, workerCount*4),
		done:     make(chan struct{}),
		running:  map[string]context.CancelFunc{},
	}
}

func (p *Pool) SetFailSessionStateFn(fn FailSessionStateFunc) {
	p.failSessionState = fn
}

func (p *Pool) SetNotifyManager(m *notify.Manager) {
	p.notifyMgr = m
}

// SetAdoptLiveRecordFn 注入 live_record 进程接管回调（ISS-6）。
// recoverRunning 检测到重启后仍存活的 ffmpeg 进程时调用，由 live_record Manager 重建 activeRecord。
func (p *Pool) SetAdoptLiveRecordFn(fn func(ctx context.Context, task Task, pid int)) {
	p.adoptLiveRecord = fn
}

// Register 注册任务处理器及其行为策略（设计 4.3：WithBypassFailState 标记状态旁路任务）。
func (p *Pool) Register(taskType string, handler Handler, opts ...RegisterOption) {
	r := registeredHandler{handler: handler}
	for _, opt := range opts {
		opt(&r)
	}
	p.handlers[taskType] = r
}

// bypassFailState 报告某任务类型是否为状态旁路任务（失败不降级主状态）。
func (p *Pool) bypassFailState(taskType string) bool {
	r, ok := p.handlers[taskType]
	return ok && r.bypassFailState
}

func (p *Pool) Start(ctx context.Context, workerCount int) error {
	go p.hub.Run()
	if err := p.recoverRunning(ctx); err != nil {
		return err
	}

	if workerCount <= 0 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		p.wg.Add(1)
		go p.loop()
	}
	return nil
}

func (p *Pool) Stop() {
	close(p.done)
	p.runningMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(p.running))
	for _, cancel := range p.running {
		cancels = append(cancels, cancel)
	}
	p.runningMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	p.wg.Wait()
	p.hub.Stop()
}

func (p *Pool) Enqueue(ctx context.Context, input CreateInput) (Task, error) {
	task, err := p.store.Create(ctx, input)
	if err != nil {
		return Task{}, err
	}
	p.enqueueID(task.ID)
	p.hub.Broadcast(task)
	return task, nil
}

func (p *Pool) Retry(ctx context.Context, id string) (Task, error) {
	task, err := p.store.Retry(ctx, id)
	if err != nil {
		return Task{}, err
	}
	p.enqueueID(task.ID)
	p.hub.Broadcast(task)
	return task, nil
}

func (p *Pool) Cancel(ctx context.Context, id string) (Task, error) {
	task, err := p.store.Cancel(ctx, id)
	if err != nil {
		return Task{}, err
	}
	p.runningMu.Lock()
	cancel := p.running[id]
	p.runningMu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.hub.Broadcast(task)
	return task, nil
}

// BatchRetry 批量重试多个失败任务，跳过无法重试的任务。
func (p *Pool) BatchRetry(ctx context.Context, ids []string) ([]Task, error) {
	var results []Task
	for _, id := range ids {
		task, err := p.Retry(ctx, id)
		if err != nil {
			slog.Warn("batch retry: skip failed task", "task_id", id, "error", err)
			continue
		}
		results = append(results, task)
	}
	return results, nil
}

func (p *Pool) Store() *Store {
	return p.store
}

func (p *Pool) Hub() *Hub {
	return p.hub
}

func (p *Pool) enqueueID(id string) {
	select {
	case p.queue <- id:
	default:
		go func() {
			p.queue <- id
		}()
	}
}

func (p *Pool) loop() {
	defer p.wg.Done()
	for {
		select {
		case id := <-p.queue:
			p.runTask(id)
		case <-p.done:
			return
		}
	}
}

func (p *Pool) runTask(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	p.runningMu.Lock()
	p.running[id] = cancel
	p.runningMu.Unlock()
	defer func() {
		p.runningMu.Lock()
		delete(p.running, id)
		p.runningMu.Unlock()
		cancel()
	}()

	task, err := p.store.MarkRunning(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrTaskConflict) {
			slog.Error("mark task running failed", "task_id", id, "error", err)
		}
		return
	}
	p.hub.Broadcast(task)

	rh, ok := p.handlers[task.Type]
	if !ok || rh.handler == nil {
		p.fail(ctx, task.ID, "task handler not registered", ErrInvalidTask)
		return
	}
	handler := rh.handler

	reporter := &taskReporter{store: p.store, hub: p.hub, taskID: task.ID}
	if err := handler(ctx, task, reporter); err != nil {
		if ctx.Err() != nil {
			return
		}
		p.fail(ctx, task.ID, "task failed", err)
		return
	}
	if ctx.Err() != nil {
		return
	}

	completed, err := p.store.MarkSucceeded(ctx, task.ID, "task completed")
	if err != nil {
		slog.Error("mark task succeeded failed", "task_id", task.ID, "error", err)
		return
	}
	p.hub.Broadcast(completed)
}

func (p *Pool) fail(ctx context.Context, id string, message string, err error) {
	task, markErr := p.store.MarkFailed(ctx, id, message, err)
	if markErr != nil {
		slog.Error("mark task failed failed", "task_id", id, "error", markErr)
		return
	}
	p.hub.Broadcast(task)
	p.syncSessionState(ctx, task, taskErrorMessage(err))
	if p.notifyMgr != nil {
		p.notifyMgr.Send(ctx, notify.EventTaskFailed, "任务失败",
			fmt.Sprintf("任务 %s (%s) 失败: %s", task.ID, task.Type, message))
	}

	// 自动重试逻辑
	if p.shouldAutoRetry(task.Type, task.Attempt) {
		delay := time.Duration(p.cfg.Worker.RetryDelay) * time.Second
		slog.Info("auto retry scheduled", "task_id", id, "type", task.Type, "attempt", task.Attempt, "delay", delay)
		time.AfterFunc(delay, func() {
			retried, retryErr := p.store.Retry(context.Background(), id)
			if retryErr != nil {
				slog.Error("auto retry failed", "task_id", id, "error", retryErr)
				return
			}
			p.enqueueID(retried.ID)
			p.hub.Broadcast(retried)
		})
	}
}

// recoverRunning 根据任务类型执行不同的恢复策略：
//   - asr/asr_poll：可重新入队，由处理器恢复或重新提交
//   - upload：可重新执行，重新入队
//   - live_record：检查 ffmpeg 进程是否仍在运行，不存活则标记 failed
//   - 其他任务：标记 failed，允许用户通过 API 重试
func (p *Pool) recoverRunning(ctx context.Context) error {
	tasks, err := p.store.ListRunning(ctx)
	if err != nil {
		return err
	}

	for _, task := range tasks {
		switch task.Type {
		case "asr", "asr_poll", "upload":
			recovered, recoverErr := p.store.ResetToPending(ctx, task.ID)
			if recoverErr != nil {
				slog.Error("failed to recover task, marking as failed",
					"task_id", task.ID, "type", task.Type, "error", recoverErr)
				_, _ = p.store.MarkFailed(ctx, task.ID, "interrupted by service restart, recovery failed", recoverErr)
				p.hub.Broadcast(task)
				continue
			}
			p.enqueueID(recovered.ID)
			p.hub.Broadcast(recovered)
			slog.Info("recovered task, re-enqueued",
				"task_id", task.ID, "type", task.Type, "session_id", task.SessionID, "attempt", recovered.Attempt)

		case "live_record":
			pid := parsePIDFromMessage(task.Message)
			if isProcessAlive(pid) {
				// ffmpeg 进程仍在运行：重建 Manager.active 记录使其可被 Stop 接管（ISS-6），
				// 保留 running 状态让录制继续。
				if p.adoptLiveRecord != nil {
					p.adoptLiveRecord(ctx, task, pid)
				}
				slog.Info("live_record task ffmpeg process still alive, keeping running",
					"task_id", task.ID, "pid", pid, "session_id", task.SessionID)
				continue
			}
			// 进程已死亡，标记 failed
			failed, failErr := p.store.MarkFailed(ctx, task.ID,
				"interrupted by service restart, ffmpeg process not found", nil)
			if failErr != nil {
				slog.Error("failed to mark live_record task as failed",
					"task_id", task.ID, "error", failErr)
				continue
			}
			p.hub.Broadcast(failed)
			slog.Info("live_record task ffmpeg process not found, marked as failed",
				"task_id", task.ID, "session_id", task.SessionID)

			p.syncSessionState(ctx, task, "interrupted by service restart, ffmpeg process not found")
		default:
			failed, failErr := p.store.MarkFailed(ctx, task.ID,
				"interrupted by service restart", nil)
			if failErr != nil {
				slog.Error("failed to mark task as failed",
					"task_id", task.ID, "type", task.Type, "error", failErr)
				continue
			}
			p.hub.Broadcast(failed)
			slog.Info("task marked as failed on recovery",
				"task_id", task.ID, "type", task.Type, "session_id", task.SessionID)
			p.syncSessionState(ctx, task, "interrupted by service restart")
		}
	}

	return nil
}

// syncSessionState 在任务失败时同步更新 session 状态为 failed。
func (p *Pool) syncSessionState(ctx context.Context, task Task, message string) {
	if p.failSessionState == nil || task.SessionID == "" {
		return
	}
	// 设计 4.3：旁路任务（upload/archive）失败不降级主状态——由注册时的 WithBypassFailState
	// 声明，替代原先 cmd/hikami 对 task.Type 的硬编码特判。
	// 任务实例级 bypass（task.BypassFailState）与类型级（WithBypassFailState）取 OR：
	// "重新生成回顾"等非推进型任务通过 CreateInput.BypassFailState=true 标记，
	// 失败时不降级 published/recap_done 主状态，仅写 last_error。
	bypass := task.BypassFailState || p.bypassFailState(task.Type)
	if err := p.failSessionState(ctx, task, "task_failed", task.ID, message, bypass); err != nil {
		slog.Error("failed to update session state on task recovery failure",
			"task_id", task.ID, "session_id", task.SessionID, "error", err)
	}
}

func taskErrorMessage(err error) string {
	if err == nil {
		return "task failed"
	}
	return err.Error()
}

// nonRetryableTypes 不可自动重试的任务类型集合
var nonRetryableTypes = map[string]struct{}{
	"normalize":   {},
	"publish":     {},
	"live_record": {},
	"import":      {},
	"archive":     {},
}

// ShouldAutoRetry 判断任务是否应该自动重试（供 handler 层和 Pool 共用）
func ShouldAutoRetry(cfg *config.Config, taskType string, attempt int) bool {
	if cfg == nil || !cfg.Worker.AutoRetry {
		return false
	}
	if attempt >= cfg.Worker.MaxRetryAttempts {
		return false
	}
	if _, ok := nonRetryableTypes[taskType]; ok {
		return false
	}
	return true
}

// shouldAutoRetry 委托给导出版本
func (p *Pool) shouldAutoRetry(taskType string, attempt int) bool {
	return ShouldAutoRetry(p.cfg, taskType, attempt)
}

type taskReporter struct {
	store  *Store
	hub    *Hub
	taskID string
}

func (r *taskReporter) Progress(ctx context.Context, progress int, message string) error {
	task, err := r.store.UpdateProgress(ctx, r.taskID, progress, message)
	if err != nil {
		return err
	}
	r.hub.Broadcast(task)
	return nil
}
