package publisher

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/notify"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

const TaskType = "publish"

var (
	ErrSessionNotReady   = errors.New("session is not ready for publish")
	ErrRecapMissing      = errors.New("recap file is missing")
	ErrPublishNotEnabled = errors.New("publish not enabled for channel")
	ErrNotPublished      = errors.New("session is not in published state")
)

type ResolvedPublishConfig struct {
	Mode            string
	CategoryID      int
	ListID          int
	PrivatePub      int
	Original        int
	Aigc            int
	TimerPubTime    int64
	CoverURL        string
	Topics          string
	TopicID         int
	TopicName       string
	CloseComment    int
	UpChooseComment int
}

func resolvePublishConfig(ch channel.Channel, cfg *config.PublishConfig) ResolvedPublishConfig {
	mode := ch.PublishMode
	if mode == "" {
		mode = cfg.Mode
	}
	categoryID := ch.PublishCategoryID
	if categoryID == 0 {
		categoryID = cfg.CategoryID
	}
	listID := ch.PublishListID
	if listID == -1 {
		listID = cfg.ListID
	}
	privatePub := ch.PublishPrivatePub
	if privatePub == 0 {
		privatePub = cfg.PrivatePub
	}
	original := ch.PublishOriginal
	if original == -1 {
		original = cfg.Original
	}
	aigc := ch.PublishAigc
	if aigc == -1 {
		aigc = cfg.Aigc
	}
	timerPubTime := ch.PublishTimerPubTime
	if timerPubTime == 0 {
		timerPubTime = cfg.TimerPubTime
	}
	coverURL := ch.PublishCoverURL
	if strings.TrimSpace(coverURL) == "" {
		coverURL = cfg.CoverURL
	}
	topics := ch.PublishTopics
	if strings.TrimSpace(topics) == "" {
		topics = cfg.Topics
	}
	return ResolvedPublishConfig{
		Mode:            mode,
		CategoryID:      categoryID,
		ListID:          listID,
		PrivatePub:      privatePub,
		Original:        original,
		Aigc:            aigc,
		TimerPubTime:    timerPubTime,
		CoverURL:        coverURL,
		Topics:          topics,
		TopicID:         cfg.TopicID,
		TopicName:       cfg.TopicName,
		CloseComment:    cfg.CloseComment,
		UpChooseComment: cfg.UpChooseComment,
	}
}

type Handler struct {
	cfg                *config.Config
	sessions           *session.Store
	states             *state.Store
	channels           *channel.Store
	client             OpusClient
	cookieAccountStore *biliutil.CookieAccountStore
	notifyMgr          *notify.Manager
	onSuccess          func(ctx context.Context, task worker.Task)
	// tasks 提供 draft_id 持久化到任务 payload 的最小能力（M11），
	// 由 worker.Store 实现；nil 时发布仍可进行，仅失去重试时的旧草稿清理。
	tasks taskPayloadWriter
}

// taskPayloadWriter 是 M11 引入的最小 store 接口（避免 publisher 反向持有整个
// worker.Store 构造依赖）：SaveDraft 成功后把 draft_id 写进任务 payload。
type taskPayloadWriter interface {
	UpdatePayload(ctx context.Context, id string, payload string) error
}

// SetTaskStore 注入任务 payload 写入能力（main.go 用 workerPool.Store()）。
func (h *Handler) SetTaskStore(store taskPayloadWriter) {
	h.tasks = store
}

func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store, channels *channel.Store, client ...OpusClient) *Handler {
	c := OpusClient(NewBiliOpusClient())
	if len(client) > 0 {
		c = client[0]
	}
	return &Handler{
		cfg:      cfg,
		sessions: sessions,
		states:   states,
		channels: channels,
		client:   c,
	}
}

func (h *Handler) SetCookieAccountStore(store *biliutil.CookieAccountStore) {
	h.cookieAccountStore = store
}

func (h *Handler) SetNotifyManager(m *notify.Manager) {
	h.notifyMgr = m
}

// SetOnSuccess 注册发布成功后的回调（范本：asr/recap 的 SetOnSuccess）。
// cmd/hikami 用它在 published 后按 archive.auto_after_publish 决定是否自动归档。
// 回调在 ApplyWithPublishTarget（状态已 published）之后、最终进度上报之前触发，
// 保证回调里读 session 状态已是 published。回调失败由调用方处理（不在此吞）。
func (h *Handler) SetOnSuccess(fn func(ctx context.Context, task worker.Task)) {
	h.onSuccess = fn
}

func (h *Handler) CreateTask(ctx context.Context, pool *worker.Pool, sessionID string) (worker.Task, error) {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return worker.Task{}, err
	}
	if sessionInfo.Status != string(state.StatusRecapDone) && sessionInfo.Status != string(state.StatusUploaded) {
		return worker.Task{}, fmt.Errorf("%w: status must be recap_done or uploaded, got %s", ErrSessionNotReady, sessionInfo.Status)
	}
	if !sessionInfo.LocalAvailable {
		return worker.Task{}, fmt.Errorf("%w: local files removed, fetch from webdav first", ErrRecapMissing)
	}
	if _, err := os.Stat(h.recapDir(sessionInfo)); err != nil {
		return worker.Task{}, fmt.Errorf("%w: %s", ErrRecapMissing, h.recapDir(sessionInfo))
	}
	ch, err := h.channels.Get(ctx, sessionInfo.ChannelID)
	if err != nil {
		return worker.Task{}, fmt.Errorf("get channel: %w", err)
	}
	if h.cookieAccountStore == nil && ch.CookieFile == "" {
		return worker.Task{}, fmt.Errorf("%w: channel %s has no cookie_file configured", ErrChannelNoCookieFile, ch.ID)
	}
	if !ch.PublishEnabled && !h.cfg.Publish.Enabled {
		return worker.Task{}, fmt.Errorf("%w: channel %s", ErrPublishNotEnabled, ch.ID)
	}
	// M11:活跃检查 + 创建原子化(旧「先查后插」两步在并发双击下会创建重复任务,
	// 重复任务 = 重复发专栏)。created=false 即已有活跃任务,维持 409 语义。
	task, created, err := pool.EnqueueIfNoActive(ctx, worker.CreateInput{
		ChannelID: sessionInfo.ChannelID,
		SessionID: sessionInfo.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
	if err != nil {
		return worker.Task{}, err
	}
	if !created {
		return worker.Task{}, fmt.Errorf("%w: active publish task already exists for session %s", worker.ErrTaskConflict, sessionInfo.ID)
	}
	return task, nil
}

func (h *Handler) Register(pool *worker.Pool) {
	pool.Register(TaskType, h.HandleTask)
}

func (h *Handler) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	sessionInfo, err := h.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	if !canHandlePublish(sessionInfo.Status) {
		return fmt.Errorf("session state %q is not valid for %s", sessionInfo.Status, TaskType)
	}
	ch, err := h.channels.Get(ctx, sessionInfo.ChannelID)
	if err != nil {
		return err
	}

	if err := reporter.Progress(ctx, 5, "loading session"); err != nil {
		return err
	}

	cookie, err := h.resolvePublishCookie(ctx, ch)
	if err != nil {
		return err
	}

	if err := reporter.Progress(ctx, 10, "loading credentials"); err != nil {
		return err
	}

	progress := func(pct int, msg string) error { return reporter.Progress(ctx, pct, msg) }
	// M11:payload 里可能带上轮 SaveDraft 持久化的 draft_id(形如 {"draft_id":"123"}),
	// 重试时先删旧草稿再新建,避免 B 站创作中心草稿箱积压。当前 CreateTask 写 "{}"。
	var stale PublishTarget
	if task.Payload != "" {
		if err := json.Unmarshal([]byte(task.Payload), &stale); err != nil {
			slog.WarnContext(ctx, "parse task payload for stale draft_id failed, ignoring", "task_id", task.ID, "error", err)
		}
	}
	var persistDraft func(draftID string) error
	if h.tasks != nil {
		store := h.tasks
		taskID := task.ID
		persistDraft = func(draftID string) error {
			return store.UpdatePayload(ctx, taskID, PublishTarget{DraftID: draftID}.Marshal())
		}
	}
	target, err := h.publishRecap(ctx, sessionInfo, ch, cookie, progress, stale.DraftID, persistDraft)
	if err != nil {
		return err
	}

	if err := reporter.Progress(ctx, 90, "updating status"); err != nil {
		return err
	}

	if _, err := h.states.ApplyWithPublishTarget(ctx, task.SessionID, task.ID, target.Marshal()); err != nil {
		return err
	}

	if h.notifyMgr != nil {
		h.notifyMgr.Send(ctx, notify.EventPublishDone, "发布完成",
			fmt.Sprintf("频道 %s 的专栏已发布", ch.ID))
	}

	// 发布成功后触发回调（用于自动归档链路）。放在 ApplyWithPublishTarget 之后
	// （状态已 published）、最终进度之前，保证回调入队结果体现在任务流里。
	if h.onSuccess != nil {
		h.onSuccess(ctx, task)
	}

	// X1:ApplyWithPublishTarget 已成功(状态 published)、onSuccess 自动归档已触发,
	// 进度上报失败降级为告警,不再让已成功的发布被任务失败回卷。
	if err := reporter.Progress(ctx, 95, "publish completed"); err != nil {
		slog.WarnContext(ctx, "post-success progress report failed", "task_id", task.ID, "error", err)
	}
	return nil
}

// publishRecap 执行「读取最新 recap → 转 opus → 存草稿 → (publish 模式)发布」核心流程，
// 返回组装好的 PublishTarget（序列化为 JSON 存入 publish_target）。HandleTask（异步，带进度
// 上报与失败状态推进）调用此方法。progress 为可选进度回调，
// nil 表示不上报进度。staleDraftID/persistDraft 是 M11 的草稿幂等链：前者为任务 payload
// 里上轮 SaveDraft 的草稿 ID（非空则先删旧草稿），后者在 SaveDraft 成功后把新 draft_id
// 持久化回任务 payload（nil 表示无持久化能力，如未注入 store 的测试路径）。
func (h *Handler) publishRecap(
	ctx context.Context,
	sessionInfo session.Session,
	ch channel.Channel,
	cookie *BiliCookie,
	progress func(pct int, msg string) error,
	staleDraftID string,
	persistDraft func(draftID string) error,
) (PublishTarget, error) {
	recapDir := h.recapDir(sessionInfo)
	mdPath, err := findRecapMarkdown(recapDir)
	if err != nil {
		return PublishTarget{}, err
	}
	mdData, err := os.ReadFile(mdPath)
	if err != nil {
		return PublishTarget{}, err
	}

	resolved := resolvePublishConfig(ch, &h.cfg.Publish)

	if progress != nil {
		if err := progress(20, "reading recap"); err != nil {
			return PublishTarget{}, err
		}
	}

	paragraphs := ConvertMarkdownToOpus(string(mdData))

	if progress != nil {
		if err := progress(40, "converting to opus format"); err != nil {
			return PublishTarget{}, err
		}
	}

	summary := extractSummary(string(mdData), h.cfg.Publish.SummaryLen)

	if progress != nil {
		if err := progress(50, "preparing draft"); err != nil {
			return PublishTarget{}, err
		}
	}

	title := extractTitle(string(mdData))
	if title == "" {
		title = sessionInfo.Title
	}

	// 封面来源优先级：配置 cover_url > recap/cover.* > raw/cover.*(仅当 auto_cover 开启)。
	// - cover_url：用户显式配置（频道 publish_cover_url 优先，回退全局 cover_url），最高优先。
	//   网络 URL 原样用；本地路径上传换 URL；上传失败/为空则回退到下一来源（避免本地路径被丢弃也无替代）。
	// - recap/cover.*：人工/回顾封面，第二优先级。
	// - raw/cover.*：download/live_record 自动取的官方源封面；仅当 AutoCover=true 且 recap 无封面时才用。
	// 上传后 URL 同时用于草稿端(arg.image_urls)和发布端(opus_req.opus.article.cover)。
	coverURL := h.resolveCoverUpload(ctx, cookie, resolved.CoverURL)
	if coverURL == "" {
		if coverPath := findCoverImage(recapDir); coverPath != "" {
			coverURL = h.uploadCoverPath(ctx, cookie, coverPath)
		}
	}
	if coverURL == "" && h.cfg.Publish.AutoCover {
		if coverPath := findCoverImage(h.rawDir(sessionInfo)); coverPath != "" {
			coverURL = h.uploadCoverPath(ctx, cookie, coverPath)
		}
	}

	draftReq := &DraftRequest{
		Title:           title,
		Paragraphs:      paragraphs,
		Summary:         summary,
		CategoryID:      resolved.CategoryID,
		ListID:          resolved.ListID,
		PrivatePub:      resolved.PrivatePub,
		Original:        resolved.Original,
		Aigc:            resolved.Aigc,
		TimerPubTime:    resolved.TimerPubTime,
		CoverURL:        coverURL,
		Tags:            resolved.Topics,
		CloseComment:    resolved.CloseComment,
		UpChooseComment: resolved.UpChooseComment,
	}

	if progress != nil {
		if err := progress(70, "saving draft"); err != nil {
			return PublishTarget{}, err
		}
	}

	// M11:重试时先删上轮遗留的旧草稿(创作中心草稿箱不留垃圾)。删除失败降级继续——
	// 上轮可能实际已发布成功(草稿随发布转正/被 B 站清理),404/已发布类错误不应阻断本轮。
	if staleDraftID != "" {
		if err := h.client.DeleteDraft(ctx, cookie, staleDraftID); err != nil {
			slog.WarnContext(ctx, "delete stale draft before retry failed, continuing",
				"stale_draft_id", staleDraftID, "error", err)
		}
	}

	draftID, err := h.client.SaveDraft(ctx, cookie, draftReq)
	if err != nil {
		return PublishTarget{}, err
	}
	// M11:SaveDraft 成功立即把 draft_id 持久化进任务 payload(在 PublishOpus 之前——
	// 发布超时/失败后重试时靠它拿到旧 draft_id 做清理)。持久化失败仅告警:损失的是
	// 下轮重试的草稿清理,不应阻断本轮发布。
	if persistDraft != nil {
		if err := persistDraft(draftID); err != nil {
			slog.WarnContext(ctx, "persist draft_id to task payload failed",
				"task_draft_id", draftID, "error", err)
		}
	}

	if resolved.Mode == "publish" {
		if progress != nil {
			if err := progress(85, "publishing"); err != nil {
				return PublishTarget{}, err
			}
		}

		originality := resolved.Original
		reproduced := 1
		if originality == 1 {
			reproduced = 0
		}

		pubReq := &PublishRequest{
			Title:           title,
			Paragraphs:      paragraphs,
			CategoryID:      resolved.CategoryID,
			ListID:          resolved.ListID,
			PrivatePub:      resolved.PrivatePub,
			Originality:     originality,
			Reproduced:      reproduced,
			DraftID:         draftID,
			Mid:             cookie.DedeUserID,
			CoverURL:        draftReq.CoverURL,
			Aigc:            resolved.Aigc,
			Tags:            resolved.Topics,
			TopicID:         resolved.TopicID,
			TopicName:       resolved.TopicName,
			TimerPubTime:    resolved.TimerPubTime,
			CloseComment:    resolved.CloseComment,
			UpChooseComment: resolved.UpChooseComment,
		}

		dynID, dynType, dynRid, err := h.client.PublishOpus(ctx, cookie, pubReq)
		if err != nil {
			return PublishTarget{}, annotateUnknownPublishOutcome(err)
		}
		return PublishTarget{DynID: dynID, DynType: dynType, DynRid: dynRid}, nil
	}

	return PublishTarget{DraftID: draftID}, nil
}

// annotateUnknownPublishOutcome 给超时类的发布错误追加人工确认提示(M11③):
// PublishOpus 超时时发布结果未知(请求可能已被 B 站受理、实际发布成功),直接自动
// 重试会重复发文,引导用户先去创作中心确认。非超时类错误原样返回。
func annotateUnknownPublishOutcome(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	unknown := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded")
	if !unknown {
		return err
	}
	return fmt.Errorf("%w；发布请求超时、结果未知，请先到 B 站创作中心确认是否已发布，避免重复发文", err)
}

func canHandlePublish(status string) bool {
	// failed 允许(M11 审核跟进,2026-08-15):publish 失败会把 recap_done 降级 failed,
	// 而 ResetFailedSession 只接受 ASR 失败——不放开 failed 的话,「发布超时→人工到
	// 创作中心确认→重试」的闭环(M11②/③)在 UI 上无路可走。与 canHandleRecap 的
	// failed 放行对齐;publish 任务只可能由 recap_done/uploaded 场次创建,放行 failed
	// 不会误触发新发布,只会让既有失败任务可重试(状态机 failed→published 合法)。
	return status == string(state.StatusRecapDone) || status == string(state.StatusUploaded) ||
		status == string(state.StatusFailed)
}

func (h *Handler) resolvePublishCookie(ctx context.Context, ch channel.Channel) (*BiliCookie, error) {
	if h.cookieAccountStore != nil {
		// 2026-07-20 修订:传入 channel.PublishAccountID 让 ResolveCookie 三级链生效
		//   level 1: channel.publish_account_id(本次改动新增的字段)
		//   level 2: 全局默认发布账号
		//   level 3: legacy channel.cookie_file(fallback)
		// 此前 publishAccountID 永远传 sql.NullInt64{},level 1 永远跳过,
		// 导致主播级发布账号字段无法生效。
		cookie, err := h.cookieAccountStore.ResolveCookie(ctx, sql.NullInt64{}, nullInt64FromPtr(ch.PublishAccountID), "publish", ch.CookieFile)
		if err == nil {
			return cookie, nil
		}
		if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
			slog.Warn("resolve publish cookie account failed, falling back to legacy cookie file",
				"channel_id", ch.ID, "error", err)
		}
	}
	if ch.CookieFile == "" {
		return nil, fmt.Errorf("%w: channel %s has no cookie_file configured", ErrChannelNoCookieFile, ch.ID)
	}
	return LoadCookie(ch.CookieFile)
}

// nullInt64FromPtr 把 *int64 转为 sql.NullInt64(nil → invalid,非 nil → valid)。
// publisher 包本地 helper(参照 live_record/manager.go:182 同名实现)。
// 用于把 channel.PublishAccountID 传入 ResolveCookie 的 publishAccountID 参数。
func nullInt64FromPtr(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func (h *Handler) recapDir(sessionInfo session.Session) string {
	return filepath.Join(h.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug, "recap")
}

// rawDir 是 recapDir 的兄弟目录，存放下载/录制阶段的原始素材（含自动取的 raw/cover.*）。
func (h *Handler) rawDir(sessionInfo session.Session) string {
	return filepath.Join(h.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug, "raw")
}

func findRecapMarkdown(recapDir string) (string, error) {
	entries, err := os.ReadDir(recapDir)
	if err != nil {
		return "", fmt.Errorf("read recap dir: %w", err)
	}
	var latest os.DirEntry
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if strings.HasSuffix(name, ".prompt.md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if latest == nil || info.ModTime().After(latestMod) {
			latest = e
			latestMod = info.ModTime()
		}
	}
	if latest == nil {
		return "", fmt.Errorf("no recap markdown found in %s", recapDir)
	}
	return filepath.Join(recapDir, latest.Name()), nil
}

// findCoverImage 在给定目录查找首个存在的 cover.{png,jpg,jpeg,webp}，找不到返回空串。
func findCoverImage(dir string) string {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		p := filepath.Join(dir, "cover"+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// uploadCoverPath 把本地封面图片文件上传到 B 站换取网络 URL。
// client 不支持封面上传或上传失败时记录警告并返回空串，让调用方回退到下一优先级来源。
func (h *Handler) uploadCoverPath(ctx context.Context, cookie *BiliCookie, coverPath string) string {
	uploader, ok := h.client.(OpusCoverUploader)
	if !ok {
		slog.Warn("cover path found but client does not support cover upload", "cover_path", coverPath)
		return ""
	}
	uploaded, err := uploader.UploadCover(ctx, cookie, coverPath)
	if err != nil {
		slog.Warn("cover upload failed", "cover_path", coverPath, "error", err)
		return ""
	}
	return uploaded
}

// resolveCoverUpload 解析配置来源（config.cover_url / channel.publish_cover_url）的封面值。
// - 空：返回空串（不带封面）。
// - 已是 http(s):// URL：原样返回。
// - 其它：视为本地文件路径，上传到 B 站换取真实 URL。
//
// 上传失败或 client 不支持封面上传时，记录警告并返回空串——避免把本地路径
// 当 URL 误塞进发布请求（bilibili_opus.go 的 image_urls / article.cover 只接受网络 URL）。
func (h *Handler) resolveCoverUpload(ctx context.Context, cookie *BiliCookie, coverURL string) string {
	coverURL = strings.TrimSpace(coverURL)
	if coverURL == "" {
		return ""
	}
	// 网络 URL（含大小写 scheme 与协议相对 URL）原样/规范化后使用，避免误判为本地路径。
	if normalized, ok := webCoverURL(coverURL); ok {
		return normalized
	}
	uploader, ok := h.client.(OpusCoverUploader)
	if !ok {
		slog.Warn("cover_url 指向本地文件，但 client 不支持封面上传，已忽略",
			"cover_url", coverURL)
		return ""
	}
	uploaded, err := uploader.UploadCover(ctx, cookie, coverURL)
	if err != nil {
		slog.Warn("cover_url 本地封面上传失败，将以无封面发布",
			"cover_url", coverURL, "error", err)
		return ""
	}
	return uploaded
}

// webCoverURL 判断 coverURL 是否为网络 URL。
// 是则返回（必要时规范化后的）URL 与 true；否则（本地路径）返回 "" 与 false。
// 处理：大小写 scheme（HTTPS://、HTTP://）、协议相对 URL（//i0.hdslb.com/a.png → https://...）。
// 仅校验 scheme，不校验可达性。
func webCoverURL(coverURL string) (string, bool) {
	if strings.HasPrefix(coverURL, "//") {
		// 协议相对 URL：B 站图床常见形式，规范化为 https。
		return "https:" + coverURL, true
	}
	lower := strings.ToLower(coverURL)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return coverURL, true
	}
	return "", false
}

func extractTitle(md string) string {
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			return strings.TrimPrefix(trimmed, "# ")
		}
	}
	return ""
}

func extractSummary(md string, maxLen int) string {
	var text strings.Builder
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ">") ||
			strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "|") ||
			isHR(trimmed) {
			continue
		}
		text.WriteString(trimmed)
		text.WriteString(" ")
		if text.Len() >= maxLen {
			break
		}
	}
	s := strings.TrimSpace(text.String())
	if len(s) > maxLen {
		s = s[:maxLen] + "..."
	}
	return s
}
