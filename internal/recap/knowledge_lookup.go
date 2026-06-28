package recap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// KnowledgeLookupResult 知识查询结果
type KnowledgeLookupResult struct {
	Topic     string          `json:"topic"`
	Source    string          `json:"source"`
	Terms     []KnowledgeTerm `json:"terms"`
	QueriedAt string          `json:"queried_at"`
}

// KnowledgeTerm 知识条目
type KnowledgeTerm struct {
	Original string `json:"original"`
	Correct  string `json:"correct"`
	Note     string `json:"note"`
}

// knowledgeLookupOptions 知识查询配置
type knowledgeLookupOptions struct {
	Enabled    bool
	MaxTerms   int
	TimeoutSec int
	Domains    []string
}

var topicPatterns = []struct {
	regex *regexp.Regexp
	name  string
}{
	{regexp.MustCompile("本所七大不可思议"), "本所七大不可思议"},
	{regexp.MustCompile("飞跃13号房|飞跃十三号房"), "飞跃13号房"},
	{regexp.MustCompile("逆转裁判"), "逆转裁判"},
	{regexp.MustCompile("喷射战士"), "喷射战士"},
}

// parseKnowledgeOptions 从 ResolvedTemplate.ExtraVars 解析配置
func parseKnowledgeOptions(extraVars map[string]string) knowledgeLookupOptions {
	opts := knowledgeLookupOptions{
		Enabled:    false,
		MaxTerms:   20,
		TimeoutSec: 15,
		Domains:    []string{"wiki.biligame.com", "zh.moegirl.org.cn"},
	}
	if v, ok := extraVars["knowledge_lookup_enabled"]; ok && (v == "true" || v == "1") {
		opts.Enabled = true
	}
	if v, ok := extraVars["knowledge_lookup_max_terms"]; ok && v != "" {
		var maxTerms int
		if n, err := fmt.Sscanf(v, "%d", &maxTerms); err == nil && n == 1 && maxTerms > 0 {
			opts.MaxTerms = maxTerms
		}
	}
	if v, ok := extraVars["knowledge_lookup_timeout_seconds"]; ok && v != "" {
		var timeoutSec int
		if n, err := fmt.Sscanf(v, "%d", &timeoutSec); err == nil && n == 1 && timeoutSec > 0 {
			opts.TimeoutSec = timeoutSec
		}
	}
	return opts
}

// performKnowledgeLookup 执行知识查询。
// 从 transcript 中识别游戏/番剧主题，查询 BWiki/萌娘百科。
// 返回查询结果，失败时返回空结果和 error。
func performKnowledgeLookup(ctx context.Context, transcript string, glossaryText string, opts knowledgeLookupOptions) (*KnowledgeLookupResult, error) {
	topic := detectTopicFromTranscript(transcript)
	if topic == "" {
		return nil, nil
	}

	if len(opts.Domains) == 0 {
		return nil, nil
	}
	source := strings.TrimSpace(opts.Domains[0])
	if source == "" {
		return nil, nil
	}
	searchURL := fmt.Sprintf("https://%s/%s", source, topic)

	queryCtx, cancel := context.WithTimeout(ctx, time.Duration(opts.TimeoutSec)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(queryCtx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("knowledge lookup request creation failed: %w", err)
	}
	req.Header.Set("User-Agent", "Hikami-Go/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("knowledge lookup request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("knowledge lookup returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return nil, fmt.Errorf("knowledge lookup read failed: %w", err)
	}

	terms := extractTermsFromHTML(string(body), opts.MaxTerms)
	if len(terms) == 0 {
		return nil, nil
	}

	terms = filterTermsByTranscript(terms, transcript, opts.MaxTerms)
	if len(terms) == 0 {
		return nil, nil
	}

	return &KnowledgeLookupResult{
		Topic:     topic,
		Source:    source,
		Terms:     terms,
		QueriedAt: time.Now().Format(time.RFC3339),
	}, nil
}

// detectTopicFromTranscript 从转写文本前 200 行识别游戏/番剧主题
func detectTopicFromTranscript(transcript string) string {
	lines := strings.Split(transcript, "\n")
	if len(lines) > 200 {
		lines = lines[:200]
	}
	head := strings.Join(lines, "\n")

	for _, p := range topicPatterns {
		if p.regex.MatchString(head) {
			return p.name
		}
	}

	return ""
}

// extractTermsFromHTML 从 HTML 中提取术语
func extractTermsFromHTML(html string, maxTerms int) []KnowledgeTerm {
	var terms []KnowledgeTerm
	re := regexp.MustCompile("<td[^>]*>([^<]+)</td>")
	matches := re.FindAllStringSubmatch(html, -1)
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) <= 1 {
			continue
		}
		name := strings.TrimSpace(m[1])
		if seen[name] || len(terms) >= maxTerms {
			continue
		}
		if len([]rune(name)) < 2 || len([]rune(name)) > 20 {
			continue
		}
		seen[name] = true
		terms = append(terms, KnowledgeTerm{
			Original: name,
			Correct:  name,
			Note:     "[自动查词]",
		})
	}
	return terms
}

// filterTermsByTranscript 只保留在转写文本中实际出现的术语
func filterTermsByTranscript(terms []KnowledgeTerm, transcript string, maxTerms int) []KnowledgeTerm {
	var filtered []KnowledgeTerm
	for _, t := range terms {
		if strings.Contains(transcript, t.Original) || strings.Contains(transcript, t.Correct) {
			filtered = append(filtered, t)
			if len(filtered) >= maxTerms {
				break
			}
		}
	}
	return filtered
}
