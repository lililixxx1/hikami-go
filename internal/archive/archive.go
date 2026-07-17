// Package archive 实现「发布成功后归档到 WebDAV」任务。
//
// 与 internal/upload 的手动上传路径的根本差异：
//   - upload 从 asr_done/recap_done 出发，成功后 Apply(EventUploadSucceeded) → uploaded
//   - archive 从 published 出发，成功后**不**推进 session 主状态，仅写 archived_at
//
// archive 任务是「状态旁路任务」：它不得改 session.Status。为保证这一点，HandleTask 自身
// 不调 states.Apply；失败时的「不降级 published」由 worker 注册元数据驱动——本包在
// Register 时声明 worker.WithBypassFailState()，worker 据此在 syncSessionState 透传
// bypass 标志，cmd/hikami 的 FailSessionStateFunc 收到后仅写 last_error，不调
// Apply(EventTaskFailed)（否则全局 EventTaskFailed 会把 published 降级为 failed，
// 丢失 UI 已发布入口）。状态旁路任务的设计详见历史归档设计文档（设计 4.3，
// 原计划文档已随仓库重建清理）。
package archive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/upload"
	"hikami-go/internal/worker"
)

// 注：archive 不加入 worker 自动重试。归档失败通常是 WebDAV 不可达/目录竞态，
// 立即重试大概率仍失败；与 publish（已在 worker.nonRetryableTypes）同策略，
// 失败后由用户手动 POST /api/sessions/:sid/archive 重试。

const TaskType = "archive"

var (
	ErrSessionNotReady = errors.New("session is not ready for archive")
	ErrArchiveMissing  = errors.New("archive source directory is missing")
	ErrConfigMissing   = errors.New("webdav remote is required for archive")
)

// Handler 归档任务处理器。复用 upload 包的 Copier/Deleter 接口与工厂（不重写 WebDAV
// 复制逻辑），清理逻辑也复用 upload.CleanupSession（设计 4.2 抽出的共享函数），仅通过
// guardStatus=StatusPublished 区分 upload 的 uploaded 守卫。
//
// 注意：nativeWebDAV 与 copier/deleter 在 NewHandler 时按启动时 cfg.WebDAV 固定，与
// upload.Handler 同样的既有约束——运行时通过 /api/config/webdav 改 WebDAV 后端类型后，
// 需重启服务才生效（upload 也是如此，属架构级限制，非本包引入）。
type Handler struct {
	cfg          *config.Config
	sessions     *session.Store
	states       *state.Store
	copier       upload.Copier
	deleter      upload.Deleter
	nativeWebDAV bool
}

// NewHandler 创建归档处理器。copier/deleter 复用 upload.NewConfiguredCopier /
// NewConfiguredDeleter 工厂产物，保证归档与手动上传走同一套 WebDAV/rclone 后端。
func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store,
	copier upload.Copier, deleter upload.Deleter) *Handler {
	return &Handler{
		cfg:          cfg,
		sessions:     sessions,
		states:       states,
		copier:       copier,
		deleter:      deleter,
		nativeWebDAV: cfg.WebDAV.NativeConfigured(),
	}
}

// Register 注册 archive 任务处理器。设计 4.3：archive 是状态旁路任务——失败时由 worker 的
// bypassFailState 元数据驱动「仅写 last_error 不降级 published」，不再依赖 cmd/hikami 硬编码特判。
func (h *Handler) Register(pool *worker.Pool) {
	pool.Register(TaskType, h.HandleTask, worker.WithBypassFailState())
}

// CreateTask 校验前置条件并入队归档任务。
//
// 状态校验只接受 published（与 upload.CreateTask 的 asr_done/recap_done 彻底分离）。
// 同时拒绝同 session 活跃的 upload 任务：两者操作同一场次目录与同一 WebDAV 目标，
// 并发复制/删除会竞态（一方 cleanup=all 删目录时另一方可能复制到一半失败）。
func (h *Handler) CreateTask(ctx context.Context, pool *worker.Pool, sessionID string) (worker.Task, error) {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return worker.Task{}, err
	}
	if sessionInfo.Status != string(state.StatusPublished) {
		return worker.Task{}, fmt.Errorf("%w: status must be %s, got %s", ErrSessionNotReady, state.StatusPublished, sessionInfo.Status)
	}
	if !h.webDAVConfigured() {
		return worker.Task{}, fmt.Errorf("%w: webdav remote is required", ErrConfigMissing)
	}
	if err := h.validateArchiveReady(sessionInfo); err != nil {
		return worker.Task{}, err
	}
	// 活跃归档任务去重（与 upload.CreateTask 同模式）
	if _, ok, err := pool.Store().ActiveBySessionAndType(ctx, sessionInfo.ID, TaskType); err != nil {
		return worker.Task{}, err
	} else if ok {
		return worker.Task{}, fmt.Errorf("%w: active archive task already exists for session %s", worker.ErrTaskConflict, sessionInfo.ID)
	}
	// 拒绝同 session 活跃 upload 任务（互斥 gate）
	if _, ok, err := pool.Store().ActiveBySessionAndType(ctx, sessionInfo.ID, upload.TaskType); err != nil {
		return worker.Task{}, err
	} else if ok {
		return worker.Task{}, fmt.Errorf("%w: active upload task exists for session %s, retry archive later", worker.ErrTaskConflict, sessionInfo.ID)
	}
	return pool.Enqueue(ctx, worker.CreateInput{ChannelID: sessionInfo.ChannelID, SessionID: sessionInfo.ID, Type: TaskType, Payload: "{}"})
}

// HandleTask 执行归档：复制本地场次目录到 WebDAV → 写 archived_at（清 last_error）→ 清理。
//
// ★ 不调 states.Apply、不发任何 Event，session.Status 全程保持 published。
// 复制失败直接返回 err，走 worker.fail；此时由 cmd/hikami 的 SetFailSessionStateFn
// archive 特判兜底，仅写 last_error，不降级 published。
func (h *Handler) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	sessionInfo, err := h.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	if sessionInfo.Status != string(state.StatusPublished) {
		return fmt.Errorf("session state %q is not valid for %s (need published)", sessionInfo.Status, TaskType)
	}
	if err := h.validateArchiveReady(sessionInfo); err != nil {
		return err
	}
	source := h.sessionDir(sessionInfo)
	target := h.archiveTarget(sessionInfo)
	if err := reporter.Progress(ctx, 30, "archiving"); err != nil {
		return err
	}
	if err := h.copier.Copy(ctx, source, target); err != nil {
		return err
	}
	// ★ 不推进主状态。归档完成仅写时间戳并清空历史 last_error（归档失败后重试成功的场景）。
	if err := h.sessions.SetArchivedAt(ctx, sessionInfo.ID, time.Now()); err != nil {
		slog.Warn("archive copy ok but archived_at update failed",
			"session_id", sessionInfo.ID, "error", err)
	}
	// 清理是 best-effort：归档（复制）已成功，删除失败仅记日志不阻断。
	h.cleanupSession(ctx, source, sessionInfo)
	return reporter.Progress(ctx, 95, "archive completed")
}

func (h *Handler) validateArchiveReady(sessionInfo session.Session) error {
	info, err := os.Stat(h.sessionDir(sessionInfo))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrArchiveMissing, h.sessionDir(sessionInfo))
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrArchiveMissing, h.sessionDir(sessionInfo))
	}
	return nil
}

func (h *Handler) sessionDir(sessionInfo session.Session) string {
	return filepath.Join(h.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug)
}

// archiveTarget 镜像 upload.uploadTarget 的 native/rclone 分支，避免混配时路径错误。
func (h *Handler) archiveTarget(sessionInfo session.Session) string {
	if h.nativeWebDAV {
		return filepath.ToSlash(filepath.Join(sessionInfo.ChannelID, sessionInfo.Slug))
	}
	remote := strings.TrimSpace(h.cfg.WebDAV.Remote)
	return remote + filepath.ToSlash(filepath.Join(h.cfg.WebDAV.BasePath, sessionInfo.ChannelID, sessionInfo.Slug))
}

func (h *Handler) webDAVConfigured() bool {
	if h.nativeWebDAV {
		return h.cfg.WebDAV.NativeConfigured()
	}
	return h.cfg.WebDAV.RcloneConfigured()
}

// cleanupSession 根据归档清理策略（archive.cleanup_policy）清理本地和远端资源。
// cleanupSession 委托给 upload.CleanupSession（设计 4.2）。archive 与 upload 的清理逻辑
// 完全一致（temp/generated/all），唯一差异是 cleanupAll 的守卫状态：archive 守卫 published、
// upload 守卫 uploaded，由 guardStatus 参数区分。清理失败仅记录日志，不阻断主流程。
func (h *Handler) cleanupSession(ctx context.Context, sessionDir string, sessionInfo session.Session) {
	upload.CleanupSession(ctx, h.sessions, h.deleter, h.cfg, sessionDir, sessionInfo, h.cfg.Archive.CleanupPolicy, state.StatusPublished)
}
