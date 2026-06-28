package recap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"hikami-go/internal/session"
)

func parseTaskRange(payload string) (*timeRange, error) {
	var p taskPayload
	if strings.TrimSpace(payload) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return nil, err
	}
	if p.StartTime == nil && p.EndTime == nil {
		return nil, nil
	}
	if p.StartTime == nil || p.EndTime == nil || *p.StartTime < 0 || *p.EndTime <= *p.StartTime {
		return nil, fmt.Errorf("invalid recap time range")
	}
	if math.IsNaN(*p.StartTime) || math.IsNaN(*p.EndTime) || math.IsInf(*p.StartTime, 0) || math.IsInf(*p.EndTime, 0) {
		return nil, fmt.Errorf("invalid recap time range")
	}
	return &timeRange{StartSec: *p.StartTime, EndSec: *p.EndTime}, nil
}

func (h *Handler) filteredTranscript(sessionInfo session.Session, r timeRange, fallback []byte) ([]byte, error) {
	packageDir := filepath.Join(h.sessionDir(sessionInfo), "package")
	for _, name := range []string{"transcript.srt", "transcript.vtt"} {
		path := filepath.Join(packageDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		filtered := filterTimedTextByRange(string(data), r)
		if strings.TrimSpace(filtered) != "" {
			return []byte(filtered), nil
		}
	}
	return fallback, nil
}

func (h *Handler) filteredDanmakuData(sessionInfo session.Session, r timeRange, fallback []byte) ([]byte, error) {
	data, err := filterDanmakuByRange(fallback, r)
	if err == nil {
		if err := h.writePartialDanmakuFile(sessionInfo, r, data); err != nil {
			return nil, err
		}
		return data, nil
	}

	rawPath := filepath.Join(h.sessionDir(sessionInfo), "raw", "danmaku.jsonl")
	data, rawErr := filterDanmakuJSONLFileByRange(rawPath, r)
	if rawErr != nil {
		if os.IsNotExist(rawErr) {
			return nil, err
		}
		return nil, rawErr
	}
	if err := h.writePartialDanmakuFile(sessionInfo, r, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (h *Handler) writePartialDanmakuFile(sessionInfo session.Session, r timeRange, data []byte) error {
	recapDir := filepath.Join(h.sessionDir(sessionInfo), "recap")
	if err := os.MkdirAll(recapDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(recapDir, "danmaku"+recapRangeSuffix(&r)+".json"), data, 0o644)
}

func filterDanmakuJSONLFileByRange(path string, r timeRange) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	startMs := rangeStartMs(r)
	endMs := rangeEndMs(r)
	filtered := make([]rawDanmakuItem, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item rawDanmakuItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		if item.TimeMS >= startMs && item.TimeMS <= endMs {
			item.TimeMS -= startMs
			filtered = append(filtered, item)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(filtered)
}

func filterDanmakuByRange(data []byte, r timeRange) ([]byte, error) {
	var items []rawDanmakuItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	startMs := rangeStartMs(r)
	endMs := rangeEndMs(r)
	filtered := make([]rawDanmakuItem, 0, len(items))
	for _, item := range items {
		if item.TimeMS >= startMs && item.TimeMS <= endMs {
			item.TimeMS -= startMs
			filtered = append(filtered, item)
		}
	}
	if filtered == nil {
		filtered = []rawDanmakuItem{}
	}
	return json.Marshal(filtered)
}

func filterTimedTextByRange(content string, r timeRange) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	blocks := strings.Split(content, "\n\n")
	var out []string
	index := 1
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) == 0 {
			continue
		}
		timeLineIndex := -1
		for i, line := range lines {
			if strings.Contains(line, "-->") {
				timeLineIndex = i
				break
			}
		}
		if timeLineIndex < 0 {
			continue
		}
		startSec, endSec, ok := parseCaptionRange(lines[timeLineIndex])
		if !ok || endSec < r.StartSec || startSec > r.EndSec {
			continue
		}
		lines[timeLineIndex] = formatCaptionTime(maxFloat64(0, startSec-r.StartSec)) + " --> " + formatCaptionTime(maxFloat64(0, endSec-r.StartSec))
		if timeLineIndex > 0 {
			lines[0] = fmt.Sprintf("%d", index)
			index++
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n") + "\n"
}

func parseCaptionRange(line string) (float64, float64, bool) {
	parts := strings.Split(line, "-->")
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, ok := parseCaptionTime(strings.TrimSpace(strings.Fields(parts[0])[0]))
	if !ok {
		return 0, 0, false
	}
	rightFields := strings.Fields(strings.TrimSpace(parts[1]))
	if len(rightFields) == 0 {
		return 0, 0, false
	}
	end, ok := parseCaptionTime(rightFields[0])
	if !ok {
		return 0, 0, false
	}
	return start, end, true
}

func parseCaptionTime(value string) (float64, bool) {
	value = strings.Replace(value, ",", ".", 1)
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, false
	}
	hour, err1 := strconv.ParseFloat(parts[0], 64)
	minute, err2 := strconv.ParseFloat(parts[1], 64)
	second, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return hour*3600 + minute*60 + second, true
}

func formatCaptionTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	totalMs := int64(math.Round(sec * 1000))
	h := totalMs / 3600000
	m := (totalMs % 3600000) / 60000
	s := (totalMs % 60000) / 1000
	ms := totalMs % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func recapRangeSuffix(r *timeRange) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("_%s-%s", formatRangePart(r.StartSec), formatRangePart(r.EndSec))
}

func formatRangePart(sec float64) string {
	totalSec := int64(math.Round(sec))
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	s := totalSec % 60
	if h > 0 {
		return fmt.Sprintf("%02d%02d%02d", h, m, s)
	}
	return fmt.Sprintf("%02d%02d", m, s)
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func rangeStartMs(r timeRange) int64 {
	return int64(math.Round(r.StartSec * 1000))
}

func rangeEndMs(r timeRange) int64 {
	return int64(math.Round(r.EndSec * 1000))
}

func rangeDurationMs(r timeRange) int64 {
	return int64(math.Round((r.EndSec - r.StartSec) * 1000))
}
