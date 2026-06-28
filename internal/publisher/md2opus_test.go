package publisher

import (
	"encoding/json"
	"testing"
)

// contentParagraphs 过滤掉前导空段落（para_type=1 且 nodes 为空），返回实际内容段落。
// 测试基于内容段落断言，不受"每段前加空段"传输细节影响。
func contentParagraphs(paras []OpusParagraph) []OpusParagraph {
	out := make([]OpusParagraph, 0, len(paras))
	for _, p := range paras {
		if p.ParaType == paraTypeText && p.Text != nil && len(p.Text.Nodes) == 0 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func TestConvertPlainText(t *testing.T) {
	result := ConvertMarkdownToOpus("hello world")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	if content[0].ParaType != paraTypeText {
		t.Errorf("expected para_type %d, got %d", paraTypeText, content[0].ParaType)
	}
	if got := extractWords(content[0]); got != "hello world" {
		t.Errorf("expected words %q, got %q", "hello world", got)
	}
	// node NodeType 必须是整数 1（字符串 type 会让 B站草稿 content 存储为空）
	if got := nodeType(content[0]); got != nodeTypeWord {
		t.Errorf("expected node type %d, got %d", nodeTypeWord, got)
	}
}

func TestConvertBold(t *testing.T) {
	result := ConvertMarkdownToOpus("**bold** text")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	p := content[0]
	if p.ParaType != paraTypeText || p.Text == nil || len(p.Text.Nodes) != 2 {
		t.Fatalf("unexpected structure: %+v", p)
	}
	if p.Text.Nodes[0].Word == nil || !p.Text.Nodes[0].Word.Style.Bold {
		t.Error("first node should be bold")
	}
}

func TestConvertH2(t *testing.T) {
	result := ConvertMarkdownToOpus("## Title Here")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	p := content[0]
	if p.ParaType != paraTypeHeading {
		t.Errorf("expected para_type %d (heading), got %d", paraTypeHeading, p.ParaType)
	}
	if p.Format == nil || p.Format.HeadingType != 2 {
		t.Errorf("expected heading_type 2")
	}
	if p.Text == nil || p.Text.Nodes[0].Word.FontSize != fontSizeH2 || p.Text.Nodes[0].Word.FontLevel != fontLevelH2 {
		t.Errorf("expected H2 font settings")
	}
	if got := extractWords(p); got != "Title Here" {
		t.Errorf("expected words %q, got %q", "Title Here", got)
	}
}

func TestConvertH3(t *testing.T) {
	result := ConvertMarkdownToOpus("### Sub Title")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	p := content[0]
	if p.ParaType != paraTypeHeading || p.Format == nil || p.Format.HeadingType != 3 {
		t.Errorf("expected H3 heading (para_type %d, heading_type 3)", paraTypeHeading)
	}
	if p.Text == nil || p.Text.Nodes[0].Word.FontSize != fontSizeH3 || p.Text.Nodes[0].Word.FontLevel != fontLevelH3 {
		t.Errorf("expected H3 font settings")
	}
}

func TestConvertBlockquote(t *testing.T) {
	result := ConvertMarkdownToOpus("> quoted text")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	p := content[0]
	if p.ParaType != paraTypeQuote {
		t.Errorf("expected para_type %d (quote), got %d", paraTypeQuote, p.ParaType)
	}
	if p.Format == nil || p.Format.CombineHash == "" {
		t.Errorf("expected non-empty combine_hash for blockquote")
	}
	if got := extractWords(p); got != "quoted text" {
		t.Errorf("expected words %q, got %q", "quoted text", got)
	}
}

func TestConvertHR(t *testing.T) {
	md := "some content\n\n---\n\nmore content\n\n---\n\nend"
	result := ConvertMarkdownToOpus(md)
	hrCount := 0
	for _, p := range result {
		if p.ParaType == paraTypeHR {
			hrCount++
		}
	}
	if hrCount < 1 {
		t.Errorf("expected at least 1 HR paragraph, got %d", hrCount)
	}
}

func TestConvertUnorderedList(t *testing.T) {
	// 连续列表项转换为一组共享 combine_hash 的 para_type=6 段落
	result := ConvertMarkdownToOpus("- item one\n- item two\n- item three")
	content := contentParagraphs(result)
	if len(content) != 3 {
		t.Fatalf("expected 3 list paragraphs, got %d", len(content))
	}
	hashes := make(map[string]bool)
	for i, p := range content {
		if p.ParaType != paraTypeList {
			t.Errorf("item %d: expected para_type %d (list), got %d", i, paraTypeList, p.ParaType)
		}
		if p.Format == nil || p.Format.ListFormat == nil {
			t.Fatalf("item %d: expected list_format", i)
		}
		if p.Format.ListFormat.Theme != "dot" {
			t.Errorf("item %d: expected dot theme", i)
		}
		if p.Format.ListFormat.Order != i+1 {
			t.Errorf("item %d: expected order %d, got %d", i, i+1, p.Format.ListFormat.Order)
		}
		hashes[p.Format.CombineHash] = true
	}
	// 同组列表项应共享同一 combine_hash
	if len(hashes) != 1 {
		t.Errorf("expected all list items to share 1 combine_hash, got %d distinct", len(hashes))
	}
}

func TestConvertOrderedList(t *testing.T) {
	result := ConvertMarkdownToOpus("1. first\n2. second\n3. third")
	content := contentParagraphs(result)
	if len(content) != 3 {
		t.Fatalf("expected 3 list paragraphs, got %d", len(content))
	}
	for _, p := range content {
		if p.ParaType != paraTypeList || p.Format == nil || p.Format.ListFormat == nil || p.Format.ListFormat.Theme != "arabic_num" {
			t.Errorf("expected arabic_num ordered list (para_type %d)", paraTypeList)
		}
	}
}

func TestSkipEmptyLines(t *testing.T) {
	result := ConvertMarkdownToOpus("line1\n\n\nline2")
	content := contentParagraphs(result)
	if len(content) != 2 {
		t.Fatalf("expected 2 content paragraphs, got %d", len(content))
	}
}

func TestSkipH1(t *testing.T) {
	result := ConvertMarkdownToOpus("# H1 Title\n## H2 Title")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph (H1 skipped), got %d", len(content))
	}
	if content[0].ParaType != paraTypeHeading || content[0].Format == nil || content[0].Format.HeadingType != 2 {
		t.Errorf("expected H2 (para_type %d, heading_type 2)", paraTypeHeading)
	}
}

func TestConvertCodeBlock(t *testing.T) {
	result := ConvertMarkdownToOpus("before\n```\ncode here\nmore code\n```\nafter")
	content := contentParagraphs(result)
	if len(content) != 4 {
		t.Fatalf("expected 4 content paragraphs (code content preserved), got %d", len(content))
	}
	assertNodeWords(t, content[0], "before")
	assertNodeWords(t, content[1], "code here")
	assertNodeWords(t, content[2], "more code")
	assertNodeWords(t, content[3], "after")
}

func TestConvertCodeBlockWithLanguage(t *testing.T) {
	result := ConvertMarkdownToOpus("before\n```java\nSystem.out.println(\"hi\");\n```\nafter")
	content := contentParagraphs(result)
	if len(content) != 3 {
		t.Fatalf("expected 3 content paragraphs (code content preserved), got %d", len(content))
	}
	assertNodeWords(t, content[0], "before")
	assertNodeWords(t, content[1], "System.out.println(\"hi\");")
	assertNodeWords(t, content[2], "after")
}

func TestConvertTable(t *testing.T) {
	result := ConvertMarkdownToOpus("before\n| a | b |\n|---|---|\n| 1 | 2 |\nafter")
	content := contentParagraphs(result)
	if len(content) != 4 {
		t.Fatalf("expected 4 content paragraphs (table converted), got %d", len(content))
	}
	assertNodeWords(t, content[0], "before")
	assertNodeWords(t, content[1], "a | b")
	assertNodeWords(t, content[2], "1 | 2")
	assertNodeWords(t, content[3], "after")
}

func TestConvertTableComplex(t *testing.T) {
	md := "before\n| 名称 | 类型 | 说明 |\n| --- | --- | --- |\n| Hikami | 项目 | 直播回顾 |\n| B站 | 平台 | 专栏发布 |\nafter"
	result := ConvertMarkdownToOpus(md)
	content := contentParagraphs(result)
	if len(content) != 5 {
		t.Fatalf("expected 5 content paragraphs (complex table converted), got %d", len(content))
	}
	assertNodeWords(t, content[0], "before")
	assertNodeWords(t, content[1], "名称 | 类型 | 说明")
	assertNodeWords(t, content[2], "Hikami | 项目 | 直播回顾")
	assertNodeWords(t, content[3], "B站 | 平台 | 专栏发布")
	assertNodeWords(t, content[4], "after")
}

func TestHRDoesNotSwallowContent(t *testing.T) {
	// --- 作为分割线，不应吞掉后续内容（曾因 headerBlock 逻辑误把整个正文当装饰块跳过）
	md := "---\nTitle Here\n---\n\nreal content"
	result := ConvertMarkdownToOpus(md)
	found := false
	for _, p := range result {
		if extractWords(p) == "real content" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'real content' paragraph (HR should not swallow content)")
	}
}

func TestStripInlineCode(t *testing.T) {
	result := ConvertMarkdownToOpus("text with `code` here")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	assertNodeWords(t, content[0], "text with code here")
}

func TestStripLinks(t *testing.T) {
	result := ConvertMarkdownToOpus("click [here](https://example.com) now")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	assertNodeWords(t, content[0], "click here（https://example.com） now")
}

func TestStripAnchorLinks(t *testing.T) {
	// 站内锚点链接（#开头）应丢弃 url 只保留文字，不重复
	result := ConvertMarkdownToOpus("1. [直播概要](#直播概要)")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	assertNodeWords(t, content[0], "直播概要")
}

func TestStripImage(t *testing.T) {
	result := ConvertMarkdownToOpus("before ![alt text](https://img.example.com/pic.png) after")
	content := contentParagraphs(result)
	if len(content) != 1 {
		t.Fatalf("expected 1 content paragraph, got %d", len(content))
	}
	assertNodeWords(t, content[0], "before alt text after")
}

func TestMixedContent(t *testing.T) {
	md := `## 直播概要

这是一段普通文字。

> 这是一段引用

- 列表项一
- 列表项二

### 小节标题

更多内容`

	result := ConvertMarkdownToOpus(md)
	content := contentParagraphs(result)
	if len(content) < 5 {
		t.Fatalf("expected at least 5 content paragraphs, got %d", len(content))
	}

	// 验证各类型存在（当前 para_type: 9=标题, 6=列表, 4=引用, 1=文本）
	types := make(map[int]bool)
	for _, p := range content {
		types[p.ParaType] = true
	}
	if !types[paraTypeHeading] {
		t.Errorf("expected heading (para_type %d)", paraTypeHeading)
	}
	if !types[paraTypeText] {
		t.Errorf("expected text (para_type %d)", paraTypeText)
	}
	if !types[paraTypeQuote] {
		t.Errorf("expected blockquote (para_type %d)", paraTypeQuote)
	}
	if !types[paraTypeList] {
		t.Errorf("expected list (para_type %d)", paraTypeList)
	}
}

func TestJSONSerialization(t *testing.T) {
	p := makeHeading("测试标题", 2)
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if m["para_type"] != float64(paraTypeHeading) {
		t.Errorf("expected para_type %d, got %v", paraTypeHeading, m["para_type"])
	}
	fmt2, _ := m["format"].(map[string]any)
	if fmt2 == nil {
		t.Fatalf("expected format field in JSON")
	}
	if fmt2["heading_type"] != float64(2) {
		t.Errorf("expected format.heading_type 2, got %v", fmt2["heading_type"])
	}
	// 扁平结构：内容在 text.nodes，不应再有 heading 字段
	if _, ok := m["heading"]; ok {
		t.Errorf("should not have legacy heading field in flat structure")
	}
}

// extractWords 提取段落 text.nodes 内所有 word 文本。
// 扁平结构下所有内容（文本/标题/列表/引用）都在 text.nodes，无需递归。
func extractWords(p OpusParagraph) string {
	var actual string
	if p.Text != nil {
		for _, n := range p.Text.Nodes {
			if n.Word != nil {
				actual += n.Word.Words
			}
		}
	}
	return actual
}

// nodeType 返回段落首个节点的 NodeType。
func nodeType(p OpusParagraph) int {
	if p.Text != nil && len(p.Text.Nodes) > 0 {
		return p.Text.Nodes[0].NodeType
	}
	return 0
}

func assertNodeWords(t *testing.T, p OpusParagraph, expected string) {
	t.Helper()
	if got := extractWords(p); got != expected {
		t.Errorf("expected words %q, got %q", expected, got)
	}
}
