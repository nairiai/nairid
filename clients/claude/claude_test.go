package claude

import (
	"reflect"
	"testing"

	"nairid/clients"
)

func TestAppendOptionArgs(t *testing.T) {
	base := []string{"-p"}
	tests := []struct {
		name    string
		options *clients.ClaudeOptions
		want    []string
	}{
		{"nil options adds nothing", nil, []string{"-p"}},
		{"model only", &clients.ClaudeOptions{Model: "claude-opus-4-8"}, []string{"-p", "--model", "claude-opus-4-8"}},
		{"empty effort omits the flag", &clients.ClaudeOptions{Model: "claude-opus-4-8", Effort: ""}, []string{"-p", "--model", "claude-opus-4-8"}},
		{"effort follows model", &clients.ClaudeOptions{Model: "claude-opus-4-8", Effort: "max"}, []string{"-p", "--model", "claude-opus-4-8", "--effort", "max"}},
		{"all options", &clients.ClaudeOptions{Model: "sonnet", Effort: "low", SystemPrompt: "be brief", DisallowedTools: []string{"WebFetch", "Task"}},
			[]string{"-p", "--model", "sonnet", "--effort", "low", "--append-system-prompt", "be brief", "--disallowedTools", "WebFetch Task"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendOptionArgs(append([]string{}, base...), tt.options)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
