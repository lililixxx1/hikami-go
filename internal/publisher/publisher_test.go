package publisher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/notify"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

func TestResolvePublishConfig(t *testing.T) {
	tests := []struct {
		name string
		ch   channel.Channel
		cfg  *config.PublishConfig
		want ResolvedPublishConfig
	}{
		{
			name: "channel overrides global",
			ch: channel.Channel{
				PublishMode:         "publish",
				PublishCategoryID:   42,
				PublishListID:       10,
				PublishPrivatePub:   1,
				PublishOriginal:     1,
				PublishAigc:         1,
				PublishTimerPubTime: 1700000000,
				PublishCoverURL:     "https://example.com/cover.png",
				PublishTopics:       "music,live",
			},
			cfg: &config.PublishConfig{Mode: "draft", CategoryID: 15, ListID: 0, PrivatePub: 2, Aigc: 0, CloseComment: 1, UpChooseComment: 1},
			want: ResolvedPublishConfig{
				Mode: "publish", CategoryID: 42, ListID: 10,
				PrivatePub: 1, Original: 1, Aigc: 1,
				TimerPubTime:    1700000000,
				CoverURL:        "https://example.com/cover.png",
				Topics:          "music,live",
				CloseComment:    1,
				UpChooseComment: 1,
			},
		},
		{
			name: "fallback to global defaults",
			ch:   channel.Channel{},
			cfg:  &config.PublishConfig{Mode: "draft", CategoryID: 15, ListID: 5, PrivatePub: 2, Original: 1, Aigc: 1, TimerPubTime: 1700000000, CoverURL: "https://example.com/global.png", Topics: "global", CloseComment: 1, UpChooseComment: 1},
			want: ResolvedPublishConfig{
				Mode: "draft", CategoryID: 15, ListID: 0,
				PrivatePub: 2, Original: 0, Aigc: 0, TimerPubTime: 1700000000,
				CoverURL:        "https://example.com/global.png",
				Topics:          "global",
				CloseComment:    1,
				UpChooseComment: 1,
			},
		},
		{
			name: "original -1 defaults to 0",
			ch:   channel.Channel{PublishOriginal: -1},
			cfg:  &config.PublishConfig{},
			want: ResolvedPublishConfig{Original: 0},
		},
		{
			name: "aigc -1 uses global",
			ch:   channel.Channel{PublishAigc: -1},
			cfg:  &config.PublishConfig{Aigc: 1},
			want: ResolvedPublishConfig{Aigc: 1},
		},
		{
			name: "list_id -1 uses global",
			ch:   channel.Channel{PublishListID: -1},
			cfg:  &config.PublishConfig{ListID: 99},
			want: ResolvedPublishConfig{ListID: 99},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePublishConfig(tt.ch, tt.cfg)
			if got != tt.want {
				t.Errorf("resolvePublishConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want string
	}{
		{"h1 found", "# 直播回顾\n正文", "直播回顾"},
		{"h1 with spaces", "#  Hello World  ", " Hello World"},
		{"h2 skipped", "## 子标题\n正文", ""},
		{"no heading", "just text\nmore text", ""},
		{"empty string", "", ""},
		{"h1 after other content", "text\n# Title", "Title"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTitle(tt.md)
			if got != tt.want {
				t.Errorf("extractTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractSummary(t *testing.T) {
	tests := []struct {
		name   string
		md     string
		maxLen int
		want   string
	}{
		{
			name:   "normal text",
			md:     "这是第一段内容。",
			maxLen: 100,
			want:   "这是第一段内容。",
		},
		{
			name:   "skip headings and special lines",
			md:     "# 标题\n> 引用\n- 列表\n正文内容",
			maxLen: 100,
			want:   "正文内容",
		},
		{
			name:   "long text stops after first line exceeds maxLen",
			md:     "短文本\n第二行",
			maxLen: 10,
			want:   "短文本",
		},
		{
			name:   "empty content",
			md:     "# Title\n## Sub\n> Quote",
			maxLen: 100,
			want:   "",
		},
		{
			name:   "skip code fences",
			md:     "```python\ncode\n```\nreal content",
			maxLen: 100,
			want:   "code real content",
		},
		{
			name:   "skip table lines",
			md:     "| a | b |\n|---|---|\nactual text",
			maxLen: 100,
			want:   "actual text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSummary(tt.md, tt.maxLen)
			if got != tt.want {
				t.Errorf("extractSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindRecapMarkdown(t *testing.T) {
	t.Run("finds latest md", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "recap1.md", "content 1")
		time.Sleep(10 * time.Millisecond)
		writeFile(t, dir, "recap2.md", "content 2")

		got, err := findRecapMarkdown(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Base(got) != "recap2.md" {
			t.Errorf("got %q, want recap2.md", got)
		}
	})

	t.Run("skips prompt md", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "recap.prompt.md", "prompt")
		writeFile(t, dir, "recap.md", "content")

		got, err := findRecapMarkdown(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Base(got) != "recap.md" {
			t.Errorf("got %q, want recap.md", got)
		}
	})

	t.Run("no md files returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "notes.txt", "not markdown")

		_, err := findRecapMarkdown(dir)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("nonexistent dir returns error", func(t *testing.T) {
		_, err := findRecapMarkdown("/nonexistent/path")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})
}

func TestFindCoverImage(t *testing.T) {
	tests := []struct {
		name     string
		createFn func(dir string)
		want     string
	}{
		{
			name:     "finds cover.png",
			createFn: func(dir string) { writeFile(t, dir, "cover.png", "png") },
			want:     "cover.png",
		},
		{
			name:     "finds cover.jpg",
			createFn: func(dir string) { writeFile(t, dir, "cover.jpg", "jpg") },
			want:     "cover.jpg",
		},
		{
			name:     "finds cover.jpeg",
			createFn: func(dir string) { writeFile(t, dir, "cover.jpeg", "jpeg") },
			want:     "cover.jpeg",
		},
		{
			name:     "png priority over jpg",
			createFn: func(dir string) { writeFile(t, dir, "cover.png", "p"); writeFile(t, dir, "cover.jpg", "j") },
			want:     "cover.png",
		},
		{
			name:     "no cover returns empty",
			createFn: func(dir string) { writeFile(t, dir, "other.png", "x") },
			want:     "",
		},
		{
			name:     "empty dir returns empty",
			createFn: func(dir string) {},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.createFn(dir)
			got := findCoverImage(dir)
			if tt.want == "" {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
			} else if filepath.Base(got) != tt.want {
				t.Errorf("got %q, want %q", filepath.Base(got), tt.want)
			}
		})
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// ==================== HandleTask 集成测试 ====================

// fakeOpusClient 实现 OpusClient 接口，记录调用并返回预设结果。
type fakeOpusClient struct {
	saveDraftFn    func(ctx context.Context, cookie *BiliCookie, req *DraftRequest) (string, error)
	publishOpusFn  func(ctx context.Context, cookie *BiliCookie, req *PublishRequest) (string, int64, string, error)
	deleteDraftFn  func(ctx context.Context, cookie *BiliCookie, draftID string) error
	lastDraftReq   *DraftRequest
	lastPublishReq *PublishRequest
}

func (f *fakeOpusClient) SaveDraft(ctx context.Context, cookie *BiliCookie, req *DraftRequest) (string, error) {
	f.lastDraftReq = req
	if f.saveDraftFn != nil {
		return f.saveDraftFn(ctx, cookie, req)
	}
	return "12345", nil
}

func (f *fakeOpusClient) PublishOpus(ctx context.Context, cookie *BiliCookie, req *PublishRequest) (string, int64, string, error) {
	f.lastPublishReq = req
	if f.publishOpusFn != nil {
		return f.publishOpusFn(ctx, cookie, req)
	}
	return "dyn_999", 0, "", nil
}

func (f *fakeOpusClient) DeleteDraft(ctx context.Context, cookie *BiliCookie, draftID string) error {
	if f.deleteDraftFn != nil {
		return f.deleteDraftFn(ctx, cookie, draftID)
	}
	return nil
}

// fakeCoverUploader 实现 OpusCoverUploader 接口。
type fakeCoverUploader struct {
	uploadFn   func(ctx context.Context, cookie *BiliCookie, imagePath string) (string, error)
	lastPath   string
	coverCalls int
}

func (f *fakeCoverUploader) UploadCover(ctx context.Context, cookie *BiliCookie, imagePath string) (string, error) {
	f.coverCalls++
	f.lastPath = imagePath
	if f.uploadFn != nil {
		return f.uploadFn(ctx, cookie, imagePath)
	}
	return "https://example.com/uploaded_cover.png", nil
}

// fakeOpusClientWithCover 同时实现 OpusClient 和 OpusCoverUploader。
type fakeOpusClientWithCover struct {
	fakeOpusClient
	fakeCoverUploader
}

// testHelper 封装 HandleTask 集成测试的公共依赖。
type testHelper struct {
	t          *testing.T
	db         *sql.DB
	tmpDir     string
	cfg        *config.Config
	sessions   *session.Store
	states     *state.Store
	channels   *channel.Store
	workerPool *worker.Pool
	hub        *worker.Hub
	taskStore  *worker.Store
}

func newTestHelper(t *testing.T) *testHelper {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := db.Migrate(database); err != nil {
		t.Fatalf("数据库迁移失败: %v", err)
	}

	outputRoot := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outputRoot, 0755); err != nil {
		t.Fatalf("创建 output 目录失败: %v", err)
	}

	cfg := &config.Config{
		OutputRoot: outputRoot,
		Publish: config.PublishConfig{
			Enabled:    true,
			Mode:       "draft",
			CategoryID: 15,
			SummaryLen: 100,
		},
	}

	sess := session.NewStore(database)
	st := state.NewStore(database)
	ch := channel.NewStore(database)
	ts := worker.NewStore(database)
	hub := worker.NewHub()
	pool := worker.NewPool(ts, hub, 1, cfg)

	return &testHelper{
		t:          t,
		db:         database,
		tmpDir:     tmpDir,
		cfg:        cfg,
		sessions:   sess,
		states:     st,
		channels:   ch,
		workerPool: pool,
		hub:        hub,
		taskStore:  ts,
	}
}

// setupSessionAndChannel 创建主播、场次和 Cookie 文件，推进状态到 recap_done。
func (h *testHelper) setupSessionAndChannel(ctx context.Context, cookieFile string, chOverrides ...func(*channel.UpsertInput)) (channel.Channel, session.Session) {
	h.t.Helper()

	chInput := channel.UpsertInput{
		ID:                "test_ch",
		Name:              "测试主播",
		UID:               12345,
		LiveRoomID:        67890,
		Enabled:           true,
		PublishEnabled:    true,
		CookieFile:        cookieFile,
		PublishMode:       "",
		PublishCategoryID: 0,
		PublishListID:     -1,
		PublishOriginal:   -1,
		PublishAigc:       -1,
	}
	for _, fn := range chOverrides {
		fn(&chInput)
	}

	ch, err := h.channels.Create(ctx, chInput)
	if err != nil {
		h.t.Fatalf("创建主播失败: %v", err)
	}

	sess, _, err := h.sessions.CreateDownload(ctx, session.CreateDownloadInput{
		ChannelID: ch.ID,
		SourceID:  "BV1test",
		Title:     "测试回放",
		SourceURL: "https://example.com/test",
		StartedAt: time.Now(),
	})
	if err != nil {
		h.t.Fatalf("创建场次失败: %v", err)
	}

	// 推进状态到 recap_done：
	// discovered -> downloading -> media_ready -> asr_submitted -> asr_done -> recap_done
	if _, err := h.states.Apply(ctx, sess.ID, state.EventDownloadStarted, "", ""); err != nil {
		h.t.Fatalf("状态转换 download_started 失败: %v", err)
	}
	if _, err := h.states.Apply(ctx, sess.ID, state.EventNormalizeSucceeded, "", ""); err != nil {
		h.t.Fatalf("状态转换 normalize_succeeded 失败: %v", err)
	}
	if _, err := h.states.Apply(ctx, sess.ID, state.EventASRSubmitted, "", ""); err != nil {
		h.t.Fatalf("状态转换 asr_submitted 失败: %v", err)
	}
	if _, err := h.states.Apply(ctx, sess.ID, state.EventASRSucceeded, "", ""); err != nil {
		h.t.Fatalf("状态转换 asr_succeeded 失败: %v", err)
	}
	if _, err := h.states.Apply(ctx, sess.ID, state.EventRecapSucceeded, "", ""); err != nil {
		h.t.Fatalf("状态转换 recap_succeeded 失败: %v", err)
	}

	return ch, sess
}

// createCookieFile 创建 Netscape 格式 Cookie 文件并返回路径。
func (h *testHelper) createCookieFile(suffix string) string {
	h.t.Helper()
	expiry := time.Now().Add(365 * 24 * time.Hour).Unix()
	content := fmt.Sprintf(
		"# Netscape HTTP Cookie File\n.bilibili.com\tTRUE\t/\tTRUE\t%d\tSESSDATA\tsess_val\n.bilibili.com\tTRUE\t/\tFALSE\t%d\tbili_jct\tcsrf_token\n.bilibili.com\tTRUE\t/\tFALSE\t%d\tDedeUserID\t42\n",
		expiry, expiry, expiry,
	)
	cookiePath := filepath.Join(h.tmpDir, "cookie"+suffix+".txt")
	if err := os.WriteFile(cookiePath, []byte(content), 0600); err != nil {
		h.t.Fatalf("写入 Cookie 文件失败: %v", err)
	}
	return cookiePath
}

// createRecapMarkdown 在场次的 recap 目录创建回顾 Markdown 文件。
func (h *testHelper) createRecapMarkdown(ch channel.Channel, sess session.Session, content string) {
	h.t.Helper()
	recapDir := filepath.Join(h.cfg.OutputRoot, ch.ID, sess.Slug, "recap")
	if err := os.MkdirAll(recapDir, 0755); err != nil {
		h.t.Fatalf("创建 recap 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(recapDir, "recap.md"), []byte(content), 0644); err != nil {
		h.t.Fatalf("写入 recap.md 失败: %v", err)
	}
}

// enqueueTask 创建并返回一个 publish 任务。
func (h *testHelper) enqueueTask(ctx context.Context, sess session.Session) worker.Task {
	h.t.Helper()
	task, err := h.taskStore.Create(ctx, worker.CreateInput{
		ChannelID: sess.ChannelID,
		SessionID: sess.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
	if err != nil {
		h.t.Fatalf("创建任务失败: %v", err)
	}
	return task
}

func TestCreateTask_Success(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 直播回顾\n\n内容。")

	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, &fakeOpusClient{})
	task, err := handler.CreateTask(ctx, h.workerPool, sess.ID)
	if err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}
	if task.Type != TaskType {
		t.Fatalf("task type = %q, want %q", task.Type, TaskType)
	}
}

func TestCreateTask_LocalUnavailable(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 直播回顾\n\n内容。")
	// 模拟上传 all 策略清理后：local_available=false（recap 目录即便存在也应被守卫拦截）
	if err := h.sessions.SetLocalAvailable(ctx, sess.ID, false); err != nil {
		t.Fatalf("set local_available false: %v", err)
	}

	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, &fakeOpusClient{})
	_, err := handler.CreateTask(ctx, h.workerPool, sess.ID)
	if err == nil {
		t.Fatalf("expected error when local files unavailable")
	}
	if !strings.Contains(err.Error(), ErrRecapMissing.Error()) {
		t.Fatalf("error = %v, want ErrRecapMissing", err)
	}
	if !strings.Contains(err.Error(), "fetch from webdav first") {
		t.Fatalf("error = %v, want hint to fetch from webdav", err)
	}
}

func TestHandleTask_Success(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 直播回顾\n\n这是回顾内容。")

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	if fake.lastDraftReq == nil {
		t.Fatal("未调用 SaveDraft")
	}
	if fake.lastDraftReq.Title != "直播回顾" {
		t.Errorf("DraftReq.Title = %q, want %q", fake.lastDraftReq.Title, "直播回顾")
	}
	if fake.lastDraftReq.CategoryID != 15 {
		t.Errorf("DraftReq.CategoryID = %d, want 15", fake.lastDraftReq.CategoryID)
	}

	updated, _ := h.sessions.Get(ctx, sess.ID)
	if got := ParsePublishTarget(updated.PublishTarget); got.DraftID != "12345" {
		t.Errorf("PublishTarget DraftID = %q, want %q (raw=%s)", got.DraftID, "12345", updated.PublishTarget)
	}
	if updated.Status != string(state.StatusPublished) {
		t.Errorf("Status = %q, want %q", updated.Status, state.StatusPublished)
	}
}

func TestHandleTask_NoRecapMarkdown(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)

	// 不创建 recap 目录或文件
	recapDir := filepath.Join(h.cfg.OutputRoot, ch.ID, sess.Slug, "recap")
	_ = recapDir // 目录不存在

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	err := handler.HandleTask(ctx, task, &noopReporter{})
	if err == nil {
		t.Fatal("期望返回错误，但得到了 nil")
	}
	t.Logf("预期错误: %v", err)
}

func TestHandleTask_NoCookie(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	ch, sess := h.setupSessionAndChannel(ctx, "") // 不设置 cookie_file
	h.createRecapMarkdown(ch, sess, "# 回顾\n内容")

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	err := handler.HandleTask(ctx, task, &noopReporter{})
	if err == nil {
		t.Fatal("期望返回错误，但得到了 nil")
	}
	if !errors.Is(err, ErrChannelNoCookieFile) {
		t.Errorf("错误类型 = %v, want ErrChannelNoCookieFile", err)
	}
}

func TestHandleTask_SaveDraftMode(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 草稿测试\n\n仅保存草稿。")

	fake := &fakeOpusClient{
		publishOpusFn: func(ctx context.Context, cookie *BiliCookie, req *PublishRequest) (string, int64, string, error) {
			t.Fatal("草稿模式不应调用 PublishOpus")
			return "", 0, "", nil
		},
	}

	// 全局配置为 draft 模式
	h.cfg.Publish.Mode = "draft"
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	if fake.lastPublishReq != nil {
		t.Error("草稿模式不应调用 PublishOpus")
	}

	updated, _ := h.sessions.Get(ctx, sess.ID)
	if got := ParsePublishTarget(updated.PublishTarget); got.DraftID != "12345" {
		t.Errorf("PublishTarget DraftID = %q, want %q (raw=%s)", got.DraftID, "12345", updated.PublishTarget)
	}
}

// TestHandleTask_PublishRequestTopic 验证全局配置的 topic_id/topic_name 被正确传递到发布请求。
// 经抓包确认(create/opus 真实请求):话题仅在发布时绑定,字段为 opus_req.topic={id,name};
// 草稿端无 topic 字段。故话题生效只看 PublishRequest.TopicID/TopicName。
func TestHandleTask_PublishRequestTopic(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath, func(input *channel.UpsertInput) {
		input.PublishMode = "publish"
	})
	h.createRecapMarkdown(ch, sess, "# 话题测试\n\n验证话题传入发布请求。")

	fake := &fakeOpusClient{}

	// 全局配置设置 topic_id + topic_name + publish 模式
	h.cfg.Publish.Mode = "publish"
	h.cfg.Publish.TopicID = 67890
	h.cfg.Publish.TopicName = "测试话题"
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	if fake.lastPublishReq == nil {
		t.Fatal("发布模式应调用 PublishOpus")
	}
	if fake.lastPublishReq.TopicID != 67890 {
		t.Errorf("publishReq.TopicID = %d, want 67890", fake.lastPublishReq.TopicID)
	}
	if fake.lastPublishReq.TopicName != "测试话题" {
		t.Errorf("publishReq.TopicName = %q, want %q", fake.lastPublishReq.TopicName, "测试话题")
	}
}

func TestHandleTask_PublishMode(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath, func(input *channel.UpsertInput) {
		input.PublishMode = "publish"
	})
	h.createRecapMarkdown(ch, sess, "# 发布测试\n\n直接发布。")

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	if fake.lastPublishReq == nil {
		t.Fatal("发布模式应调用 PublishOpus")
	}
	if fake.lastPublishReq.Title != "发布测试" {
		t.Errorf("PublishReq.Title = %q, want %q", fake.lastPublishReq.Title, "发布测试")
	}

	updated, _ := h.sessions.Get(ctx, sess.ID)
	if got := ParsePublishTarget(updated.PublishTarget); got.DynID != "dyn_999" {
		t.Errorf("PublishTarget DynID = %q, want %q (raw=%s)", got.DynID, "dyn_999", updated.PublishTarget)
	}
}

func TestHandleTask_PublishFail(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath, func(input *channel.UpsertInput) {
		input.PublishMode = "publish"
	})
	h.createRecapMarkdown(ch, sess, "# 失败测试\n\n发布会失败。")

	fake := &fakeOpusClient{
		publishOpusFn: func(ctx context.Context, cookie *BiliCookie, req *PublishRequest) (string, int64, string, error) {
			return "", 0, "", ErrContentRejected
		},
	}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	err := handler.HandleTask(ctx, task, &noopReporter{})
	if err == nil {
		t.Fatal("期望发布失败返回错误")
	}
	if !errors.Is(err, ErrContentRejected) {
		t.Errorf("错误类型 = %v, want ErrContentRejected", err)
	}
}

func TestHandleTask_SaveDraftFail(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 草稿失败测试\n\n保存草稿会失败。")

	fake := &fakeOpusClient{
		saveDraftFn: func(ctx context.Context, cookie *BiliCookie, req *DraftRequest) (string, error) {
			return "", ErrCookieExpired
		},
	}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	err := handler.HandleTask(ctx, task, &noopReporter{})
	if err == nil {
		t.Fatal("期望草稿保存失败返回错误")
	}
	if !errors.Is(err, ErrCookieExpired) {
		t.Errorf("错误类型 = %v, want ErrCookieExpired", err)
	}
}

func TestHandleTask_ContextCancelled(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 取消测试\n\ncontext 被取消。")

	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel() // 立即取消

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	err := handler.HandleTask(cancelledCtx, task, &noopReporter{})
	if err == nil {
		t.Fatal("期望取消 context 返回错误")
	}
	t.Logf("取消 context 错误: %v", err)
}

func TestHandleTask_CoverImage(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath, func(input *channel.UpsertInput) {
		input.PublishMode = "publish"
	})

	// 创建 recap 目录并放置封面图和 Markdown
	recapDir := filepath.Join(h.cfg.OutputRoot, ch.ID, sess.Slug, "recap")
	if err := os.MkdirAll(recapDir, 0755); err != nil {
		t.Fatalf("创建 recap 目录失败: %v", err)
	}
	os.WriteFile(filepath.Join(recapDir, "recap.md"), []byte("# 封面测试\n内容"), 0644)
	os.WriteFile(filepath.Join(recapDir, "cover.png"), []byte("fake png"), 0644)

	fake := &fakeOpusClientWithCover{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	if fake.coverCalls == 0 {
		t.Error("期望调用 UploadCover")
	}
	// 封面 URL 应同时流入草稿端(arg.image_urls)和发布端(opus_req.opus.article.cover)
	wantCover := "https://example.com/uploaded_cover.png"
	if fake.lastDraftReq == nil || fake.lastDraftReq.CoverURL != wantCover {
		got := ""
		if fake.lastDraftReq != nil {
			got = fake.lastDraftReq.CoverURL
		}
		t.Errorf("draftReq.CoverURL = %q, want %q", got, wantCover)
	}
	if fake.lastPublishReq == nil || fake.lastPublishReq.CoverURL != wantCover {
		got := ""
		if fake.lastPublishReq != nil {
			got = fake.lastPublishReq.CoverURL
		}
		t.Errorf("publishReq.CoverURL = %q, want %q", got, wantCover)
	}
}

// TestHandleTask_CoverURLLocalPath 验证配置项 cover_url 填本地文件路径时会被上传。
// 回归：此前 cover_url 无论是 URL 还是本地路径，都被直接当 URL 塞进 B 站请求，
// 导致配置 /home/x/cover.png 这类本地路径时封面失效（B 站访问不到本地文件）。
// 修复后：非 http(s):// 前缀的 cover_url 视为本地路径，走 UploadCover 换取真实 URL。
func TestHandleTask_CoverURLLocalPath(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath, func(input *channel.UpsertInput) {
		input.PublishMode = "publish"
	})

	// recap 目录只放 markdown，不放 cover 文件——确保走的是 cover_url 配置路径，而非 recap 自动发现。
	recapDir := filepath.Join(h.cfg.OutputRoot, ch.ID, sess.Slug, "recap")
	if err := os.MkdirAll(recapDir, 0755); err != nil {
		t.Fatalf("创建 recap 目录失败: %v", err)
	}
	os.WriteFile(filepath.Join(recapDir, "recap.md"), []byte("# 封面配置测试\n内容"), 0644)

	// 准备一个真实的本地封面文件，路径写入全局 config.cover_url。
	coverFile := filepath.Join(t.TempDir(), "Hikami-1.png")
	if err := os.WriteFile(coverFile, []byte("fake png"), 0644); err != nil {
		t.Fatalf("写入本地封面文件失败: %v", err)
	}
	h.cfg.Publish.CoverURL = coverFile

	fake := &fakeOpusClientWithCover{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	// 应触发封面上传，且传入的 path 是配置的本地路径。
	if fake.coverCalls == 0 {
		t.Fatal("期望对本地封面路径调用 UploadCover，实际未调用")
	}
	if fake.lastPath != coverFile {
		t.Errorf("UploadCover 入参 path = %q, want %q", fake.lastPath, coverFile)
	}

	// 上传换回的真实 URL 应同时流入草稿端和发布端。
	wantCover := "https://example.com/uploaded_cover.png"
	if fake.lastDraftReq == nil || fake.lastDraftReq.CoverURL != wantCover {
		got := ""
		if fake.lastDraftReq != nil {
			got = fake.lastDraftReq.CoverURL
		}
		t.Errorf("draftReq.CoverURL = %q, want %q（本地路径不应残留）", got, wantCover)
	}
	if fake.lastPublishReq == nil || fake.lastPublishReq.CoverURL != wantCover {
		got := ""
		if fake.lastPublishReq != nil {
			got = fake.lastPublishReq.CoverURL
		}
		t.Errorf("publishReq.CoverURL = %q, want %q", got, wantCover)
	}
}

// TestHandleTask_ConfigCoverOverridesRecap 验证配置项 cover_url 优先级最高：
// 同时存在配置 cover_url 与 recap/cover.* 时，应只采用配置来源，不再上传 recap 封面。
// 修复前优先级为 recap > 配置，导致用户填的自定义封面被官方/回顾封面覆盖。
func TestHandleTask_ConfigCoverOverridesRecap(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath, func(input *channel.UpsertInput) {
		input.PublishMode = "publish"
	})

	// recap 目录放 cover.png，同时配置一个本地路径的 cover_url
	recapDir := filepath.Join(h.cfg.OutputRoot, ch.ID, sess.Slug, "recap")
	if err := os.MkdirAll(recapDir, 0755); err != nil {
		t.Fatalf("创建 recap 目录失败: %v", err)
	}
	os.WriteFile(filepath.Join(recapDir, "recap.md"), []byte("# 优先级测试\n内容"), 0644)
	os.WriteFile(filepath.Join(recapDir, "cover.png"), []byte("recap png"), 0644)

	configCover := filepath.Join(t.TempDir(), "config_cover.png")
	os.WriteFile(configCover, []byte("config png"), 0644)
	h.cfg.Publish.CoverURL = configCover

	fake := &fakeOpusClientWithCover{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	// 应只上传一次（配置的本地路径），recap 的 cover.png 不应被多余上传
	if fake.coverCalls != 1 {
		t.Errorf("应只上传配置 cover 1 次，got %d 次", fake.coverCalls)
	}
	if fake.lastPath != configCover {
		t.Errorf("UploadCover 入参 path = %q, want %q（应上传配置路径而非 recap 封面）", fake.lastPath, configCover)
	}
	wantCover := "https://example.com/uploaded_cover.png"
	if fake.lastDraftReq == nil || fake.lastDraftReq.CoverURL != wantCover {
		got := ""
		if fake.lastDraftReq != nil {
			got = fake.lastDraftReq.CoverURL
		}
		t.Errorf("draftReq.CoverURL = %q, want %q", got, wantCover)
	}
}

// TestHandleTask_ConfigCoverLocalUploadFailsFallsBackToRecap 验证：配置 cover_url 为本地路径，
// 但上传失败时（resolveCoverUpload 返回空），应回退到下一优先级 recap/cover.*，而非无封面发布。
func TestHandleTask_ConfigCoverLocalUploadFailsFallsBackToRecap(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath, func(input *channel.UpsertInput) {
		input.PublishMode = "publish"
	})

	recapDir := filepath.Join(h.cfg.OutputRoot, ch.ID, sess.Slug, "recap")
	if err := os.MkdirAll(recapDir, 0755); err != nil {
		t.Fatalf("创建 recap 目录失败: %v", err)
	}
	os.WriteFile(filepath.Join(recapDir, "recap.md"), []byte("# 回退测试\n内容"), 0644)
	os.WriteFile(filepath.Join(recapDir, "cover.png"), []byte("recap png"), 0644)

	// 配置一个本地路径 cover_url；让配置路径上传失败、recap cover 上传成功 → 验证回退到 recap。
	configCover := filepath.Join(t.TempDir(), "config_cover.png")
	os.WriteFile(configCover, []byte("config png"), 0644)
	h.cfg.Publish.CoverURL = configCover
	recapCover := filepath.Join(recapDir, "cover.png")

	fake := &fakeOpusClientWithCover{}
	var paths []string
	fake.uploadFn = func(_ context.Context, _ *BiliCookie, imagePath string) (string, error) {
		paths = append(paths, imagePath)
		// 按路径（而非序号）控制失败，避免实现误改上传顺序后测试仍通过
		if imagePath == configCover {
			return "", errors.New("upload failed")
		}
		return "https://example.com/uploaded_cover.png", nil
	}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	// 应上传两次，且顺序必须是：先配置路径（失败），再 recap cover（成功回退）
	if fake.coverCalls != 2 {
		t.Errorf("配置上传失败后应回退上传 recap cover，期望 2 次上传，got %d", fake.coverCalls)
	}
	if len(paths) != 2 || paths[0] != configCover || paths[1] != recapCover {
		t.Errorf("上传顺序/路径不对，got %v, want [%q, %q]", paths, configCover, recapCover)
	}
	wantCover := "https://example.com/uploaded_cover.png"
	if fake.lastDraftReq == nil || fake.lastDraftReq.CoverURL != wantCover {
		got := ""
		if fake.lastDraftReq != nil {
			got = fake.lastDraftReq.CoverURL
		}
		t.Errorf("回退后 draftReq.CoverURL = %q, want %q", got, wantCover)
	}
}
func TestResolveCoverUpload(t *testing.T) {
	cookie := &BiliCookie{DedeUserID: "1", BiliJct: "x", SESSDATA: "y"}

	t.Run("empty", func(t *testing.T) {
		h := &Handler{}
		if got := h.resolveCoverUpload(context.Background(), cookie, "  "); got != "" {
			t.Errorf("空值应返回空串，got %q", got)
		}
	})

	t.Run("http_url_unchanged", func(t *testing.T) {
		h := &Handler{} // 不实现 OpusCoverUploader，http URL 不应触发上传
		url := "https://i0.hdslb.com/bfs/archive/cover.png"
		if got := h.resolveCoverUpload(context.Background(), cookie, url); got != url {
			t.Errorf("http URL 应原样返回，got %q", got)
		}
	})

	t.Run("uppercase_scheme_url", func(t *testing.T) {
		h := &Handler{} // HTTPS:// 是合法网络 URL，不应被误判为本地路径
		for _, url := range []string{"HTTPS://i0.hdslb.com/a.png", "HTTP://example.com/a.png"} {
			if got := h.resolveCoverUpload(context.Background(), cookie, url); got != url {
				t.Errorf("大写 scheme URL %q 应原样返回，got %q", url, got)
			}
		}
		if h.resolveCoverUpload(context.Background(), cookie, "  ") != "" {
			t.Error("trimmed 后空串应返回空")
		}
	})

	t.Run("protocol_relative_url", func(t *testing.T) {
		h := &Handler{}
		// //i0.hdslb.com 协议相对 URL 应规范化为 https:// 并原样使用，不当本地路径
		got := h.resolveCoverUpload(context.Background(), cookie, "//i0.hdslb.com/bfs/a.png")
		if want := "https://i0.hdslb.com/bfs/a.png"; got != want {
			t.Errorf("协议相对 URL 应规范化为 %q，got %q", want, got)
		}
	})

	t.Run("local_path_uploaded", func(t *testing.T) {
		fake := &fakeOpusClientWithCover{}
		h := &Handler{client: fake}
		got := h.resolveCoverUpload(context.Background(), cookie, "/tmp/cover.png")
		if fake.coverCalls != 1 {
			t.Errorf("本地路径应触发上传 1 次，got %d", fake.coverCalls)
		}
		if fake.lastPath != "/tmp/cover.png" {
			t.Errorf("上传 path = %q, want /tmp/cover.png", fake.lastPath)
		}
		if want := "https://example.com/uploaded_cover.png"; got != want {
			t.Errorf("返回值 = %q, want %q", got, want)
		}
	})

	t.Run("local_path_upload_fail_returns_empty", func(t *testing.T) {
		fake := &fakeOpusClientWithCover{}
		fake.uploadFn = func(_ context.Context, _ *BiliCookie, _ string) (string, error) {
			return "", errors.New("network error")
		}
		h := &Handler{client: fake}
		got := h.resolveCoverUpload(context.Background(), cookie, "/tmp/cover.png")
		if got != "" {
			t.Errorf("上传失败应返回空串（避免本地路径残留），got %q", got)
		}
	})

	t.Run("local_path_no_uploader_returns_empty", func(t *testing.T) {
		// client 只实现 OpusClient，不实现 OpusCoverUploader —— 断言失败应静默置空。
		h := &Handler{client: &fakeOpusClient{}}
		got := h.resolveCoverUpload(context.Background(), cookie, "/tmp/cover.png")
		if got != "" {
			t.Errorf("client 不支持封面上传时应返回空串，got %q", got)
		}
	})
}

func TestHandleTask_NoTitleFallback(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)

	// Markdown 中没有 h1 标题
	h.createRecapMarkdown(ch, sess, "没有标题的回顾内容。")

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	// 标题应 fallback 到 session title
	if fake.lastDraftReq.Title != "测试回放" {
		t.Errorf("Title = %q, want %q (session title)", fake.lastDraftReq.Title, "测试回放")
	}
}

func TestResolvePublishConfig_ChannelOverride(t *testing.T) {
	ch := channel.Channel{
		PublishMode:         "publish",
		PublishCategoryID:   42,
		PublishListID:       10,
		PublishPrivatePub:   1,
		PublishOriginal:     1,
		PublishAigc:         1,
		PublishTimerPubTime: 1700000000,
		PublishCoverURL:     "https://example.com/cover.png",
		PublishTopics:       "music,live",
	}
	cfg := &config.PublishConfig{Mode: "draft", CategoryID: 15, ListID: 5, PrivatePub: 2, Aigc: 0, CoverURL: "https://example.com/global.png", Topics: "global", CloseComment: 1, UpChooseComment: 1}

	got := resolvePublishConfig(ch, cfg)

	if got.Mode != "publish" {
		t.Errorf("Mode = %q, want %q", got.Mode, "publish")
	}
	if got.CategoryID != 42 {
		t.Errorf("CategoryID = %d, want 42", got.CategoryID)
	}
	if got.ListID != 10 {
		t.Errorf("ListID = %d, want 10", got.ListID)
	}
	if got.Aigc != 1 {
		t.Errorf("Aigc = %d, want 1", got.Aigc)
	}
	if got.CoverURL != "https://example.com/cover.png" {
		t.Errorf("CoverURL = %q, want cover URL", got.CoverURL)
	}
	if got.Topics != "music,live" {
		t.Errorf("Topics = %q, want %q", got.Topics, "music,live")
	}
}

func TestResolvePublishConfig_Fallback(t *testing.T) {
	// 主播级字段使用 DB 默认的"跟随全局"标记值，应回退到全局配置
	ch := channel.Channel{
		PublishListID:   -1, // -1 表示跟随全局
		PublishOriginal: -1,
		PublishAigc:     -1, // -1 表示跟随全局
	}
	cfg := &config.PublishConfig{
		Mode:            "draft",
		CategoryID:      15,
		ListID:          5,
		PrivatePub:      2,
		Original:        1,
		Aigc:            1,
		TimerPubTime:    1700000000,
		CoverURL:        "https://example.com/global.png",
		Topics:          "global",
		CloseComment:    1,
		UpChooseComment: 1,
	}

	got := resolvePublishConfig(ch, cfg)

	if got.Mode != "draft" {
		t.Errorf("Mode = %q, want %q", got.Mode, "draft")
	}
	if got.CategoryID != 15 {
		t.Errorf("CategoryID = %d, want 15", got.CategoryID)
	}
	if got.ListID != 5 {
		t.Errorf("ListID = %d, want 5", got.ListID)
	}
	if got.PrivatePub != 2 {
		t.Errorf("PrivatePub = %d, want 2", got.PrivatePub)
	}
	if got.Original != 1 {
		t.Errorf("Original = %d, want 1", got.Original)
	}
	if got.Aigc != 1 {
		t.Errorf("Aigc = %d, want 1", got.Aigc)
	}
	if got.TimerPubTime != 1700000000 {
		t.Errorf("TimerPubTime = %d, want 1700000000", got.TimerPubTime)
	}
	if got.CoverURL != "https://example.com/global.png" {
		t.Errorf("CoverURL = %q, want global cover", got.CoverURL)
	}
	if got.Topics != "global" {
		t.Errorf("Topics = %q, want global", got.Topics)
	}
	if got.CloseComment != 1 {
		t.Errorf("CloseComment = %d, want 1", got.CloseComment)
	}
	if got.UpChooseComment != 1 {
		t.Errorf("UpChooseComment = %d, want 1", got.UpChooseComment)
	}
}

func TestResolvePublishConfig_OriginalMinusOne(t *testing.T) {
	ch := channel.Channel{PublishOriginal: -1}
	cfg := &config.PublishConfig{}

	got := resolvePublishConfig(ch, cfg)
	if got.Original != 0 {
		t.Errorf("Original = %d, want 0 (fallback from -1)", got.Original)
	}
}

// noopReporter 是一个不做任何操作的 Reporter，用于测试。
type noopReporter struct{}

func (n *noopReporter) Progress(ctx context.Context, progress int, message string) error {
	return nil
}

func TestResolvePublishConfig_TopicID(t *testing.T) {
	t.Run("global topic_id passes through", func(t *testing.T) {
		cfg := &config.PublishConfig{
			Mode:            "draft",
			CategoryID:      15,
			TopicID:         12345,
			TopicName:       "测试话题",
			CloseComment:    1,
			UpChooseComment: 1,
		}
		got := resolvePublishConfig(channel.Channel{}, cfg)
		if got.TopicID != 12345 {
			t.Errorf("TopicID = %d, want 12345", got.TopicID)
		}
		if got.TopicName != "测试话题" {
			t.Errorf("TopicName = %q, want %q", got.TopicName, "测试话题")
		}
	})

	t.Run("zero topic_id is valid", func(t *testing.T) {
		cfg := &config.PublishConfig{
			Mode:       "draft",
			CategoryID: 15,
		}
		got := resolvePublishConfig(channel.Channel{}, cfg)
		if got.TopicID != 0 {
			t.Errorf("TopicID = %d, want 0", got.TopicID)
		}
		if got.TopicName != "" {
			t.Errorf("TopicName = %q, want empty", got.TopicName)
		}
	})
}

// ==================== HandleTask 补充测试 ====================

func TestHandleTask_CookieAccountStore(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	// 创建 cookie 文件（用于 CookieAccountStore 的 cookie_file 字段）
	cookiePath := h.createCookieFile("_account")
	// 主播不设 cookie_file，走 CookieAccountStore 路径
	ch, sess := h.setupSessionAndChannel(ctx, "")
	h.createRecapMarkdown(ch, sess, "# 直播回顾\n\nCookieAccountStore 测试。")

	// 创建 CookieAccountStore 并添加默认发布账号
	accountStore := biliutil.NewCookieAccountStore(h.db, h.tmpDir)
	account, err := accountStore.CreateImported(ctx, &biliutil.CookieAccount{
		UID:               42,
		Nickname:          "测试账号",
		CookieFile:        "",
		IsDefaultDownload: false,
		IsDefaultPublish:  true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}

	// 更新账号的 cookie_file 指向已创建的 cookie 文件
	_, _ = h.db.ExecContext(ctx,
		"UPDATE bili_cookie_accounts SET cookie_file = ? WHERE id = ?",
		cookiePath, account)

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.SetCookieAccountStore(accountStore)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	if fake.lastDraftReq == nil {
		t.Fatal("未调用 SaveDraft")
	}
	if fake.lastDraftReq.Title != "直播回顾" {
		t.Errorf("Title = %q, want %q", fake.lastDraftReq.Title, "直播回顾")
	}

	// 验证状态和 publish target
	updated, _ := h.sessions.Get(ctx, sess.ID)
	if updated.Status != string(state.StatusPublished) {
		t.Errorf("Status = %q, want %q", updated.Status, state.StatusPublished)
	}
}

func TestHandleTask_NotifyManager(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 通知测试\n\n内容。")

	// captureNotifier 实现 notify.Notifier，通过 channel 同步异步调用
	notifyCh := make(chan struct {
		title string
		body  string
	}, 1)
	fakeNotifier := &captureNotifier{ch: notifyCh}
	mgr := notify.NewManager(fakeNotifier, []string{notify.EventPublishDone})

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.SetNotifyManager(mgr)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	// Manager.Send 是异步的，等待通知到达
	select {
	case sent := <-notifyCh:
		if sent.title == "" {
			t.Fatal("通知 title 为空")
		}
		if !strings.Contains(sent.body, ch.ID) {
			t.Errorf("通知 body = %q, 应包含 channel ID %q", sent.body, ch.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：未收到通知")
	}
}

// captureNotifier 捕获 Send 调用并通过 channel 传递
type captureNotifier struct {
	ch chan struct {
		title string
		body  string
	}
}

func (n *captureNotifier) Send(_ context.Context, title, body string) error {
	n.ch <- struct {
		title string
		body  string
	}{title: title, body: body}
	return nil
}

func TestHandleTask_InvalidStatus(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, _ := h.setupSessionAndChannel(ctx, cookiePath)

	// 创建新场次只推进到 media_ready
	sess2, _, err := h.sessions.CreateDownload(ctx, session.CreateDownloadInput{
		ChannelID: ch.ID,
		SourceID:  "BV1media",
		Title:     "未完成场次",
		SourceURL: "https://example.com/test2",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("创建场次失败: %v", err)
	}
	if _, err := h.states.Apply(ctx, sess2.ID, state.EventDownloadStarted, "", ""); err != nil {
		t.Fatalf("状态转换失败: %v", err)
	}
	if _, err := h.states.Apply(ctx, sess2.ID, state.EventNormalizeSucceeded, "", ""); err != nil {
		t.Fatalf("状态转换失败: %v", err)
	}

	h.createRecapMarkdown(ch, sess2, "# 内容")

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	task := h.enqueueTask(ctx, sess2)
	err = handler.HandleTask(ctx, task, &noopReporter{})
	if err == nil {
		t.Fatal("期望返回错误，但得到了 nil")
	}
	if !strings.Contains(err.Error(), "session state") && !strings.Contains(err.Error(), "not valid") {
		t.Errorf("错误 = %q, 应包含 session state 相关信息", err.Error())
	}

	if fake.lastDraftReq != nil {
		t.Fatal("不应调用 SaveDraft")
	}

	updated, _ := h.sessions.Get(ctx, sess2.ID)
	if updated.Status != string(state.StatusMediaReady) {
		t.Errorf("Status = %q, 期望保持 %q", updated.Status, state.StatusMediaReady)
	}
}

func TestHandleTask_UploadedStatus(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 已上传状态测试\n\n从 uploaded 状态发布。")

	// 推进到 uploaded 状态
	if _, err := h.states.Apply(ctx, sess.ID, state.EventUploadSucceeded, "", ""); err != nil {
		t.Fatalf("状态转换 upload_succeeded 失败: %v", err)
	}

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}

	if fake.lastDraftReq == nil {
		t.Fatal("未调用 SaveDraft")
	}

	updated, _ := h.sessions.Get(ctx, sess.ID)
	if updated.Status != string(state.StatusPublished) {
		t.Errorf("Status = %q, want %q", updated.Status, state.StatusPublished)
	}
	if got := ParsePublishTarget(updated.PublishTarget); got.DraftID != "12345" {
		t.Errorf("PublishTarget DraftID = %q, want %q (raw=%s)", got.DraftID, "12345", updated.PublishTarget)
	}
}

// TestHandleTask_TriggersOnSuccess 验证发布成功后触发 onSuccess 回调（自动归档链路入口）。
func TestHandleTask_TriggersOnSuccess(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 回顾\n内容")

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)

	called := false
	var gotTask worker.Task
	handler.SetOnSuccess(func(ctx context.Context, task worker.Task) {
		called = true
		gotTask = task
	})

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}
	if !called {
		t.Fatal("onSuccess 回调未被触发")
	}
	if gotTask.SessionID != sess.ID {
		t.Errorf("回调 task.SessionID = %q, want %q", gotTask.SessionID, sess.ID)
	}
}

// TestHandleTask_OnSuccessNilDoesNotPanic 验证未注册回调时不 panic。
func TestHandleTask_OnSuccessNilDoesNotPanic(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	ch, sess := h.setupSessionAndChannel(ctx, cookiePath)
	h.createRecapMarkdown(ch, sess, "# 回顾\n内容")

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	// 不调 SetOnSuccess（onSuccess 为 nil）

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask 失败: %v", err)
	}
}

// TestHandleTask_FailureDoesNotTriggerOnSuccess 验证发布失败路径不触发回调。
func TestHandleTask_FailureDoesNotTriggerOnSuccess(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	cookiePath := h.createCookieFile("")
	_, sess := h.setupSessionAndChannel(ctx, cookiePath)
	// 不创建 recap markdown → publishRecap 失败

	fake := &fakeOpusClient{}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	called := false
	handler.SetOnSuccess(func(ctx context.Context, task worker.Task) { called = true })

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err == nil {
		t.Fatal("期望 publishRecap 失败返回错误")
	}
	if called {
		t.Error("失败路径不应触发 onSuccess 回调")
	}
}

// TestHandleTask_PublishAccountIDWinsOverLegacy:
// 验证 channel.PublishAccountID 生效后,ResolveCookie 优先用账号 cookie 而非 legacy cookie_file。
// 主播级发布账号(本次新增字段)是 ResolveCookie 三级链 level 1,
// 此前 publisher.go:382 永远传 sql.NullInt64{},level 1 永远跳过。
//
// codex r18 MEDIUM 修订:legacy 和 account cookie 的 SESSDATA/DedeUserID 必须不同,
// 否则即使实现回退到 legacy 断言也会通过(原 writeTestCookieFileAt 对两者都写 DedeUserID=99999)。
func TestHandleTask_PublishAccountIDWinsOverLegacy(t *testing.T) {
	h := newTestHelper(t)
	ctx := context.Background()

	// legacy cookie_file(SESSDATA=legacy-sess, DedeUserID=legacy-uid)
	legacyPath := filepath.Join(h.tmpDir, "legacy.txt")
	writeTestCookieFileAt(t, legacyPath, "legacy-sess", "legacy-uid")

	// 账号 cookie(SESSDATA=account-sess, DedeUserID=account-uid),非默认发布账号(is_default_publish=0)
	cookieDir := filepath.Join(h.tmpDir, "cookies")
	if err := os.MkdirAll(cookieDir, 0755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	accountPath := filepath.Join(cookieDir, "account.txt")
	writeTestCookieFileAt(t, accountPath, "account-sess", "account-uid")

	// 裸 SQL 插入账号(绕过 ValidateCookiePath 限制)
	var accountID int64
	err := h.db.QueryRowContext(ctx, `
		INSERT INTO bili_cookie_accounts (uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at)
		VALUES (?, ?, ?, 0, 0, ?, ?)
		RETURNING id`,
		99999, "test-account", accountPath, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339),
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	// 设 channel:PublishAccountID 指向 account + CookieFile 指向 legacy(应被 account 覆盖)
	publishAccountID := accountID
	ch, sess := h.setupSessionAndChannel(ctx, legacyPath, func(input *channel.UpsertInput) {
		input.PublishAccountID = &publishAccountID
	})
	h.createRecapMarkdown(ch, sess, "# 测试\n内容")

	// 注入 cookieAccountStore
	store := biliutil.NewCookieAccountStore(h.db, cookieDir)

	// 用 saveDraftFn 闭包捕获收到的 cookie(同时记 SESSDATA + DedeUserID,
	// 即使一个字段没区分另一个也能抓到回归)
	var gotSESSDATA, gotDedeUserID string
	fake := &fakeOpusClient{
		saveDraftFn: func(_ context.Context, cookie *BiliCookie, _ *DraftRequest) (string, error) {
			gotSESSDATA = cookie.SESSDATA
			gotDedeUserID = cookie.DedeUserID
			return "12345", nil
		},
	}
	handler := NewHandler(h.cfg, h.sessions, h.states, h.channels, fake)
	handler.SetCookieAccountStore(store)
	handler.Register(h.workerPool)

	task := h.enqueueTask(ctx, sess)
	if err := handler.HandleTask(ctx, task, &noopReporter{}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	// 断言:用的是 account cookie(SESSDATA=account-sess, DedeUserID=account-uid)
	if gotSESSDATA != "account-sess" {
		t.Errorf("cookie.SESSDATA = %q, want %q (账号 cookie 应优先于 legacy cookie_file)", gotSESSDATA, "account-sess")
	}
	if gotDedeUserID != "account-uid" {
		t.Errorf("cookie.DedeUserID = %q, want %q (账号 cookie 应优先于 legacy cookie_file)", gotDedeUserID, "account-uid")
	}
}

// writeTestCookieFileAt 在指定路径写明文 Netscape cookie 文件(测试 helper)。
// codex r18 MEDIUM:SESSDATA + DedeUserID 都按传入参数写入,便于断言区分两个 cookie。
func writeTestCookieFileAt(t *testing.T, path, sessdata, dedeUserID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "# Netscape HTTP Cookie File\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tSESSDATA\t" + sessdata + "\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tbili_jct\tcsrf-" + sessdata + "\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tDedeUserID\t" + dedeUserID + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
}
