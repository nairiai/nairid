package core

import (
	"errors"
	"testing"
)

func TestErrClaudeCommandErr_NamesTheAgentCLI(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"Codex", "Codex command failed: exit status 1\nOutput: boom"},
		{"Cursor", "Cursor command failed: exit status 1\nOutput: boom"},
		{"", "agent command failed: exit status 1\nOutput: boom"},
	}
	for _, tt := range tests {
		err := &ErrClaudeCommandErr{Agent: tt.agent, Err: errors.New("exit status 1"), Output: "boom"}
		if got := err.Error(); got != tt.want {
			t.Errorf("Agent %q: got %q, want %q", tt.agent, got, tt.want)
		}
	}
}
