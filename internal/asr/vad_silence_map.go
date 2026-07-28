package asr

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// SilenceMap 是 silence_map.json 的内存表示。
//
// 不变量(由 VADProcessor.BuildSilenceMap 保证,SaveJSON 前断言;RemapSegments 不再校验):
//  1. KeptSegments 按 OriginalStartMS 严格升序
//  2. 相邻段 TrimmedEndMS[i] == TrimmedStartMS[i+1](trimmed 时间线连续)
//  3. 每段 (OriginalEndMS - OriginalStartMS) == (TrimmedEndMS - TrimmedStartMS)(长度相等,
//     段内映射是简单线性平移:t_orig = t_trim - TrimmedStart + OriginalStart)
//  4. padding 体现在 original 侧:kept.original 范围 = 说话段两端各向静音区扩 padding
//
// Trim(VADProcessor.Trim)用 ffmpeg atrim+concat 按 kept.original_* 范围切,输出严格对应
// TrimmedDurationMS(qoder C-1 关键修订:不能用 silenceremove,其输出无 padding 与不变量矛盾)。
//
// 详见 plans/plan-vad-2026-07-27.md §2.3 / §2.4 / §2.5。
type SilenceMap struct {
	VADVersion         int             `json:"vad_version"`
	Params             SilenceMapParam `json:"params"`
	OriginalDurationMS int64           `json:"original_duration_ms"`
	TrimmedDurationMS  int64           `json:"trimmed_duration_ms"`
	KeptSegments       []KeptSegment   `json:"kept_segments"`
}

// SilenceMapParam 记录生成本 map 时用的 VAD 参数,供审计/可复现。
type SilenceMapParam struct {
	ThresholdDB   int     `json:"threshold_db"`
	MinSilenceSec float64 `json:"min_silence_sec"`
	PaddingSec    float64 `json:"padding_sec"`
	Detection     string  `json:"detection"`
}

// KeptSegment 描述一段在 trimmed 音频里保留的区间,及其在原始音频里的对应区间。
// 由于不变量 3(长度相等),段内映射是线性平移。
type KeptSegment struct {
	OriginalStartMS int64 `json:"original_start_ms"`
	OriginalEndMS   int64 `json:"original_end_ms"`
	TrimmedStartMS  int64 `json:"trimmed_start_ms"`
	TrimmedEndMS    int64 `json:"trimmed_end_ms"`
}

// LoadSilenceMap 从路径读取并校验 silence_map.json。
//
// 文件不存在返回 (nil, nil):调用方据此跳过 remap,实现旧 session 零回归
// (2026-07-27 VAD 引入前的 session 没有 silence_map.json)。
//
// KeptSegments 为空(全静音场景)同样返回 (nil, nil):跳过 remap,segments 保持原样。
func LoadSilenceMap(path string) (*SilenceMap, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sm SilenceMap
	if err := json.Unmarshal(data, &sm); err != nil {
		return nil, fmt.Errorf("parse silence_map.json: %w", err)
	}
	if sm.VADVersion != 1 {
		return nil, fmt.Errorf("unsupported vad_version: %d", sm.VADVersion)
	}
	if len(sm.KeptSegments) == 0 {
		return nil, nil
	}
	return &sm, nil
}

// RemapTrimmedToOriginal 把 trimmed 时间线上的毫秒值映射回原始时间线。
//
// 依赖不变量 3(段内线性平移)+ 不变量 2(连续,故二分找段即可)。O(log n)。
//
// 边界:
//   - t < 首段 TrimmedStartMS:返回首段 OriginalStartMS(罕见,ASR 误判把段塞到 silence 区)
//   - t >= 末段 TrimmedEndMS:返回末段 OriginalEndMS + (t - 末段 TrimmedEndMS)(线性外推)
//   - 落在某段内:OriginalStartMS + (t - TrimmedStartMS)
func (sm *SilenceMap) RemapTrimmedToOriginal(tMS int64) int64 {
	if tMS <= 0 {
		return 0
	}
	segs := sm.KeptSegments
	if len(segs) == 0 {
		return tMS
	}
	if tMS < segs[0].TrimmedStartMS {
		return segs[0].OriginalStartMS
	}
	last := segs[len(segs)-1]
	if tMS >= last.TrimmedEndMS {
		return last.OriginalEndMS + (tMS - last.TrimmedEndMS)
	}
	// 二分找最后一个 TrimmedStartMS <= tMS 的段
	idx := sort.Search(len(segs), func(i int) bool {
		return segs[i].TrimmedStartMS > tMS
	})
	if idx == 0 {
		return segs[0].OriginalStartMS
	}
	seg := segs[idx-1]
	return seg.OriginalStartMS + (tMS - seg.TrimmedStartMS)
}

// RemapSegments 就地把 segments 的 start_ms/end_ms 从 trimmed 时间线平移回原始时间线。
//
// segments 元素是 map[string]any,字段名 "start_ms"/"end_ms"(DashScope 格式,见 extractSegments)。
//
// 不幂等:重复调用会二次平移,累积漂移。HandleTask 保证只调一次,且 remap 后立即写盘
// (写盘后 segments.json 已是原始时间线,后续不会再走此函数)。
// 字段值非数字(int/int64/float64/json.Number 之外)会被 numberToInt 拒绝,该字段保持不变(不 panic)。
func (sm *SilenceMap) RemapSegments(segments []map[string]any) {
	for _, seg := range segments {
		if v, ok := seg["start_ms"]; ok {
			if ms, ok := numberToInt(v); ok {
				seg["start_ms"] = sm.RemapTrimmedToOriginal(ms)
			}
		}
		if v, ok := seg["end_ms"]; ok {
			if ms, ok := numberToInt(v); ok {
				seg["end_ms"] = sm.RemapTrimmedToOriginal(ms)
			}
		}
	}
}

// SaveJSON 原子写 silence_map.json(tmp + rename,防止中途崩溃产生半文件)。
func (sm *SilenceMap) SaveJSON(path string) error {
	data, err := json.MarshalIndent(sm, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// OutputRatio 返回 trimmed/orig 比例,供 MinOutputRatio 守卫用。
// nil 或 OriginalDurationMS=0 返回 1.0(不触发守卫)。
func (sm *SilenceMap) OutputRatio() float64 {
	if sm == nil || sm.OriginalDurationMS == 0 {
		return 1.0
	}
	return float64(sm.TrimmedDurationMS) / float64(sm.OriginalDurationMS)
}
