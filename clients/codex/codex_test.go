package codex

import (
	"reflect"
	"testing"

	"nairid/clients"
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
