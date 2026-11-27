package codex

import (
	"context"
	"strings"
	"testing"

	"nairid/clients"
	"nairid/internal/testsupport"
)

func TestContinueSession_LogsTheArgvWithoutThePrompt(t *testing.T) {
	testsupport.InstallFakeCLI(t, "codex")
	logs := testsupport.CaptureLog(t)
	const prompt = "rotate the acme staging password"

	_, err := NewCodexClient("bypassPermissions", "").ContinueSession(context.Background(), "thread-1", prompt, &clients.CodexOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := logs.String()
	if strings.Contains(got, prompt) {
		t.Errorf("the log contains the prompt:\n%s", got)
	}
	if !strings.Contains(got, "resume thread-1] (prompt 32 chars)") {
		t.Errorf("the log lacks the argv line:\n%s", got)
	}
}
