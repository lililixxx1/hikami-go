package discover

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hikami-go/internal/biliutil"
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

// TestPreviewChannelUsesChannelCookieResolution: v3 重构后 PreviewChannel 走独立的
// previewCoreForChannel(用 resolveChannelCookie),不再转发到 previewCore(URL 模式)。
// 关键不变式:PreviewChannel 不标 Exists(PreviewAll 外层批量标注,避免双重标注)。
// (旧 TestPreviewChannelForwardsToPreview 改名,因 v3 拆分后两者不再等价——codex r15c LOW #2)
func TestPreviewChannelUsesChannelCookieResolution(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	store := channel.NewStore(database)
	manager := NewManager(store, session.NewStore(database), pool, fakeLister{})

	ctx := context.Background()
	ch, err := store.Get(ctx, "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}

	results, err := manager.PreviewChannel(ctx, ch)
	if err != nil {
		t.Fatalf("PreviewChannel: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("PreviewChannel returned no results, expected at least 1")
	}

	// 不标 Exists 的不变式保留(避免 PreviewAll 双重标注——codex r13b MEDIUM #3)
	for i, r := range results {
		if r.Exists {
			t.Errorf("[%d] PreviewChannel 标注了 Exists=true,应保持 false(PreviewAll 外层批量标注)", i)
		}
	}
	// ChannelID 应等于频道 ID(走 previewCoreForChannel,而非 URL 模式的 _unassigned)
	for i, r := range results {
		if r.ChannelID != ch.ID {
			t.Errorf("[%d] ChannelID = %q, want %q (PreviewChannel 应保留频道 ID)", i, r.ChannelID, ch.ID)
		}
	}
}

// ============================================================================
// 发现阶段 cookie 解析测试(v3 新增,2026-07-19)
//
// 覆盖 codex r15c/r15b/r15 的关键场景:
//   - URL 模式 resolveURLCookie:用户显式优先 / 默认账号回退 / 加密场景 / 空白分支
//   - 频道模式 resolveChannelCookie:频道账号覆盖 / 默认账号 vs legacy / 纯 legacy 退化
//   - 临时文件 cleanup 用 os.CreateTemp + os.Remove
//
// recordingLister 在 List 调用期间读取临时文件内容,避免 cleanup 后读不到
// (codex r15b MEDIUM #3)
// ============================================================================

type listerRecord struct {
	cookiePath string
	content    []byte // 在 List 内读取(cleanup 前保存);nil 表示未读(空路径或读失败)
	existed    bool   // 调用 List 时该路径是否存在
}

type recordingLister struct {
	mu      sync.Mutex
	records []listerRecord
	entries []Entry
	listErr error
}

func (r *recordingLister) List(ctx context.Context, sourceURL string, cookieFile string) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := listerRecord{cookiePath: cookieFile}
	if cookieFile != "" {
		if content, err := os.ReadFile(cookieFile); err == nil {
			rec.content = content
			rec.existed = true
		}
	}
	r.records = append(r.records, rec)
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.entries, nil
}

func (r *recordingLister) lastRecord() (listerRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) == 0 {
		return listerRecord{}, false
	}
	return r.records[len(r.records)-1], true
}

func (r *recordingLister) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

// writeTestCookieFile 用 biliutil.WriteNetscapeCookieFile 写明文 cookie 文件。
// 若 encrypt=true 则启用 SetCookieEncryptionKey 写加密格式(测试完后清理 key)。
// 返回落盘文件路径。
func writeTestCookieFile(t *testing.T, dir string, uid int64, encrypt bool) string {
	t.Helper()
	if encrypt {
		// 32 字节 hex key(AES-256)
		biliutil.SetCookieEncryptionKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
		t.Cleanup(func() { biliutil.SetCookieEncryptionKey("") })
	}
	farFuture := time.Now().Add(365 * 24 * time.Hour)
	result, err := biliutil.WriteNetscapeCookieFile([]*http.Cookie{
		{Name: "SESSDATA", Value: "sess-" + fmtUID(uid), Expires: farFuture},
		{Name: "bili_jct", Value: "csrf-" + fmtUID(uid), Expires: farFuture},
		{Name: "DedeUserID", Value: fmtUID(uid), Expires: farFuture},
	}, biliutil.CookieWriteOptions{
		Dir:   dir,
		UID:   uid,
		Usage: "download",
	})
	if err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	return result.Path
}

func fmtUID(uid int64) string {
	return fmt.Sprintf("uid%d", uid)
}

// newDiscoverTestDBWithCookieStore 创建带 bili_cookie_accounts 表的测试 DB + 账号池。
// 返回的 cookieAccountStore 的 allowedDirs 含 cookieDir,允许在此目录下放 cookie 文件。
func newDiscoverTestDBWithCookieStore(t *testing.T, cookieDir string) (*sql.DB, *biliutil.CookieAccountStore) {
	t.Helper()
	database := newDiscoverTestDB(t)
	// newDiscoverTestDB 已 Migrate,bili_cookie_accounts 表应已存在(v35 schema)
	store := biliutil.NewCookieAccountStore(database, cookieDir)
	return database, store
}

// insertBiliAccount 用裸 SQL 插入账号记录(绕过 ValidateCookiePath,测试专用)。
// 返回 account id。
func insertBiliAccount(t *testing.T, db *sql.DB, uid int64, nickname, cookieFile string, isDefaultDownload bool) int64 {
	t.Helper()
	now := time.Now().Format(time.RFC3339)
	dl := 0
	if isDefaultDownload {
		dl = 1
	}
	res, err := db.Exec(`
		INSERT INTO bili_cookie_accounts (uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?)
	`, uid, nickname, cookieFile, dl, now, now)
	if err != nil {
		t.Fatalf("insert bili account: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// -------------------- URL 模式测试(resolveURLCookie / Preview) --------------------

// TestPreview_NoCookieStore_UnauthMode:未注入 cookieAccounts → lister 收到空串(公开回放仍发现)
func TestPreview_NoCookieStore_UnauthMode(t *testing.T) {
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1", Title: "x"}}}
	// 故意不调 WithCookieAccountStore
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithOutputRoot(t.TempDir()),
	)

	_, err := manager.Preview(context.Background(), PreviewInput{SourceURL: "https://example.com"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	if rec.cookiePath != "" {
		t.Errorf("cookiePath = %q, want empty (no store injected)", rec.cookiePath)
	}
}

// TestPreview_ExplicitCookieFileWins:用户填 CookieFile → lister 收到原路径,无临时文件
func TestPreview_ExplicitCookieFileWins(t *testing.T) {
	database, store := newDiscoverTestDBWithCookieStore(t, t.TempDir())
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	// 用 t.TempDir() 下的路径(不创建文件),避免本机 /tmp 巧合存在同名文件导致非确定性
	// codex r16 SUGGESTION
	explicit := filepath.Join(t.TempDir(), "explicit.txt")
	_, err := manager.Preview(context.Background(), PreviewInput{
		SourceURL:  "https://example.com",
		CookieFile: explicit,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	if rec.cookiePath != explicit {
		t.Errorf("cookiePath = %q, want %q (用户显式应原值返回)", rec.cookiePath, explicit)
	}
	if rec.content != nil {
		t.Errorf("不应读取临时文件(用户路径,我们不读)")
	}
}

// TestPreview_DefaultAccount_WritesTempCookie:账号池有默认账号 → lister 收到临时文件路径(≠ account.CookieFile),
// 内容含 SESSDATA(证明已解密成明文 Netscape)
func TestPreview_DefaultAccount_WritesTempCookie(t *testing.T) {
	cookieDir := t.TempDir()
	database, store := newDiscoverTestDBWithCookieStore(t, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	accountPath := writeTestCookieFile(t, cookieDir, 42, false)
	insertBiliAccount(t, database, 42, "test", accountPath, true)

	_, err := manager.Preview(context.Background(), PreviewInput{SourceURL: "https://example.com"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	if rec.cookiePath == "" {
		t.Fatal("cookiePath empty, want temp file path")
	}
	if rec.cookiePath == accountPath {
		t.Errorf("cookiePath = accountPath %q(应写临时文件,不能直接用账号原始路径)", accountPath)
	}
	if !rec.existed {
		t.Errorf("临时文件 %q 在 List 调用期间应存在", rec.cookiePath)
	}
	if !bytes.Contains(rec.content, []byte("SESSDATA")) {
		t.Errorf("临时文件内容应含 SESSDATA(明文 Netscape),实际: %q", string(rec.content))
	}
}

// TestPreview_DefaultAccount_Encrypted:加密账号 → 临时文件不含 HIKAMI_V1 魔数(已解密)
func TestPreview_DefaultAccount_Encrypted(t *testing.T) {
	cookieDir := t.TempDir()
	database, store := newDiscoverTestDBWithCookieStore(t, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	accountPath := writeTestCookieFile(t, cookieDir, 42, true) // 启用加密
	insertBiliAccount(t, database, 42, "enc", accountPath, true)

	// 先验证 accountPath 确实是加密的(含 HIKAMI_V1)
	rawAccount, _ := os.ReadFile(accountPath)
	if !bytes.Contains(rawAccount, []byte("HIKAMI_V1")) {
		t.Fatalf("setup 失败:accountPath 应含 HIKAMI_V1 魔数,实际: %q", string(rawAccount))
	}

	_, err := manager.Preview(context.Background(), PreviewInput{SourceURL: "https://example.com"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	if rec.cookiePath == accountPath {
		t.Fatalf("cookiePath = accountPath(应写临时明文文件,不能直接用加密原路径)")
	}
	if bytes.Contains(rec.content, []byte("HIKAMI_V1")) {
		t.Errorf("临时文件内容含 HIKAMI_V1 魔数(应已解密为明文),实际: %q", string(rec.content))
	}
	if !bytes.Contains(rec.content, []byte("SESSDATA")) {
		t.Errorf("临时文件内容应含 SESSDATA(明文 Netscape),实际: %q", string(rec.content))
	}
}

// TestPreview_BlankCookieFile_FallsBack:CookieFile 为纯空白 → 回退默认账号(codex r15 MEDIUM #3)
func TestPreview_BlankCookieFile_FallsBack(t *testing.T) {
	cookieDir := t.TempDir()
	database, store := newDiscoverTestDBWithCookieStore(t, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	accountPath := writeTestCookieFile(t, cookieDir, 42, false)
	insertBiliAccount(t, database, 42, "test", accountPath, true)

	_, err := manager.Preview(context.Background(), PreviewInput{
		SourceURL:  "https://example.com",
		CookieFile: "   ", // 纯空白
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	if rec.cookiePath == "" {
		t.Fatal("纯空白 CookieFile 应回退默认账号,但 lister 收到空串")
	}
	if rec.cookiePath == accountPath {
		t.Errorf("cookiePath = accountPath(应写临时文件)")
	}
	if !bytes.Contains(rec.content, []byte("SESSDATA")) {
		t.Errorf("临时文件应含 SESSDATA,实际: %q", string(rec.content))
	}
}

// TestPreview_NoDefaultAccount_NoError:无默认账号 → 不阻断,lister 收到空串
func TestPreview_NoDefaultAccount_NoError(t *testing.T) {
	cookieDir := t.TempDir()
	database, store := newDiscoverTestDBWithCookieStore(t, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	// 账号存在但不设默认
	accountPath := writeTestCookieFile(t, cookieDir, 42, false)
	insertBiliAccount(t, database, 42, "non-default", accountPath, false)

	_, err := manager.Preview(context.Background(), PreviewInput{SourceURL: "https://example.com"})
	if err != nil {
		t.Fatalf("preview: %v (无默认账号不应阻断)", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	if rec.cookiePath != "" {
		t.Errorf("cookiePath = %q, want empty (无默认账号应回退空串)", rec.cookiePath)
	}
}

// TestPreview_TempCookieCleanedUp:List 返回后临时文件已删除(cleanup 生效)
func TestPreview_TempCookieCleanedUp(t *testing.T) {
	cookieDir := t.TempDir()
	database, store := newDiscoverTestDBWithCookieStore(t, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	accountPath := writeTestCookieFile(t, cookieDir, 42, false)
	insertBiliAccount(t, database, 42, "test", accountPath, true)

	_, err := manager.Preview(context.Background(), PreviewInput{SourceURL: "https://example.com"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok || rec.cookiePath == "" {
		t.Fatal("无临时文件路径可供检查")
	}
	// Preview 返回后,cleanup 应已删除临时文件
	if _, err := os.Stat(rec.cookiePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("临时文件 %q 应被 cleanup 删除,实际 err = %v", rec.cookiePath, err)
	}
}

// -------------------- 频道模式测试(resolveChannelCookie / PreviewChannel / DiscoverChannel) --------------------

// helper:构造频道 + 默认账号,返回 (database, store, cookieDir, manager, channelObj)
func setupChannelAndStore(t *testing.T, opts struct {
	DownloadAccountID  *int64
	DownloadCookieFile string
	HasDefaultAccount  bool
	DefaultUID         int64
}) (*sql.DB, *biliutil.CookieAccountStore, string, *Manager, channel.Channel) {
	t.Helper()
	cookieDir := t.TempDir()
	database, store := newDiscoverTestDBWithCookieStore(t, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1", Title: "test"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)
	// 修改 huize 频道加 cookie 配置
	var accountID int64
	if opts.HasDefaultAccount {
		accountPath := writeTestCookieFile(t, cookieDir, opts.DefaultUID, false)
		accountID = insertBiliAccount(t, database, opts.DefaultUID, "default", accountPath, true)
	}
	_, err := database.Exec(`
		UPDATE channels
		SET download_cookie_file = ?, download_account_id = ?
		WHERE id = 'huize'
	`, opts.DownloadCookieFile, sql.NullInt64{Int64: accountID, Valid: opts.DownloadAccountID != nil})
	if err != nil {
		t.Fatalf("update channel: %v", err)
	}
	chStore := channel.NewStore(database)
	ch, err := chStore.Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	return database, store, cookieDir, manager, ch
}

// TestPreviewChannel_AccountIDWinsOverLegacyFile:
// 频道同时有 DownloadAccountID 和 DownloadCookieFile → 走账号(临时文件),不走 legacy 原路径
// (codex r15b MEDIUM #1)
func TestPreviewChannel_AccountIDWinsOverLegacyFile(t *testing.T) {
	database := newDiscoverTestDB(t)
	cookieDir := t.TempDir()
	store := biliutil.NewCookieAccountStore(database, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	// 准备账号 A(非默认)+ 默认账号 D
	pathA := writeTestCookieFile(t, cookieDir, 100, false)
	idA := insertBiliAccount(t, database, 100, "A", pathA, false)
	pathD := writeTestCookieFile(t, cookieDir, 200, false)
	insertBiliAccount(t, database, 200, "D", pathD, true)

	// 频道挂 DownloadAccountID=A + DownloadCookieFile=legacy.txt(都存在)
	_, err := database.Exec(`
		UPDATE channels
		SET download_account_id = ?, download_cookie_file = ?
		WHERE id = 'huize'
	`, idA, filepath.Join(cookieDir, "legacy.txt"))
	if err != nil {
		t.Fatalf("update channel: %v", err)
	}
	// 写一个合法 legacy cookie 文件(三字段齐全,供 ResolveCookie 退化兜底——codex r16 LOW #2)
	legacyPath := filepath.Join(cookieDir, "legacy.txt")
	if err := os.WriteFile(legacyPath, validLegacyNetscapeCookie(), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}

	_, err = manager.PreviewChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("PreviewChannel: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	// 应走账号 A(临时文件),不是 legacy 原路径
	if rec.cookiePath == legacyPath {
		t.Errorf("走 legacy 原路径,应走账号 A 的临时文件(优先级:账号覆盖 > legacy)")
	}
	if rec.cookiePath == pathA {
		t.Errorf("cookiePath = 账号 A 原始路径(应写临时明文文件)")
	}
	// 精确断言:必须走账号 A(SESSDATA=sess-uid100),不是默认账号 D(sess-uid200),
	// 也不是 legacy(cookie 内容为 "legacy")——codex r16 LOW #3
	if !bytes.Contains(rec.content, []byte("sess-uid100")) {
		t.Errorf("临时文件应含账号 A 的 SESSDATA(sess-uid100),实际: %q", string(rec.content))
	}
	if bytes.Contains(rec.content, []byte("sess-uid200")) {
		t.Errorf("走了默认账号 D(sess-uid200),应优先频道账号 A(codex r16 LOW #3)")
	}
	if bytes.Contains(rec.content, []byte("legacy")) {
		t.Errorf("走了 legacy cookie,应优先账号 A")
	}
}

// TestPreviewChannel_DefaultAccountWinsOverLegacyFile:
// 频道无 DownloadAccountID、有全局默认账号、有 DownloadCookieFile → 走默认账号(临时文件)
// (codex r15b MEDIUM #2)
func TestPreviewChannel_DefaultAccountWinsOverLegacyFile(t *testing.T) {
	database := newDiscoverTestDB(t)
	cookieDir := t.TempDir()
	store := biliutil.NewCookieAccountStore(database, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	// 默认账号 D
	pathD := writeTestCookieFile(t, cookieDir, 200, false)
	insertBiliAccount(t, database, 200, "D", pathD, true)

	// 频道挂 DownloadCookieFile=legacy.txt(无 DownloadAccountID)
	legacyPath := filepath.Join(cookieDir, "legacy.txt")
	if err := os.WriteFile(legacyPath, validLegacyNetscapeCookie(), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	_, err := database.Exec(`UPDATE channels SET download_cookie_file = ? WHERE id = 'huize'`, legacyPath)
	if err != nil {
		t.Fatalf("update channel: %v", err)
	}

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}

	_, err = manager.PreviewChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("PreviewChannel: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	// 应走默认账号 D(临时文件),不是 legacy 原路径
	if rec.cookiePath == legacyPath {
		t.Errorf("走 legacy,应走默认账号 D 的临时文件(优先级:全局默认 > legacy)")
	}
	if rec.cookiePath == pathD {
		t.Errorf("cookiePath = 默认账号 D 原始路径(应写临时明文文件)")
	}
	if bytes.Contains(rec.content, []byte("legacy")) {
		t.Errorf("走了 legacy cookie,应优先默认账号 D")
	}
	// 精确断言:必须走默认账号 D(sess-uid200)——codex r16 LOW #3
	if !bytes.Contains(rec.content, []byte("sess-uid200")) {
		t.Errorf("临时文件应含默认账号 D 的 cookie(sess-uid200),实际: %q", string(rec.content))
	}
}

// TestPreviewChannel_OnlyLegacyFile:无账号配置、无全局默认、有 DownloadCookieFile → 走 legacy(零回归)
// 关键:ResolveCookie 第 3 级 fallback 加载 legacy 成功后,helper 把它写成临时明文文件
// (与下载链路 writeTempCookieFile 完全一致),所以 lister 收到的是临时路径而非 legacy 原路径,
// 但临时文件的内容应来自 legacy fixture(含 legacy-sess)。codex r16 LOW #2:旧 fixture 缺字段,
// 走的是"ResolveCookie 加载失败→退化 legacy 原路径"的错误路径,不是真正的成功 fallback。
func TestPreviewChannel_OnlyLegacyFile(t *testing.T) {
	database := newDiscoverTestDB(t)
	cookieDir := t.TempDir()
	store := biliutil.NewCookieAccountStore(database, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	legacyPath := filepath.Join(cookieDir, "legacy.txt")
	if err := os.WriteFile(legacyPath, validLegacyNetscapeCookie(), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	_, err := database.Exec(`UPDATE channels SET download_cookie_file = ? WHERE id = 'huize'`, legacyPath)
	if err != nil {
		t.Fatalf("update channel: %v", err)
	}

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}

	_, err = manager.PreviewChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("PreviewChannel: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	// legacy 通过 ResolveCookie 第 3 级加载成功,helper 把它写成临时文件(与下载链路一致)
	if rec.cookiePath == legacyPath {
		t.Errorf("cookiePath = legacy 原路径 %q (ResolveCookie 加载成功后应写临时文件)", legacyPath)
	}
	// 临时文件内容应含 legacy fixture 的 SESSDATA 值(证明用的是 legacy cookie)
	if !bytes.Contains(rec.content, []byte("legacy-sess")) {
		t.Errorf("临时文件应含 legacy cookie 的 SESSDATA(legacy-sess),实际: %q", string(rec.content))
	}
}

// validLegacyNetscapeCookie 返回合法的明文 Netscape cookie(三字段齐全),供 legacy fallback 测试使用。
// codex r16 LOW #2:旧 fixture 只含 SESSDATA,实际走的是 ResolveCookie 加载失败的错误退化路径。
func validLegacyNetscapeCookie() []byte {
	return []byte("# Netscape HTTP Cookie File\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tSESSDATA\tlegacy-sess\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tbili_jct\tlegacy-csrf\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tDedeUserID\tlegacy-uid\n")
}

// TestPreviewChannel_NoConfigAtAll:全空 → lister 收到空串(不阻断)
func TestPreviewChannel_NoConfigAtAll(t *testing.T) {
	database := newDiscoverTestDB(t)
	cookieDir := t.TempDir()
	store := biliutil.NewCookieAccountStore(database, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BV1"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	// huize 默认无 DownloadCookieFile,账号池也无账号

	_, err = manager.PreviewChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("PreviewChannel: %v (全空不应阻断)", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	if rec.cookiePath != "" {
		t.Errorf("cookiePath = %q, want empty (全空配置)", rec.cookiePath)
	}
}

// TestDiscoverChannel_UsesChannelCookieResolution:DiscoverChannel 也走 resolveChannelCookie
func TestDiscoverChannel_UsesChannelCookieResolution(t *testing.T) {
	database := newDiscoverTestDB(t)
	cookieDir := t.TempDir()
	store := biliutil.NewCookieAccountStore(database, cookieDir)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	rl := &recordingLister{entries: []Entry{{ID: "BVwithPrefix", Title: "【直播回放】test"}}}
	manager := NewManager(channel.NewStore(database), session.NewStore(database), pool, rl,
		WithCookieAccountStore(store),
		WithOutputRoot(t.TempDir()),
	)

	// 默认账号 + 频道无 legacy
	pathD := writeTestCookieFile(t, cookieDir, 200, false)
	insertBiliAccount(t, database, 200, "D", pathD, true)

	ch, err := channel.NewStore(database).Get(context.Background(), "huize")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}

	_, err = manager.DiscoverChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("DiscoverChannel: %v", err)
	}
	rec, ok := rl.lastRecord()
	if !ok {
		t.Fatal("lister not called")
	}
	// DiscoverChannel 应走默认账号的临时文件
	if rec.cookiePath == "" {
		t.Fatal("cookiePath 空,应有默认账号临时文件")
	}
	if rec.cookiePath == pathD {
		t.Errorf("cookiePath = 原始账号路径(应写临时明文文件)")
	}
}
