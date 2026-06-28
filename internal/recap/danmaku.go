package recap

import (
	"encoding/json"
	"sort"
	"strconv"
)

// Danmaku analysis types

type danmakuStats struct {
	TotalCount        int            `json:"total_count"`
	UniqueUsers       int            `json:"unique_users"`
	UniqueTexts       int            `json:"unique_texts"`
	DurationMin       float64        `json:"duration_min"`
	AvgPerMin         float64        `json:"avg_per_min"`
	TopDanmaku        []danmakuEntry `json:"top_danmaku"`
	HighWeightDanmaku []danmakuEntry `json:"high_weight_danmaku"`
	PeakMoments       []peakMoment   `json:"peak_moments"`
	BurstMoments      []burstMoment  `json:"burst_moments,omitempty"`
	Topics            []danmakuTopic `json:"topics,omitempty"`
	Keywords          []keywordCount `json:"keywords"`
}

type danmakuEntry struct {
	TimeMinSec string `json:"time"`
	Text       string `json:"text"`
}

type peakMoment struct {
	TimeMinSec string `json:"time"`
	Count      int    `json:"count"`
}

type burstMoment struct {
	TimeMinSec  string  `json:"time"`
	PeakCount   int     `json:"peak_count"`
	BurstFactor float64 `json:"burst_factor"`
}

type danmakuTopic struct {
	TimeRange   string   `json:"time_range"`
	Keyword     string   `json:"keyword"`
	Count       int      `json:"count"`
	SampleTexts []string `json:"sample_texts"`
}

type keywordCount struct {
	Word  string `json:"word"`
	Count int    `json:"count"`
}

// rawDanmakuItem matches the normalize output format
type rawDanmakuItem struct {
	TimeMS int64  `json:"time_ms"`
	Text   string `json:"text"`
	UserID string `json:"user_id,omitempty"`
	Weight int    `json:"weight,omitempty"`
}

// rawBiliDanmakuItem 匹配原始 B 站弹幕格式。
type rawBiliDanmakuItem struct {
	Stime  float64 `json:"stime"`
	Text   string  `json:"text"`
	Uhash  string  `json:"uhash,omitempty"`
	Weight int     `json:"weight,omitempty"`
}

// danmakuBucket is used internally for density-based analysis
type danmakuBucket struct {
	startMs int
	count   int
	items   []rawDanmakuItem
}

var danmakuKeywords = []string{"哈哈", "草", "好感动", "可爱", "加油", "6", "乐", "？", "完了", "乐"}

func analyzeDanmaku(data []byte, durationMs int64) (*danmakuStats, error) {
	return analyzeDanmakuWithWindow(data, durationMs, 30)
}

func analyzeDanmakuWithWindow(data []byte, durationMs int64, windowSec int) (*danmakuStats, error) {
	if windowSec <= 0 {
		windowSec = 30
	}
	var items []rawDanmakuItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	items, err := convertBiliDanmakuItems(data, items)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}

	stats := &danmakuStats{TotalCount: len(items)}
	windowMs := int64(windowSec) * 1000

	// Sub-analyses using extracted functions
	stats.UniqueUsers = computeUniqueUsers(items)
	stats.UniqueTexts = computeUniqueTexts(items)
	stats.DurationMin, stats.AvgPerMin = computeDensityStats(items, durationMs)
	stats.PeakMoments = detectPeakMoments(items, windowMs)

	// Score-based representative danmaku selection
	scored := scoreDanmaku(items, stats.PeakMoments)
	if len(scored) > 0 {
		stats.TopDanmaku = make([]danmakuEntry, len(scored))
		for i, s := range scored {
			stats.TopDanmaku[i] = s.Entry
		}
	} else {
		// Fallback: use bucket-based selection
		buckets := buildBuckets(items, windowMs)
		var sortedBuckets []*danmakuBucket
		for _, b := range buckets {
			sortedBuckets = append(sortedBuckets, b)
		}
		sort.Slice(sortedBuckets, func(i, j int) bool {
			return sortedBuckets[i].count > sortedBuckets[j].count
		})
		stats.TopDanmaku = selectRepresentativeDanmaku(sortedBuckets)
	}

	// Burst detection
	stats.BurstMoments = detectBurstMoments(items, windowMs, 3.0)

	// Topic clustering (2-minute windows)
	stats.Topics = clusterDanmakuTopics(items, 120000)

	stats.Keywords = computeKeywordStats(items)
	stats.HighWeightDanmaku = collectHighWeightDanmaku(items)

	return stats, nil
}

func convertBiliDanmakuItems(data []byte, items []rawDanmakuItem) ([]rawDanmakuItem, error) {
	if len(items) == 0 || items[0].TimeMS != 0 || !hasBiliStime(data) {
		return items, nil
	}

	var biliItems []rawBiliDanmakuItem
	if err := json.Unmarshal(data, &biliItems); err != nil {
		return nil, err
	}

	converted := make([]rawDanmakuItem, len(biliItems))
	for i, item := range biliItems {
		converted[i] = rawDanmakuItem{
			TimeMS: int64(item.Stime),
			Text:   item.Text,
			UserID: item.Uhash,
			Weight: item.Weight,
		}
	}
	return converted, nil
}

func hasBiliStime(data []byte) bool {
	var rawItems []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawItems); err != nil || len(rawItems) == 0 {
		return false
	}
	_, ok := rawItems[0]["stime"]
	return ok
}

func divmod(a, b int) (int, int) {
	return a / b, a % b
}

func formatMinSec(m, s int) string {
	return pad2(m) + ":" + pad2(s)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
