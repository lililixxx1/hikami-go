package recap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hikami-go/internal/glossary"
	"hikami-go/internal/session"
)

type correctionRule struct {
	Term      string
	Canonical string
}

type correctionReport struct {
	GeneratedAt  string   `json:"generated_at"`
	Source       string   `json:"source"`
	AppliedCount int      `json:"applied_count"`
	AppliedTerms []string `json:"applied_terms"`
}

func buildCorrectionRules(ctx context.Context, store *glossary.Store, channelID string) ([]correctionRule, error) {
	if store == nil {
		return nil, nil
	}
	entries, err := store.ListByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	rules := make([]correctionRule, 0, len(entries))
	for _, entry := range entries {
		term := strings.TrimSpace(entry.Term)
		canonical := strings.TrimSpace(entry.Canonical)
		if !entry.Enabled || term == "" || canonical == "" || term == canonical {
			continue
		}
		rules = append(rules, correctionRule{Term: term, Canonical: canonical})
	}
	sort.SliceStable(rules, func(i, j int) bool {
		return len([]rune(rules[i].Term)) > len([]rune(rules[j].Term))
	})
	return rules, nil
}

func correctTextWithRules(input string, rules []correctionRule) (string, []string) {
	if input == "" || len(rules) == 0 {
		return input, nil
	}
	appliedSet := make(map[string]struct{})
	output := input
	for _, rule := range rules {
		if !strings.Contains(output, rule.Term) {
			continue
		}
		// replaceTermBoundaryAware 对含 ASCII 字母数字的 term 做词边界判断；
		// 只在输出真变化时才记 applied，使 correction report 更准确。
		replaced := replaceTermBoundaryAware(output, rule.Term, rule.Canonical)
		if replaced != output {
			appliedSet[rule.Term] = struct{}{}
			output = replaced
		}
	}
	if len(appliedSet) == 0 {
		return output, nil
	}
	applied := make([]string, 0, len(appliedSet))
	for term := range appliedSet {
		applied = append(applied, term)
	}
	sort.Strings(applied)
	return output, applied
}

func (h *Handler) correctedTranscriptForPrompt(ctx context.Context, sessionInfo session.Session, recapRange *timeRange, fallback []byte, recapDir string) ([]byte, correctionReport, error) {
	rules, err := buildCorrectionRules(ctx, h.glossaryStore, sessionInfo.ChannelID)
	if err != nil {
		return nil, correctionReport{}, err
	}
	source := "transcript.txt"
	input := fallback
	if recapRange == nil {
		if timed, err := h.correctedTimedTranscript(sessionInfo, rules); err == nil && len(timed.text) > 0 {
			source = "segments.json"
			input = timed.text
			return h.writeCorrectedTranscriptArtifacts(recapDir, nil, input, timed.report)
		}
	} else {
		source = "filtered_transcript"
	}

	corrected, applied := correctTextWithRules(string(input), rules)
	report := newCorrectionReport(source, applied)
	return h.writeCorrectedTranscriptArtifacts(recapDir, recapRange, []byte(corrected), report)
}

type correctedTimedTranscriptResult struct {
	text   []byte
	report correctionReport
}

func (h *Handler) correctedTimedTranscript(sessionInfo session.Session, rules []correctionRule) (correctedTimedTranscriptResult, error) {
	return correctedTimedTranscriptFromPackageDir(filepath.Join(h.sessionDir(sessionInfo), "package"), rules)
}

func correctedTimedTranscriptFromPackageDir(packageDir string, rules []correctionRule) (correctedTimedTranscriptResult, error) {
	data, err := os.ReadFile(filepath.Join(packageDir, "segments.json"))
	if err != nil {
		return correctedTimedTranscriptResult{}, err
	}
	var segments []transcriptSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		return correctedTimedTranscriptResult{}, err
	}
	var b strings.Builder
	appliedSet := make(map[string]struct{})
	b.WriteString("【带时间戳转写（术语校正版）】\n\n")
	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" || seg.StartMS < 0 {
			continue
		}
		text, applied := correctTextWithRules(text, rules)
		for _, term := range applied {
			appliedSet[term] = struct{}{}
		}
		b.WriteString("[")
		b.WriteString(formatRecapTimestamp(seg.StartMS))
		b.WriteString("] ")
		b.WriteString(text)
		b.WriteString("\n")
	}
	appliedTerms := sortedStringSet(appliedSet)
	return correctedTimedTranscriptResult{
		text:   []byte(b.String()),
		report: newCorrectionReport("segments.json", appliedTerms),
	}, nil
}

func (h *Handler) writeCorrectedTranscriptArtifacts(recapDir string, recapRange *timeRange, text []byte, report correctionReport) ([]byte, correctionReport, error) {
	if recapDir == "" {
		return text, report, nil
	}
	suffix := recapRangeSuffix(recapRange)
	if err := os.MkdirAll(recapDir, 0o755); err != nil {
		return nil, correctionReport{}, err
	}
	if err := os.WriteFile(filepath.Join(recapDir, "transcript"+suffix+".corrected.txt"), text, 0o644); err != nil {
		return nil, correctionReport{}, err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, correctionReport{}, err
	}
	if err := os.WriteFile(filepath.Join(recapDir, "transcript"+suffix+".correction.json"), append(data, '\n'), 0o644); err != nil {
		return nil, correctionReport{}, err
	}
	return text, report, nil
}

func newCorrectionReport(source string, appliedTerms []string) correctionReport {
	appliedTerms = uniqueSortedStrings(appliedTerms)
	return correctionReport{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		Source:       source,
		AppliedCount: len(appliedTerms),
		AppliedTerms: appliedTerms,
	}
}

func sortedStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	return sortedStringSet(set)
}
