package asr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"hikami-go/internal/executil"
	"hikami-go/internal/fsutil"
)

// InaSegment 是 inaSpeechSegmenter smn 引擎返回的一段分类结果。
// detect_gender=false 时标签为 speech/music/noise/noEnergy。
type InaSegment struct {
	Label   string `json:"label"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

type inaSegmentation struct {
	Engine       string       `json:"engine"`
	DetectGender bool         `json:"detect_gender"`
	Segments     []InaSegment `json:"segments"`
}

// DetectInaSpeech 调用隔离 Python 环境中的 inaSpeechSegmenter，并把完整分类结果原子保存。
func (p *VADProcessor) DetectInaSpeech(ctx context.Context, audioPath, resultPath string) ([]InaSegment, int64, error) {
	if p.inaGate != nil {
		select {
		case p.inaGate <- struct{}{}:
			defer func() { <-p.inaGate }()
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	python := p.cfg.VAD.EffectiveInaPython()
	script := p.cfg.VAD.EffectiveInaScript()
	tmpPath := resultPath + ".tmp"
	_ = os.Remove(tmpPath)

	cmd := exec.CommandContext(ctx, python, script,
		"--input", audioPath,
		"--output", tmpPath,
		"--ffmpeg", p.ffmpeg,
		"--batch-size", strconv.Itoa(p.cfg.VAD.InaBatchSize),
	)
	cmd.Env = inaProcessEnv(python)
	executil.HideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		return nil, 0, fmt.Errorf("ina speech segmentation failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(tmpPath)
	_ = os.Remove(tmpPath)
	if err != nil {
		return nil, 0, fmt.Errorf("read ina segmentation: %w", err)
	}
	var result inaSegmentation
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, 0, fmt.Errorf("parse ina segmentation: %w", err)
	}
	if result.Engine != "ina-smn" {
		return nil, 0, fmt.Errorf("unexpected ina segmentation engine %q", result.Engine)
	}
	if err := fsutil.WriteFileAtomic(resultPath, data, 0o644); err != nil {
		return nil, 0, fmt.Errorf("save ina segmentation: %w", err)
	}
	origMS, err := p.probeDurationMS(ctx, audioPath)
	if err != nil {
		return nil, 0, fmt.Errorf("ina segmentation ffprobe duration: %w", err)
	}
	return result.Segments, origMS, nil
}

// BuildInaSpeechMap 只保留 speech 标签，music/noise/noEnergy 全部裁掉。
// 输出沿用 SilenceMap，以便现有 ASR 结果反向映射逻辑保持不变。
func (p *VADProcessor) BuildInaSpeechMap(segments []InaSegment, origDurationMS int64) *SilenceMap {
	if origDurationMS <= 0 {
		return nil
	}
	paddingMS := int64(p.cfg.VAD.PaddingSec * 1000)
	minSpeechMS := int64(p.cfg.VAD.InaMinSpeechSec * 1000)
	mergeGapMS := int64(p.cfg.VAD.InaMergeGapSec * 1000)
	if paddingMS < 0 {
		paddingMS = 0
	}
	if minSpeechMS < 0 {
		minSpeechMS = 0
	}
	if mergeGapMS < 0 {
		mergeGapMS = 0
	}

	sorted := append([]InaSegment(nil), segments...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].StartMS < sorted[j].StartMS })
	type interval struct{ start, end int64 }
	kept := make([]interval, 0, len(sorted))
	for _, seg := range sorted {
		if strings.ToLower(strings.TrimSpace(seg.Label)) != "speech" || seg.EndMS <= seg.StartMS {
			continue
		}
		if seg.EndMS-seg.StartMS < minSpeechMS {
			continue
		}
		start := max(int64(0), seg.StartMS-paddingMS)
		end := min(origDurationMS, seg.EndMS+paddingMS)
		if end <= start {
			continue
		}
		if len(kept) > 0 && start-kept[len(kept)-1].end <= mergeGapMS {
			kept[len(kept)-1].end = max(kept[len(kept)-1].end, end)
			continue
		}
		kept = append(kept, interval{start: start, end: end})
	}
	if len(kept) == 0 {
		return nil
	}

	sm := &SilenceMap{
		VADVersion:         1,
		OriginalDurationMS: origDurationMS,
		Params: SilenceMapParam{
			PaddingSec:   p.cfg.VAD.PaddingSec,
			Detection:    "ina-smn",
			Engine:       "ina",
			MinSpeechSec: p.cfg.VAD.InaMinSpeechSec,
			MergeGapSec:  p.cfg.VAD.InaMergeGapSec,
		},
	}
	var trimmedMS int64
	for _, seg := range kept {
		duration := seg.end - seg.start
		sm.KeptSegments = append(sm.KeptSegments, KeptSegment{
			OriginalStartMS: seg.start,
			OriginalEndMS:   seg.end,
			TrimmedStartMS:  trimmedMS,
			TrimmedEndMS:    trimmedMS + duration,
		})
		trimmedMS += duration
	}
	sm.TrimmedDurationMS = trimmedMS
	return sm
}

func inaProcessEnv(python string) []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "LD_LIBRARY_PATH=") || strings.HasPrefix(item, "TF_CPP_MIN_LOG_LEVEL=") {
			continue
		}
		env = append(env, item)
	}
	paths := make([]string, 0, 16)
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/usr/lib/wsl/lib"); err == nil {
			paths = append(paths, "/usr/lib/wsl/lib")
		}
		venvRoot := filepath.Dir(filepath.Dir(python))
		matches, _ := filepath.Glob(filepath.Join(venvRoot, "lib", "python*", "site-packages", "nvidia", "*", "lib"))
		paths = append(paths, matches...)
	}
	if current := strings.TrimSpace(os.Getenv("LD_LIBRARY_PATH")); current != "" {
		paths = append(paths, current)
	}
	if len(paths) > 0 {
		env = append(env, "LD_LIBRARY_PATH="+strings.Join(paths, string(os.PathListSeparator)))
	}
	env = append(env, "TF_CPP_MIN_LOG_LEVEL=2")
	return env
}
