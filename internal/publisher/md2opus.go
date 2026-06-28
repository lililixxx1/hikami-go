package publisher

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	fontSizeRegular  = 17
	fontSizeH2       = 22
	fontSizeH3       = 20
	fontLevelRegular = "regular"
	fontLevelH2      = "xLarge"
	fontLevelH3      = "large"
)

// nodeTypeWord 是 B 站 opus 编辑器文本节点类型常量。
// 经抓包官方 draft/add 请求确认：节点用整数 "node_type": 1 标识（非字符串 type），
// 字符串形式会导致草稿 content 字段存储为空（正文空白）。
const nodeTypeWord = 1

// para_type 常量（经抓包 B站当前 opus 编辑器真实请求确认）：
//
//	1=文本, 3=分割线, 4=引用, 6=列表, 9=标题
const (
	paraTypeText    = 1
	paraTypeHR      = 3
	paraTypeQuote   = 4
	paraTypeList    = 6
	paraTypeHeading = 9
)

// listItemAcc 用于在转换时临时累积连续的列表项，最后合并成一组共享 combine_hash 的 para_type=6 段落。
type listItemAcc struct {
	text  string
	theme string
}

// ConvertMarkdownToOpus 将 Markdown 转换为 B 站当前 opus 编辑器的段落序列。
// 段落结构为扁平式：所有文字放 text.nodes，用 para_type + format 字段区分类型，
// 连续列表/引用用 format.combine_hash 关联。段落紧凑排列，不插入空段落分隔符
// （空段落会导致草稿正文每句之间出现多余空行，已移除）。
func ConvertMarkdownToOpus(md string) []OpusParagraph {
	lines := strings.Split(md, "\n")
	var paragraphs []OpusParagraph

	inCodeBlock := false
	var pending []listItemAcc

	// flushList 把已累积的连续列表项转换成一组共享 combine_hash 的 para_type=6 段落。
	flushList := func() {
		if len(pending) == 0 {
			return
		}
		theme := pending[0].theme
		hash := uniqueHash("list")
		for i, it := range pending {
			paragraphs = append(paragraphs, makeListParagraph(it.text, theme, i+1, hash))
		}
		pending = nil
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// 代码块边界跳过，代码内容按普通文本保留
		if strings.HasPrefix(trimmed, "```") {
			flushList()
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			if trimmed == "" {
				continue
			}
			flushList()
			paragraphs = append(paragraphs, makeTextParagraph(trimmed))
			continue
		}

		// 表格行：用 | 连接单元格；表头行（下一行是分隔行）整行加粗；分隔行跳过
		if strings.HasPrefix(trimmed, "|") {
			flushList()
			cells := parseTableCells(trimmed)
			if len(cells) > 0 && !isTableSeparator(cells) {
				isHeader := false
				if i+1 < len(lines) {
					next := strings.TrimSpace(lines[i+1])
					if strings.HasPrefix(next, "|") && isTableSeparator(parseTableCells(next)) {
						isHeader = true
					}
				}
				text := formatTableRow(cells)
				if isHeader {
					text = "**" + text + "**"
				}
				paragraphs = append(paragraphs, makeTextParagraph(text))
			}
			continue
		}

		// 空行
		if trimmed == "" {
			flushList()
			continue
		}

		// 分割线（---），作为普通 HR 输出，不吞后续内容
		if isHR(trimmed) {
			flushList()
			paragraphs = append(paragraphs, makeHR())
			continue
		}

		// H1 跳过
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			flushList()
			continue
		}

		// H2 / H3（para_type=9 + format.heading_type）
		if strings.HasPrefix(trimmed, "## ") {
			flushList()
			paragraphs = append(paragraphs, makeHeading(strings.TrimPrefix(trimmed, "## "), 2))
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			flushList()
			paragraphs = append(paragraphs, makeHeading(strings.TrimPrefix(trimmed, "### "), 3))
			continue
		}

		// 引用块（para_type=4 + combine_hash）
		if strings.HasPrefix(trimmed, "> ") {
			flushList()
			paragraphs = append(paragraphs, makeBlockquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "> "))))
			continue
		}
		if trimmed == ">" {
			flushList()
			paragraphs = append(paragraphs, makeBlockquote(""))
			continue
		}

		// 无序列表（累积，由 flushList 合并）
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			pending = append(pending, listItemAcc{trimmed[2:], "dot"})
			continue
		}

		// 有序列表（累积，由 flushList 合并）
		if olMatch := matchOrderedList(trimmed); olMatch != "" {
			pending = append(pending, listItemAcc{olMatch, "arabic_num"})
			continue
		}

		// 普通文本
		flushList()
		paragraphs = append(paragraphs, makeTextParagraph(trimmed))
	}
	flushList()

	return paragraphs
}

// uniqueHash 生成 B站编辑器风格的 combine_hash：前缀_时间戳_随机hex。
// 用于关联连续的列表项或引用段落（同组共享同一 hash）。
func uniqueHash(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%d_%s", prefix, 0, hex.EncodeToString(b))
}

func parseTableCells(line string) []string {
	trimmed := strings.Trim(line, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cell := strings.TrimSpace(part)
		if cell != "" {
			cells = append(cells, cell)
		}
	}
	return cells
}

func isTableSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		normalized := strings.Trim(cell, " :-")
		if normalized != "" {
			return false
		}
		if !strings.Contains(cell, "-") {
			return false
		}
	}
	return true
}

func formatTableRow(cells []string) string {
	return strings.Join(cells, " | ")
}

func isHR(line string) bool {
	cleaned := strings.ReplaceAll(line, "-", "")
	cleaned = strings.ReplaceAll(cleaned, "*", "")
	cleaned = strings.ReplaceAll(cleaned, "_", "")
	return len(cleaned) == 0 && len(line) >= 3
}

func matchOrderedList(line string) string {
	// 匹配 "1. xxx" 或 "2. xxx" 等
	for i := 0; i < len(line); i++ {
		if line[i] < '0' || line[i] > '9' {
			if i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' ' {
				return line[i+2:]
			}
			break
		}
	}
	return ""
}

func makeWord(words string, bold bool) OpusWord {
	w := OpusWord{
		Words:     words,
		FontSize:  fontSizeRegular,
		FontLevel: fontLevelRegular,
	}
	if bold {
		w.Style = &OpusStyle{Bold: true}
	}
	return w
}

func makeNode(words string, bold bool) OpusNode {
	return OpusNode{
		NodeType: nodeTypeWord,
		Word:     ptrWord(makeWord(words, bold)),
	}
}

func ptrWord(w OpusWord) *OpusWord { return &w }

func defaultIndent() *OpusIndent {
	return &OpusIndent{FirstLineIndent: 0, Indent: 0}
}

func makeTextParagraph(text string) OpusParagraph {
	return OpusParagraph{
		ParaType: paraTypeText,
		Format:   &OpusFormat{Indent: defaultIndent()},
		Text:     &OpusText{Nodes: parseInlineBold(text)},
	}
}

// makeHeading 生成标题段落（para_type=9, format.heading_type=级别）。
// 内容放在 text.nodes（与官方一致），而非旧的 heading.nodes。
func makeHeading(text string, level int) OpusParagraph {
	fontSize := fontSizeH2
	fontLevel := fontLevelH2
	if level == 3 {
		fontSize = fontSizeH3
		fontLevel = fontLevelH3
	}
	nodes := parseInlineBold(text)
	for i := range nodes {
		if nodes[i].Word != nil {
			nodes[i].Word.FontSize = fontSize
			nodes[i].Word.FontLevel = fontLevel
		}
	}
	return OpusParagraph{
		ParaType: paraTypeHeading,
		Format:   &OpusFormat{Indent: defaultIndent(), HeadingType: level},
		Text:     &OpusText{Nodes: nodes},
	}
}

// makeListParagraph 生成单个列表项段落（para_type=6, format.list_format）。
// 同一组列表的各段落共享同一 combine_hash 以便编辑器识别为同一列表。
func makeListParagraph(text, theme string, order int, hash string) OpusParagraph {
	return OpusParagraph{
		ParaType: paraTypeList,
		Format: &OpusFormat{
			Indent:      defaultIndent(),
			ListFormat:  &OpusListFormat{Level: 1, Order: order, Theme: theme},
			CombineHash: hash,
		},
		Text: &OpusText{Nodes: parseInlineBold(text)},
	}
}

// makeBlockquote 生成引用段落（para_type=4, format.combine_hash）。
// 连续引用段落共享同一 hash。内容放在 text.nodes（与官方一致），而非旧的 blockquote.children。
func makeBlockquote(text string) OpusParagraph {
	var nodes []OpusNode
	if text == "" {
		nodes = []OpusNode{}
	} else {
		nodes = parseInlineBold(text)
	}
	return OpusParagraph{
		ParaType: paraTypeQuote,
		Format:   &OpusFormat{Indent: defaultIndent(), CombineHash: uniqueHash("blockquote")},
		Text:     &OpusText{Nodes: nodes},
	}
}

// makeHR 生成分割线段落（para_type=3）。
// 经抓包确认：分割线只含 line.line_type=1，不带 format/align 字段，
// 否则会被渲染器误判成含"1"的代码块。
func makeHR() OpusParagraph {
	return OpusParagraph{
		ParaType: paraTypeHR,
		Line:     &OpusLine{LineType: 1},
	}
}

func parseInlineBold(text string) []OpusNode {
	// 去除行内代码标记
	text = stripInlineCode(text)
	// 去除链接，保留文本
	text = stripLinks(text)

	parts := strings.Split(text, "**")
	var nodes []OpusNode
	for i, part := range parts {
		if part == "" {
			continue
		}
		bold := i%2 == 1
		nodes = append(nodes, makeNode(part, bold))
	}
	if len(nodes) == 0 {
		nodes = append(nodes, makeNode(text, false))
	}
	return nodes
}

func stripInlineCode(text string) string {
	var result strings.Builder
	inCode := false
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			inCode = !inCode
			continue
		}
		result.WriteByte(text[i])
	}
	return result.String()
}

func stripLinks(text string) string {
	// 处理图片 ![alt](src) → alt
	for {
		imgStart := strings.Index(text, "![")
		if imgStart == -1 {
			break
		}
		altEnd := strings.Index(text[imgStart:], "](")
		if altEnd == -1 {
			break
		}
		altEnd += imgStart
		urlEnd := strings.Index(text[altEnd:], ")")
		if urlEnd == -1 {
			break
		}
		altText := text[imgStart+2 : altEnd]
		text = text[:imgStart] + altText + text[altEnd+urlEnd+1:]
	}
	// 处理链接 [text](url) → text（url）
	for {
		start := strings.Index(text, "[")
		if start == -1 {
			break
		}
		end := strings.Index(text[start:], "](")
		if end == -1 {
			break
		}
		end += start
		urlEnd := strings.Index(text[end:], ")")
		if urlEnd == -1 {
			break
		}
		linkText := text[start+1 : end]
		linkURL := text[end+2 : end+urlEnd]
		replacement := linkText
		// 站内锚点（#开头）在 B站专栏无意义，直接丢弃 url 只保留文字；
		// 外部 url 保留为"文字（url）"形式。
		if linkURL != "" && !strings.HasPrefix(linkURL, "#") {
			replacement = linkText + "（" + linkURL + "）"
		}
		text = text[:start] + replacement + text[end+urlEnd+1:]
	}
	return text
}
