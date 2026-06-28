package recap

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

type recapModelContextKey struct{}

func withRecapModel(ctx context.Context, model string) context.Context {
	model = strings.TrimSpace(model)
	if model == "" {
		return ctx
	}
	return context.WithValue(ctx, recapModelContextKey{}, model)
}

func recapModelFromContext(ctx context.Context, fallback string) string {
	if model, ok := ctx.Value(recapModelContextKey{}).(string); ok {
		model = strings.TrimSpace(model)
		if model != "" {
			return model
		}
	}
	return fallback
}

type LocalProvider struct{}

// ErrRecapDisabled 在 recap_ai.enabled=false 时由 NewConfiguredProvider 返回的
// disabledProvider 在 Generate 时抛出。设计 4.1：禁用就是禁用——不再静默退回 LocalProvider
// 占位，避免自动链误产占位回顾；能力 gate（RecapGenerate）也会据此在自动链/手动 API 跳过。
var ErrRecapDisabled = fmt.Errorf("recap AI is disabled (recap_ai.enabled=false)")

// disabledProvider 实现 Provider 但 Generate 永远返回 ErrRecapDisabled。
type disabledProvider struct{}

func (disabledProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	return aiprovider.GenerateResult{}, ErrRecapDisabled
}

// Provider is the interface for AI recap generation backends.
type Provider interface {
	Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error)
}

func (LocalProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	_ = systemPrompt
	content := fmt.Sprintf("# %s\n\n生成时间：%s\n\n## 摘要\n\n%s\n", sessionInfo.Title, time.Now().Format(time.RFC3339), firstParagraph(prompt))
	return aiprovider.GenerateResult{
		Content: content,
		Raw:     `{"provider":"local_placeholder"}`,
	}, nil
}

func NewConfiguredProvider(cfg *config.Config) Provider {
	if cfg == nil {
		return LocalProvider{}
	}
	// recap_ai.enabled=false：禁用就是禁用。不再退回 LocalProvider 占位（设计 4.1），
	// 返回 disabledProvider，Generate 时抛 ErrRecapDisabled；自动链/手动 API 的能力 gate
	// 也会因 RecapGenerate=false 提前跳过，这里作为最后一道防线。
	if !cfg.RecapAI.Enabled {
		return disabledProvider{}
	}
	// provider/api_key_env 留空兜底:经 Effective* 解析,空 provider 视为 openai_compatible,
	// 空 api_key_env 视为 AI_API_KEY,与响应层一致(避免首页能力显示与实际不符)。
	envKey := cfg.RecapAI.EffectiveAPIKeyEnv()
	switch cfg.RecapAI.EffectiveProvider() {
	case "openai_compatible":
		if os.Getenv(envKey) == "" {
			return LocalProvider{}
		}
		return &OpenAICompatibleProvider{cfg: cfg, httpClient: &http.Client{Timeout: time.Duration(cfg.RecapAI.TimeoutSeconds) * time.Second}}
	case "anthropic":
		if os.Getenv(envKey) == "" {
			return LocalProvider{}
		}
		return NewAnthropicProvider(cfg)
	case "claude_cli":
		return NewClaudeCLIProvider(cfg)
	case "codex_cli":
		return NewCodexCLIProvider(cfg)
	default:
		return LocalProvider{}
	}
}

// aiPreamblePrefixes lists common AI conversational opening phrases.
var aiPreamblePrefixes = []string{
	"好的", "没问题", "当然可以", "当然没问题", "这是", "以下是为",
	"以下是为您", "好的，", "没问题！", "没问题，", "当然，",
	"好的，以下是", "以下是我", "我来为您", "这里是",
	"我来帮", "让我为您", "我为你", "我来为你",
}

// stripAIPreamble removes AI conversational openings before the first markdown heading.
func stripAIPreamble(content string) string {
	idx := strings.Index(content, "#")
	if idx <= 0 {
		return content
	}
	prefix := strings.TrimSpace(content[:idx])
	if prefix == "" {
		return content
	}
	matchesPrefix := false
	for _, p := range aiPreamblePrefixes {
		if strings.HasPrefix(prefix, p) {
			matchesPrefix = true
			break
		}
	}
	if !matchesPrefix {
		return content
	}
	if len(prefix) >= 200 {
		return content
	}
	if strings.Contains(prefix, "- ") || strings.HasPrefix(strings.TrimSpace(prefix), "1.") {
		return content
	}
	return content[idx:]
}

func firstParagraph(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "暂无转写内容。"
	}
	parts := strings.Split(value, "\n\n")
	return strings.TrimSpace(parts[0])
}

func safeName(value string) string {
	return strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(value)
}

// suggestedTermRegex matches patterns like [应为：XXX] or [应为:XXX] in recap text.
var suggestedTermRegex = regexp.MustCompile(`\[应为[：:]\s*([^\]]+)\]`)

// extractSuggestedTerms scans recap markdown for term correction suggestions.
func extractSuggestedTerms(recap string) []string {
	matches := suggestedTermRegex.FindAllStringSubmatch(recap, -1)
	seen := make(map[string]struct{})
	var terms []string
	for _, m := range matches {
		if len(m) > 1 {
			t := strings.TrimSpace(m[1])
			if t != "" {
				if _, ok := seen[t]; !ok {
					seen[t] = struct{}{}
					terms = append(terms, t)
				}
			}
		}
	}
	return terms
}
