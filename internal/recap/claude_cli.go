package recap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/executil"
	"hikami-go/internal/session"
)

type ClaudeCLIProvider struct {
	cfg *config.Config
}

func NewClaudeCLIProvider(cfg *config.Config) *ClaudeCLIProvider {
	return &ClaudeCLIProvider{cfg: cfg}
}

func (p *ClaudeCLIProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	_ = sessionInfo
	cliPath := p.cfg.RecapAI.CLIPath
	if cliPath == "" {
		cliPath = "claude"
	}

	timeout := time.Duration(p.cfg.RecapAI.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fullPrompt := "--- System Instructions ---\n" + systemPrompt + "\n\n--- User Request ---\n" + prompt
	args := claudeCLIArgs(recapModelFromContext(ctx, p.cfg.RecapAI.Model))
	cmd := exec.CommandContext(ctx, cliPath, args...)
	executil.HideWindow(cmd)
	cmd.Stdin = bytes.NewReader([]byte(fullPrompt))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return aiprovider.GenerateResult{Raw: stderr.String()}, fmt.Errorf("claude cli failed: %w: %s", err, stderr.String())
	}

	raw := stdout.String()
	content := parseCLIJSONContent([]byte(raw))
	if content == "" {
		content = raw
	}
	content = stripAIPreamble(content)
	return aiprovider.GenerateResult{
		Content: content,
		Raw:     raw,
	}, nil
}

// claudeCLIArgs 组装 claude CLI 参数。model 为空时不传 --model(交由 CLI 自身默认模型),
// 否则空 model 会拼成 `--model ""` 导致 CLI 报错(M7,2026-08-15 全项目审核;
// 对齐 codexCLIArgs 的条件追加)。
func claudeCLIArgs(model string) []string {
	var args []string
	if model = strings.TrimSpace(model); model != "" {
		args = append(args, "--model", model)
	}
	return append(args, "--output-format", "json")
}

type claudeCLIResponse struct {
	Result string `json:"result"`
}

func parseCLIJSONContent(data []byte) string {
	var resp claudeCLIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return ""
	}
	if resp.Result == "" {
		return ""
	}
	return resp.Result
}
