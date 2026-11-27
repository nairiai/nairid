package claude

import (
	"context"
	"strings"
	"testing"

	"nairid/clients"
	"nairid/internal/testsupport"
)

func TestStartNewSession_LogsTheArgvWithoutThePrompt(t *testing.T) {
	testsupport.InstallFakeCLI(t, "claude")
	logs := testsupport.CaptureLog(t)
	const prompt = "rotate the acme staging password"

	_, err := NewClaudeClient("bypassPermissions", nil).StartNewSession(context.Background(), prompt, &clients.ClaudeOptions{Model: "sonnet"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := logs.String()
	if strings.Contains(got, prompt) {
		t.Errorf("the log contains the prompt:\n%s", got)
	}
	want := "Claude command: [--dangerously-skip-permissions --verbose --output-format stream-json -p --model sonnet] (prompt 32 chars)"
	if !strings.Contains(got, want) {
		t.Errorf("the log lacks %q:\n%s", want, got)
	}
}
