package recap

import (
	"fmt"
	"strings"
)

// FormatDanmakuStats generates a Markdown section for danmaku statistics.
func FormatDanmakuStats(stats *danmakuStats, vars *TemplateVars) string {
	if stats == nil || stats.TotalCount == 0 {
		return ""
	}
	_ = vars // reserved for future template variable usage
	var b strings.Builder
	b.WriteString("### 📊 弹幕统计\n\n")
	b.WriteString(fmt.Sprintf("• 总弹幕数：%d 条\n", stats.TotalCount))
	if stats.UniqueTexts > 0 {
		b.WriteString(fmt.Sprintf("• 去重弹幕：%d 条\n", stats.UniqueTexts))
	}
	b.WriteString(fmt.Sprintf("• 独立用户：%d 人\n", stats.UniqueUsers))
	if len(stats.HighWeightDanmaku) > 0 {
		b.WriteString(fmt.Sprintf("• 高权重弹幕（≥8）：%d 条\n", len(stats.HighWeightDanmaku)))
	}
	if stats.AvgPerMin > 0 {
		b.WriteString(fmt.Sprintf("• 平均每分钟：%.1f 条\n", stats.AvgPerMin))
	}
	if stats.UniqueUsers > 0 {
		perUser := float64(stats.TotalCount) / float64(stats.UniqueUsers)
		b.WriteString(fmt.Sprintf("• 人均弹幕：%.1f 条\n", perUser))
	}

	if len(stats.BurstMoments) > 0 {
		b.WriteString("\n### 弹幕突发时刻\n\n")
		b.WriteString("| 时间 | 弹幕数 | 突发倍数 |\n|------|--------|----------|\n")
		for _, bm := range stats.BurstMoments {
			b.WriteString(fmt.Sprintf("| %s | %d 条 | %.1fx |\n", bm.TimeMinSec, bm.PeakCount, bm.BurstFactor))
		}
	}

	return b.String()
}

// appendDanmakuStats inserts programmatic danmaku statistics into the recap markdown.
// It finds the "## 弹幕互动精选" section and inserts the stats as a separate
// "### 📊 弹幕统计" subsection, keeping it distinct from AI-generated content.
func appendDanmakuStats(recap string, statsSection string) string {
	if statsSection == "" {
		return recap
	}
	_, contentStart, nextStart, ok := findSectionByTitle(recap, "弹幕互动精选")
	if !ok {
		return recap + "\n\n## 弹幕互动精选\n\n" + statsSection
	}

	// Remove any AI-generated stats heading to avoid duplication
	aiStatsHeading := "### 📊 弹幕统计"
	statsIdx := strings.Index(recap[contentStart:], aiStatsHeading)
	if statsIdx >= 0 {
		absoluteIdx := contentStart + statsIdx
		// Find end of this subsection (next ### or ##)
		endIdx := absoluteIdx + len(aiStatsHeading)
		rest := recap[endIdx:]
		subEnd := findNextSubsection(rest)
		if subEnd >= 0 {
			rest = rest[subEnd:]
		} else {
			if nextStart >= 0 {
				rest = recap[nextStart:]
			} else {
				rest = ""
			}
		}
		recap = recap[:absoluteIdx] + rest
		// Recalculate positions after edit
		_, contentStart, nextStart, ok = findSectionByTitle(recap, "弹幕互动精选")
		if !ok {
			return recap
		}
	}

	insert := strings.TrimSpace(statsSection) + "\n\n"
	if nextStart < 0 {
		content := strings.TrimSpace(recap[contentStart:])
		if content == "" {
			return strings.TrimRight(recap[:contentStart], " \n") + "\n\n" + insert
		}
		return strings.TrimRight(recap[:contentStart], " \n") + "\n\n" + insert + content + "\n"
	}
	content := strings.TrimSpace(recap[contentStart:nextStart])
	prefix := strings.TrimRight(recap[:contentStart], " \n")
	suffix := recap[nextStart:]
	if content == "" {
		return prefix + "\n\n" + insert + suffix
	}
	return prefix + "\n\n" + insert + content + "\n\n" + strings.TrimLeft(suffix, "\n")
}

// findNextSubsection returns the index of the next ### or ## heading in text.
func findNextSubsection(text string) int {
	lines := strings.SplitAfter(text, "\n")
	offset := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i > 0 && (strings.HasPrefix(trimmed, "### ") || (strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### "))) {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func findSectionByTitle(markdown string, title string) (headingStart int, contentStart int, nextStart int, ok bool) {
	lines := strings.SplitAfter(markdown, "\n")
	offset := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") && strings.Contains(trimmed, title) {
			contentStart = offset + len(line)
			nextStart = -1
			nextOffset := contentStart
			for _, nextLine := range lines[i+1:] {
				nextTrimmed := strings.TrimSpace(nextLine)
				if strings.HasPrefix(nextTrimmed, "## ") && !strings.HasPrefix(nextTrimmed, "### ") {
					nextStart = nextOffset
					break
				}
				nextOffset += len(nextLine)
			}
			return offset, contentStart, nextStart, true
		}
		offset += len(line)
	}
	return 0, 0, 0, false
}
