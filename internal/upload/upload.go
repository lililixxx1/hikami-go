package upload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

const TaskType = "upload"

var (
	ErrSessionNotReady = errors.New("session is not ready for upload")
	ErrArchiveMissing  = errors.New("upload archive is missing")
	ErrConfigMissing   = errors.New("upload config is missing")
)

type Copier interface {
	Copy(ctx context.Context, source string, target string) error
}

type Deleter interface {
	Delete(ctx context.Context, target string) error
}

type Fetcher interface {
	Fetch(ctx context.Context, source string, target string) error
}

type RcloneCopier struct {
	Command string
}

func (c RcloneCopier) Copy(ctx context.Context, source string, target string) error {
	command := c.Command
	if command == "" {
		command = "rclone"
	}
	cmd := exec.CommandContext(ctx, command, "copy", source, target)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rclone copy failed: %w: %s", err, string(output))
	}
	return nil
}

func (c RcloneCopier) Delete(ctx context.Context, target string) error {
	command := c.Command
	if command == "" {
		command = "rclone"
	}
	cmd := exec.CommandContext(ctx, command, "delete", target)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rclone delete failed: %w: %s", err, string(output))
	}
	return nil
}

type Handler struct {
	cfg          *config.Config
	sessions     *session.Store
	states       *state.Store
	copier       Copier
	deleter      Deleter
	nativeWebDAV bool
}

func NewConfiguredCopier(cfg *config.Config) Copier {
	if cfg.WebDAV.NativeConfigured() {
		return NewWebDAVCopier(&cfg.WebDAV)
	}
	return RcloneCopier{Command: cfg.Rclone}
}

func NewConfiguredDeleter(cfg *config.Config) Deleter {
	if cfg.WebDAV.NativeConfigured() {
		return NewWebDAVCopier(&cfg.WebDAV)
	}
	return RcloneCopier{Command: cfg.Rclone}
}

func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store, copier Copier) *Handler {
	return &Handler{
		cfg:          cfg,
		sessions:     sessions,
		states:       states,
		copier:       copier,
		deleter:      NewConfiguredDeleter(cfg),
		nativeWebDAV: cfg.WebDAV.NativeConfigured(),
	}
}

// Register 注册 upload 任务处理器。设计 4.3：upload 是状态旁路任务——失败时由 worker 的
// bypassFailState 元数据驱动「仅写 last_error 不降级主状态」，不再依赖 cmd/hikami 硬编码特判。
func (h *Handler) Register(pool *worker.Pool) {
	pool.Register(TaskType, h.HandleTask, worker.WithBypassFailState())
}

func (h *Handler) CreateTask(ctx context.Context, pool *worker.Pool, sessionID string) (worker.Task, error) {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return worker.Task{}, err
	}
	if err := h.validateUploadReady(sessionInfo); err != nil {
		return worker.Task{}, err
	}
	if _, ok, err := pool.Store().ActiveBySessionAndType(ctx, sessionInfo.ID, TaskType); err != nil {
		return worker.Task{}, err
	} else if ok {
		return worker.Task{}, fmt.Errorf("%w: active upload task already exists for session %s", worker.ErrTaskConflict, sessionInfo.ID)
	}
	return pool.Enqueue(ctx, worker.CreateInput{ChannelID: sessionInfo.ChannelID, SessionID: sessionInfo.ID, Type: TaskType, Payload: "{}"})
}

func (h *Handler) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	sessionInfo, err := h.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	if !canHandleUpload(sessionInfo.Status) {
		return fmt.Errorf("session state %q is not valid for %s", sessionInfo.Status, TaskType)
	}
	// 前置产物校验（ISS-5/8）：sessionDir 缺失时直接失败，避免上传空/无效内容；
	// 失败由 worker 层统一回退 session 状态，沿用 upload 既有"不 Apply 主状态"语义。
	if err := h.validateUploadReady(sessionInfo); err != nil {
		return err
	}
	source := h.sessionDir(sessionInfo)
	target := h.uploadTarget(sessionInfo)
	if err := reporter.Progress(ctx, 30, "uploading archive"); err != nil {
		return err
	}
	if err := h.copier.Copy(ctx, source, target); err != nil {
		return err
	}
	if _, err := h.states.Apply(ctx, task.SessionID, state.EventUploadSucceeded, task.ID, ""); err != nil {
		return err
	}

	// 上传成功后执行清理策略
	h.cleanupSession(ctx, source, sessionInfo)

	return reporter.Progress(ctx, 95, "upload completed")
}

func (h *Handler) Fetch(ctx context.Context, sessionID string) (session.Session, error) {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return session.Session{}, err
	}
	if !h.webDAVConfigured() {
		return session.Session{}, fmt.Errorf("%w: webdav remote is required", ErrConfigMissing)
	}
	source := h.uploadTarget(sessionInfo)
	target := h.sessionDir(sessionInfo)
	if fetcher, ok := h.copier.(Fetcher); ok && h.nativeWebDAV {
		if err := fetcher.Fetch(ctx, source, target); err != nil {
			return session.Session{}, err
		}
	} else {
		if err := h.copier.Copy(ctx, source, target); err != nil {
			return session.Session{}, err
		}
	}
	// 本地目录已恢复，标记 local_available=true，解除 glossary/recap/publisher 守卫。
	// 置位失败仅记录：文件已取回，标记滞后不影响后续读取，但会误导守卫，故 Warn 醒目。
	if err := h.sessions.SetLocalAvailable(ctx, sessionInfo.ID, true); err != nil {
		slog.Warn("failed to mark local_available=true after fetch",
			"session_id", sessionInfo.ID, "error", err)
	}
	// 返回最新 session 以反映置位后的 local_available。
	return h.sessions.Get(ctx, sessionInfo.ID)
}

func (h *Handler) validateUploadReady(sessionInfo session.Session) error {
	if !canHandleUpload(sessionInfo.Status) {
		return fmt.Errorf("%w: status must be %s or %s, got %s", ErrSessionNotReady, state.StatusASRDone, state.StatusRecapDone, sessionInfo.Status)
	}
	if !h.webDAVConfigured() {
		return fmt.Errorf("%w: webdav remote is required", ErrConfigMissing)
	}
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

func canHandleUpload(status string) bool {
	return status == string(state.StatusASRDone) || status == string(state.StatusRecapDone)
}

func (h *Handler) sessionDir(sessionInfo session.Session) string {
	return filepath.Join(h.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug)
}

func (h *Handler) remotePath(sessionInfo session.Session) string {
	remote := strings.TrimSpace(h.cfg.WebDAV.Remote)
	return remote + filepath.ToSlash(filepath.Join(h.cfg.WebDAV.BasePath, sessionInfo.ChannelID, sessionInfo.Slug))
}

func (h *Handler) uploadTarget(sessionInfo session.Session) string {
	if h.nativeWebDAV {
		return h.remotePathNative(sessionInfo)
	}
	return h.remotePath(sessionInfo)
}

func (h *Handler) remotePathNative(sessionInfo session.Session) string {
	return filepath.ToSlash(filepath.Join(sessionInfo.ChannelID, sessionInfo.Slug))
}

func (h *Handler) webDAVConfigured() bool {
	if h.nativeWebDAV {
		return h.cfg.WebDAV.NativeConfigured()
	}
	return h.cfg.WebDAV.RcloneConfigured()
}

// cleanupSession 根据上传清理策略清理本地和远端资源。
// 清理失败仅记录日志，不阻断主流程。
func (h *Handler) cleanupSession(ctx context.Context, sessionDir string, sessionInfo session.Session) {
	CleanupSession(ctx, h.sessions, h.deleter, h.cfg, sessionDir, sessionInfo, h.cfg.Upload.CleanupPolicy, state.StatusUploaded)
}

// CleanupSession 是 upload 与 archive 共享的清理实现（设计 4.2）。upload 守卫 uploaded、
// archive 守卫 published，通过 guardStatus 参数区分；其余 temp/generated/all 逻辑完全一致，
// 抽到包级消除重复（此前 archive 自带一份，维护成本翻倍）。
//
// 参数：
//   - sessions: 用于 cleanupAll 前的状态校验与成功后 SetLocalAvailable(false)
//   - deleter: temp 策略删除远端临时对象（可为 nil）
//   - cfg: 取 ASRTemp.RcloneRemote/BasePath 拼远端临时路径
//   - guardStatus: cleanupAll 的前置状态守卫（upload=uploaded, archive=published）
func CleanupSession(ctx context.Context, sessions *session.Store, deleter Deleter, cfg *config.Config,
	sessionDir string, sessionInfo session.Session, policy string, guardStatus state.Status) {
	policy = strings.TrimSpace(policy)
	if policy == "" || policy == "none" {
		return
	}

	slog.Info("executing cleanup policy",
		"policy", policy, "session_id", sessionInfo.ID, "session_dir", sessionDir, "guard_status", guardStatus)

	switch policy {
	case "temp":
		cleanupTempShared(ctx, deleter, cfg, sessionDir, sessionInfo)
	case "generated":
		cleanupGeneratedShared(sessionDir)
	case "all":
		cleanupAllShared(ctx, sessions, sessionDir, sessionInfo, guardStatus)
	default:
		slog.Warn("unknown cleanup policy, skipping", "policy", policy)
	}
}

// cleanupTempShared 删除 ASR 临时公开音频记录和远端临时对象。
func cleanupTempShared(ctx context.Context, deleter Deleter, cfg *config.Config, sessionDir string, sessionInfo session.Session) {
	// 删除本地 asr/audio.public.json
	publicAudio := filepath.Join(sessionDir, "asr", "audio.public.json")
	if err := os.Remove(publicAudio); err != nil && !os.IsNotExist(err) {
		slog.Error("failed to remove public audio file",
			"path", publicAudio, "error", err)
	} else if err == nil {
		slog.Info("removed public audio file", "path", publicAudio)
	}

	// 删除远端临时对象
	if deleter != nil && cfg != nil && cfg.ASRTemp.RcloneRemote != "" {
		tempRemote := cfg.ASRTemp.RcloneRemote + filepath.ToSlash(
			filepath.Join(cfg.ASRTemp.BasePath, sessionInfo.ChannelID, sessionInfo.Slug))
		if err := deleter.Delete(ctx, tempRemote); err != nil {
			slog.Error("failed to delete remote temp object",
				"remote", tempRemote, "error", err)
		} else {
			slog.Info("deleted remote temp object", "remote", tempRemote)
		}
	}
}

// cleanupGeneratedShared 删除可重新生成的中间文件（asr/ 目录），保留 raw/、package/、recap/。
func cleanupGeneratedShared(sessionDir string) {
	asrDir := filepath.Join(sessionDir, "asr")
	if err := os.RemoveAll(asrDir); err != nil {
		slog.Error("failed to remove asr directory",
			"path", asrDir, "error", err)
	} else {
		slog.Info("removed asr directory", "path", asrDir)
	}
}

// cleanupAllShared 确认状态仍为 guardStatus 后删除整个场次本地目录，并标记 local_available=false。
// guardStatus 区分调用方：upload 守卫 uploaded、archive 守卫 published（归档期间可能被并发回退）。
func cleanupAllShared(ctx context.Context, sessions *session.Store, sessionDir string, sessionInfo session.Session, guardStatus state.Status) {
	updated, err := sessions.Get(ctx, sessionInfo.ID)
	if err != nil {
		slog.Error("failed to verify session status before full cleanup",
			"session_id", sessionInfo.ID, "guard_status", guardStatus, "error", err)
		return
	}
	if updated.Status != string(guardStatus) {
		slog.Warn("skipping full cleanup: session status is not the guard status",
			"session_id", sessionInfo.ID, "status", updated.Status, "guard_status", guardStatus)
		return
	}

	// 删除整个本地场次目录（raw/asr/package/recap/metadata.json 全部释放）
	if err := os.RemoveAll(sessionDir); err != nil {
		slog.Error("failed to remove session directory",
			"path", sessionDir, "error", err)
		return
	}
	slog.Info("removed session directory", "path", sessionDir)

	// 本地目录已删除，标记 local_available=false，驱动 glossary/recap/publisher 守卫。
	// 置位失败仅记录：文件已删，标记滞后可由后续 Fetch 修正，不阻断主流程。
	if err := sessions.SetLocalAvailable(ctx, sessionInfo.ID, false); err != nil {
		slog.Warn("failed to mark local_available=false after cleanup",
			"session_id", sessionInfo.ID, "error", err)
	}
}
