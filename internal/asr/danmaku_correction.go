package asr

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

type danmakuTimingItem struct {
	TimeMS          int64  `json:"time_ms"`
	OriginalTimeMS  int64  `json:"original_time_ms,omitempty"`
	CorrectedTimeMS int64  `json:"corrected_time_ms,omitempty"`
	Type            string `json:"type"`
	UserID          string `json:"user_id,omitempty"`
	UserName        string `json:"user_name,omitempty"`
	Text            string `json:"text"`
	Color           string `json:"color,omitempty"`
	RawTime         string `json:"raw_time,omitempty"`
	ReceivedAt      string `json:"received_at,omitempty"`
	Source          string `json:"source"`
}

type asrSegmentTiming struct {
	StartMS int64
	EndMS   int64
}

func correctDanmakuTiming(packageDir string, rawSegments []map[string]any) error {
	path := filepath.Join(packageDir, "danmaku.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var items []danmakuTimingItem
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	segments := normalizeSegmentTiming(rawSegments)
	if len(items) == 0 || len(segments) == 0 {
		return nil
	}

	for i := range items {
		if items[i].OriginalTimeMS == 0 {
			items[i].OriginalTimeMS = items[i].TimeMS
		}
		corrected := clampToSegmentBounds(items[i].TimeMS, segments)
		items[i].TimeMS = corrected
		items[i].CorrectedTimeMS = corrected
	}

	out, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func normalizeSegmentTiming(rawSegments []map[string]any) []asrSegmentTiming {
	segments := make([]asrSegmentTiming, 0, len(rawSegments))
	for _, raw := range rawSegments {
		start, hasStart := numberToInt(raw["start_ms"])
		end, hasEnd := numberToInt(raw["end_ms"])
		if !hasStart || !hasEnd || end < start {
			continue
		}
		segments = append(segments, asrSegmentTiming{StartMS: start, EndMS: end})
	}
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].StartMS < segments[j].StartMS
	})
	return segments
}

func clampToSegmentBounds(timeMS int64, segments []asrSegmentTiming) int64 {
	for _, segment := range segments {
		if timeMS >= segment.StartMS && timeMS <= segment.EndMS {
			return timeMS
		}
		if timeMS < segment.StartMS {
			return segment.StartMS
		}
	}
	last := segments[len(segments)-1]
	if timeMS > last.EndMS {
		return last.EndMS
	}
	return timeMS
}
