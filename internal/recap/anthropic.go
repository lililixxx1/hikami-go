package recap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

type AnthropicProvider struct {
	cfg        *config.Config
	httpClient *http.Client
}

func NewAnthropicProvider(cfg *config.Config) *AnthropicProvider {
	return &AnthropicProvider{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: time.Duration(cfg.RecapAI.TimeoutSeconds) * time.Second},
	}
}

func (p *AnthropicProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	_ = sessionInfo
	// 走 EffectiveAPIKeyEnv:与 probe/工厂一致,空 env 名兜底到 AI_API_KEY(codex 审核中[4])。
	apiKey := os.Getenv(p.cfg.RecapAI.EffectiveAPIKeyEnv())
	if apiKey == "" {
		return aiprovider.GenerateResult{}, errors.New("anthropic api key not set")
	}

	// Anthropic 走自己的官方地址兜底(非 DeepSeek 默认),保持与厂商一致。
	baseURL := strings.TrimRight(p.cfg.RecapAI.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	endpoint := baseURL + "/messages"

	maxTokens := p.cfg.RecapAI.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}
	// model 走 EffectiveModel(DeepSeek 默认):provider 切到 anthropic 时 model 必须用户显式填,
	// 否则发 deepseek-v4-pro 给 Anthropic 会 400。留空兜底只保证请求不因空 model 失败。
	body := map[string]any{
		"model":      recapModelFromContext(ctx, p.cfg.RecapAI.EffectiveModel()),
		"max_tokens": maxTokens,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return aiprovider.GenerateResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return aiprovider.GenerateResult{}, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return aiprovider.GenerateResult{}, err
	}
	defer resp.Body.Close()

	rawData, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return aiprovider.GenerateResult{Raw: string(rawData)}, fmt.Errorf("anthropic http status %d: %s", resp.StatusCode, string(rawData))
	}

	result := parseAnthropicResult(rawData)
	if result.Content == "" {
		return aiprovider.GenerateResult{Raw: string(rawData)}, errors.New("anthropic response missing content")
	}
	result.Content = stripAIPreamble(result.Content)
	result.Raw = string(rawData)
	return result, nil
}

func parseAnthropicContent(data []byte) string {
	return parseAnthropicResult(data).Content
}

func parseAnthropicResult(data []byte) aiprovider.GenerateResult {
	var raw struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return aiprovider.GenerateResult{}
	}
	for _, c := range raw.Content {
		if c.Type == "text" {
			return aiprovider.GenerateResult{
				Content:      c.Text,
				FinishReason: raw.StopReason,
			}
		}
	}
	return aiprovider.GenerateResult{FinishReason: raw.StopReason}
}
