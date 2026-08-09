package asr

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"hikami-go/internal/config"
	"hikami-go/internal/executil"
)

// VADProcessor 封装 ffmpeg silencedetect(扫描静音)和按 SilenceMap 精确切音频的调用。
// 不持有状态,可并发调用(每个 session 一个独立输出路径)。
//
// 持有 *config.Config 指针(而非 VADConfig 值拷贝),让 handler updateVADConfig 改参后
// 立即生效(qoder v2 I-1)。数据竞争级别与 asr.Handler.cfg.VAD.Enabled 读取一致 —— 项目已接受
// 此范式(handler 在 publishMu 下写,worker goroutine 无锁读)。
//
// 详见 plans/plan-vad-2026-07-27.md Phase 3。
type VADProcessor struct {
	ffmpeg  string // ffmpeg 可执行路径(来自 cfg.FFmpeg,已 resolve)
	ffprobe string // ffprobe 路径(用 ffprobe 拿 duration,不用 ffmpeg 解析)
	cfg     *config.Config
	inaGate chan struct{} // TensorFlow 默认占满单卡显存，ina 分段强制单并发
}

// NewVADProcessor 创建 VADProcessor。cfg.FFmpeg/FFprobe 应已由 runtime.ResolveFFmpeg 解析。
func NewVADProcessor(cfg *config.Config) *VADProcessor {
	return &VADProcessor{
		ffmpeg:  cfg.FFmpeg,
		ffprobe: cfg.FFprobe,
		cfg:     cfg,
		inaGate: make(chan struct{}, 1),
	}
}

// SilenceInterval 是 ffmpeg silencedetect 输出的一行解析结果。
// silence_start=N → Start;silence_end=N → 同时填 Start+End。
// EndMS=0 表示只有 start 没配对 end(音频以静音结尾),BuildSilenceMap 会丢弃尾部静音。
type SilenceInterval struct {
	StartMS int64
	EndMS   int64
}

// Detect 跑 silencedetect 扫描静音区间。
//
// silencedetect 只支持 peak 检测模式,VADConfig.DetectionMode 字段保留供未来扩展,当前忽略
// (用户配 rms 会 WARN 但仍用 peak,避免与 silenceremove 默认 rms 不一致 —— 不过本计划最终
// 不用 silenceremove,见 Trim 注释)。
//
// 返回静音区间表 + 原始音频时长(ms),供 BuildSilenceMap 构造 SilenceMap。
func (p *VADProcessor) Detect(ctx context.Context, audioPath string) ([]SilenceInterval, int64, error) {
	if p.cfg.VAD.DetectionMode != "peak" {
		// 用户配置非 peak 也忽略(silencedetect 只支持 peak;字段保留供未来扩展)
		slog.Warn("vad: detection_mode != peak is ignored (silencedetect only supports peak)",
			"configured", p.cfg.VAD.DetectionMode)
	}
	threshold := fmt.Sprintf("%ddB", p.cfg.VAD.ThresholdDB)
	duration := strconv.FormatFloat(p.cfg.VAD.MinSilenceSec, 'f', -1, 64)

	// ffmpeg -af silencedetect=noise=-40dB:d=2 -f null -
	// silencedetect 输出在 stderr(loglevel 默认 info)
	cmd := exec.CommandContext(ctx, p.ffmpeg,
		"-hide_banner",
		"-i", audioPath,
		"-af", fmt.Sprintf("silencedetect=noise=%s:d=%s", threshold, duration),
		"-f", "null", "-",
	)
	executil.HideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, 0, fmt.Errorf("vad detect: ffmpeg failed: %w: %s", err, string(output))
	}
	intervals := parseSilenceDetect(string(output))
	origMS, err := p.probeDurationMS(ctx, audioPath)
	if err != nil {
		return nil, 0, fmt.Errorf("vad detect: ffprobe duration: %w", err)
	}
	return intervals, origMS, nil
}

// silencedetect 行格式(stderr,带 [Parsed_silencedetect ...] 前缀):
//
//	[silencedetect @ 0x...] silence_start: 1234.56
//	[silencedetect @ 0x...] silence_end: 1234.56 | silence_duration: 2.34
var (
	reSilenceStart = regexp.MustCompile(`silence_start:\s*([0-9.]+)`)
	reSilenceEnd   = regexp.MustCompile(`silence_end:\s*([0-9.]+)`)
)

// parseSilenceDetect 从 ffmpeg stderr 解析静音区间。导出供测试。
func parseSilenceDetect(log string) []SilenceInterval {
	var intervals []SilenceInterval
	var pending *SilenceInterval
	scanner := bufio.NewScanner(strings.NewReader(log))
	// 长音频日志行数多,加大 buffer(默认 64K 可能不够)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m := reSilenceStart.FindStringSubmatch(line); m != nil {
			if sec, err := strconv.ParseFloat(m[1], 64); err == nil {
				intervals = append(intervals, SilenceInterval{StartMS: int64(sec * 1000)})
				pending = &intervals[len(intervals)-1]
			}
		}
		if m := reSilenceEnd.FindStringSubmatch(line); m != nil && pending != nil {
			if sec, err := strconv.ParseFloat(m[1], 64); err == nil {
				pending.EndMS = int64(sec * 1000)
				pending = nil
			}
		}
	}
	// pending != nil:音频以静音结尾,只有 silence_start 没 end。EndMS 留 0,
	// BuildSilenceMap 会丢弃尾部静音(因为裁掉到文件尾是自然的)。
	return intervals
}

// probeDurationMS 用 ffprobe 拿音频时长(毫秒)。比解析 ffmpeg stderr 可靠。
func (p *VADProcessor) probeDurationMS(ctx context.Context, audioPath string) (int64, error) {
	cmd := exec.CommandContext(ctx, p.ffprobe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		audioPath,
	)
	executil.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration: %w", err)
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration parse: %w", err)
	}
	return int64(sec * 1000), nil
}

// 大量短片段使用单次 PCM 流裁剪，避免 atrim+concat 把每帧扇出到数百个分支；
// 普通静音 VAD 的少量片段仍沿用 atrim+concat。
const streamTrimSegmentThreshold = 64

const (
	trimSampleRate     = int64(16000)
	trimBytesPerSample = int64(2) // s16le mono
)

// Trim 按 SilenceMap 的 kept_segments 精确切音频(qoder C-1 关键修订)。
//
// 不能用 silenceremove:它的输出不含 padding(纯语音硬拼),与 silence_map 的 trimmed 时间线
// 不一致,会导致反向映射累积漂移。atrim 按 kept.original 范围切,每段含 padding,concat 拼接,
// 输出严格对应 silence_map.TrimmedDurationMS。
//
// filter_complex 生成:对 N 个 kept 段,
//
//	[0:a]atrim=start:0.000:end:4.120,asetpts=PTS-STARTPTS[a0];
//	[0:a]atrim=start:8.200:end:15.300,asetpts=PTS-STARTPTS[a1];
//	[a0][a1]concat=n=2:v=0:a=1[vad_out]
//
// atrim 时间单位是秒,由 ms/1000 转换(保留 3 位小数 = 1ms 精度)。
func (p *VADProcessor) Trim(ctx context.Context, inputPath, outputPath string, sm *SilenceMap) error {
	if sm == nil || len(sm.KeptSegments) == 0 {
		return fmt.Errorf("vad trim: silence map is empty")
	}
	if len(sm.KeptSegments) > streamTrimSegmentThreshold {
		return p.trimPCMStream(ctx, inputPath, outputPath, sm.KeptSegments)
	}
	filter := buildAtrimConcatFilter(sm.KeptSegments)
	cmd := exec.CommandContext(ctx, p.ffmpeg,
		"-y", "-hide_banner", "-loglevel", "warning",
		"-i", inputPath,
		"-filter_complex", filter,
		"-map", "[vad_out]",
		// 输入已是 normalize 产出的 mono/16kHz/64k MP3,atrim+concat 不改变声道/采样率,
		// 这些参数幂等(重编码为相同参数,代际损失可忽略 —— MP3 同码率二次编码 PSNR 通常 > 60dB,
		// 远高于 ASR 模型的容忍下限)。保留为显式声明,避免输入参数漂移。qoder v1 M-3。
		"-ac", "1", "-ar", "16000", "-b:a", "64k",
		"-f", "mp3",
		outputPath,
	)
	executil.HideWindow(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("vad trim: ffmpeg failed: %w: %s", err, string(output))
	}
	return nil
}

// trimPCMStream 让一个 ffmpeg 顺序解码为 s16le，Go 在字节流上跳过不保留的
// 区间，再交给第二个 ffmpeg 编码为 MP3。整个输入只解码一次，复杂度不会随
// speech 片段数量相乘。
func (p *VADProcessor) trimPCMStream(ctx context.Context, inputPath, outputPath string, segs []KeptSegment) error {
	decoder := exec.CommandContext(ctx, p.ffmpeg,
		"-hide_banner", "-loglevel", "error", "-i", inputPath,
		"-vn", "-ac", "1", "-ar", strconv.FormatInt(trimSampleRate, 10),
		"-f", "s16le", "pipe:1",
	)
	decoderOut, err := decoder.StdoutPipe()
	if err != nil {
		return fmt.Errorf("vad trim: decoder stdout: %w", err)
	}
	var decoderErr bytes.Buffer
	decoder.Stderr = &decoderErr

	encoder := exec.CommandContext(ctx, p.ffmpeg,
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "s16le", "-ar", strconv.FormatInt(trimSampleRate, 10), "-ac", "1", "-i", "pipe:0",
		"-b:a", "64k", "-f", "mp3", outputPath,
	)
	encoderIn, err := encoder.StdinPipe()
	if err != nil {
		return fmt.Errorf("vad trim: encoder stdin: %w", err)
	}
	var encoderErr bytes.Buffer
	encoder.Stderr = &encoderErr
	executil.HideWindow(decoder)
	executil.HideWindow(encoder)

	if err := decoder.Start(); err != nil {
		return fmt.Errorf("vad trim: start decoder: %w", err)
	}
	if err := encoder.Start(); err != nil {
		_ = decoder.Process.Kill()
		_ = decoder.Wait()
		return fmt.Errorf("vad trim: start encoder: %w", err)
	}

	fail := func(copyErr error) error {
		_ = encoderIn.Close()
		_ = decoderOut.Close()
		_ = decoder.Process.Kill()
		_ = encoder.Process.Kill()
		_ = decoder.Wait()
		_ = encoder.Wait()
		return fmt.Errorf("vad trim: stream copy: %w (decoder: %s; encoder: %s)",
			copyErr, strings.TrimSpace(decoderErr.String()), strings.TrimSpace(encoderErr.String()))
	}

	var cursor int64
	for _, seg := range segs {
		start, end := pcmByteRange(seg)
		if start < cursor || end <= start {
			return fail(fmt.Errorf("invalid kept segment %d-%d after cursor %d", start, end, cursor))
		}
		if _, err := io.CopyN(io.Discard, decoderOut, start-cursor); err != nil {
			return fail(err)
		}
		if _, err := io.CopyN(encoderIn, decoderOut, end-start); err != nil {
			return fail(err)
		}
		cursor = end
	}
	if err := encoderIn.Close(); err != nil {
		return fail(err)
	}
	if _, err := io.Copy(io.Discard, decoderOut); err != nil {
		return fail(err)
	}
	if err := decoder.Wait(); err != nil {
		_ = encoder.Process.Kill()
		_ = encoder.Wait()
		return fmt.Errorf("vad trim: decoder failed: %w: %s", err, strings.TrimSpace(decoderErr.String()))
	}
	if err := encoder.Wait(); err != nil {
		return fmt.Errorf("vad trim: encoder failed: %w: %s", err, strings.TrimSpace(encoderErr.String()))
	}
	return nil
}

func pcmByteRange(seg KeptSegment) (int64, int64) {
	bytesPerMS := trimSampleRate * trimBytesPerSample / 1000
	return seg.OriginalStartMS * bytesPerMS, seg.OriginalEndMS * bytesPerMS
}

// buildAtrimConcatFilter 构造 atrim+concat filter_complex 字符串。导出供测试(无需真跑 ffmpeg)。
//
// ffmpeg atrim 同时支持位置参数(start:0.000:end:4.120)和命名参数(start=0.000:end=4.120),
// 这里用命名参数形式,可读性更好。
// 输出形如:
//
//	[0:a]atrim=start=0.000:end=4.120,asetpts=PTS-STARTPTS[a0];[0:a]atrim=start=8.200:end=15.300,asetpts=PTS-STARTPTS[a1];[a0][a1]concat=n=2:v=0:a=1[vad_out]
func buildAtrimConcatFilter(segs []KeptSegment) string {
	filterParts := make([]string, 0, len(segs)+1)
	concatInputs := make([]string, 0, len(segs))
	for i, seg := range segs {
		startSec := float64(seg.OriginalStartMS) / 1000.0
		endSec := float64(seg.OriginalEndMS) / 1000.0
		label := fmt.Sprintf("a%d", i)
		filterParts = append(filterParts,
			fmt.Sprintf("[0:a]atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS[%s]",
				startSec, endSec, label))
		concatInputs = append(concatInputs, "["+label+"]")
	}
	filterParts = append(filterParts,
		strings.Join(concatInputs, "")+
			fmt.Sprintf("concat=n=%d:v=0:a=1[vad_out]", len(segs)))
	return strings.Join(filterParts, ";")
}

// BuildSilenceMap 把 detect 结果转成 SilenceMap。
//
// 算法(qoder v1 I-2/C-1 修订,清理死代码):
//  1. detect 返回 [s1, e1], [s2, e2], ... 表示"静音段"(要裁掉的)
//  2. speech 段是静音段之间的区域:[0, s1], [e1, s2], ..., [eN, origDuration]
//  3. 每个 speech 段两端各向相邻静音区扩 padding(padding 不超过静音区中点)
//     - 首 speech 段 [0, s1]:前面无静音 → paddingStart=0;后面静音 [s1,e1] → paddingEnd
//     - 中间 speech [e_{i-1}, s_i]:两边静音 → paddingStart + paddingEnd
//     - 尾 speech [eN, origDuration]:前面静音 → paddingStart;后面无静音(到文件尾)→ paddingEnd=0
//  4. 尾部静音(EndMS=0,音频以静音结尾)→ 被 valid 过滤丢弃,自然不形成尾 speech 段
//  5. 不变量:saved segment 的 original 长度 == trimmed 长度(padding 在 original 侧体现,见 SilenceMap 注释)
//
// 边界:
//   - intervals 为空(无静音)→ kept_segments = [{0, origMS, 0, origMS}](全说话,trimmed 与 original 相同)
//   - origMS <= 0 → nil(让 LoadSilenceMap 跳过 remap)
func (p *VADProcessor) BuildSilenceMap(intervals []SilenceInterval, origDurationMS int64) *SilenceMap {
	if origDurationMS <= 0 {
		return nil
	}
	paddingMS := int64(p.cfg.VAD.PaddingSec * 1000)
	if paddingMS < 0 {
		paddingMS = 0
	}
	minSilenceMS := int64(p.cfg.VAD.MinSilenceSec * 1000)

	// 过滤掉过短的 silence(silencedetect 已按 d 过滤,这里二次防御)+ 尾部静音(EndMS=0 不形成 kept)
	var valid []SilenceInterval
	for _, iv := range intervals {
		if iv.EndMS == 0 {
			continue
		}
		if iv.EndMS-iv.StartMS < minSilenceMS {
			continue
		}
		valid = append(valid, iv)
	}

	sm := &SilenceMap{
		VADVersion:         1,
		Params:             SilenceMapParam{ThresholdDB: p.cfg.VAD.ThresholdDB, MinSilenceSec: p.cfg.VAD.MinSilenceSec, PaddingSec: p.cfg.VAD.PaddingSec, Detection: "peak"},
		OriginalDurationMS: origDurationMS,
	}

	// 无静音:单段全说话
	if len(valid) == 0 {
		sm.KeptSegments = []KeptSegment{{
			OriginalStartMS: 0,
			OriginalEndMS:   origDurationMS,
			TrimmedStartMS:  0,
			TrimmedEndMS:    origDurationMS,
		}}
		sm.TrimmedDurationMS = origDurationMS
		return sm
	}

	// 构造 speech 段列表。每个 speech 段记录其前后相邻的静音区间(用于 padding 借取)。
	type speechRange struct {
		start, end               int64
		prevSilence, nextSilence *SilenceInterval // nil 表示该侧无静音(首段前/末段后)
	}
	var speeches []speechRange
	// 首 speech:[0, valid[0].StartMS],前面无静音,后面 valid[0]
	if valid[0].StartMS > 0 {
		speeches = append(speeches, speechRange{start: 0, end: valid[0].StartMS, nextSilence: &valid[0]})
	}
	// 中间 speech:[valid[i].EndMS, valid[i+1].StartMS]
	for i := 0; i < len(valid)-1; i++ {
		s := valid[i].EndMS
		e := valid[i+1].StartMS
		if e > s {
			speeches = append(speeches, speechRange{
				start: s, end: e,
				prevSilence: &valid[i], nextSilence: &valid[i+1],
			})
		}
	}
	// 尾 speech:[valid[last].EndMS, origDurationMS],前面 valid[last],后面无静音
	last := valid[len(valid)-1]
	if last.EndMS < origDurationMS {
		speeches = append(speeches, speechRange{start: last.EndMS, end: origDurationMS, prevSilence: &last})
	}

	if len(speeches) == 0 {
		return nil // 全静音(所有区间覆盖全段)
	}

	// 对每个 speech 段加 padding,生成 kept 段。
	var kept []KeptSegment
	cursorTrim := int64(0)
	for _, sp := range speeches {
		keptStart := sp.start
		if sp.prevSilence != nil {
			// 从前段静音借 padding,不超过静音区中点
			padStart := paddingMS
			avail := sp.start - sp.prevSilence.StartMS // 前段静音总长(从静音 start 到 speech start)
			if 2*padStart > avail {
				padStart = avail / 2
			}
			keptStart = sp.start - padStart
		}
		keptEnd := sp.end
		if sp.nextSilence != nil {
			padEnd := paddingMS
			avail := sp.nextSilence.EndMS - sp.end // 后段静音总长(从 speech end 到静音 end)
			if 2*padEnd > avail {
				padEnd = avail / 2
			}
			keptEnd = sp.end + padEnd
		}
		// 防御:夹到合法范围 + 不与前段重叠
		if keptStart < 0 {
			keptStart = 0
		}
		if len(kept) > 0 && keptStart < kept[len(kept)-1].OriginalEndMS {
			keptStart = kept[len(kept)-1].OriginalEndMS
		}
		if keptEnd > origDurationMS {
			keptEnd = origDurationMS
		}
		if keptEnd <= keptStart {
			continue // padding 完全吃掉了极短 speech,跳过
		}
		kept = append(kept, KeptSegment{
			OriginalStartMS: keptStart,
			OriginalEndMS:   keptEnd,
			TrimmedStartMS:  cursorTrim,
			TrimmedEndMS:    cursorTrim + (keptEnd - keptStart),
		})
		cursorTrim += (keptEnd - keptStart)
	}
	if len(kept) == 0 {
		return nil
	}
	sm.KeptSegments = kept
	sm.TrimmedDurationMS = cursorTrim
	return sm
}
