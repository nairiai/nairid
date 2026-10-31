package core

import (
	"errors"
	"fmt"
	"testing"
	"time"
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

func TestFormatTimeout(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{10 * time.Minute, "10m"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{24 * time.Hour, "24h"},
		{90 * time.Second, "1m30s"},
	}
	for _, tt := range tests {
		if got := FormatTimeout(tt.in); got != tt.want {
			t.Errorf("FormatTimeout(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSessionTimeoutUserMessage(t *testing.T) {
	err := &ErrSessionTimeout{Timeout: time.Hour, Err: errors.New("signal: killed")}

	want := "nairid stopped this run after it hit the 1h session timeout. Raise it in the agent's Advanced settings."
	if got := err.UserMessage(); got != want {
		t.Errorf("UserMessage() = %q, want %q", got, want)
	}
}

func TestIsSessionTimeout_FindsItThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("error continuing Claude session: %w", &ErrClaudeCommandErr{
		Err:    &ErrSessionTimeout{Timeout: 2 * time.Hour, Err: errors.New("signal: killed")},
		Output: "partial",
	})

	timeoutErr, ok := IsSessionTimeout(wrapped)
	if !ok || timeoutErr.Timeout != 2*time.Hour {
		t.Fatalf("IsSessionTimeout(%v) = %v, %v; want the 2h timeout", wrapped, timeoutErr, ok)
	}
	if _, ok := IsSessionTimeout(errors.New("exit status 1")); ok {
		t.Error("a plain error was reported as a timeout")
	}
}
