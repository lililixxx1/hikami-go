package recap

import (
	"fmt"
	"strings"
)

// TemplateVars holds the standard variables available for template rendering.
type TemplateVars struct {
	ChannelName  string
	ChannelID    string
	Date         string // YYYY.MM.DD
	DateTime     string // RFC3339
	Title        string
	Duration     string // "2小时30分钟"
	DurationMin  int
	FanName      string
	LiveType     string // AI 从固定列表中选择
	DanmakuCount int
	UniqueUsers  int
	AvgPerMin    string
	Slug         string
}

// RenderTemplate replaces {{key}} placeholders in tmpl with values from vars and extraVars.
// Standard variables are replaced first, then extraVars.
// Unknown variables are preserved as-is.
// Returns the original tmpl if vars is nil or tmpl is empty.
func RenderTemplate(tmpl string, vars *TemplateVars, extraVars map[string]string) string {
	if tmpl == "" {
		return tmpl
	}

	if vars == nil {
		vars = &TemplateVars{}
	}

	// Replace standard variables
	replacements := map[string]string{
		"{{channel_name}}":  vars.ChannelName,
		"{{channel_id}}":    vars.ChannelID,
		"{{date}}":          vars.Date,
		"{{date_time}}":     vars.DateTime,
		"{{title}}":         vars.Title,
		"{{duration}}":      vars.Duration,
		"{{duration_min}}":  fmt.Sprintf("%d", vars.DurationMin),
		"{{live_type}}":     vars.LiveType,
		"{{fan_name}}":      vars.FanName,
		"{{danmaku_count}}": fmt.Sprintf("%d", vars.DanmakuCount),
		"{{unique_users}}":  fmt.Sprintf("%d", vars.UniqueUsers),
		"{{avg_per_min}}":   vars.AvgPerMin,
		"{{slug}}":          vars.Slug,
	}

	for k, v := range replacements {
		tmpl = strings.ReplaceAll(tmpl, k, v)
	}

	// Replace extra variables
	for k, v := range extraVars {
		tmpl = strings.ReplaceAll(tmpl, "{{"+k+"}}", v)
	}

	return tmpl
}
