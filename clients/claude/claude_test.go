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

func TestBuildPermissionArgs(t *testing.T) {
	allow := func() []string { return []string{"mcp__github", "mcp__nairi"} }
	none := func() []string { return nil }
	tests := []struct {
		name         string
		mode         string
		allowedTools func() []string
		want         []string
	}{
		{"bypass ignores allow rules", "bypassPermissions", allow, []string{"--dangerously-skip-permissions"}},
		{"acceptEdits allows the MCP servers", "acceptEdits", allow,
			[]string{"--permission-mode", "acceptEdits", "--allowedTools", "mcp__github mcp__nairi"}},
		{"no servers omits the flag", "acceptEdits", none, []string{"--permission-mode", "acceptEdits"}},
		{"nil provider omits the flag", "acceptEdits", nil, []string{"--permission-mode", "acceptEdits"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewClaudeClient(tt.mode, tt.allowedTools).buildPermissionArgs()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
