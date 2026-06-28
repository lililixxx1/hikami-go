package recap

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// topicBoundary represents a detected topic transition point in the stream.
type topicBoundary struct {
	TimeSec    float64
	Confidence float64 // 0-1
	Reason     string  // "silence_gap", "danmaku_burst", "danmaku_drop"
}

// segmentSuggestion is a recommended segment for the recap.
type segmentSuggestion struct {
	StartSec   float64
	EndSec     float64
	Label      string // e.g. "00:00-05:30"
	Confidence float64
}

// detectTopicBoundaries finds natural segment boundaries by analyzing
// SRT/VTT timing gaps and danmaku density changes.
func detectTopicBoundaries(srtContent string, stats *danmakuStats, meta *sessionMetadata) []topicBoundary {
	if meta == nil || meta.DurationMs < 30*60*1000 {
		return nil // Only for streams > 30 minutes
	}

	var boundaries []topicBoundary

	// 1. Detect silence gaps from SRT/VTT timing data
	if srtContent != "" {
		gaps := detectSilenceGaps(srtContent)
		for _, g := range gaps {
			boundaries = append(boundaries, topicBoundary{
				TimeSec:    g,
				Confidence: 0.7,
				Reason:     "silence_gap",
			})
		}
	}

	// 2. Detect danmaku density changes (burst starts/ends)
	if stats != nil && len(stats.BurstMoments) > 0 {
		for _, bm := range stats.BurstMoments {
			sec := parseMinSecToSec(bm.TimeMinSec)
			if sec > 0 {
				boundaries = append(boundaries, topicBoundary{
					TimeSec:    sec,
					Confidence: math.Min(float64(bm.BurstFactor)/5.0, 1.0),
					Reason:     "danmaku_burst",
				})
			}
		}
	}

	// 3. Merge nearby boundaries (within 60 seconds of each other)
	boundaries = mergeBoundaries(boundaries, 60)

	return boundaries
}

// buildSegmentSuggestions divides the stream into segments based on detected boundaries.
func buildSegmentSuggestions(boundaries []topicBoundary, totalDurationMs int64) []segmentSuggestion {
	if len(boundaries) == 0 || totalDurationMs <= 0 {
		return nil
	}

	totalSec := float64(totalDurationMs) / 1000.0

	// Sort boundaries by time
	sort.Slice(boundaries, func(i, j int) bool {
		return boundaries[i].TimeSec < boundaries[j].TimeSec
	})

	// Build segments from boundaries
	type cutPoint struct {
		sec        float64
		confidence float64
	}
	var cuts []cutPoint
	for _, b := range boundaries {
		// Skip boundaries too close to start or end
		if b.TimeSec < 60 || b.TimeSec > totalSec-60 {
			continue
		}
		cuts = append(cuts, cutPoint{sec: b.TimeSec, confidence: b.Confidence})
	}

	// Limit to reasonable number of segments (max 12 for a long stream)
	// Sort by confidence, keep top N
	if len(cuts) > 12 {
		sort.Slice(cuts, func(i, j int) bool {
			return cuts[i].confidence > cuts[j].confidence
		})
		cuts = cuts[:12]
		sort.Slice(cuts, func(i, j int) bool {
			return cuts[i].sec < cuts[j].sec
		})
	}

	// Build segment suggestions
	var segments []segmentSuggestion
	start := 0.0
	for _, c := range cuts {
		segments = append(segments, segmentSuggestion{
			StartSec:   start,
			EndSec:     c.sec,
			Label:      formatSecRange(start, c.sec),
			Confidence: c.confidence,
		})
		start = c.sec
	}
	// Final segment
	segments = append(segments, segmentSuggestion{
		StartSec:   start,
		EndSec:     totalSec,
		Label:      formatSecRange(start, totalSec),
		Confidence: 0.5,
	})

	return segments
}

// formatSegmentSuggestionsForPrompt formats segments for AI prompt injection.
func formatSegmentSuggestionsForPrompt(segments []segmentSuggestion) string {
	if len(segments) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## 建议分段方案\n\n")
	b.WriteString("基于弹幕密度和转写间隔分析，建议按以下分段组织回顾内容（仅供参考，可根据实际话题调整）：\n\n")
	for i, s := range segments {
		b.WriteString(fmt.Sprintf("%d. %s", i+1, s.Label))
		if s.Confidence >= 0.7 {
			b.WriteString("（高置信）")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// detectSilenceGaps parses SRT/VTT content and finds gaps > thresholdSeconds between segments.
func detectSilenceGaps(content string) []float64 {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	blocks := strings.Split(content, "\n\n")

	var endTimes []float64
	var startTimes []float64

	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		for _, line := range lines {
			if !strings.Contains(line, "-->") {
				continue
			}
			start, end, ok := parseCaptionRange(line)
			if !ok {
				continue
			}
			startTimes = append(startTimes, start)
			endTimes = append(endTimes, end)
			break
		}
	}

	if len(endTimes) < 2 {
		return nil
	}

	// Sort by end time
	type segment struct{ start, end float64 }
	var segs []segment
	for i := range endTimes {
		segs = append(segs, segment{start: startTimes[i], end: endTimes[i]})
	}
	sort.Slice(segs, func(i, j int) bool {
		return segs[i].start < segs[j].start
	})

	// Detect gaps > 5 seconds
	var gaps []float64
	threshold := 5.0
	for i := 1; i < len(segs); i++ {
		gap := segs[i].start - segs[i-1].end
		if gap > threshold {
			// Boundary at the midpoint of the gap
			gaps = append(gaps, segs[i-1].end+gap/2)
		}
	}

	return gaps
}

// mergeBoundaries combines boundaries within minDist seconds of each other.
func mergeBoundaries(boundaries []topicBoundary, minDist float64) []topicBoundary {
	if len(boundaries) <= 1 {
		return boundaries
	}

	// Sort by time
	sort.Slice(boundaries, func(i, j int) bool {
		return boundaries[i].TimeSec < boundaries[j].TimeSec
	})

	var merged []topicBoundary
	current := boundaries[0]

	for i := 1; i < len(boundaries); i++ {
		if boundaries[i].TimeSec-current.TimeSec < minDist {
			// Merge: keep higher confidence, prefer silence_gap reason
			if boundaries[i].Confidence > current.Confidence {
				current.Confidence = boundaries[i].Confidence
			}
			if current.Reason != "silence_gap" {
				current.Reason = boundaries[i].Reason
			}
			// Use midpoint
			current.TimeSec = (current.TimeSec + boundaries[i].TimeSec) / 2
		} else {
			merged = append(merged, current)
			current = boundaries[i]
		}
	}
	merged = append(merged, current)

	return merged
}

// parseMinSecToSec converts "MM:SS" to seconds.
func parseMinSecToSec(minSec string) float64 {
	parts := strings.SplitN(minSec, ":", 2)
	if len(parts) != 2 {
		return -1
	}
	m := parseFloat(parts[0])
	s := parseFloat(parts[1])
	if m < 0 || s < 0 {
		return -1
	}
	return m*60 + s
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	var n float64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + float64(c-'0')
		} else {
			return -1
		}
	}
	return n
}

func formatSecRange(start, end float64) string {
	sm, ss := divmod(int(start), 60)
	em, es := divmod(int(end), 60)
	return fmt.Sprintf("%02d:%02d - %02d:%02d", sm, ss, em, es)
}
