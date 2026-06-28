package glossary

import (
	"context"
	"math"
	"testing"

	"hikami-go/internal/db"
)

func openCandidateTestDB(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	return NewStore(database)
}

func TestUpsertCandidate_New(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item := DiscoveryItem{
		Term:            "原神",
		Canonical:       "原神",
		Category:        "游戏",
		Confidence:      0.9,
		OccurrenceCount: 5,
		Reason:          "主播反复提到",
	}
	err := s.UpsertCandidate(ctx, "ch1", item, "sess1")
	if err != nil {
		t.Fatal(err)
	}

	candidates, err := s.ListCandidates(ctx, "ch1", "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.Term != "原神" {
		t.Fatalf("expected term 原神, got %q", c.Term)
	}
	if c.Canonical != "原神" {
		t.Fatalf("expected canonical 原神, got %q", c.Canonical)
	}
	if c.Category != "游戏" {
		t.Fatalf("expected category 游戏, got %q", c.Category)
	}
	if c.Status != "pending" {
		t.Fatalf("expected status pending, got %q", c.Status)
	}
	if c.Confidence != 0.9 {
		t.Fatalf("expected confidence 0.9, got %f", c.Confidence)
	}
	if c.OccurrenceCount != 5 {
		t.Fatalf("expected occurrence_count 5, got %d", c.OccurrenceCount)
	}
	if c.SessionCount != 1 {
		t.Fatalf("expected session_count 1, got %d", c.SessionCount)
	}
	if c.FirstSessionID != "sess1" {
		t.Fatalf("expected first_session_id sess1, got %q", c.FirstSessionID)
	}
	if c.LastSessionID != "sess1" {
		t.Fatalf("expected last_session_id sess1, got %q", c.LastSessionID)
	}
	if c.Reason != "主播反复提到" {
		t.Fatalf("expected reason '主播反复提到', got %q", c.Reason)
	}
	if c.Score <= 0 {
		t.Fatalf("expected positive score, got %f", c.Score)
	}
	t.Logf("score = %.4f", c.Score)
}

func TestUpsertCandidate_Merge(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item1 := DiscoveryItem{
		Term:            "原神",
		Canonical:       "原神",
		Category:        "游戏",
		Confidence:      0.8,
		OccurrenceCount: 3,
		Reason:          "第一次",
	}
	if err := s.UpsertCandidate(ctx, "ch1", item1, "sess1"); err != nil {
		t.Fatal(err)
	}

	item2 := DiscoveryItem{
		Term:            "原神",
		Canonical:       "原神",
		Category:        "游戏名",
		Confidence:      0.95,
		OccurrenceCount: 7,
		Reason:          "第二次",
	}
	if err := s.UpsertCandidate(ctx, "ch1", item2, "sess2"); err != nil {
		t.Fatal(err)
	}

	candidates, err := s.ListCandidates(ctx, "ch1", "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate after merge, got %d", len(candidates))
	}
	c := candidates[0]
	if c.OccurrenceCount != 10 {
		t.Fatalf("expected occurrence_count 10, got %d", c.OccurrenceCount)
	}
	if c.SessionCount != 2 {
		t.Fatalf("expected session_count 2, got %d", c.SessionCount)
	}
	if c.Confidence != 0.95 {
		t.Fatalf("expected max confidence 0.95, got %f", c.Confidence)
	}
	if c.Category != "游戏名" {
		t.Fatalf("expected updated category 游戏名, got %q", c.Category)
	}
	if c.Reason != "第二次" {
		t.Fatalf("expected updated reason '第二次', got %q", c.Reason)
	}
	if c.LastSessionID != "sess2" {
		t.Fatalf("expected last_session_id sess2, got %q", c.LastSessionID)
	}
	if c.FirstSessionID != "sess1" {
		t.Fatalf("expected first_session_id sess1, got %q", c.FirstSessionID)
	}
}

func TestCalculateCandidateScore(t *testing.T) {
	tests := []struct {
		name           string
		confidence     float64
		occurrence     int
		session        int
		expectedApprox float64
	}{
		{"高分", 1.0, 10, 5, 1.0},
		{"低分", 0.0, 0, 0, 0.0},
		{"中等", 0.5, 3, 1, 0.65*0.5 + 0.20*(1.0/3.0) + 0.15*(math.Log1p(3)/math.Log1p(8))},
		{"超范围confidence", 1.5, 0, 0, 0.65},
		{"负confidence", -0.5, 0, 0, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateCandidateScore(tt.confidence, tt.occurrence, tt.session)
			if score < 0 || score > 1 {
				t.Fatalf("score out of range [0,1]: %f", score)
			}
			rounded := math.Round(score*10000) / 10000
			expectedRounded := math.Round(tt.expectedApprox*10000) / 10000
			if tt.expectedApprox > 0 && rounded != expectedRounded {
				t.Fatalf("expected ~%.4f, got %.4f", expectedRounded, rounded)
			}
			t.Logf("%s: confidence=%.2f occ=%d sess=%d => score=%.4f", tt.name, tt.confidence, tt.occurrence, tt.session, score)
		})
	}
}

func TestListCandidates_StatusFilter(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item := DiscoveryItem{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.9, OccurrenceCount: 1, Reason: "测试"}
	if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
		t.Fatal(err)
	}

	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	if len(candidates) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(candidates))
	}

	candidates, _ = s.ListCandidates(ctx, "ch1", "approved")
	if len(candidates) != 0 {
		t.Fatalf("expected 0 approved, got %d", len(candidates))
	}

	candidates, _ = s.ListCandidates(ctx, "ch1", "all")
	if len(candidates) != 1 {
		t.Fatalf("expected 1 all, got %d", len(candidates))
	}
}

func TestListCandidates_ChannelScope(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item := DiscoveryItem{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.9, OccurrenceCount: 1, Reason: "测试"}
	if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertCandidate(ctx, "ch2", item, "s1"); err != nil {
		t.Fatal(err)
	}

	c1, _ := s.ListCandidates(ctx, "ch1", "pending")
	if len(c1) != 1 {
		t.Fatalf("expected 1 for ch1, got %d", len(c1))
	}
	c2, _ := s.ListCandidates(ctx, "ch2", "pending")
	if len(c2) != 1 {
		t.Fatalf("expected 1 for ch2, got %d", len(c2))
	}
	if c1[0].ChannelID != "ch1" || c2[0].ChannelID != "ch2" {
		t.Fatal("channel isolation failed")
	}
}

func TestApproveCandidate(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item := DiscoveryItem{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.9, OccurrenceCount: 1, Reason: "测试"}
	if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
		t.Fatal(err)
	}

	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	id := candidates[0].ID

	if err := s.ApproveCandidate(ctx, id, "", "", ""); err != nil {
		t.Fatal(err)
	}

	approved, _ := s.ListCandidates(ctx, "ch1", "approved")
	if len(approved) != 1 {
		t.Fatalf("expected 1 approved candidate, got %d", len(approved))
	}

	entries, err := s.queryEntries(ctx, sqlListByChannel, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 glossary entry after approve, got %d", len(entries))
	}
	if entries[0].Term != "原神" {
		t.Fatalf("expected term 原神, got %q", entries[0].Term)
	}
	if !entries[0].Enabled {
		t.Fatal("expected glossary entry to be enabled")
	}
}

func TestRejectCandidate(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item := DiscoveryItem{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.9, OccurrenceCount: 1, Reason: "测试"}
	if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
		t.Fatal(err)
	}

	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	id := candidates[0].ID

	if err := s.RejectCandidate(ctx, id); err != nil {
		t.Fatal(err)
	}

	pending, _ := s.ListCandidates(ctx, "ch1", "pending")
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending after reject, got %d", len(pending))
	}

	rejected, _ := s.ListCandidates(ctx, "ch1", "rejected")
	if len(rejected) != 1 {
		t.Fatalf("expected 1 rejected, got %d", len(rejected))
	}
}

func TestBatchApprove(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	for _, term := range []string{"原神", "星铁", "绝区零"} {
		item := DiscoveryItem{Term: term, Canonical: term, Category: "游戏", Confidence: 0.8, OccurrenceCount: 1, Reason: "测试"}
		if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
			t.Fatal(err)
		}
	}

	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	if len(candidates) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(candidates))
	}

	var ids []int64
	for _, c := range candidates {
		ids = append(ids, c.ID)
	}

	n, err := s.BatchApproveCandidates(ctx, "ch1", ids)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("expected 3 approved, got %d", n)
	}

	approved, _ := s.ListCandidates(ctx, "ch1", "approved")
	if len(approved) != 3 {
		t.Fatalf("expected 3 approved, got %d", len(approved))
	}

	entries, _ := s.queryEntries(ctx, sqlListByChannel, "ch1")
	if len(entries) != 3 {
		t.Fatalf("expected 3 glossary entries, got %d", len(entries))
	}
}

func TestBatchReject(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	for _, term := range []string{"原神", "星铁"} {
		item := DiscoveryItem{Term: term, Canonical: term, Category: "游戏", Confidence: 0.8, OccurrenceCount: 1, Reason: "测试"}
		if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
			t.Fatal(err)
		}
	}

	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	var ids []int64
	for _, c := range candidates {
		ids = append(ids, c.ID)
	}

	n, err := s.BatchRejectCandidates(ctx, "ch1", ids)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rejected, got %d", n)
	}

	pending, _ := s.ListCandidates(ctx, "ch1", "pending")
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending after batch reject, got %d", len(pending))
	}
}

func TestApproveAlreadyApproved(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item := DiscoveryItem{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.9, OccurrenceCount: 1, Reason: "测试"}
	if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
		t.Fatal(err)
	}

	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	id := candidates[0].ID

	if err := s.ApproveCandidate(ctx, id, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveCandidate(ctx, id, "", "", ""); err != nil {
		t.Fatalf("second approve should be idempotent, got: %v", err)
	}

	entries, _ := s.queryEntries(ctx, sqlListByChannel, "ch1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 glossary entry (idempotent), got %d", len(entries))
	}
}

func TestGetCandidateByID(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item := DiscoveryItem{Term: "原神", Canonical: "原神", Category: "游戏", Confidence: 0.9, OccurrenceCount: 1, Reason: "测试"}
	if err := s.UpsertCandidate(ctx, "ch1", item, "s1"); err != nil {
		t.Fatal(err)
	}

	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	id := candidates[0].ID

	got, err := s.GetCandidate(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Term != "原神" {
		t.Fatalf("expected term 原神, got %q", got.Term)
	}

	_, err = s.GetCandidate(ctx, 99999)
	if err == nil {
		t.Fatal("expected error for non-existent candidate")
	}
}

func TestUpsertCandidate_NormalizedKey(t *testing.T) {
	s := openCandidateTestDB(t)
	ctx := context.Background()

	item1 := DiscoveryItem{Term: " 原神 ", Canonical: "原神", Category: "游戏", Confidence: 0.8, OccurrenceCount: 2, Reason: "测试1"}
	if err := s.UpsertCandidate(ctx, "ch1", item1, "s1"); err != nil {
		t.Fatal(err)
	}

	item2 := DiscoveryItem{Term: "原神", Canonical: " 原神 ", Category: "游戏", Confidence: 0.9, OccurrenceCount: 3, Reason: "测试2"}
	if err := s.UpsertCandidate(ctx, "ch1", item2, "s1"); err != nil {
		t.Fatal(err)
	}

	candidates, _ := s.ListCandidates(ctx, "ch1", "pending")
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (merged by normalized_key), got %d", len(candidates))
	}
	if candidates[0].OccurrenceCount != 5 {
		t.Fatalf("expected occurrence_count 5, got %d", candidates[0].OccurrenceCount)
	}

	key1 := normalizeKey(" 原神 ", "原神")
	key2 := normalizeKey("原神", " 原神 ")
	if key1 != key2 {
		t.Fatalf("normalized keys should match: %q vs %q", key1, key2)
	}
	t.Logf("normalized_key = %q", key1)
}
