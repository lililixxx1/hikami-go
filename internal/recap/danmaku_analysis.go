package recap

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func computeUniqueUsers(items []rawDanmakuItem) int {
	userSet := make(map[string]struct{}, len(items))
	for _, d := range items {
		if d.UserID != "" {
			userSet[d.UserID] = struct{}{}
		}
	}
	return len(userSet)
}

func computeUniqueTexts(items []rawDanmakuItem) int {
	textSet := make(map[string]struct{}, len(items))
	for _, d := range items {
		t := strings.TrimSpace(d.Text)
		if t != "" {
			textSet[t] = struct{}{}
		}
	}
	return len(textSet)
}

func computeDensityStats(items []rawDanmakuItem, durationMs int64) (durMin float64, avgPerMin float64) {
	if durationMs <= 0 {
		return 0, 0
	}
	durMin = float64(durationMs) / 60000.0
	if durMin > 0 {
		avgPerMin = float64(len(items)) / durMin
	}
	return durMin, avgPerMin
}

func detectPeakMoments(items []rawDanmakuItem, windowMs int64) []peakMoment {
	buckets := buildBuckets(items, windowMs)

	var sortedBuckets []*danmakuBucket
	for _, b := range buckets {
		sortedBuckets = append(sortedBuckets, b)
	}
	sort.Slice(sortedBuckets, func(i, j int) bool {
		return sortedBuckets[i].count > sortedBuckets[j].count
	})

	limit := 10
	if len(sortedBuckets) < limit {
		limit = len(sortedBuckets)
	}
	peaks := sortedBuckets[:limit]
	sort.Slice(peaks, func(i, j int) bool {
		return peaks[i].startMs < peaks[j].startMs
	})

	var result []peakMoment
	for _, p := range peaks {
		m, s := divmod(p.startMs/1000, 60)
		result = append(result, peakMoment{
			TimeMinSec: formatMinSec(m, s),
			Count:      p.count,
		})
	}
	return result
}

// buildBuckets groups danmaku into time windows.
func buildBuckets(items []rawDanmakuItem, windowMs int64) map[int64]*danmakuBucket {
	buckets := make(map[int64]*danmakuBucket)
	for _, d := range items {
		b := d.TimeMS / windowMs
		if _, ok := buckets[b]; !ok {
			buckets[b] = &danmakuBucket{startMs: int(b * windowMs)}
		}
		buckets[b].count++
		buckets[b].items = append(buckets[b].items, d)
	}
	return buckets
}

// danmakuScore holds a scored danmaku entry for representative selection.
type danmakuScore struct {
	Entry   danmakuEntry
	Score   float64
	TimeMS  int64
	Weight  int
	TextLen int
}

// scoreDanmaku applies multi-factor scoring to select representative danmaku.
//
// Formula: score = (weight_norm × 2.0) + (uniqueness × 1.0) + (length_score × 0.5) + (context × 1.5)
func scoreDanmaku(items []rawDanmakuItem, peaks []peakMoment) []danmakuScore {
	if len(items) == 0 {
		return nil
	}

	// Build peak time set for context scoring
	peakMsSet := make(map[int64]bool)
	windowMs := int64(30000) // 30 second window
	for _, p := range peaks {
		// Parse MM:SS back to ms
		parts := strings.SplitN(p.TimeMinSec, ":", 2)
		if len(parts) != 2 {
			continue
		}
		m := parseInt(parts[0])
		s := parseInt(parts[1])
		if m < 0 || s < 0 {
			continue
		}
		centerMs := int64(m*60+s) * 1000
		for offset := -windowMs; offset <= windowMs; offset += windowMs {
			peakMsSet[centerMs+offset] = true
		}
	}

	// Track text occurrences for uniqueness
	textCount := make(map[string]int)
	for _, d := range items {
		t := strings.TrimSpace(d.Text)
		if t != "" {
			textCount[t]++
		}
	}

	var scored []danmakuScore
	for _, d := range items {
		text := strings.TrimSpace(d.Text)
		if text == "" || len(text) > 80 || len(text) < 2 {
			continue
		}

		// Weight factor (normalized 0-1)
		weightNorm := float64(d.Weight) / 10.0
		if weightNorm > 1.0 {
			weightNorm = 1.0
		}

		// Uniqueness factor
		uniqueness := 1.0
		if textCount[text] > 1 {
			uniqueness = 0.3
		}
		if textCount[text] > 10 {
			uniqueness = 0.1
		}

		// Length score (bell curve, optimal 10-30 chars)
		lengthScore := 0.0
		textLen := len([]rune(text))
		if textLen >= 5 && textLen <= 50 {
			// Gaussian-ish centered at 15
			lengthScore = math.Exp(-math.Pow(float64(textLen-15), 2) / 200.0)
		}

		// Context relevance (proximity to peak moments)
		context := 0.0
		if len(peakMsSet) > 0 {
			_, nearPeak := peakMsSet[d.TimeMS]
			if nearPeak {
				context = 1.0
			}
		}

		score := (weightNorm * 2.0) + (uniqueness * 1.0) + (lengthScore * 0.5) + (context * 1.5)

		sec := int(d.TimeMS / 1000)
		m, s := divmod(sec, 60)

		scored = append(scored, danmakuScore{
			Entry: danmakuEntry{
				TimeMinSec: formatMinSec(m, s),
				Text:       text,
			},
			Score:   score,
			TimeMS:  d.TimeMS,
			Weight:  d.Weight,
			TextLen: textLen,
		})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Deduplicate by text, keep highest scoring
	seen := make(map[string]struct{})
	var result []danmakuScore
	for _, s := range scored {
		if _, ok := seen[s.Entry.Text]; ok {
			continue
		}
		seen[s.Entry.Text] = struct{}{}
		result = append(result, s)
		if len(result) >= 30 {
			break
		}
	}

	return result
}

func selectRepresentativeDanmaku(sortedBucketsByCount []*danmakuBucket) []danmakuEntry {
	seen := make(map[string]struct{})
	var result []danmakuEntry
	for _, b := range sortedBucketsByCount {
		picked := 0
		for _, d := range b.items {
			text := strings.TrimSpace(d.Text)
			if text == "" || len(text) > 50 {
				continue
			}
			if _, ok := seen[text]; ok {
				continue
			}
			seen[text] = struct{}{}
			sec := int(d.TimeMS / 1000)
			m, s := divmod(sec, 60)
			result = append(result, danmakuEntry{
				TimeMinSec: formatMinSec(m, s),
				Text:       text,
			})
			picked++
			if picked >= 3 || len(result) >= 30 {
				break
			}
		}
		if len(result) >= 30 {
			break
		}
	}
	return result
}

// detectBurstMoments finds windows where danmaku rate exceeds baseline × burstFactor.
func detectBurstMoments(items []rawDanmakuItem, windowMs int64, burstFactor float64) []burstMoment {
	if len(items) == 0 || windowMs <= 0 {
		return nil
	}
	if burstFactor <= 0 {
		burstFactor = 3.0
	}

	buckets := buildBuckets(items, windowMs)
	if len(buckets) == 0 {
		return nil
	}

	// Compute baseline rate (average count per window)
	totalCount := 0
	for _, b := range buckets {
		totalCount += b.count
	}
	baseline := float64(totalCount) / float64(len(buckets))
	if baseline < 1 {
		return nil
	}

	// Find burst windows
	var bursts []burstMoment
	for _, b := range buckets {
		rate := float64(b.count) / baseline
		if rate >= burstFactor {
			m, s := divmod(b.startMs/1000, 60)
			bursts = append(bursts, burstMoment{
				TimeMinSec:  formatMinSec(m, s),
				PeakCount:   b.count,
				BurstFactor: math.Round(rate*100) / 100,
			})
		}
	}

	// Sort by burst factor descending, keep top 10
	sort.Slice(bursts, func(i, j int) bool {
		return bursts[i].BurstFactor > bursts[j].BurstFactor
	})
	if len(bursts) > 10 {
		bursts = bursts[:10]
	}

	// Re-sort by time for output
	sort.Slice(bursts, func(i, j int) bool {
		return bursts[i].TimeMinSec < bursts[j].TimeMinSec
	})

	return bursts
}

// clusterDanmakuTopics groups consecutive danmaku by dominant keyword in 2-minute windows.
func clusterDanmakuTopics(items []rawDanmakuItem, windowMs int64) []danmakuTopic {
	if len(items) == 0 || windowMs <= 0 {
		return nil
	}

	// Default 2-minute windows
	if windowMs <= 0 {
		windowMs = 120000
	}

	buckets := buildBuckets(items, windowMs)

	type windowTopic struct {
		startMs int
		endMs   int
		keyword string
		count   int
		samples []string
	}

	var topics []windowTopic
	for _, b := range buckets {
		if b.count < 3 {
			continue
		}
		// Find dominant word (>1 char, not pure punctuation/numbers)
		wordCount := make(map[string]int)
		for _, d := range b.items {
			text := strings.TrimSpace(d.Text)
			for _, word := range extractWords(text) {
				wordCount[word]++
			}
		}
		var bestWord string
		bestCount := 0
		for w, c := range wordCount {
			if !isTopicKeywordCandidate(w) {
				continue
			}
			if c > bestCount {
				bestCount = c
				bestWord = w
			}
		}
		if bestWord == "" || bestCount < 2 {
			continue
		}

		// Collect sample texts
		var samples []string
		seen := make(map[string]struct{})
		for _, d := range b.items {
			t := strings.TrimSpace(d.Text)
			if t == "" {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			samples = append(samples, t)
			if len(samples) >= 5 {
				break
			}
		}

		topics = append(topics, windowTopic{
			startMs: b.startMs,
			endMs:   b.startMs + int(windowMs),
			keyword: bestWord,
			count:   bestCount,
			samples: samples,
		})
	}

	// Merge adjacent windows with same keyword
	var merged []windowTopic
	for _, t := range topics {
		if len(merged) > 0 && merged[len(merged)-1].keyword == t.keyword && t.startMs <= merged[len(merged)-1].endMs+int(windowMs) {
			last := &merged[len(merged)-1]
			last.endMs = t.endMs
			if t.count > last.count {
				last.count = t.count
			}
			last.samples = append(last.samples, t.samples...)
		} else {
			merged = append(merged, t)
		}
	}

	// Sort by count, keep top 10
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].count > merged[j].count
	})
	if len(merged) > 10 {
		merged = merged[:10]
	}
	// Re-sort by time
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].startMs < merged[j].startMs
	})

	var result []danmakuTopic
	for _, t := range merged {
		startM, startS := divmod(t.startMs/1000, 60)
		endM, endS := divmod(t.endMs/1000, 60)
		result = append(result, danmakuTopic{
			TimeRange:   fmt.Sprintf("%s-%s", formatMinSec(startM, startS), formatMinSec(endM, endS)),
			Keyword:     t.keyword,
			Count:       t.count,
			SampleTexts: t.samples,
		})
	}
	return result
}

// extractWords splits text into candidate words (>1 rune, not stop words).
func extractWords(text string) []string {
	var words []string
	for _, r := range []rune(text) {
		_ = r
		break
	}
	// Simple approach: split by whitespace and common delimiters
	for _, segment := range strings.Fields(text) {
		segment = strings.Trim(segment, "，。！？、,.!?~～…：:;；")
		if len([]rune(segment)) < 2 {
			continue
		}
		// Skip pure numbers and common stop words
		if isAllDigits(segment) {
			continue
		}
		if isStopWord(segment) {
			continue
		}
		words = append(words, segment)
	}
	return words
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var stopWords = map[string]bool{
	"哈哈": true, "草": true, "好": true, "了": true, "的": true,
	"是": true, "在": true, "有": true, "和": true, "就": true,
	"不": true, "都": true, "这": true, "我": true, "你": true,
	"他": true, "她": true, "吗": true, "吧": true, "啊": true,
	"嗯": true, "哦": true, "呢": true, "呀": true, "哈": true,
	"233": true, "hhh": true, "hhhh": true,
	"111": true, "222": true, "...": true, "……": true,
}

func isStopWord(s string) bool {
	return stopWords[s]
}

func isTopicKeywordCandidate(s string) bool {
	s = strings.TrimSpace(s)
	runeLen := len([]rune(s))
	if runeLen < 2 || runeLen > 18 {
		return false
	}
	if strings.ContainsAny(s, "|<>") || strings.Contains(s, "爆了") || strings.Contains(s, "哈哈") {
		return false
	}
	if isAllDigits(s) || isStopWord(s) {
		return false
	}
	return true
}

func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			return -1
		}
	}
	return n
}

func computeKeywordStats(items []rawDanmakuItem) []keywordCount {
	var result []keywordCount
	for _, kw := range danmakuKeywords {
		count := 0
		for _, d := range items {
			if strings.Contains(d.Text, kw) {
				count++
			}
		}
		if count > 0 {
			result = append(result, keywordCount{Word: kw, Count: count})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})
	return result
}

func collectHighWeightDanmaku(items []rawDanmakuItem) []danmakuEntry {
	seen := make(map[string]struct{})
	var result []danmakuEntry
	for _, d := range items {
		if d.Weight < 8 {
			continue
		}
		text := strings.TrimSpace(d.Text)
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		sec := int(d.TimeMS / 1000)
		m, s := divmod(sec, 60)
		result = append(result, danmakuEntry{
			TimeMinSec: formatMinSec(m, s),
			Text:       text,
		})
		if len(result) >= 20 {
			break
		}
	}
	return result
}
