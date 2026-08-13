package biliutil

import (
	"regexp"
	"strconv"
	"time"
)

var replayDatePatterns = []*regexp.Regexp{
	// 官方回放标题常见格式：2026年08月04日18点场。小时可省略。
	regexp.MustCompile(`((?:19|20)[0-9]{2})年(0?[1-9]|1[0-2])月(0?[1-9]|[12][0-9]|3[01])日(?:\s*([01]?[0-9]|2[0-3])点)?`),
	// 录播账号常见格式：20260804 或 2026-08-04 / 2026.08.04。
	regexp.MustCompile(`(?:^|[^0-9])((?:19|20)[0-9]{2})(?:[-./]?)(0[1-9]|1[0-2])(?:[-./]?)(0[1-9]|[12][0-9]|3[01])(?:[^0-9]|$)`),
}

// ReplayDateFromTitle 从录播标题提取明确日期。官方中文标题还可能带小时（如
// “18点场”）；其它格式使用本地零点，按仅精确到日期的元数据处理。
func ReplayDateFromTitle(title string) (time.Time, bool) {
	var match []string
	for _, pattern := range replayDatePatterns {
		match = pattern.FindStringSubmatch(title)
		if len(match) >= 4 {
			break
		}
	}
	if len(match) < 4 {
		return time.Time{}, false
	}
	year, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])
	hour := 0
	if len(match) >= 5 && match[4] != "" {
		hour, _ = strconv.Atoi(match[4])
	}
	result := time.Date(year, time.Month(month), day, hour, 0, 0, 0, time.Local)
	if result.Year() != year || int(result.Month()) != month || result.Day() != day {
		return time.Time{}, false
	}
	return result, true
}
