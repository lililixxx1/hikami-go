package asr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 见 plans/plan-vad-2026-07-27.md Phase 2。

func TestRemapTrimmedToOriginal_Basic(t *testing.T) {
	// 单段恒等 [0, 1000]→[0, 1000]
	sm := &SilenceMap{KeptSegments: []KeptSegment{{
		OriginalStartMS: 0, OriginalEndMS: 1000,
		TrimmedStartMS: 0, TrimmedEndMS: 1000,
	}}}
	cases := []struct{ in, want int64 }{{0, 0}, {500, 500}, {1000, 1000}, {1500, 1500}}
	for _, c := range cases {
		if got := sm.RemapTrimmedToOriginal(c.in); got != c.want {
			t.Errorf("Remap(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestRemapTrimmedToOriginal_TwoSegments(t *testing.T) {
	// 原始 [0, 4000] + [8200, 15300],trimmed 连续 [0, 4000] + [4000, 11100]
	// (每段长度相等:4000 / 7100,映射是线性平移)
	sm := &SilenceMap{KeptSegments: []KeptSegment{
		{OriginalStartMS: 0, OriginalEndMS: 4000, TrimmedStartMS: 0, TrimmedEndMS: 4000},
		{OriginalStartMS: 8200, OriginalEndMS: 15300, TrimmedStartMS: 4000, TrimmedEndMS: 11100},
	}}
	cases := []struct {
		name string
		in   int64
		want int64
	}{
		{"seg0 start", 0, 0},
		{"seg0 mid", 2000, 2000},
		{"seg0/1 boundary (trimmed)", 4000, 8200}, // 落在 seg1 起点
		{"seg1 mid", 7000, 11200},                 // 8200 + (7000-4000) = 11200
		{"seg1 end", 11100, 15300},
		{"after last (linear extrapolate)", 12100, 16300}, // 15300 + (12100-11100)
		{"before first", -100, 0},                         // tMS<=0 → 0
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sm.RemapTrimmedToOriginal(c.in); got != c.want {
				t.Errorf("Remap(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestRemapTrimmedToOriginal_Boundaries(t *testing.T) {
	sm := &SilenceMap{KeptSegments: []KeptSegment{
		{OriginalStartMS: 100, OriginalEndMS: 1100, TrimmedStartMS: 0, TrimmedEndMS: 1000},
		{OriginalStartMS: 2000, OriginalEndMS: 3000, TrimmedStartMS: 1000, TrimmedEndMS: 2000},
	}}
	// t<=0 → 0(早返回)
	if got := sm.RemapTrimmedToOriginal(0); got != 0 {
		t.Errorf("t=0 (early return) = %d, want 0", got)
	}
	// t = 末段 trimmed_end → 末段 original_end
	if got := sm.RemapTrimmedToOriginal(2000); got != 3000 {
		t.Errorf("t=2000 (at last seg end) = %d, want 3000", got)
	}
	// t > 末段 trimmed_end → 线性外推
	if got := sm.RemapTrimmedToOriginal(2500); got != 3500 {
		t.Errorf("t=2500 (after last seg) = %d, want 3500", got)
	}
}

func TestRemapSegments_AllFields(t *testing.T) {
	sm := &SilenceMap{KeptSegments: []KeptSegment{
		{OriginalStartMS: 1000, OriginalEndMS: 5000, TrimmedStartMS: 0, TrimmedEndMS: 4000},
	}}
	segs := []map[string]any{
		{
			"start_ms":    int64(2000), // → 1000 + (2000-0) = 3000
			"end_ms":      int64(3000), // → 1000 + (3000-0) = 4000
			"text":        "hello",
			"channel_id":  int64(0),
			"sentence_id": int64(1),
		},
	}
	sm.RemapSegments(segs)
	if segs[0]["start_ms"] != int64(3000) {
		t.Errorf("start_ms = %v, want 3000", segs[0]["start_ms"])
	}
	if segs[0]["end_ms"] != int64(4000) {
		t.Errorf("end_ms = %v, want 4000", segs[0]["end_ms"])
	}
	// 其他字段保持不变
	if segs[0]["text"] != "hello" {
		t.Errorf("text changed: %v", segs[0]["text"])
	}
	if segs[0]["channel_id"] != int64(0) {
		t.Errorf("channel_id changed: %v", segs[0]["channel_id"])
	}
	if segs[0]["sentence_id"] != int64(1) {
		t.Errorf("sentence_id changed: %v", segs[0]["sentence_id"])
	}
}

func TestRemapSegments_NonIntField(t *testing.T) {
	// start_ms 是字符串(理论不会发生,但不该 panic)
	sm := &SilenceMap{KeptSegments: []KeptSegment{
		{OriginalStartMS: 0, OriginalEndMS: 1000, TrimmedStartMS: 0, TrimmedEndMS: 1000},
	}}
	segs := []map[string]any{
		{"start_ms": "not-a-number", "end_ms": int64(500)},
	}
	sm.RemapSegments(segs)
	// start_ms 应保持原值(numberToInt 拒绝字符串)
	if segs[0]["start_ms"] != "not-a-number" {
		t.Errorf("non-int start_ms changed: %v", segs[0]["start_ms"])
	}
	// end_ms 正常映射(恒等段)
	if segs[0]["end_ms"] != int64(500) {
		t.Errorf("end_ms = %v, want 500", segs[0]["end_ms"])
	}
}

func TestRemapSegments_FloatField(t *testing.T) {
	// DashScope JSON unmarshal 后 begin_time/end_time 是 float64
	sm := &SilenceMap{KeptSegments: []KeptSegment{
		{OriginalStartMS: 1000, OriginalEndMS: 5000, TrimmedStartMS: 0, TrimmedEndMS: 4000},
	}}
	segs := []map[string]any{
		{"start_ms": float64(2000), "end_ms": float64(3000)},
	}
	sm.RemapSegments(segs)
	if segs[0]["start_ms"] != int64(3000) {
		t.Errorf("float start_ms = %v, want 3000", segs[0]["start_ms"])
	}
	if segs[0]["end_ms"] != int64(4000) {
		t.Errorf("float end_ms = %v, want 4000", segs[0]["end_ms"])
	}
}

func TestLoadSilenceMap_NotExist(t *testing.T) {
	sm, err := LoadSilenceMap(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Errorf("LoadSilenceMap missing file: unexpected error %v", err)
	}
	if sm != nil {
		t.Errorf("LoadSilenceMap missing file: want nil, got %+v", sm)
	}
}

func TestLoadSilenceMap_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "silence_map.json")
	sm := &SilenceMap{VADVersion: 1, KeptSegments: []KeptSegment{}}
	if err := sm.SaveJSON(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSilenceMap(path)
	if err != nil {
		t.Errorf("LoadSilenceMap empty: unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("LoadSilenceMap empty: want nil, got %+v", got)
	}
}

func TestLoadSilenceMap_BadVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "silence_map.json")
	if err := os.WriteFile(path, []byte(`{"vad_version":2,"kept_segments":[{"original_start_ms":0,"original_end_ms":1,"trimmed_start_ms":0,"trimmed_end_ms":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSilenceMap(path)
	if err == nil {
		t.Error("LoadSilenceMap bad version: expected error, got nil")
	}
}

func TestLoadSilenceMap_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "silence_map.json")
	original := &SilenceMap{
		VADVersion:         1,
		Params:             SilenceMapParam{ThresholdDB: -40, MinSilenceSec: 2.0, PaddingSec: 0.2, Detection: "peak"},
		OriginalDurationMS: 19429340,
		TrimmedDurationMS:  18884210,
		KeptSegments: []KeptSegment{
			{OriginalStartMS: 0, OriginalEndMS: 4120, TrimmedStartMS: 0, TrimmedEndMS: 4120},
			{OriginalStartMS: 8200, OriginalEndMS: 15300, TrimmedStartMS: 4120, TrimmedEndMS: 11220},
		},
	}
	if err := original.SaveJSON(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSilenceMap(path)
	if err != nil {
		t.Fatalf("LoadSilenceMap: %v", err)
	}
	if !reflect.DeepEqual(loaded, original) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", loaded, original)
	}
}

func TestSilenceMap_OutputRatio(t *testing.T) {
	cases := []struct {
		name string
		sm   *SilenceMap
		want float64
	}{
		{"nil", nil, 1.0},
		{"zero_orig", &SilenceMap{OriginalDurationMS: 0, TrimmedDurationMS: 100}, 1.0},
		{"half", &SilenceMap{OriginalDurationMS: 1000, TrimmedDurationMS: 500}, 0.5},
		{"full", &SilenceMap{OriginalDurationMS: 1000, TrimmedDurationMS: 1000}, 1.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.sm.OutputRatio(); got != c.want {
				t.Errorf("OutputRatio() = %v, want %v", got, c.want)
			}
		})
	}
}

// 防回归:确认 SilenceMap 能被标准 json 库正确序列化(确保 json tag 无拼写错误)。
func TestSilenceMap_JSONTags(t *testing.T) {
	sm := SilenceMap{
		VADVersion:         1,
		Params:             SilenceMapParam{ThresholdDB: -40, MinSilenceSec: 2, PaddingSec: 0.2, Detection: "peak"},
		OriginalDurationMS: 1000,
		TrimmedDurationMS:  800,
		KeptSegments:       []KeptSegment{{OriginalStartMS: 0, OriginalEndMS: 800, TrimmedStartMS: 0, TrimmedEndMS: 800}},
	}
	data, err := json.Marshal(sm)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, key := range []string{`"vad_version"`, `"params"`, `"threshold_db"`, `"min_silence_sec"`, `"padding_sec"`, `"detection"`, `"original_duration_ms"`, `"trimmed_duration_ms"`, `"kept_segments"`, `"original_start_ms"`, `"original_end_ms"`, `"trimmed_start_ms"`, `"trimmed_end_ms"`} {
		if !strings.Contains(s, key) {
			t.Errorf("missing json key %s in output: %s", key, s)
		}
	}
}
