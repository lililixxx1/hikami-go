package recap

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
	args := []string{"exec", "--model", recapModelFromContext(ctx, p.cfg.RecapAI.Model), "-"}
	cmd := exec.CommandContext(ctx, cliPath, args...)
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
