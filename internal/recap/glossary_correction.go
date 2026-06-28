package recap

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"hikami-go/internal/glossary"
)

// quotedContentRe matches text inside quotation marks: straight "", Chinese curly """, and corner brackets 「」.
var quotedContentRe = regexp.MustCompile(`"[^"]*"|"[^"]*"|「[^」]*」`)

type glossaryReplacement struct {
	term      string
	canonical string
}

func applyGlossaryCorrections(ctx context.Context, store *glossary.Store, channelID string, content string) string {
	if store == nil || content == "" {
		return content
	}
	entries, err := store.ListByChannel(ctx, channelID)
	if err != nil || len(entries) == 0 {
		return content
	}

	replacements := make([]glossaryReplacement, 0, len(entries))
	for _, entry := range entries {
		term := strings.TrimSpace(entry.Term)
		canonical := strings.TrimSpace(entry.Canonical)
		if !entry.Enabled || term == "" || canonical == "" || term == canonical {
			continue
		}
		replacements = append(replacements, glossaryReplacement{term: term, canonical: canonical})
	}
	sort.SliceStable(replacements, func(i, j int) bool {
		return len([]rune(replacements[i].term)) > len([]rune(replacements[j].term))
	})

	lines := strings.SplitAfter(content, "\n")
	for i, line := range lines {
		if isMarkdownQuoteLine(line) || isDanmakuQuoteLine(line) {
			continue
		}
		lines[i] = applyReplacementsPreservingQuotes(line, replacements)
	}
	return strings.Join(lines, "")
}

// applyReplacementsPreservingQuotes applies term replacements while preserving
// text inside quotation marks (both Chinese "" and straight "") verbatim.
func applyReplacementsPreservingQuotes(line string, replacements []glossaryReplacement) string {
	// Collect all quoted segments and replace with placeholders
	var quotes []string
	result := quotedContentRe.ReplaceAllStringFunc(line, func(match string) string {
		idx := len(quotes)
		quotes = append(quotes, match)
		return fmt.Sprintf("\x00Q%d\x00", idx)
	})

	// Apply corrections to non-quoted parts
	for _, repl := range replacements {
		result = strings.ReplaceAll(result, repl.term, repl.canonical)
	}

	// Restore quoted segments
	for i, q := range quotes {
		result = strings.Replace(result, fmt.Sprintf("\x00Q%d\x00", i), q, 1)
	}
	return result
}

func isMarkdownQuoteLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, ">") {
		return false
	}
	// CommonMark: 4+ spaces or tab-only indent is a code block, not a quote.
	indent := len(line) - len(trimmed)
	return indent < 4
}

func isDanmakuQuoteLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, "▶")
}

var suggestedTermMarkerRegex = regexp.MustCompile(`\[应为[：:][^\]]+\]`)

func cleanSuggestedTermMarkers(content string) string {
	if content == "" {
		return content
	}
	return suggestedTermMarkerRegex.ReplaceAllString(content, "")
}

var finalAddressHeadingRE = regexp.MustCompile(`(?m)^##\s+[^\n]*(致[^#\n]*)$`)

const generatedNotice = "\n\n> 本文由 Hikami-Go 自动生成，基于直播转写和弹幕数据分析。"

func ensureFinalAddressSection(content string) string {
	matches := finalAddressHeadingRE.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return content
	}
	start := matches[len(matches)-1][0]
	before := strings.TrimRight(content[:start], " \n")
	finalSection := strings.TrimSpace(content[start:])
	finalSection = trimTrailingGeneratedNotice(finalSection)
	if before == "" {
		return finalSection + "\n"
	}
	return before + "\n\n" + finalSection + "\n"
}

func trimTrailingGeneratedNotice(section string) string {
	lines := strings.Split(section, "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" || isGeneratedNoticeLine(last) || last == "---" {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// generatedNoticeRE 匹配回顾末尾的自动生成署名，结构为「本文/本回顾由 <品牌名/AI> 自动生成」。
// 用结构匹配而非绑定具体品牌名，使历史 Hazel 签名及 AI/Hikami-Go 变体都能被正确去重，
// 避免改名过渡期或 AI 模仿历史输出时出现重复署名。
var generatedNoticeRE = regexp.MustCompile(`(本文|本回顾)由.{0,20}?自动生成`)

// hasGeneratedNotice 报告回顾正文中是否已存在自动生成署名（不限品牌名）。
func hasGeneratedNotice(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if isGeneratedNoticeLine(line) {
			return true
		}
	}
	return false
}

func isGeneratedNoticeLine(line string) bool {
	line = strings.Trim(line, ">*_ ")
	return generatedNoticeRE.MatchString(line)
}
