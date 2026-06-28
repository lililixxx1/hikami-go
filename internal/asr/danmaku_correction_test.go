package asr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCorrectDanmakuTimingClampsToASRSegments(t *testing.T) {
	dir := t.TempDir()
	items := []danmakuTimingItem{
		{TimeMS: 500, Text: "before", Source: "live_record"},
		{TimeMS: 1500, Text: "inside", Source: "live_record"},
		{TimeMS: 9000, Text: "after", Source: "live_record"},
	}
	data, _ := json.Marshal(items)
	if err := os.WriteFile(filepath.Join(dir, "danmaku.json"), data, 0644); err != nil {
		t.Fatalf("write danmaku: %v", err)
	}
	segments := []map[string]any{
		{"start_ms": int64(1000), "end_ms": int64(2000), "text": "seg1"},
		{"start_ms": int64(5000), "end_ms": int64(6000), "text": "seg2"},
	}

	if err := correctDanmakuTiming(dir, segments); err != nil {
		t.Fatalf("correct: %v", err)
	}

	var got []danmakuTimingItem
	out, _ := os.ReadFile(filepath.Join(dir, "danmaku.json"))
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got[0].TimeMS != 1000 || got[0].OriginalTimeMS != 500 || got[0].CorrectedTimeMS != 1000 {
		t.Fatalf("unexpected first item: %+v", got[0])
	}
	if got[1].TimeMS != 1500 || got[1].OriginalTimeMS != 1500 || got[1].CorrectedTimeMS != 1500 {
		t.Fatalf("unexpected second item: %+v", got[1])
	}
	if got[2].TimeMS != 6000 || got[2].OriginalTimeMS != 9000 || got[2].CorrectedTimeMS != 6000 {
		t.Fatalf("unexpected third item: %+v", got[2])
	}
}
