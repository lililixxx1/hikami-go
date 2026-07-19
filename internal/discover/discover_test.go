package discover

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"hikami-go/internal/channel"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
	"hikami-go/internal/worker"
)

func TestDiscoverAllCreatesDownloadTasksForNewReplaySessions(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		fakeLister{},
	)

	results, err := manager.DiscoverAll(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result count = %d, want 1: %+v", len(results), results)
	}
	if !results[0].Created || results[0].TaskID == "" {
		t.Fatalf("unexpected result: %+v", results[0])
	}

	tasks, err := pool.Store().List(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Type != "download" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
}

func TestDiscoverAllSkipsExistingReplaySessions(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeLister{})

	if _, err := manager.DiscoverAll(context.Background()); err != nil {
		t.Fatalf("first discover: %v", err)
	}
	results, err := manager.DiscoverAll(context.Background())
	if err != nil {
		t.Fatalf("second discover: %v", err)
	}
	if len(results) != 1 || results[0].Created {
		t.Fatalf("unexpected second result: %+v", results)
	}

	tasks, err := pool.Store().List(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
}

func newDiscoverTestDB(t *testing.T) *sql.DB {
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
	_, err = database.Exec(`
		INSERT INTO channels(id, name, uid, replay_source_url, title_prefix, enabled)
		VALUES ('huize', 'Hikami', 1, 'https://space.bilibili.com/1/video', '【直播回放】', 1);
	`)
	if err != nil {
		t.Fatalf("seed database: %v", err)
	}
	return database
}

type fakeLister struct{}

func (fakeLister) List(ctx context.Context, sourceURL string, cookieFile string) ([]Entry, error) {
	return []Entry{
		{ID: "BV1", Title: "【直播回放】测试", WebpageURL: "https://www.bilibili.com/video/BV1"},
		{ID: "BV2", Title: "普通投稿", WebpageURL: "https://www.bilibili.com/video/BV2"},
	}, nil
}

func TestDiscoverAllSkipsLiveOnlyChannels(t *testing.T) {
	database := newDiscoverTestDB(t)
	// Update the channel to live_only source mode
	if _, err := database.Exec(`UPDATE channels SET source_mode = 'live_only' WHERE id = 'huize'`); err != nil {
		t.Fatalf("update source_mode: %v", err)
	}
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		fakeLister{},
	)

	results, err := manager.DiscoverAll(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for live_only channel, got %d: %+v", len(results), results)
	}
}

type fakeListerMany struct{}

func (fakeListerMany) List(ctx context.Context, sourceURL string, cookieFile string) ([]Entry, error) {
	return []Entry{
		{ID: "BV1", Title: "【直播回放】测试1", WebpageURL: "https://www.bilibili.com/video/BV1"},
		{ID: "BV2", Title: "【直播回放】测试2", WebpageURL: "https://www.bilibili.com/video/BV2"},
		{ID: "BV3", Title: "【直播回放】测试3", WebpageURL: "https://www.bilibili.com/video/BV3"},
		{ID: "BV4", Title: "普通投稿", WebpageURL: "https://www.bilibili.com/video/BV4"},
		{ID: "BV5", Title: "【直播回放】测试5", WebpageURL: "https://www.bilibili.com/video/BV5"},
	}, nil
}

func newDiscoverTestDBWithLimit(t *testing.T, limit int) *sql.DB {
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
	_, err = database.Exec(`
		INSERT INTO channels(id, name, uid, replay_source_url, title_prefix, enabled, discover_limit)
		VALUES ('huize', 'Hikami', 1, 'https://space.bilibili.com/1/video', '【直播回放】', 1, ?);
	`, limit)
	if err != nil {
		t.Fatalf("seed database: %v", err)
	}
	return database
}

func TestDiscoverLimitRespectsLimit(t *testing.T) {
	database := newDiscoverTestDBWithLimit(t, 2)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		fakeListerMany{},
	)

	results, err := manager.DiscoverAll(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	// 4 entries match prefix, but limit is 2 so only 2 should be created
	createdCount := 0
	for _, r := range results {
		if r.Created {
			createdCount++
		}
	}
	if createdCount != 2 {
		t.Fatalf("created count = %d, want 2", createdCount)
	}
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2 (should stop after limit)", len(results))
	}
}

func TestDiscoverLimitZeroNoLimit(t *testing.T) {
	database := newDiscoverTestDBWithLimit(t, 0)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		fakeListerMany{},
	)

	results, err := manager.DiscoverAll(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	// 4 entries match prefix, no limit
	createdCount := 0
	for _, r := range results {
		if r.Created {
			createdCount++
		}
	}
	if createdCount != 4 {
		t.Fatalf("created count = %d, want 4", createdCount)
	}
}

// TestPreviewAll_MarksExists 验证 PreviewAll 为已建过 download 场次的回放标注 Exists=true。
// 流程：先 DiscoverAll 建 BV1 场次（标题带前缀会命中），再 PreviewAll 检查 BV1 的 Exists。
func TestPreviewAll_MarksExists(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeLister{})

	// 先 DiscoverAll：BV1（【直播回放】测试）会建场次，BV2（普通投稿）被 title_prefix 过滤
	if _, err := manager.DiscoverAll(context.Background()); err != nil {
		t.Fatalf("discover all: %v", err)
	}

	// 再 PreviewAll：BV1 应标 Exists=true
	results, err := manager.PreviewAll(context.Background())
	if err != nil {
		t.Fatalf("preview all: %v", err)
	}
	// fakeLister 的 BV1 带前缀会进 PreviewChannel 结果；BV2 被过滤掉
	var bv1 *Result
	for i := range results {
		if results[i].SourceID == "BV1" {
			bv1 = &results[i]
		}
	}
	if bv1 == nil {
		t.Fatalf("BV1 not in preview results: %+v", results)
	}
	if !bv1.Exists {
		t.Fatalf("BV1 should be marked Exists=true after DiscoverAll: %+v", bv1)
	}
	// Created 在 preview 阶段应为零值 false
	if bv1.Created {
		t.Fatalf("preview result Created should be false: %+v", bv1)
	}
	// SessionID/TaskID 在 preview 阶段应为空
	if bv1.SessionID != "" || bv1.TaskID != "" {
		t.Fatalf("preview result should not have SessionID/TaskID: %+v", bv1)
	}
}

// countingLister 记录 List 调用次数，用于验证 Execute 不重跑 yt-dlp。
type countingLister struct {
	calls int
}

func (c *countingLister) List(ctx context.Context, sourceURL string, cookieFile string) ([]Entry, error) {
	c.calls++
	return []Entry{{ID: "BV1", Title: "【直播回放】测试", WebpageURL: "https://www.bilibili.com/video/BV1"}}, nil
}

// TestExecute_DoesNotRunYTDLP 验证 Execute 不调用 lister（预览阶段已拿到 entry，执行阶段直接建场次）。
func TestExecute_DoesNotRunYTDLP(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	lister := &countingLister{}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, lister)

	// Execute 直接用前端勾选的 entry，不应触发 lister
	results := manager.Execute(context.Background(), []ExecuteItem{
		{ChannelID: "huize", SourceID: "BV1", Title: "【直播回放】测试", SourceURL: "https://www.bilibili.com/video/BV1"},
	})
	if len(results) != 1 || !results[0].Created {
		t.Fatalf("execute result unexpected: %+v", results)
	}
	if lister.calls != 0 {
		t.Fatalf("Execute should not call lister, got %d calls", lister.calls)
	}
	// 验证场次已建 + 任务已入队
	tasks, err := pool.Store().List(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Type != "download" {
		t.Fatalf("expected 1 download task, got: %+v", tasks)
	}
}

// TestExecute_Idempotent 验证 Execute 对同一 source_id 重复调用不重复建场次/入队。
func TestExecute_Idempotent(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeLister{})
	item := ExecuteItem{ChannelID: "huize", SourceID: "BV1", Title: "【直播回放】测试", SourceURL: "https://www.bilibili.com/video/BV1"}

	// 第一次：created=true
	first := manager.Execute(context.Background(), []ExecuteItem{item})
	if len(first) != 1 || !first[0].Created || first[0].TaskID == "" {
		t.Fatalf("first execute should create: %+v", first)
	}

	// 第二次：created=false，TaskID 空，不重复入队
	second := manager.Execute(context.Background(), []ExecuteItem{item})
	if len(second) != 1 {
		t.Fatalf("second execute result count = %d, want 1", len(second))
	}
	if second[0].Created {
		t.Fatalf("second execute should not create: %+v", second)
	}
	if second[0].TaskID != "" {
		t.Fatalf("second execute should not enqueue task: %+v", second)
	}

	// 任务总数仍为 1
	tasks, err := pool.Store().List(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1 (idempotent)", len(tasks))
	}
}

// TestPreviewAllRespectsDiscoverLimit 验证 PreviewAll 复用 DiscoverChannel 的 discover_limit 语义：
// 仅保留前 DiscoverLimit 个「新」（!Exists）项，超限的截断（codex 审核 P1 回归测试）。
// fakeListerMany 返回 4 个带前缀的视频（BV1-3,5），limit=2 时预览应只含 2 个新项。
func TestPreviewAllRespectsDiscoverLimit(t *testing.T) {
	database := newDiscoverTestDBWithLimit(t, 2)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeListerMany{})

	results, err := manager.PreviewAll(context.Background())
	if err != nil {
		t.Fatalf("preview all: %v", err)
	}
	// 4 个带前缀的视频都是新的（未建过场次），limit=2 应只保留前 2 个
	newCount := 0
	for _, r := range results {
		if !r.Exists {
			newCount++
		}
	}
	if newCount != 2 {
		t.Fatalf("new item count = %d, want 2 (discover_limit): %+v", newCount, results)
	}
	if len(results) != 2 {
		t.Fatalf("total result count = %d, want 2 (truncated by limit): %+v", len(results), results)
	}
}

// TestPreviewAllLimitBreaksAfterExistingItem 验证 limit 达限后该频道剩余项（含已存在项）全部 break，
// 完全镜像 DiscoverChannel 语义（codex 审核 P2 第3轮：达限后紧跟的已存在项也应丢弃）。
// 构造：先 Execute 建 BV1（Exists=true），fakeListerMany 顺序 BV1(存在),BV2(新),BV3(新),BV5(新)，limit=2。
// 期望：BV1(不计数)→BV2(计数1)→BV3(计数2 达限)→BV5 break 丢弃。结果含 BV1,BV2,BV3，不含 BV5。
func TestPreviewAllLimitBreaksAfterExistingItem(t *testing.T) {
	database := newDiscoverTestDBWithLimit(t, 2)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeListerMany{})

	// 先让 BV1 已存在（建过 download 场次）
	manager.Execute(context.Background(), []ExecuteItem{
		{ChannelID: "huize", SourceID: "BV1", Title: "【直播回放】测试1", SourceURL: "https://www.bilibili.com/video/BV1"},
	})

	results, err := manager.PreviewAll(context.Background())
	if err != nil {
		t.Fatalf("preview all: %v", err)
	}
	// 收集返回的 source_id 集合
	got := make(map[string]bool)
	for _, r := range results {
		got[r.SourceID] = true
	}
	// BV1(存在,不计数) + BV2(新,计数1) + BV3(新,计数2 达限) 应保留；BV5(新) 达限后 break 应丢弃
	if !got["BV1"] || !got["BV2"] || !got["BV3"] {
		t.Fatalf("should contain BV1,BV2,BV3: %+v", got)
	}
	if got["BV5"] {
		t.Fatalf("BV5 should be truncated after limit reached: %+v", got)
	}
}

// --- Title resolver tests ---

// fakeTitleResolver 记录调用并返回预设标题。
type fakeTitleResolver struct {
	titles map[string]string
	calls  []string
}

func (r *fakeTitleResolver) ResolveDownloadTitle(_ context.Context, _, sourceID string) string {
	r.calls = append(r.calls, sourceID)
	if t, ok := r.titles[sourceID]; ok {
		return t
	}
	return sourceID // 兜底
}

// emptyTitleLister 模拟 yt-dlp --flat-playlist 下 B站标题为空的场景。
type emptyTitleLister struct{}

func (emptyTitleLister) List(_ context.Context, _ string, _ string) ([]Entry, error) {
	return []Entry{
		{ID: "BV1", Title: "", WebpageURL: "https://www.bilibili.com/video/BV1"},
		{ID: "BV2", Title: "已有标题", WebpageURL: "https://www.bilibili.com/video/BV2"},
	}, nil
}

// TestDiscoverChannelResolvesEmptyTitle 验证 DiscoverChannel 对空标题调 TitleResolver 取真实标题。
func TestDiscoverChannelResolvesEmptyTitle(t *testing.T) {
	database := newDiscoverTestDB(t)
	// 移除 title_prefix 以避免过滤
	if _, err := database.Exec(`UPDATE channels SET title_prefix = '' WHERE id = 'huize'`); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	resolver := &fakeTitleResolver{titles: map[string]string{"BV1": "真实标题1"}}
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		emptyTitleLister{},
		WithTitleResolver(resolver),
	)

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	results, err := manager.DiscoverChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("discover channel: %v", err)
	}

	// BV1 空标题 → resolver 被调用，返回 "真实标题1"
	// BV2 有标题 → resolver 不被调用
	if len(resolver.calls) != 1 || resolver.calls[0] != "BV1" {
		t.Fatalf("resolver should be called once for BV1, got: %v", resolver.calls)
	}

	var bv1, bv2 *Result
	for i := range results {
		switch results[i].SourceID {
		case "BV1":
			bv1 = &results[i]
		case "BV2":
			bv2 = &results[i]
		}
	}
	if bv1 == nil || bv1.Title != "真实标题1" {
		t.Fatalf("BV1 title should be resolved to '真实标题1', got: %+v", bv1)
	}
	if bv2 == nil || bv2.Title != "已有标题" {
		t.Fatalf("BV2 title should remain '已有标题', got: %+v", bv2)
	}
}

// TestPreviewChannelResolvesEmptyTitle 验证 PreviewChannel 也解析空标题。
func TestPreviewChannelResolvesEmptyTitle(t *testing.T) {
	database := newDiscoverTestDB(t)
	if _, err := database.Exec(`UPDATE channels SET title_prefix = '' WHERE id = 'huize'`); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	resolver := &fakeTitleResolver{titles: map[string]string{"BV1": "预览标题1"}}
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		emptyTitleLister{},
		WithTitleResolver(resolver),
	)

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	results, err := manager.PreviewChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("preview channel: %v", err)
	}

	if len(resolver.calls) != 1 {
		t.Fatalf("resolver should be called once for BV1, got: %v", resolver.calls)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "预览标题1" {
		t.Fatalf("BV1 title should be '预览标题1', got %q", results[0].Title)
	}
	if results[1].Title != "已有标题" {
		t.Fatalf("BV2 title should remain '已有标题', got %q", results[1].Title)
	}
}

// TestExecuteResolvesEmptyTitle 验证 Execute 对空标题做防御性解析。
func TestExecuteResolvesEmptyTitle(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	resolver := &fakeTitleResolver{titles: map[string]string{"BV1": "执行标题1"}}
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		fakeLister{},
		WithTitleResolver(resolver),
	)

	results := manager.Execute(context.Background(), []ExecuteItem{
		{ChannelID: "huize", SourceID: "BV1", Title: "", SourceURL: "https://www.bilibili.com/video/BV1"},
		{ChannelID: "huize", SourceID: "BV2", Title: "已有标题", SourceURL: "https://www.bilibili.com/video/BV2"},
	})

	if len(resolver.calls) != 1 || resolver.calls[0] != "BV1" {
		t.Fatalf("resolver should be called once for BV1, got: %v", resolver.calls)
	}
	if results[0].Title != "执行标题1" {
		t.Fatalf("BV1 title should be '执行标题1', got %q", results[0].Title)
	}
	if results[1].Title != "已有标题" {
		t.Fatalf("BV2 title should remain '已有标题', got %q", results[1].Title)
	}
}

// TestDiscoverNoResolverKeepsOriginalBehavior 验证无 TitleResolver 时保持原行为（空标题兜底 sourceID）。
func TestDiscoverNoResolverKeepsOriginalBehavior(t *testing.T) {
	database := newDiscoverTestDB(t)
	if _, err := database.Exec(`UPDATE channels SET title_prefix = '' WHERE id = 'huize'`); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		emptyTitleLister{},
		// 无 WithTitleResolver
	)

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	results, err := manager.DiscoverChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("discover channel: %v", err)
	}

	// BV1 空标题 → CreateDownload 兜底为 sourceID "BV1"
	// BV2 有标题 → 保持 "已有标题"
	if results[0].Title != "BV1" {
		t.Fatalf("BV1 title should fallback to sourceID 'BV1', got %q", results[0].Title)
	}
	if results[1].Title != "已有标题" {
		t.Fatalf("BV2 title should remain '已有标题', got %q", results[1].Title)
	}
}

// TestResolveTitleFallsBackOnEmptyResolverResult 验证 resolver 返回空串时兜底为 sourceID。
func TestResolveTitleFallsBackOnEmptyResolverResult(t *testing.T) {
	database := newDiscoverTestDB(t)
	if _, err := database.Exec(`UPDATE channels SET title_prefix = '' WHERE id = 'huize'`); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	// resolver 对 BV1 返回空串
	resolver := &fakeTitleResolver{titles: map[string]string{"BV1": ""}}
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		emptyTitleLister{},
		WithTitleResolver(resolver),
	)

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	results, err := manager.DiscoverChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("discover channel: %v", err)
	}

	// BV1 空标题 → resolver 返回空串 → 兜底为 sourceID "BV1"
	if results[0].Title != "BV1" {
		t.Fatalf("BV1 title should fallback to sourceID when resolver returns empty, got %q", results[0].Title)
	}
}

// TestDiscoverLimitSkipsTitleResolution 验证达到 discover_limit 后不再调用 resolver。
func TestDiscoverLimitSkipsTitleResolution(t *testing.T) {
	database := newDiscoverTestDBWithLimit(t, 1)
	// 移除 title_prefix 以让所有 entry 通过过滤
	if _, err := database.Exec(`UPDATE channels SET title_prefix = '' WHERE id = 'huize'`); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	resolver := &fakeTitleResolver{titles: map[string]string{
		"BV1": "标题1", "BV2": "标题2", "BV3": "标题3",
	}}
	// 使用返回空标题的 lister，触发 resolver
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		emptyTitleListerMany{},
		WithTitleResolver(resolver),
	)

	results, err := manager.DiscoverAll(context.Background())
	if err != nil {
		t.Fatalf("discover all: %v", err)
	}

	// limit=1, emptyTitleListerMany 有 3 个空标题 entry
	// BV1 → resolveTitle 被调用（返回"标题1"）, created=true, createdCount=1
	// BV2 → limit check 先于 resolveTitle, break, resolver 不被调用
	// resolver 应只被调用 1 次（BV1）
	if len(resolver.calls) != 1 {
		t.Fatalf("resolver should be called once (only BV1 before limit), got %d calls: %v", len(resolver.calls), resolver.calls)
	}

	// 结果应该只有 1 个 created
	createdCount := 0
	for _, r := range results {
		if r.Created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
}

// emptyTitleListerMany 模拟多个空标题 entry（用于 limit + resolver 测试）。
type emptyTitleListerMany struct{}

func (emptyTitleListerMany) List(_ context.Context, _ string, _ string) ([]Entry, error) {
	return []Entry{
		{ID: "BV1", Title: "", WebpageURL: "https://www.bilibili.com/video/BV1"},
		{ID: "BV2", Title: "", WebpageURL: "https://www.bilibili.com/video/BV2"},
		{ID: "BV3", Title: "", WebpageURL: "https://www.bilibili.com/video/BV3"},
	}, nil
}

// ---------------------------------------------------------------------------
// Preview (URL 驱动入口) tests (2026-07-19 解耦改动)
// ---------------------------------------------------------------------------

// TestPreviewUnassigned: 不绑定 channel 表的 Preview,ChannelID 空串时自动填占位 _unassigned。
// 这是「回顾管理·回放」页独立入口的核心契约。
func TestPreviewUnassigned(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeLister{})

	results, err := manager.Preview(context.Background(), PreviewInput{
		SourceURL: "https://space.bilibili.com/999/lists/1",
		// ChannelID 留空 → Preview 内部填 channel.UnassignedID
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// fakeLister 返回 BV1(【直播回放】测试)+ BV2(普通投稿),无 title_prefix 过滤,都进结果
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	for _, r := range results {
		if r.ChannelID != channel.UnassignedID {
			t.Errorf("ChannelID = %q, want %q (空 ChannelID 应自动填占位)", r.ChannelID, channel.UnassignedID)
		}
	}
}

// TestPreviewWithExplicitChannelID: 显式传 ChannelID 时不被覆盖为占位(用于后续绑定真实主播)。
func TestPreviewWithExplicitChannelID(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeLister{})

	results, err := manager.Preview(context.Background(), PreviewInput{
		SourceURL: "https://space.bilibili.com/1/lists/1",
		ChannelID: "bili_123",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, r := range results {
		if r.ChannelID != "bili_123" {
			t.Errorf("ChannelID = %q, want bili_123 (显式传值应保留)", r.ChannelID)
		}
	}
}

// TestPreviewAnnotatesExists: Preview 内部自动调 annotateExists,不需要 handler 单独调。
// 这验证 codex r13b SUGGESTION 的封装设计:handler 不接触标注逻辑。
func TestPreviewAnnotatesExists(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeLister{})

	// 先 Execute 建一次 BV1 场次(让它变成 Exists)
	manager.Execute(context.Background(), []ExecuteItem{
		{ChannelID: "huize", SourceID: "BV1", Title: "测试", SourceURL: "https://www.bilibili.com/video/BV1"},
	})

	// 用相同的 ChannelID="huize" 再 Preview,BV1 应被标注 Exists=true
	results, err := manager.Preview(context.Background(), PreviewInput{
		SourceURL:   "https://space.bilibili.com/1/lists/1",
		TitlePrefix: "【直播回放】", // 与 fakeLister 的 BV1 标题匹配,BV2 被过滤
		ChannelID:   "huize",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	// 只 BV1 通过 title_prefix
	if len(results) != 1 || results[0].SourceID != "BV1" {
		t.Fatalf("results = %+v, want only BV1", results)
	}
	if !results[0].Exists {
		t.Errorf("BV1.Exists = false, want true (Preview 内部应自动调 annotateExists)")
	}
}

// TestPreviewChannelForwardsToPreview: 旧 PreviewChannel 转发到新 Preview,行为等价(零回归)。
// 验证 PreviewChannel(ctx, item) 与 Preview(ctx, PreviewInput{...}) 对相同 channel 返回一致结果。
func TestPreviewChannelForwardsToPreview(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	store := channel.NewStore(database)
	manager := NewManager(store, session.NewStore(database), pool, fakeLister{})

	ctx := context.Background()
	ch, err := store.Get(ctx, "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}

	oldResults, err := manager.PreviewChannel(ctx, ch)
	if err != nil {
		t.Fatalf("PreviewChannel: %v", err)
	}

	newResults, err := manager.Preview(ctx, PreviewInput{
		SourceURL:   ch.ReplaySourceURL,
		CookieFile:  ch.DownloadCookieFile,
		TitlePrefix: ch.TitlePrefix,
		ChannelID:   ch.ID,
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	// 两者条数一致(注意:PreviewChannel 不标注 exists,Preview 标注;比较 ChannelID/SourceID/Title)
	if len(oldResults) != len(newResults) {
		t.Fatalf("len mismatch: PreviewChannel=%d Preview=%d", len(oldResults), len(newResults))
	}
	for i := range oldResults {
		if oldResults[i].ChannelID != newResults[i].ChannelID {
			t.Errorf("[%d] ChannelID: PreviewChannel=%q Preview=%q", i, oldResults[i].ChannelID, newResults[i].ChannelID)
		}
		if oldResults[i].SourceID != newResults[i].SourceID {
			t.Errorf("[%d] SourceID: PreviewChannel=%q Preview=%q", i, oldResults[i].SourceID, newResults[i].SourceID)
		}
		if oldResults[i].Title != newResults[i].Title {
			t.Errorf("[%d] Title: PreviewChannel=%q Preview=%q", i, oldResults[i].Title, newResults[i].Title)
		}
	}
}
