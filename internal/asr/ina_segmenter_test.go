package asr

import (
	"testing"

	"hikami-go/internal/config"
)

func newInaTestProcessor() *VADProcessor {
	return &VADProcessor{cfg: &config.Config{VAD: config.VADConfig{
		Engine:          "ina",
		PaddingSec:      0.2,
		InaMinSpeechSec: 0.6,
		InaMergeGapSec:  0.4,
	}}}
}

func TestBuildInaSpeechMapKeepsOnlySpeech(t *testing.T) {
	p := newInaTestProcessor()
	segments := []InaSegment{
		{Label: "music", StartMS: 0, EndMS: 1000},
		{Label: "speech", StartMS: 1000, EndMS: 5000},
		{Label: "noise", StartMS: 5000, EndMS: 10000},
		{Label: "speech", StartMS: 10000, EndMS: 12000},
		{Label: "music", StartMS: 12000, EndMS: 15000},
	}
	sm := p.BuildInaSpeechMap(segments, 15000)
	if sm == nil {
		t.Fatal("BuildInaSpeechMap returned nil")
	}
	if sm.Params.Engine != "ina" || sm.Params.Detection != "ina-smn" {
		t.Fatalf("params = %+v", sm.Params)
	}
	if len(sm.KeptSegments) != 2 {
		t.Fatalf("kept segments = %d, want 2: %+v", len(sm.KeptSegments), sm.KeptSegments)
	}
	want := []KeptSegment{
		{OriginalStartMS: 800, OriginalEndMS: 5200, TrimmedStartMS: 0, TrimmedEndMS: 4400},
		{OriginalStartMS: 9800, OriginalEndMS: 12200, TrimmedStartMS: 4400, TrimmedEndMS: 6800},
	}
	for i := range want {
		if sm.KeptSegments[i] != want[i] {
			t.Errorf("segment[%d] = %+v, want %+v", i, sm.KeptSegments[i], want[i])
		}
	}
	if sm.TrimmedDurationMS != 6800 {
		t.Errorf("trimmed duration = %d, want 6800", sm.TrimmedDurationMS)
	}
}

func TestBuildInaSpeechMapMergesNearbySpeechAndDropsMorsels(t *testing.T) {
	p := newInaTestProcessor()
	segments := []InaSegment{
		{Label: "speech", StartMS: 1000, EndMS: 1400}, // < 0.6s, drop
		{Label: "speech", StartMS: 2000, EndMS: 3000},
		{Label: "speech", StartMS: 3500, EndMS: 4500}, // padding 后间隔 0.1s, merge
	}
	sm := p.BuildInaSpeechMap(segments, 5000)
	if sm == nil || len(sm.KeptSegments) != 1 {
		t.Fatalf("map = %+v, want one merged segment", sm)
	}
	seg := sm.KeptSegments[0]
	if seg.OriginalStartMS != 1800 || seg.OriginalEndMS != 4700 {
		t.Fatalf("merged segment = %+v, want [1800,4700]", seg)
	}
}

func TestBuildInaSpeechMapNoSpeech(t *testing.T) {
	p := newInaTestProcessor()
	if got := p.BuildInaSpeechMap([]InaSegment{{Label: "music", StartMS: 0, EndMS: 5000}}, 5000); got != nil {
		t.Fatalf("music-only map = %+v, want nil", got)
	}
}
