package glossary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/db"
	"hikami-go/internal/session"
)

type mockProvider struct {
	fn func(ctx context.Context, systemPrompt string, prompt string, sess session.Session) (aiprovider.GenerateResult, error)
}

func (m *mockProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sess session.Session) (aiprovider.GenerateResult, error) {
	if m.fn == nil {
		return aiprovider.GenerateResult{Content: `{"items":[]}`, FinishReason: "stop"}, nil
	}
	return m.fn(ctx, systemPrompt, prompt, sess)
}

func openDiscoveryTestDB(t *testing.T) (*Store, *session.Store) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	return NewStore(database), session.NewStore(database)
}

func TestChunkText_Basic(t *testing.T) {
	d := &Discoverer{chunkChars: 100, maxChunks: 8}
	text := strings.Repeat("这是一段测试文本。", 20)
	chunks := d.buildChunks([]byte(text), nil)
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}
	totalLen := 0
	for _, c := range chunks {
		if c.Text == "" {
			t.Fatal("chunk text should not be empty")
		}
		totalLen += len(c.Text)
		t.Logf("chunk %d: %d chars", c.Index, len(c.Text))
	}
	if totalLen == 0 {
		t.Fatal("total text length should be > 0")
	}
}

func TestChunkText_Segments(t *testing.T) {
	d := &Discoverer{chunkChars: 100, maxChunks: 8}
	segments := []TranscriptSegment{
		{StartMS: 0, EndMS: 5000, Text: "第一段话"},
		{StartMS: 5000, EndMS: 10000, Text: "第二段话"},
		{StartMS: 10000, EndMS: 15000, Text: "第三段话"},
	}
	chunks := d.buildChunks(nil, segments)
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk from segments")
	}
	if chunks[0].StartMS != 0 {
		t.Fatalf("expected start_ms 0, got %d", chunks[0].StartMS)
	}
	t.Logf("got %d chunks from segments", len(chunks))
}

func TestChunkText_MaxChunks(t *testing.T) {
	d := &Discoverer{chunkChars: 50, maxChunks: 3}
	text := strings.Repeat("段落内容", 100)
	chunks := d.buildChunks([]byte(text), nil)
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}
	if len(chunks) > 3 {
		t.Logf("buildChunks produced %d chunks, Discover will truncate to %d", len(chunks), d.maxChunks)
	} else {
		t.Logf("maxChunks=3, got %d chunks", len(chunks))
	}
}

func TestDiscover_EmptyTranscript(t *testing.T) {
	store, _ := openDiscoveryTestDB(t)
	provider := &mockProvider{}
	d := NewDiscoverer(store, provider, nil, WithDiscoveryChunkChars(1000))

	err := d.Discover(context.Background(), "ch1", "s1", nil, nil, "")
	if err != nil {
		t.Fatalf("expected nil error for empty transcript, got: %v", err)
	}
}

func TestDiscover_ProviderError(t *testing.T) {
	store, _ := openDiscoveryTestDB(t)
	provider := &mockProvider{
		fn: func(ctx context.Context, systemPrompt string, prompt string, sess session.Session) (aiprovider.GenerateResult, error) {
			return aiprovider.GenerateResult{}, fmt.Errorf("provider error")
		},
	}
	d := NewDiscoverer(store, provider, nil,
		WithDiscoveryChunkChars(1000),
		WithDiscoveryMaxRetries(0),
		WithDiscoveryRetryDelay(time.Millisecond),
	)

	text := []byte("这是一段足够长的测试文本内容" + strings.Repeat("填充", 500))
	err := d.Discover(context.Background(), "ch1", "s1", text, nil, "")
	if err == nil {
		t.Fatal("expected error from provider")
	}
	t.Logf("got expected error: %v", err)
}

func TestDiscover_InvalidJSON(t *testing.T) {
	store, _ := openDiscoveryTestDB(t)
	provider := &mockProvider{
		fn: func(ctx context.Context, systemPrompt string, prompt string, sess session.Session) (aiprovider.GenerateResult, error) {
			return aiprovider.GenerateResult{Content: "not valid json at all", FinishReason: "stop"}, nil
		},
	}
	d := NewDiscoverer(store, provider, nil, WithDiscoveryChunkChars(1000))

	text := []byte("这是一段足够长的测试文本内容" + strings.Repeat("填充", 500))
	err := d.Discover(context.Background(), "ch1", "s1", text, nil, "")
	if err == nil {
		t.Fatal("expected error from invalid JSON")
	}
	t.Logf("got expected error: %v", err)
}

func TestDiscover_Success(t *testing.T) {
	store, _ := openDiscoveryTestDB(t)
	response := DiscoveryResult{
		Items: []DiscoveryItem{
			{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.9, OccurrenceCount: 3, Reason: "多次提到"},
			{Term: "星铁", Canonical: "崩坏星穹铁道", Category: "游戏", Confidence: 0.85, OccurrenceCount: 2, Reason: "主播简称"},
		},
	}
	respJSON, _ := json.Marshal(response)
	provider := &mockProvider{
		fn: func(ctx context.Context, systemPrompt string, prompt string, sess session.Session) (aiprovider.GenerateResult, error) {
			return aiprovider.GenerateResult{Content: string(respJSON), FinishReason: "stop"}, nil
		},
	}
	d := NewDiscoverer(store, provider, nil, WithDiscoveryChunkChars(1000))

	text := []byte("这是一段足够长的测试文本内容" + strings.Repeat("原神星铁填充", 100))
	err := d.Discover(context.Background(), "ch1", "s1", text, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	candidates, err := store.ListCandidates(context.Background(), "ch1", "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	t.Logf("discovered %d candidates", len(candidates))
}

func TestDiscover_RetrySuccess(t *testing.T) {
	store, _ := openDiscoveryTestDB(t)
	response := DiscoveryResult{
		Items: []DiscoveryItem{
			{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.9, OccurrenceCount: 1, Reason: "重试后成功"},
		},
	}
	respJSON, _ := json.Marshal(response)
	callCount := 0
	provider := &mockProvider{
		fn: func(ctx context.Context, systemPrompt string, prompt string, sess session.Session) (aiprovider.GenerateResult, error) {
			callCount++
			if callCount == 1 {
				return aiprovider.GenerateResult{}, fmt.Errorf("temporary provider error")
			}
			return aiprovider.GenerateResult{Content: string(respJSON), FinishReason: "stop"}, nil
		},
	}
	d := NewDiscoverer(store, provider, nil,
		WithDiscoveryChunkChars(1000),
		WithDiscoveryMaxRetries(1),
		WithDiscoveryRetryDelay(time.Millisecond),
	)

	text := []byte("这是一段足够长的测试文本内容" + strings.Repeat("原神填充", 100))
	err := d.Discover(context.Background(), "ch1", "s1", text, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 generate calls, got %d", callCount)
	}

	candidates, err := store.ListCandidates(context.Background(), "ch1", "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
}

func TestDiscover_RetryExhausted(t *testing.T) {
	store, _ := openDiscoveryTestDB(t)
	callCount := 0
	provider := &mockProvider{
		fn: func(ctx context.Context, systemPrompt string, prompt string, sess session.Session) (aiprovider.GenerateResult, error) {
			callCount++
			return aiprovider.GenerateResult{}, fmt.Errorf("provider error")
		},
	}
	d := NewDiscoverer(store, provider, nil,
		WithDiscoveryChunkChars(1000),
		WithDiscoveryMaxRetries(1),
		WithDiscoveryRetryDelay(time.Millisecond),
	)

	text := []byte("这是一段足够长的测试文本内容" + strings.Repeat("原神填充", 100))
	err := d.Discover(context.Background(), "ch1", "s1", text, nil, "")
	if err == nil {
		t.Fatal("expected error from exhausted retries")
	}
	if callCount != 2 {
		t.Fatalf("expected 2 generate calls, got %d", callCount)
	}
	t.Logf("got expected error: %v", err)
}

func TestDiscover_MergeCandidates(t *testing.T) {
	store, _ := openDiscoveryTestDB(t)
	callCount := 0
	provider := &mockProvider{
		fn: func(ctx context.Context, systemPrompt string, prompt string, sess session.Session) (aiprovider.GenerateResult, error) {
			callCount++
			resp := DiscoveryResult{
				Items: []DiscoveryItem{
					{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.8, OccurrenceCount: 2, Reason: fmt.Sprintf("chunk%d", callCount)},
				},
			}
			respJSON, _ := json.Marshal(resp)
			return aiprovider.GenerateResult{Content: string(respJSON), FinishReason: "stop"}, nil
		},
	}
	d := NewDiscoverer(store, provider, nil, WithDiscoveryChunkChars(200), WithDiscoveryMaxChunks(4))

	text := []byte(strings.Repeat("这是一段测试文本内容关于原神的讨论。", 200))
	err := d.Discover(context.Background(), "ch1", "s1", text, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	candidates, _ := store.ListCandidates(context.Background(), "ch1", "pending")
	if len(candidates) != 1 {
		t.Fatalf("expected 1 merged candidate, got %d", len(candidates))
	}
	if candidates[0].OccurrenceCount < 2 {
		t.Fatalf("expected merged occurrence_count >= 2, got %d", candidates[0].OccurrenceCount)
	}
	t.Logf("merged: occ=%d sess=%d score=%.4f", candidates[0].OccurrenceCount, candidates[0].SessionCount, candidates[0].Score)
}

func TestBuildDiscoveryPrompt(t *testing.T) {
	chunk := DiscoveryChunk{Index: 1, StartMS: 30000, EndMS: 60000, Text: "测试文本内容"}
	prompt := buildDiscoveryUserPrompt("| AI | 人工智能 | 技术 |", chunk)

	if !strings.Contains(prompt, "AI") {
		t.Fatal("prompt should contain existing glossary")
	}
	if !strings.Contains(prompt, "测试文本内容") {
		t.Fatal("prompt should contain chunk text")
	}
	if !strings.Contains(prompt, "片段序号：1") {
		t.Fatal("prompt should contain chunk index")
	}
	if !strings.Contains(prompt, "00:30") {
		t.Fatal("prompt should contain start timestamp")
	}
	if !strings.Contains(prompt, "01:00") {
		t.Fatal("prompt should contain end timestamp")
	}
}

func TestBuildDiscoveryPrompt_EmptyGlossary(t *testing.T) {
	chunk := DiscoveryChunk{Index: 1, Text: "测试"}
	prompt := buildDiscoveryUserPrompt("", chunk)
	if !strings.Contains(prompt, "（空）") {
		t.Fatal("prompt should show empty glossary marker")
	}
}

func TestParseDiscoveryResult(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectCount int
	}{
		{"正常JSON", `{"items":[{"term":"原神","canonical":"原神","category":"游戏","confidence":0.9,"occurrence_count":1,"reason":"测试"}]}`, 1},
		{"带markdown标记", "```json\n{\"items\":[]}\n```", 0},
		{"空items", `{"items":[]}`, 0},
		{"缺少term跳过", `{"items":[{"term":"","canonical":"原神","category":"游戏","confidence":0.9,"occurrence_count":1,"reason":""}]}`, 0},
		{"缺少canonical跳过", `{"items":[{"term":"原神","canonical":"","category":"游戏","confidence":0.9,"occurrence_count":1,"reason":""}]}`, 0},
		{"多个有效", `{"items":[{"term":"原神","canonical":"原神","category":"游戏","confidence":0.9,"occurrence_count":1,"reason":"a"},{"term":"星铁","canonical":"崩坏星穹铁道","category":"游戏","confidence":0.8,"occurrence_count":2,"reason":"b"}]}`, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := parseDiscoveryResult(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(items) != tt.expectCount {
				t.Fatalf("expected %d items, got %d", tt.expectCount, len(items))
			}
		})
	}
}

func TestDiscover_Timestamp(t *testing.T) {
	tests := []struct {
		ms       int64
		expected string
	}{
		{0, "00:00"},
		{30000, "00:30"},
		{90000, "01:30"},
		{3600000, "01:00:00"},
		{3661000, "01:01:01"},
	}
	for _, tt := range tests {
		got := formatDiscoveryTimestamp(tt.ms)
		if got != tt.expected {
			t.Errorf("formatDiscoveryTimestamp(%d) = %q, want %q", tt.ms, got, tt.expected)
		}
	}
}
