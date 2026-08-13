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
