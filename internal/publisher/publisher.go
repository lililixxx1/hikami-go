package publisher

import (
	"context"
	"database/sql"
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
	if _, ok, err := pool.Store().ActiveBySessionAndType(ctx, sessionInfo.ID, TaskType); err != nil {
		return worker.Task{}, err
	} else if ok {
		return worker.Task{}, fmt.Errorf("%w: active publish task already exists for session %s", worker.ErrTaskConflict, sessionInfo.ID)
	}
	return pool.Enqueue(ctx, worker.CreateInput{
		ChannelID: sessionInfo.ChannelID,
		SessionID: sessionInfo.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
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
	target, err := h.publishRecap(ctx, sessionInfo, ch, cookie, progress)
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

	return reporter.Progress(ctx, 95, "publish completed")
}

// publishRecap 执行「读取最新 recap → 转 opus → 存草稿 → (publish 模式)发布」核心流程，
// 返回组装好的 PublishTarget（序列化为 JSON 存入 publish_target）。HandleTask（异步，带进度
// 上报与失败状态推进）和 EditOpus（同步删+重发）共用此方法。progress 为可选进度回调，
// nil 表示不上报进度（同步路径）。
func (h *Handler) publishRecap(
	ctx context.Context,
	sessionInfo session.Session,
	ch channel.Channel,
	cookie *BiliCookie,
	progress func(pct int, msg string) error,
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

	// 封面来源（优先级：recap 目录 cover.* > 配置 cover_url）。
	// 先查 recap cover：找到则只上传它，避免对配置的本地路径做无用上传（codex 审核）。
	// 仅当 recap 无 cover 时，才 fallback 到配置来源的 cover_url（本地路径自动上传 / 网络 URL 原样用）。
	// 上传后 URL 同时用于草稿端(arg.image_urls)和发布端(opus_req.opus.article.cover)。
	coverURL := ""
	if coverPath := findCoverImage(recapDir); coverPath != "" {
		if uploader, ok := h.client.(OpusCoverUploader); ok {
			if uploaded, err := uploader.UploadCover(ctx, cookie, coverPath); err == nil {
				coverURL = uploaded
			}
		}
	}
	if coverURL == "" {
		coverURL = h.resolveCoverUpload(ctx, cookie, resolved.CoverURL)
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

	draftID, err := h.client.SaveDraft(ctx, cookie, draftReq)
	if err != nil {
		return PublishTarget{}, err
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
			return PublishTarget{}, err
		}
		return PublishTarget{DynID: dynID, DynType: dynType, DynRid: dynRid}, nil
	}

	return PublishTarget{DraftID: draftID}, nil
}

// RemoveOpus 删除已发布专栏并将会话状态回退到 uploaded（本地产物仍在，可重新发布）。
// 同步执行：读取 publish_target 取 dyn_id → 调 B 站 operate/remove → 状态机 published→uploaded。
// 仅对真正发布（有 dyn_id）的专栏有效；草稿模式或未发布返回 ErrNotPublished。
func (h *Handler) RemoveOpus(ctx context.Context, sessionID string) error {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if sessionInfo.Status != string(state.StatusPublished) {
		return fmt.Errorf("%w: status must be published, got %s", ErrNotPublished, sessionInfo.Status)
	}
	target := ParsePublishTarget(sessionInfo.PublishTarget)
	if target.DynID == "" {
		return fmt.Errorf("%w: only a published opus (with dyn_id) can be removed", ErrNotPublished)
	}
	ch, err := h.channels.Get(ctx, sessionInfo.ChannelID)
	if err != nil {
		return err
	}
	cookie, err := h.resolvePublishCookie(ctx, ch)
	if err != nil {
		return err
	}
	if err := h.client.RemoveOpus(ctx, cookie, target.DynID, target.DynType, target.DynRid); err != nil {
		return err
	}
	// 删除成功：状态 published→uploaded，清空 publish_target（保留 published_at 作历史）
	if _, err := h.states.ApplyRevertPublish(ctx, sessionID, ""); err != nil {
		return err
	}
	return nil
}

// EditOpus 编辑已发布专栏：用最新 recap markdown 重新发布一个新专栏，再删除旧专栏，
// 最后把 publish_target 更新为新专栏。状态保持 published（原地替换，不走状态机回退）。
// 顺序上「先发新再删旧」——若发新失败，旧专栏仍在；若删旧失败，仅记录 warn 不阻断（新专栏已生效）。
func (h *Handler) EditOpus(ctx context.Context, sessionID string) (PublishTarget, error) {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return PublishTarget{}, err
	}
	if sessionInfo.Status != string(state.StatusPublished) {
		return PublishTarget{}, fmt.Errorf("%w: status must be published, got %s", ErrNotPublished, sessionInfo.Status)
	}
	// LocalAvailable 守卫：归档 cleanup=all（或 upload cleanup=all）删除本地目录后，
	// EditOpus 需读本地 recap markdown，无守卫会报底层 os.ReadDir 错误。明确提示先 Fetch。
	if !sessionInfo.LocalAvailable {
		return PublishTarget{}, fmt.Errorf("%w: local files removed, fetch from webdav first", ErrRecapMissing)
	}
	ch, err := h.channels.Get(ctx, sessionInfo.ChannelID)
	if err != nil {
		return PublishTarget{}, err
	}
	cookie, err := h.resolvePublishCookie(ctx, ch)
	if err != nil {
		return PublishTarget{}, err
	}

	// 1. 先用最新 recap 发布新专栏（失败则旧专栏仍在，安全）
	newTarget, err := h.publishRecap(ctx, sessionInfo, ch, cookie, nil)
	if err != nil {
		return PublishTarget{}, err
	}

	// 2. 删除旧专栏（仅当新旧 dyn_id 不同且旧专栏确实已发布）
	oldTarget := ParsePublishTarget(sessionInfo.PublishTarget)
	if oldTarget.DynID != "" && oldTarget.DynID != newTarget.DynID {
		if rmErr := h.client.RemoveOpus(ctx, cookie, oldTarget.DynID, oldTarget.DynType, oldTarget.DynRid); rmErr != nil {
			// 新专栏已发布成功，旧专栏删除失败：新旧可能并存，仅记录告警不阻断
			slog.Warn("删除旧专栏失败，新旧专栏可能并存，请到 B 站手动清理",
				"channel_id", ch.ID, "session_id", sessionID, "old_dyn_id", oldTarget.DynID, "error", rmErr)
		}
	}

	// 3. 更新 publish_target 为新专栏，状态保持 published（无需状态机转换）
	if err := h.sessions.SetPublishTarget(ctx, sessionID, newTarget.Marshal()); err != nil {
		return PublishTarget{}, err
	}

	if h.notifyMgr != nil {
		h.notifyMgr.Send(ctx, notify.EventPublishDone, "专栏已更新",
			fmt.Sprintf("频道 %s 的专栏已用最新回顾重新发布", ch.ID))
	}
	return newTarget, nil
}

func canHandlePublish(status string) bool {
	return status == string(state.StatusRecapDone) || status == string(state.StatusUploaded)
}

func (h *Handler) resolvePublishCookie(ctx context.Context, ch channel.Channel) (*BiliCookie, error) {
	if h.cookieAccountStore != nil {
		cookie, err := h.cookieAccountStore.ResolveCookie(ctx, sql.NullInt64{}, sql.NullInt64{}, "publish", ch.CookieFile)
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

func (h *Handler) recapDir(sessionInfo session.Session) string {
	return filepath.Join(h.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug, "recap")
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

func findCoverImage(recapDir string) string {
	for _, name := range []string{"cover.png", "cover.jpg", "cover.jpeg"} {
		p := filepath.Join(recapDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
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
