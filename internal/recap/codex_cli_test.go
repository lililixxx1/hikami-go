package recap

import (
	"reflect"
	"testing"
)

func TestCodexCLIArgs(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  []string
	}{
		{
			name: "uses CLI default model when blank",
			want: []string{"exec", "--sandbox", "read-only", "--ephemeral", "--color", "never", "--skip-git-repo-check", "-"},
		},
		{
			name:  "passes explicit model",
			model: " gpt-5.6-sol ",
			want:  []string{"exec", "--sandbox", "read-only", "--ephemeral", "--color", "never", "--skip-git-repo-check", "--model", "gpt-5.6-sol", "-"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexCLIArgs(tt.model); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("codexCLIArgs(%q) = %#v, want %#v", tt.model, got, tt.want)
			}
		})
	}
}

func TestClaudeCLIArgs(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  []string
	}{
		{
			name: "omits --model when blank (M7: 空 model 曾拼成 --model \"\" 致 CLI 报错)",
			want: []string{"--output-format", "json"},
		},
		{
			name:  "passes explicit model",
			model: " claude-opus-4.6 ",
			want:  []string{"--model", "claude-opus-4.6", "--output-format", "json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeCLIArgs(tt.model); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("claudeCLIArgs(%q) = %#v, want %#v", tt.model, got, tt.want)
			}
		})
	}
}
