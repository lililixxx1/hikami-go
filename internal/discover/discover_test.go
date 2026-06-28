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
