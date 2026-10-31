package codex

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"nairid/clients"
	"nairid/core"
)

func TestBuildBaseArgs_Effort(t *testing.T) {
	c := NewCodexClient("bypassPermissions", "")
	execArgs := []string{"--search", "exec", "--dangerously-bypass-approvals-and-sandbox", "--json", "--skip-git-repo-check"}

	tests := []struct {
		name    string
		options *clients.CodexOptions
		want    []string
	}{
		{"nil options", nil, execArgs},
		{"empty effort gives today's args", &clients.CodexOptions{Model: "gpt-5.5"}, append([]string{"-m", "gpt-5.5"}, execArgs...)},
		{"effort is a global -c override before exec", &clients.CodexOptions{Model: "gpt-5.5", Effort: "xhigh"},
			append([]string{"-m", "gpt-5.5", "-c", "model_reasoning_effort=xhigh"}, execArgs...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.buildBaseArgs(tt.options)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleCommandError_NamesCodex(t *testing.T) {
	runErr := &clients.CommandError{Err: errors.New("exit status 1"), Output: "model not supported"}

	got := handleCommandError(context.Background(), runErr, "Codex", time.Minute)

	cmdErr, ok := core.IsClaudeCommandErr(got)
	if !ok || cmdErr.Agent != "Codex" {
		t.Fatalf("got %#v, want a command error for Codex", got)
	}
	if !strings.HasPrefix(got.Error(), "Codex command failed: exit status 1") {
		t.Errorf("error reads %q", got.Error())
	}
}
