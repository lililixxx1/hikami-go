package discover

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hikami-go/internal/channel"
	"hikami-go/internal/session"
	"hikami-go/internal/worker"
)

// manyEmptyTitleLister 返回 n 个空标题 entry,模拟 yt-dlp --flat-playlist 对合集页
// 几乎全返回空标题(触发逐条回源 view API 解析真实标题)的真实场景。
type manyEmptyTitleLister struct{ n int }

func (l manyEmptyTitleLister) List(_ context.Context, _ string, _ string) ([]Entry, error) {
	entries := make([]Entry, l.n)
	for i := 0; i < l.n; i++ {
		id := fmt.Sprintf("BV%03d", i)
		entries[i] = Entry{
			ID:         id,
			Title:      "", // 空标题 → 触发 ResolveDownloadTitle
			WebpageURL: "https://www.bilibili.com/video/" + id,
		}
	}
	return entries, nil
}

// slowTitleResolver 每次解析 sleep 一段固定时间,并用原子计数器记录并发在途数,
// 用于断言 previewFromEntries 真的并发执行(而非串行),且并发度受 previewTitleConcurrency 限流。
type slowTitleResolver struct {
	delay         time.Duration
	inflight      int32
	maxInflight   int32
	processedCnt  int32
	processedMu   sync.Mutex
	processedList []string
}

func (r *slowTitleResolver) ResolveDownloadTitle(_ context.Context, _, sourceID string) string {
	cur := atomic.AddInt32(&r.inflight, 1)
	// 记录历史最大并发在途数。
	for {
		old := atomic.LoadInt32(&r.maxInflight)
		if cur <= old {
			break
		}
		if atomic.CompareAndSwapInt32(&r.maxInflight, old, cur) {
			break
		}
	}
	time.Sleep(r.delay)
	atomic.AddInt32(&r.inflight, -1)

	r.processedMu.Lock()
	r.processedList = append(r.processedList, sourceID)
	r.processedMu.Unlock()
	atomic.AddInt32(&r.processedCnt, 1)
	return sourceID
}

// TestPreviewFromEntries_ConcurrentTitleResolution 钉死 B2:previewFromEntries 对标题解析
// 做了有界并发,而非串行。10 条 × 50ms:
//   - 串行预期 ~500ms
//   - 并发(5 路)预期 ~2 批 ≈ 100ms,且最大并发在途数接近 5
func TestPreviewFromEntries_ConcurrentTitleResolution(t *testing.T) {
	const n = 10
	const delay = 50 * time.Millisecond
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	resolver := &slowTitleResolver{delay: delay}
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		manyEmptyTitleLister{n: n},
		WithTitleResolver(resolver),
	)

	start := time.Now()
	results, err := manager.Preview(context.Background(), PreviewInput{
		SourceURL: "https://www.bilibili.com/123",
		ChannelID: channel.UnassignedID,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(results) != n {
		t.Fatalf("results count = %d, want %d", len(results), n)
	}

	// 核心断言:并发后耗时应显著小于串行(n×delay)。留足余量,串行 500ms,这里上限 350ms。
	if elapsed > n*delay/2+100*time.Millisecond {
		t.Errorf("elapsed = %v, want < ~%v (串行预期 %v;并发 N=%d 应显著更快)",
			elapsed, n*delay/2, n*delay, previewTitleConcurrency)
	}
	// 最大并发在途数应 >1(证明确实并发),且 <= previewTitleConcurrency(证明有界限流)。
	if maxIn := atomic.LoadInt32(&resolver.maxInflight); maxIn <= 1 {
		t.Errorf("max inflight = %d, want >1 (证明标题解析并发执行)", maxIn)
	} else if maxIn > previewTitleConcurrency {
		t.Errorf("max inflight = %d, exceeds limit %d (证明有界限流失效)", maxIn, previewTitleConcurrency)
	}
	if got := atomic.LoadInt32(&resolver.processedCnt); got != int32(n) {
		t.Errorf("processed = %d, want %d", got, n)
	}
}

// TestPreviewFromEntries_PreservesOrder 钉死并发写回后结果顺序与输入一致。
func TestPreviewFromEntries_PreservesOrder(t *testing.T) {
	const n = 8
	database := newDiscoverTestDB(t)
	pool := worker.NewPool(worker.NewStore(database), worker.NewHub(), 1, nil)
	resolver := &slowTitleResolver{delay: 5 * time.Millisecond}
	manager := NewManager(
		channel.NewStore(database),
		session.NewStore(database),
		pool,
		manyEmptyTitleLister{n: n},
		WithTitleResolver(resolver),
	)

	results, err := manager.Preview(context.Background(), PreviewInput{
		SourceURL: "https://www.bilibili.com/123",
		ChannelID: channel.UnassignedID,
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	// manyEmptyTitleLister 按 BV%03d 生成,并发写回后 results 仍应保持该序。
	for i, r := range results {
		want := fmt.Sprintf("BV%03d", i)
		if r.SourceID != want {
			t.Errorf("results[%d].SourceID = %q, want %q (顺序应与输入一致)", i, r.SourceID, want)
		}
	}
}
