package recap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"hikami-go/internal/glossary"
	"hikami-go/internal/session"
)

// promptData holds all data needed to build a recap prompt.
type promptData struct {
	SessionInfo        session.Session
	Transcript         []byte
	Stats              *danmakuStats
	Meta               *sessionMetadata
	Resolved           *ResolvedTemplate
	Vars               *TemplateVars
	GlossaryText       string
	KnowledgeResult    *KnowledgeLookupResult
	SegmentSuggestions []segmentSuggestion
	SpeakerStats       *speakerStats
}

// PromptSection builds one section of the recap generation prompt.
type PromptSection interface {
	Name() string
	Build(data *promptData) string
}

// --- Section implementations ---

type basicInfoSection struct{}

func (s *basicInfoSection) Name() string { return "basic_info" }

func (s *basicInfoSection) Build(data *promptData) string {
	var b strings.Builder
	b.WriteString("## 基本信息\n\n")
	b.WriteString("- 标题：")
	b.WriteString(data.SessionInfo.Title)
	b.WriteString("\n")
	if data.SessionInfo.StartedAt != "" {
		b.WriteString("- 日期：")
		b.WriteString(data.SessionInfo.StartedAt)
		b.WriteString("\n")
	}
	if data.Meta != nil && data.Meta.DurationMs > 0 {
		durMin := data.Meta.DurationMs / 60000
		durHour := durMin / 60
		durMinRem := durMin % 60
		b.WriteString(fmt.Sprintf("- 时长：%d小时%d分钟\n", durHour, durMinRem))
	} else if data.SessionInfo.StartedAt != "" && data.SessionInfo.EndedAt != "" {
		if t1, err := time.Parse(time.RFC3339, data.SessionInfo.StartedAt); err == nil {
			if t2, err := time.Parse(time.RFC3339, data.SessionInfo.EndedAt); err == nil {
				durMin := int(t2.Sub(t1).Minutes())
				durHour := durMin / 60
				durMinRem := durMin % 60
				b.WriteString(fmt.Sprintf("- 时长：%d小时%d分钟\n", durHour, durMinRem))
			}
		}
	}
	if data.Stats != nil {
		b.WriteString(fmt.Sprintf("- 弹幕数：%d\n", data.Stats.TotalCount))
		b.WriteString(fmt.Sprintf("- 独立弹幕用户：%d人\n", data.Stats.UniqueUsers))
		if data.Stats.AvgPerMin > 0 {
			b.WriteString(fmt.Sprintf("- 平均每分钟弹幕：%.1f条\n", data.Stats.AvgPerMin))
		}
	}
	return b.String()
}

type speakerInfoSection struct{}

func (s *speakerInfoSection) Name() string { return "speaker_info" }

func (s *speakerInfoSection) Build(data *promptData) string {
	if data.SpeakerStats == nil || data.SpeakerStats.EffectiveCount < 2 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## 说话人统计\n\n")
	b.WriteString("以下信息来自 ASR 自动检测，仅用于判断对话结构，不能确认真实身份；不要自行命名说话人，不要把说话人编号当作人物身份。\n\n")
	b.WriteString(fmt.Sprintf("- 有效说话人数：%d人\n", data.SpeakerStats.EffectiveCount))
	b.WriteString("- 各说话人时长占比：\n")
	for _, speaker := range data.SpeakerStats.Speakers {
		b.WriteString(fmt.Sprintf("  - 说话人 %d：%.1f%%（约 %s，%d 个片段）\n",
			speaker.ID,
			speaker.DurationRatio*100,
			formatSpeakerDuration(speaker.DurationMS),
			speaker.SegmentCount,
		))
	}
	b.WriteString(fmt.Sprintf("- 说话人切换频率：约 %.2f 次/分钟\n", data.SpeakerStats.SwitchesPerMinute))
	return b.String()
}

func formatSpeakerDuration(ms int64) string {
	if ms <= 0 {
		return "0秒"
	}
	totalSec := ms / 1000
	if totalSec <= 0 {
		return "1秒"
	}
	hour := totalSec / 3600
	minute := (totalSec % 3600) / 60
	second := totalSec % 60
	if hour > 0 {
		if minute > 0 {
			return fmt.Sprintf("%d小时%d分钟", hour, minute)
		}
		return fmt.Sprintf("%d小时", hour)
	}
	if minute > 0 {
		if second > 0 {
			return fmt.Sprintf("%d分钟%d秒", minute, second)
		}
		return fmt.Sprintf("%d分钟", minute)
	}
	return fmt.Sprintf("%d秒", second)
}

type formatSection struct{}

func (s *formatSection) Name() string { return "format" }

func (s *formatSection) Build(data *promptData) string {
	return "## 输出格式要求\n\n" + RenderTemplate(data.Resolved.UserFormat, data.Vars, data.Resolved.ExtraVars) + "\n"
}

type longStreamSection struct{}

func (s *longStreamSection) Name() string { return "long_stream" }

func (s *longStreamSection) Build(data *promptData) string {
	if data.Meta == nil || data.Meta.DurationMs <= 180*60*1000 {
		return ""
	}
	durMin := data.Meta.DurationMs / 60000
	parts := durMin / 45
	if parts < 2 {
		parts = 2
	}
	var b strings.Builder
	b.WriteString("\n## 长直播分段建议\n\n")
	b.WriteString(fmt.Sprintf("本场直播时长超过 3 小时（%d 分钟），建议分为 %d 个主要部分，每部分 500-800 字。\n", durMin, parts))
	b.WriteString("每个部分应包含：描述性标题（第N部分：标题）、时间范围（HH:MM - HH:MM）、内容叙述、弹幕互动。\n")
	b.WriteString("根据主要话题转换点自然分段，不必严格等分。\n")
	return b.String()
}

type segmentationSection struct{}

func (s *segmentationSection) Name() string { return "segmentation" }

func (s *segmentationSection) Build(data *promptData) string {
	return formatSegmentSuggestionsForPrompt(data.SegmentSuggestions)
}

type glossarySection struct {
	store *glossary.Store
	ctx   context.Context
}

func (s *glossarySection) Name() string { return "glossary" }

func (s *glossarySection) Build(data *promptData) string {
	if s.store == nil || data.GlossaryText == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 术语校正参考\n\n")
	b.WriteString(data.GlossaryText)
	b.WriteString("\n\n校正规则：回顾正文中使用正确写法；Markdown 引用块（>）保留主播原始说法，如发现明显误识别可在引用旁标注 [应为：xxx]，该标注只用于术语建议提取；组合词同样需要校正。\n")
	b.WriteString("校正优先级：主播正式名/昵称 > 粉丝称呼 > 游戏角色/作品名 > 其他通用术语。\n")
	b.WriteString("分类说明：人名类（主播、粉丝、嘉宾）必须全文统一；称呼类禁止混用；游戏/番剧类使用公认译名。\n\n")
	b.WriteString("校正示例（以当前主播为例）：\n")
	b.WriteString("- ASR 转写中的\"辉子版/灰子版/辉子满\" → 统一为术语表中的正确写法\n")
	b.WriteString("- ASR 转写中的\"灰子们/辉子们\" → 统一为术语表中的粉丝称呼\n")
	b.WriteString("- 游戏角色名以转写和知识库为准，不确定的用 [?] 标记\n")
	return b.String()
}

type knowledgeSection struct{}

func (s *knowledgeSection) Name() string { return "knowledge" }

func (s *knowledgeSection) Build(data *promptData) string {
	if data.KnowledgeResult == nil || len(data.KnowledgeResult.Terms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 可能相关专有名词参考\n\n")
	b.WriteString(fmt.Sprintf("以下信息来自 %s，仅供参考。不确定的不要强行改写，冲突时优先术语校正表。\n\n", data.KnowledgeResult.Source))
	b.WriteString("| 名称 | 说明 |\n|------|------|\n")
	for _, term := range data.KnowledgeResult.Terms {
		b.WriteString(fmt.Sprintf("| %s | %s |\n", term.Correct, term.Note))
	}
	b.WriteString("\n")
	return b.String()
}

type danmakuSection struct{}

func (s *danmakuSection) Name() string { return "danmaku" }

func (s *danmakuSection) Build(data *promptData) string {
	if data.Stats == nil || data.Stats.TotalCount == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 弹幕分析数据\n\n")
	b.WriteString(fmt.Sprintf("弹幕总量：%d条，独立用户：%d人", data.Stats.TotalCount, data.Stats.UniqueUsers))
	if data.Stats.AvgPerMin > 0 {
		b.WriteString(fmt.Sprintf("，平均每分钟%.1f条", data.Stats.AvgPerMin))
	}
	b.WriteString("\n\n")

	if len(data.Stats.TopDanmaku) > 0 {
		b.WriteString("### 代表性弹幕\n\n")
		for _, d := range data.Stats.TopDanmaku {
			b.WriteString(fmt.Sprintf("- [%s] \"%s\"\n", d.TimeMinSec, d.Text))
		}
		b.WriteString("\n")
	}

	if len(data.Stats.Keywords) > 0 {
		b.WriteString("### 关键词统计\n\n")
		for _, kw := range data.Stats.Keywords {
			b.WriteString(fmt.Sprintf("- %s：%d条\n", kw.Word, kw.Count))
		}
		b.WriteString("\n")
	}

	// Burst moments (sudden engagement spikes)
	if len(data.Stats.BurstMoments) > 0 {
		b.WriteString("### 弹幕突发时刻\n\n")
		b.WriteString("以下时刻弹幕率突然飙升（超过平均 3 倍以上）：\n\n")
		for _, bm := range data.Stats.BurstMoments {
			b.WriteString(fmt.Sprintf("- %s：弹幕 %d条（%.1f倍突发）\n", bm.TimeMinSec, bm.PeakCount, bm.BurstFactor))
		}
		b.WriteString("\n")
	}

	// Topic clusters
	if len(data.Stats.Topics) > 0 {
		b.WriteString("### 弹幕话题聚类\n\n")
		for _, t := range data.Stats.Topics {
			b.WriteString(fmt.Sprintf("- %s 关键词「%s」(%d次)：", t.TimeRange, t.Keyword, t.Count))
			sampleCount := len(t.SampleTexts)
			if sampleCount > 3 {
				sampleCount = 3
			}
			for i := 0; i < sampleCount; i++ {
				if i > 0 {
					b.WriteString("、")
				}
				b.WriteString(fmt.Sprintf("「%s」", t.SampleTexts[i]))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// High-weight danmaku
	if len(data.Stats.HighWeightDanmaku) > 0 {
		b.WriteString("\n### 高权重弹幕\n\n")
		b.WriteString("以下弹幕因高互动权重被特别标注：\n\n")
		for _, d := range data.Stats.HighWeightDanmaku {
			b.WriteString(fmt.Sprintf("- [%s] %s\n", d.TimeMinSec, d.Text))
		}
		b.WriteString("\n")
	}

	return b.String()
}

type transcriptSection struct{}

func (s *transcriptSection) Name() string { return "transcript" }

func (s *transcriptSection) Build(data *promptData) string {
	if len(data.Transcript) == 0 {
		return ""
	}
	return "\n## 转写原文\n\n" + string(data.Transcript)
}

// --- PromptBuilder ---

// PromptBuilder assembles recap prompts from ordered sections.
type PromptBuilder struct {
	sections      []PromptSection
	glossaryStore *glossary.Store
}

// NewPromptBuilder creates a builder with the default section order.
func NewPromptBuilder(glossaryStore *glossary.Store) *PromptBuilder {
	return &PromptBuilder{
		glossaryStore: glossaryStore,
		sections: []PromptSection{
			&basicInfoSection{},
			&speakerInfoSection{},
			&formatSection{},
			&longStreamSection{},
			&segmentationSection{},
			nil, // glossarySection placeholder, filled at Build time
			&knowledgeSection{},
			&danmakuSection{},
			&transcriptSection{},
		},
	}
}

// Build assembles the full prompt from all sections.
func (pb *PromptBuilder) Build(ctx context.Context, data *promptData) string {
	var b strings.Builder
	b.WriteString("# 直播回顾生成任务\n\n")

	for _, s := range pb.sections {
		// Dynamic glossary section (needs ctx and store)
		if s == nil {
			s = &glossarySection{store: pb.glossaryStore, ctx: ctx}
		}
		content := s.Build(data)
		if content != "" {
			b.WriteString(content)
		}
	}

	return b.String()
}

// buildPrompt is the Handler method that bridges to PromptBuilder.
func (h *Handler) buildPrompt(ctx context.Context, sessionInfo session.Session, transcript []byte, stats *danmakuStats, meta *sessionMetadata, resolved *ResolvedTemplate, vars *TemplateVars, segSuggestions []segmentSuggestion) string {
	return h.buildPromptWithKnowledge(ctx, sessionInfo, transcript, stats, meta, resolved, vars, segSuggestions, nil)
}

func (h *Handler) buildPromptWithKnowledge(ctx context.Context, sessionInfo session.Session, transcript []byte, stats *danmakuStats, meta *sessionMetadata, resolved *ResolvedTemplate, vars *TemplateVars, segSuggestions []segmentSuggestion, knowledgeResult *KnowledgeLookupResult) string {
	return h.buildPromptWithKnowledgeAndSpeakers(ctx, sessionInfo, transcript, stats, meta, resolved, vars, segSuggestions, knowledgeResult, nil)
}

func (h *Handler) buildPromptWithKnowledgeAndSpeakers(ctx context.Context, sessionInfo session.Session, transcript []byte, stats *danmakuStats, meta *sessionMetadata, resolved *ResolvedTemplate, vars *TemplateVars, segSuggestions []segmentSuggestion, knowledgeResult *KnowledgeLookupResult, speakerStats *speakerStats) string {
	glossaryText, _ := h.glossaryStore.ExportForPrompt(ctx, sessionInfo.ChannelID)

	data := &promptData{
		SessionInfo:        sessionInfo,
		Transcript:         transcript,
		Stats:              stats,
		Meta:               meta,
		Resolved:           resolved,
		Vars:               vars,
		GlossaryText:       glossaryText,
		KnowledgeResult:    knowledgeResult,
		SegmentSuggestions: segSuggestions,
		SpeakerStats:       speakerStats,
	}

	builder := NewPromptBuilder(h.glossaryStore)
	return builder.Build(ctx, data)
}
