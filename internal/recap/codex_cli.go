package recap

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"hikami-go/internal/aiprovider"
	"hikami-go/internal/config"
	"hikami-go/internal/executil"
	"hikami-go/internal/session"
)

type CodexCLIProvider struct {
	cfg *config.Config
}

func NewCodexCLIProvider(cfg *config.Config) *CodexCLIProvider {
	return &CodexCLIProvider{cfg: cfg}
}

func (p *CodexCLIProvider) Generate(ctx context.Context, systemPrompt string, prompt string, sessionInfo session.Session) (aiprovider.GenerateResult, error) {
	_ = sessionInfo
	cliPath := p.cfg.RecapAI.CLIPath
	if cliPath == "" {
		cliPath = "codex"
	}

	timeout := time.Duration(p.cfg.RecapAI.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fullPrompt := "--- System Instructions ---\n" + systemPrompt + "\n\n--- User Request ---\n" + prompt
	args := codexCLIArgs(recapModelFromContext(ctx, p.cfg.RecapAI.Model))
	cmd := exec.CommandContext(ctx, cliPath, args...)
	// 回顾生成只需要 stdin 中的提示词。从仓库目录运行会让 Codex 加载项目
	// AGENTS.md 等编码代理上下文，导致每次回顾浪费大量 token。
	cmd.Dir = os.TempDir()
	executil.HideWindow(cmd)
	cmd.Stdin = bytes.NewReader([]byte(fullPrompt))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return aiprovider.GenerateResult{Raw: stderr.String()}, fmt.Errorf("codex cli failed: %w: %s", err, stderr.String())
	}

	content := stdout.String()
	raw := content
	content = stripAIPreamble(content)
	return aiprovider.GenerateResult{
		Content: content,
		Raw:     raw,
	}, nil
}

func codexCLIArgs(model string) []string {
	args := []string{
		"exec",
		"--sandbox", "read-only",
		"--ephemeral",
		"--color", "never",
		"--skip-git-repo-check",
	}
	if model = strings.TrimSpace(model); model != "" {
		args = append(args, "--model", model)
	}
	return append(args, "-")
}
