package recap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

type OpenAICompatibleProvider struct {
	cfg        *config.Config
	httpClient *http.Client
}

func (p *OpenAICompatibleProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	_ = sessionInfo
	// base_url / model / api_key_env 留空兜底:经 Effective* 解析,空值回落 DeepSeek 官方默认,
	// 避免空 base_url 拼出无 host 的 /chat/completions 或空 model 触发 400。
	endpoint := strings.TrimRight(p.cfg.RecapAI.EffectiveBaseURL(), "/") + "/chat/completions"
	body := map[string]any{
		"model": recapModelFromContext(ctx, p.cfg.RecapAI.EffectiveModel()),
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": prompt},
		},
	}
	if p.cfg.RecapAI.MaxTokens > 0 {
		body["max_tokens"] = p.cfg.RecapAI.MaxTokens
	}
	data, err := json.Marshal(body)
	if err != nil {
		return aiprovider.GenerateResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return aiprovider.GenerateResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+os.Getenv(p.cfg.RecapAI.EffectiveAPIKeyEnv()))
	request.Header.Set("Content-Type", "application/json")
	response, err := p.httpClient.Do(request)
	if err != nil {
		return aiprovider.GenerateResult{}, err
	}
	defer response.Body.Close()
	rawData, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return aiprovider.GenerateResult{Raw: string(rawData)}, fmt.Errorf("recap provider http status %d: %s", response.StatusCode, string(rawData))
	}
	result := parseChatCompletionResult(rawData)
	if result.Content == "" {
		return aiprovider.GenerateResult{Raw: string(rawData)}, fmt.Errorf("recap provider response missing content")
	}
	result.Content = stripAIPreamble(result.Content)
	result.Raw = string(rawData)
	return result, nil
}

func parseChatCompletionContent(data []byte) string {
	return parseChatCompletionResult(data).Content
}

func parseChatCompletionResult(data []byte) aiprovider.GenerateResult {
	var raw struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return aiprovider.GenerateResult{}
	}
	if len(raw.Choices) == 0 {
		return aiprovider.GenerateResult{}
	}
	return aiprovider.GenerateResult{
		Content:      raw.Choices[0].Message.Content,
		FinishReason: raw.Choices[0].FinishReason,
	}
}
