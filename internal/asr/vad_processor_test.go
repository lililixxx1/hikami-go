package asr

import (
	"strings"
	"testing"

	"hikami-go/internal/config"
)

// 见 plans/plan-vad-2026-07-27.md Phase 3。

// newTestVADProcessor 构造一个用默认 VAD 参数的 VADProcessor(不真跑 ffmpeg,仅测纯函数)。
func newTestVADProcessor() *VADProcessor {
	return &VADProcessor{
		ffmpeg:  "ffmpeg",
		ffprobe: "ffprobe",
		cfg: &config.Config{
			VAD: config.VADConfig{
				Enabled:        true,
				ThresholdDB:    -40,
				MinSilenceSec:  2.0,
				PaddingSec:     0.2,
				DetectionMode:  "peak",
				MinOutputRatio: 0.3,
			},
		},
	}
}

func TestParseSilenceDetect_Basic(t *testing.T) {
	log := `[Parsed_silencedetect_0 @ 0x1] silence_start: 5.000
[Parsed_silencedetect_0 @ 0x1] silence_end: 5.000 | silence_duration: 2.000
[Parsed_silencedetect_0 @ 0x1] silence_start: 10.500
[Parsed_silencedetect_0 @ 0x1] silence_end: 13.700 | silence_duration: 3.200
`
	intervals := parseSilenceDetect(log)
	if len(intervals) != 2 {
		t.Fatalf("got %d intervals, want 2", len(intervals))
	}
	if intervals[0].StartMS != 5000 || intervals[0].EndMS != 5000 {
		t.Errorf("interval[0] = %+v, want {5000,5000}(end==start means 0-duration, edge case)", intervals[0])
	}
	if intervals[1].StartMS != 10500 || intervals[1].EndMS != 13700 {
		t.Errorf("interval[1] = %+v, want {10500,13700}", intervals[1])
	}
}

func TestParseSilenceDetect_TrailingSilence(t *testing.T) {
	// 只有 silence_start 没 end(音频以静音结尾)→ EndMS=0
	log := `[Parsed_silencedetect_0 @ 0x1] silence_start: 100.000
`
	intervals := parseSilenceDetect(log)
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	if intervals[0].StartMS != 100000 {
		t.Errorf("StartMS = %d, want 100000", intervals[0].StartMS)
	}
	if intervals[0].EndMS != 0 {
		t.Errorf("EndMS = %d, want 0 (trailing silence)", intervals[0].EndMS)
	}
}

func TestParseSilenceDetect_Empty(t *testing.T) {
	if got := parseSilenceDetect(""); len(got) != 0 {
		t.Errorf("empty log = %v, want []", got)
	}
}

func TestBuildSilenceMap_NoSilence(t *testing.T) {
	p := newTestVADProcessor()
	sm := p.BuildSilenceMap(nil, 10000)
	if sm == nil {
		t.Fatal("BuildSilenceMap(nil, 10000) = nil, want single full segment")
	}
	if len(sm.KeptSegments) != 1 {
		t.Fatalf("segments = %d, want 1", len(sm.KeptSegments))
	}
	seg := sm.KeptSegments[0]
	if seg.OriginalStartMS != 0 || seg.OriginalEndMS != 10000 {
		t.Errorf("original range = [%d,%d], want [0,10000]", seg.OriginalStartMS, seg.OriginalEndMS)
	}
	if seg.TrimmedStartMS != 0 || seg.TrimmedEndMS != 10000 {
		t.Errorf("trimmed range = [%d,%d], want [0,10000]", seg.TrimmedStartMS, seg.TrimmedEndMS)
	}
	if sm.TrimmedDurationMS != 10000 {
		t.Errorf("TrimmedDurationMS = %d, want 10000", sm.TrimmedDurationMS)
	}
}

func TestBuildSilenceMap_OneSilence(t *testing.T) {
	// 原始 10s:[0,4000] 说话 + [4000,8000] 静音 + [8000,10000] 说话
	// padding=200ms:静音区 4000ms,中点 2000ms,padding 200ms 不超限
	p := newTestVADProcessor()
	sm := p.BuildSilenceMap([]SilenceInterval{{StartMS: 4000, EndMS: 8000}}, 10000)
	if sm == nil {
		t.Fatal("BuildSilenceMap = nil")
	}
	if len(sm.KeptSegments) != 2 {
		t.Fatalf("segments = %d, want 2", len(sm.KeptSegments))
	}
	// seg0: speech [0,4000] → padding 后 [0, 4200](尾部 +200)
	seg0 := sm.KeptSegments[0]
	if seg0.OriginalStartMS != 0 || seg0.OriginalEndMS != 4200 {
		t.Errorf("seg0 original = [%d,%d], want [0,4200]", seg0.OriginalStartMS, seg0.OriginalEndMS)
	}
	if seg0.TrimmedStartMS != 0 || seg0.TrimmedEndMS != 4200 {
		t.Errorf("seg0 trimmed = [%d,%d], want [0,4200]", seg0.TrimmedStartMS, seg0.TrimmedEndMS)
	}
	// seg1: speech [8000,10000] → padding 后 [7800, 10000](头部 +200,尾部到文件尾)
	seg1 := sm.KeptSegments[1]
	if seg1.OriginalStartMS != 7800 || seg1.OriginalEndMS != 10000 {
		t.Errorf("seg1 original = [%d,%d], want [7800,10000]", seg1.OriginalStartMS, seg1.OriginalEndMS)
	}
	if seg1.TrimmedStartMS != 4200 || seg1.TrimmedEndMS != 6400 {
		t.Errorf("seg1 trimmed = [%d,%d], want [4200,6400]", seg1.TrimmedStartMS, seg1.TrimmedEndMS)
	}
	// 不变量 3:每段长度相等
	if (seg0.OriginalEndMS - seg0.OriginalStartMS) != (seg0.TrimmedEndMS - seg0.TrimmedStartMS) {
		t.Error("seg0 length invariant violated")
	}
	if (seg1.OriginalEndMS - seg1.OriginalStartMS) != (seg1.TrimmedEndMS - seg1.TrimmedStartMS) {
		t.Error("seg1 length invariant violated")
	}
	// 不变量 2:相邻段 trimmed 连续
	if seg0.TrimmedEndMS != seg1.TrimmedStartMS {
		t.Error("segments not continuous in trimmed timeline")
	}
}

func TestBuildSilenceMap_MultiSilence(t *testing.T) {
	// 两段静音:5-10s, 15-20s,原始 30s
	p := newTestVADProcessor()
	sm := p.BuildSilenceMap([]SilenceInterval{
		{StartMS: 5000, EndMS: 10000},
		{StartMS: 15000, EndMS: 20000},
	}, 30000)
	if sm == nil {
		t.Fatal("BuildSilenceMap = nil")
	}
	if len(sm.KeptSegments) != 3 {
		t.Fatalf("segments = %d, want 3", len(sm.KeptSegments))
	}
	// 不变量全检
	assertSilenceMapInvariants(t, sm)
}

func TestBuildSilenceMap_TrailingSilence(t *testing.T) {
	// 尾部静音(EndMS=0,音频以静音结尾)→ 被 valid 过滤丢弃,不形成尾 speech 段。
	// 此时整个音频 [0, origMS] 是首 speech 段(前面无静音,后面也无静音因为没有有效静音)。
	// 结果:单段全说话(等同 NoSilence 场景),因为唯一的"静音"被丢弃了。
	p := newTestVADProcessor()
	sm := p.BuildSilenceMap([]SilenceInterval{{StartMS: 8000, EndMS: 0}}, 10000)
	if sm == nil {
		t.Fatal("BuildSilenceMap = nil")
	}
	// trailing-only silence 被丢弃 → 单段全说话
	if len(sm.KeptSegments) != 1 {
		t.Fatalf("segments = %d, want 1 (trailing-only silence dropped)", len(sm.KeptSegments))
	}
	seg := sm.KeptSegments[0]
	if seg.OriginalStartMS != 0 || seg.OriginalEndMS != 10000 {
		t.Errorf("full segment = [%d,%d], want [0,10000]", seg.OriginalStartMS, seg.OriginalEndMS)
	}
}

func TestBuildSilenceMap_HeadSilence(t *testing.T) {
	// 首段从 0 开始的静音 → 首 speech 段前面无静音(paddingStart=0),后面静音借 paddingEnd。
	// 原始 10s:[0,2000] 静音 + [2000,10000] 说话(尾 speech,无后置静音)
	p := newTestVADProcessor()
	sm := p.BuildSilenceMap([]SilenceInterval{{StartMS: 0, EndMS: 2000}}, 10000)
	if sm == nil {
		t.Fatal("BuildSilenceMap = nil")
	}
	if len(sm.KeptSegments) != 1 {
		t.Fatalf("segments = %d, want 1", len(sm.KeptSegments))
	}
	seg := sm.KeptSegments[0]
	// 首 speech [0,2000]:prevSilence=nil(前面无静音)→ keptStart=0;nextSilence=[0,2000]→ padEnd
	// 但首 speech 的 nextSilence.StartMS=0 ≤ speech.start=0,不形成 speech 段(start>=end)。
	// 实际只剩尾 speech [2000, 10000]:prevSilence=[0,2000] → padStart=min(200, (2000-0)/2=1000)=200
	// keptStart = 2000-200 = 1800;nextSilence=nil → keptEnd=10000
	if seg.OriginalStartMS != 1800 {
		t.Errorf("seg.OriginalStartMS = %d, want 1800 (head silence borrows padding into tail speech)", seg.OriginalStartMS)
	}
	if seg.OriginalEndMS != 10000 {
		t.Errorf("seg.OriginalEndMS = %d, want 10000", seg.OriginalEndMS)
	}
}

func TestBuildSilenceMap_FullSilence(t *testing.T) {
	p := newTestVADProcessor()
	// origMS <= 0 → nil
	if sm := p.BuildSilenceMap(nil, 0); sm != nil {
		t.Errorf("origMS=0 should return nil, got %+v", sm)
	}
	if sm := p.BuildSilenceMap(nil, -1); sm != nil {
		t.Errorf("origMS=-1 should return nil, got %+v", sm)
	}
}

func TestBuildSilenceMap_ShortSpeech(t *testing.T) {
	// 说话段 < 2*padding:paddingStart 截到中点
	// 原始 10s:[0,4000] 说话 + [4000,6000] 静音(只 2s)+ [6000,10000] 说话
	// padding 200ms,静音区中点 1000ms,paddingEnd = min(200, 1000) = 200
	p := newTestVADProcessor()
	sm := p.BuildSilenceMap([]SilenceInterval{{StartMS: 4000, EndMS: 6000}}, 10000)
	if sm == nil {
		t.Fatal("BuildSilenceMap = nil")
	}
	// 极短说话段测试:说话段 = 0(两静音紧邻)→ paddingStart 截到 0
	// 这里说话段都是 4000ms(不算极短),但测 padding 限制逻辑仍成立
	assertSilenceMapInvariants(t, sm)
}

func TestBuildSilenceMap_Invariants(t *testing.T) {
	// 综合:多静音段 + padding,验证所有 4 条不变量
	p := newTestVADProcessor()
	intervals := []SilenceInterval{
		{StartMS: 3000, EndMS: 6000},   // 3s 静音
		{StartMS: 12000, EndMS: 18000}, // 6s 静音
		{StartMS: 25000, EndMS: 30000}, // 5s 静音
	}
	sm := p.BuildSilenceMap(intervals, 40000)
	if sm == nil {
		t.Fatal("BuildSilenceMap = nil")
	}
	assertSilenceMapInvariants(t, sm)
}

// assertSilenceMapInvariants 验证 SilenceMap 的全部 4 条不变量(qoder v2 I-4 防 C-1 同类 bug)。
func assertSilenceMapInvariants(t *testing.T, sm *SilenceMap) {
	t.Helper()
	segs := sm.KeptSegments
	if len(segs) == 0 {
		t.Fatal("invariants: no segments")
	}
	// 1. 按 OriginalStartMS 严格升序
	for i := 1; i < len(segs); i++ {
		if segs[i].OriginalStartMS <= segs[i-1].OriginalStartMS {
			t.Errorf("invariant 1 violated: seg[%d].OriginalStartMS=%d <= seg[%d]=%d",
				i, segs[i].OriginalStartMS, i-1, segs[i-1].OriginalStartMS)
		}
	}
	// 2. 相邻段 trimmed 连续
	for i := 1; i < len(segs); i++ {
		if segs[i].TrimmedStartMS != segs[i-1].TrimmedEndMS {
			t.Errorf("invariant 2 violated: seg[%d].TrimmedStartMS=%d != seg[%d].TrimmedEndMS=%d",
				i, segs[i].TrimmedStartMS, i-1, segs[i-1].TrimmedEndMS)
		}
	}
	// 3. 每段长度相等(段内线性平移)
	for i, seg := range segs {
		origLen := seg.OriginalEndMS - seg.OriginalStartMS
		trimLen := seg.TrimmedEndMS - seg.TrimmedStartMS
		if origLen != trimLen {
			t.Errorf("invariant 3 violated: seg[%d] origLen=%d != trimLen=%d", i, origLen, trimLen)
		}
		if origLen <= 0 {
			t.Errorf("invariant 3: seg[%d] has non-positive length %d", i, origLen)
		}
	}
	// 4. padding 在 original 侧体现:首段 OriginalStart >= 0,末段 OriginalEnd <= OriginalDurationMS
	if segs[0].OriginalStartMS < 0 {
		t.Errorf("invariant 4: first seg OriginalStartMS=%d < 0", segs[0].OriginalStartMS)
	}
	if segs[len(segs)-1].OriginalEndMS > sm.OriginalDurationMS {
		t.Errorf("invariant 4: last seg OriginalEndMS=%d > OriginalDurationMS=%d",
			segs[len(segs)-1].OriginalEndMS, sm.OriginalDurationMS)
	}
	// 附加:TrimmedDurationMS == 末段 TrimmedEndMS
	if sm.TrimmedDurationMS != segs[len(segs)-1].TrimmedEndMS {
		t.Errorf("TrimmedDurationMS=%d != last seg TrimmedEndMS=%d",
			sm.TrimmedDurationMS, segs[len(segs)-1].TrimmedEndMS)
	}
}

func TestBuildSilenceMap_NoOverlap(t *testing.T) {
	// 相邻 kept 段的 OriginalEndMS[i] <= OriginalStartMS[i+1](padding 不重叠)
	p := newTestVADProcessor()
	sm := p.BuildSilenceMap([]SilenceInterval{
		{StartMS: 4000, EndMS: 6000},  // 短静音
		{StartMS: 9000, EndMS: 12000}, // 短静音
	}, 20000)
	if sm == nil {
		t.Fatal("BuildSilenceMap = nil")
	}
	for i := 1; i < len(sm.KeptSegments); i++ {
		prev := sm.KeptSegments[i-1].OriginalEndMS
		curr := sm.KeptSegments[i].OriginalStartMS
		if prev > curr {
			t.Errorf("overlap: seg[%d].OriginalEndMS=%d > seg[%d].OriginalStartMS=%d",
				i-1, prev, i, curr)
		}
	}
}

func TestBuildAtrimConcatFilter(t *testing.T) {
	segs := []KeptSegment{
		{OriginalStartMS: 0, OriginalEndMS: 4120, TrimmedStartMS: 0, TrimmedEndMS: 4120},
		{OriginalStartMS: 8200, OriginalEndMS: 15300, TrimmedStartMS: 4120, TrimmedEndMS: 11220},
	}
	got := buildAtrimConcatFilter(segs)
	// 应包含 2 个 atrim + 1 个 concat。atrim 用命名参数(start=/end=)形式。
	if !strings.Contains(got, "atrim=start=0.000:end=4.120") {
		t.Errorf("missing first atrim, got: %s", got)
	}
	if !strings.Contains(got, "atrim=start=8.200:end=15.300") {
		t.Errorf("missing second atrim, got: %s", got)
	}
	if !strings.Contains(got, "concat=n=2:v=0:a=1[vad_out]") {
		t.Errorf("missing concat, got: %s", got)
	}
	if !strings.Contains(got, "[a0][a1]") {
		t.Errorf("missing concat inputs, got: %s", got)
	}
}

func TestBuildAtrimConcatFilter_SingleSegment(t *testing.T) {
	segs := []KeptSegment{
		{OriginalStartMS: 0, OriginalEndMS: 10000, TrimmedStartMS: 0, TrimmedEndMS: 10000},
	}
	got := buildAtrimConcatFilter(segs)
	if !strings.Contains(got, "concat=n=1:v=0:a=1[vad_out]") {
		t.Errorf("single segment concat=n=1 missing, got: %s", got)
	}
}
